package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shopnotification"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"github.com/google/uuid"
)

const shopNotificationLease = 2 * time.Minute

func shopNotificationClaimable(now time.Time) predicate.ShopNotification {
	return shopnotification.Or(
		shopnotification.And(shopnotification.StatusIn("PENDING", "FAILED"), shopnotification.AttemptsLT(8), shopnotification.NextAttemptAtLTE(now)),
		shopnotification.And(shopnotification.StatusEQ("SENDING"), shopnotification.ClaimedAtLTE(now.Add(-shopNotificationLease))),
	)
}

func (s *ShopService) RetryNotification(ctx context.Context, orderID int64, channel string) (*ShopOrderDetail, error) {
	if channel != "email" && channel != "feishu" {
		return nil, infraerrors.BadRequest("SHOP_NOTIFICATION_CHANNEL_INVALID", "无效的通知渠道")
	}
	job, err := s.client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(orderID), shopnotification.ChannelEQ(channel)).Order(dbent.Desc(shopnotification.FieldID)).First(ctx)
	if dbent.IsNotFound(err) {
		return nil, infraerrors.BadRequest("SHOP_NOTIFICATION_NOT_FOUND", "当前订单没有该通知")
	}
	if err != nil {
		return nil, fmt.Errorf("get shop notification: %w", err)
	}
	if job.Status == "SENT" {
		return s.Order(ctx, orderID, 0, true)
	}
	if job.Status == "SENDING" && job.ClaimedAt != nil && job.ClaimedAt.After(time.Now().Add(-shopNotificationLease)) {
		return nil, infraerrors.Conflict("SHOP_NOTIFICATION_SENDING", "通知正在发送，请稍后刷新")
	}
	_, err = s.client.ShopNotification.UpdateOne(job).Where(shopnotification.StatusEQ(job.Status), shopnotification.ClaimTokenEQ(job.ClaimToken), shopnotification.AttemptsEQ(job.Attempts)).SetStatus("PENDING").SetAttempts(0).SetErrorKind("").SetClaimToken("").ClearClaimedAt().SetNextAttemptAt(time.Now()).Save(ctx)
	if dbent.IsNotFound(err) {
		return nil, shopConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("retry shop notification: %w", err)
	}
	return s.Order(ctx, orderID, 0, true)
}

// RecoverNotifications claims durable jobs independently from fulfillment.
// Delivery is at least once across an SMTP success followed by a process exit;
// a notification retry can never change or duplicate the fulfilled order.
func (s *ShopService) RecoverNotifications(ctx context.Context) error {
	if !runtimegate.SharedWorkAllowed() {
		return nil
	}
	jobs, err := s.client.ShopNotification.Query().Where(shopNotificationClaimable(time.Now())).Order(dbent.Asc(shopnotification.FieldNextAttemptAt), dbent.Asc(shopnotification.FieldID)).Limit(20).All(ctx)
	if err != nil {
		return fmt.Errorf("list shop notification jobs: %w", err)
	}
	for _, candidate := range jobs {
		if err = ctx.Err(); err != nil {
			return err
		}
		if !runtimegate.SharedWorkAllowed() {
			return nil
		}
		token := uuid.NewString()
		now := time.Now()
		job, claimErr := s.client.ShopNotification.UpdateOne(candidate).Where(shopNotificationClaimable(now)).SetStatus("SENDING").SetClaimToken(token).SetClaimedAt(now).AddAttempts(1).Save(ctx)
		if dbent.IsNotFound(claimErr) {
			continue
		}
		if claimErr != nil {
			return fmt.Errorf("claim shop notification: %w", claimErr)
		}
		sendErr := s.sendNotification(ctx, job)
		b := s.client.ShopNotification.UpdateOne(job).Where(shopnotification.ClaimTokenEQ(token), shopnotification.StatusEQ("SENDING")).SetClaimToken("").ClearClaimedAt()
		if sendErr == nil {
			b.SetStatus("SENT").SetSentAt(time.Now()).SetErrorKind("")
		} else {
			delay := time.Duration(1<<min(job.Attempts, 6)) * time.Minute
			b.SetStatus("FAILED").SetErrorKind("DELIVERY_UNAVAILABLE").SetNextAttemptAt(time.Now().Add(delay))
		}
		if _, err = b.Save(ctx); err != nil && !dbent.IsNotFound(err) {
			return fmt.Errorf("record shop notification result: %w", err)
		}
	}
	return nil
}

func (s *ShopService) frontendURL(ctx context.Context, path string) string {
	if s.settings == nil {
		return path
	}
	base := strings.TrimRight(s.settings.GetFrontendURL(ctx), "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return path
	}
	return base + path
}

func (s *ShopService) sendNotification(ctx context.Context, job *dbent.ShopNotification) error {
	o, err := s.client.PaymentOrder.Get(ctx, job.OrderID)
	if err != nil {
		return fmt.Errorf("load shop notice order: %w", err)
	}
	f, err := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(job.OrderID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("load shop notice fulfillment: %w", err)
	}
	p, err := shopSnapshot(f)
	if err != nil {
		if job.Channel != "feishu" {
			return err
		}
		p = ShopProduct{Name: "商品配置待复核"}
	}
	if !runtimegate.SharedWorkAllowed() {
		return errors.New("shop notification generation draining")
	}
	owned, err := s.client.ShopNotification.Query().Where(shopnotification.IDEQ(job.ID), shopnotification.StatusEQ("SENDING"), shopnotification.ClaimTokenEQ(job.ClaimToken)).Exist(ctx)
	if err != nil || !owned {
		return errors.New("shop notification lease lost")
	}
	if job.Channel == "feishu" {
		if o.PaidAt == nil || (job.Event != "action_review" && !shopPaidActionAllowed(o) && f.Status != ShopCompleted) {
			return nil
		}
		if s.feishu == nil {
			return errors.New("shop feishu unavailable")
		}
		event := "代充订单已付款"
		if job.Event == "input_received" {
			event = "代充资料已提交"
		} else if p.FulfillmentMode == ShopAccountDelivery {
			event = "成品号已付款，待交付"
		}
		if job.Event == "action_review" || p.Name == "商品配置待复核" {
			event = "商城自动交付待复核"
		}
		message := fmt.Sprintf("%s\n订单：%s\n商品：%s\n金额：CNY %.2f\n处理状态：%s\n后台：%s", event, o.OutTradeNo, p.Name, o.PayAmount, f.Status, s.frontendURL(ctx, fmt.Sprintf("/admin/shop/orders/%d", o.ID)))
		return s.feishu.SendText(ctx, message)
	}
	if s.email == nil {
		return errors.New("shop email unavailable")
	}
	// Obsolete requests for input are discarded after a resubmission or completion.
	if job.Event == "request_input" && (f.Status != ShopNeedsInput || f.Revision != job.Revision || !shopPaidActionAllowed(o)) {
		return nil
	}
	if job.Event == "complete" && (f.Status != ShopCompleted || o.Status != OrderStatusCompleted) {
		return nil
	}
	if job.Event == "complete" {
		var detail ShopOrderDetail
		detail.OrderID = o.ID
		if err = s.attachShopAutomation(ctx, &detail, false); err != nil {
			return err
		}
		if detail.Automation != nil && detail.Automation.Status != shopaction.Succeeded {
			return nil
		}
	}
	subject := "商城订单已完成"
	body := "<p>您的商城订单已完成。</p>"
	if job.Event == "request_input" {
		subject = "商城订单需要补充资料"
		body = "<p>请打开订单补充资料：</p><pre style=\"white-space:pre-wrap\">" + html.EscapeString(f.StatusNote) + "</pre>"
	}
	body += "<p>商品：" + html.EscapeString(p.Name) + "<br>订单：" + html.EscapeString(o.OutTradeNo) + "</p>"
	if job.Event == "complete" && f.DeliveryCiphertext != "" {
		content, err := s.decrypt(o.ID, "delivery", 1, f.DeliveryCiphertext)
		if err != nil {
			return err
		}
		body += "<h3>交付信息</h3><pre style=\"white-space:pre-wrap;overflow-wrap:anywhere\">" + html.EscapeString(content) + "</pre>"
	}
	body += "<p><a href=\"" + html.EscapeString(s.frontendURL(ctx, fmt.Sprintf("/shop/orders/%d", o.ID))) + "\">查看订单</a></p>"
	if p.GuideKey != "" {
		body += "<p><a href=\"" + html.EscapeString(s.frontendURL(ctx, "/shop/help/"+p.GuideKey)) + "\">查看使用文档</a></p>"
	}
	return s.email.SendEmail(ctx, o.UserEmail, subject, body)
}

type ShopNotificationService struct {
	shop       *ShopService
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
	ctx        context.Context
	cancel     context.CancelFunc
	startOnce  sync.Once
	wg         sync.WaitGroup
}

func NewShopNotificationService(shop *ShopService, locks LeaderLockCache, db *sql.DB) *ShopNotificationService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ShopNotificationService{shop: shop, lockCache: locks, db: db, instanceID: uuid.NewString(), ctx: ctx, cancel: cancel}
}

func (s *ShopNotificationService) Start() {
	s.startOnce.Do(func() {
		workers := 1
		actionsConfigured := s.shop != nil && len(s.shop.actionsCfg.Integrations) > 0
		if actionsConfigured {
			workers++
		}
		s.wg.Add(workers)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()
			for {
				s.run()
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		if actionsConfigured {
			go func() {
				defer s.wg.Done()
				ticker := time.NewTicker(20 * time.Second)
				defer ticker.Stop()
				for {
					s.runActions()
					select {
					case <-s.ctx.Done():
						return
					case <-ticker.C:
					}
				}
			}()
		}
	})
}

func (s *ShopNotificationService) runActions() {
	if !runtimegate.SharedWorkAllowed() || (s.lockCache == nil && s.db == nil) {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 55*time.Second)
	defer cancel()
	leaseCtx, release, acquired := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, "shop:actions:leader", s.instanceID, shopActionLease)
	if !acquired {
		return
	}
	defer release()
	if err := s.shop.RecoverShopActions(leaseCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("shop action recovery incomplete", "error_kind", "DELIVERY_UNAVAILABLE")
	}
}

func (s *ShopNotificationService) Stop() {
	if s != nil {
		s.cancel()
		s.wg.Wait()
	}
}

func (s *ShopNotificationService) run() {
	if !runtimegate.SharedWorkAllowed() {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 55*time.Second)
	defer cancel()
	leaseCtx, release, acquired := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, "shop:notifications:leader", s.instanceID, shopNotificationLease)
	if !acquired {
		return
	}
	defer release()
	if err := s.shop.RecoverNotifications(leaseCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("shop notification recovery incomplete", "error_kind", "STORE_UNAVAILABLE")
	}
}
