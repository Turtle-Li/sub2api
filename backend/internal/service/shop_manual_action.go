package service

import (
	"context"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *ShopService) Action(ctx context.Context, orderID, adminID int64, in ShopActionInput) (*ShopOrderDetail, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if len(in.Note) > 2000 || len(in.DeliveryContent) > 32*1024 || !utf8.ValidString(in.Note+in.DeliveryContent) || strings.ContainsRune(in.Note+in.DeliveryContent, 0) {
		return nil, infraerrors.BadRequest("SHOP_ACTION_INVALID", "备注或交付内容过长或格式无效")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	f, err := lockShopFulfillment(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	if f.Revision != in.Revision {
		return nil, shopConflict()
	}
	if !shopPaidActionAllowed(o) {
		return nil, infraerrors.Conflict("SHOP_PAYMENT_NOT_ACTIONABLE", "当前支付状态不允许交付")
	}
	if err := ensureShopFinancialFence(ctx, client, o.ID); err != nil {
		return nil, err
	}
	p, err := shopSnapshot(f)
	if err != nil {
		return nil, err
	}
	if shopAutomaticFulfillment(p) {
		return nil, infraerrors.Conflict("SHOP_AUTOMATIC_DELIVERY", "此商品由系统自动交付，请勿人工重复发货")
	}
	if f.Status != ShopQueued && f.Status != ShopProcessing {
		return nil, infraerrors.Conflict("SHOP_STATE_INVALID", "当前订单不允许此操作")
	}
	b := client.ShopFulfillment.UpdateOne(f).Where(shopfulfillment.RevisionEQ(in.Revision)).SetProcessedBy(adminID).AddRevision(1)
	switch in.Action {
	case "start":
		if f.Status != ShopQueued {
			return nil, shopConflict()
		}
		if p.FulfillmentMode == ShopSessionTopup && f.InputCiphertext == "" {
			return nil, infraerrors.Conflict("SHOP_INPUT_REQUIRED", "用户尚未提交资料")
		}
		b.SetStatus(ShopProcessing)
	case "request_input":
		if p.FulfillmentMode != ShopSessionTopup || strings.TrimSpace(in.Note) == "" {
			return nil, infraerrors.BadRequest("SHOP_NOTE_REQUIRED", "请填写需要用户补充的内容")
		}
		b.SetStatus(ShopNeedsInput).SetStatusNote(strings.TrimSpace(in.Note)).ClearInputCiphertext().SetInputAccount("").ClearInputSubmittedAt().AddInputRevision(1)
	case "complete":
		if p.FulfillmentMode == ShopSessionTopup {
			if f.InputCiphertext == "" || f.InputRevision != in.InputRevision {
				return nil, shopConflict()
			}
			content, err := s.decrypt(o.ID, "input", f.InputRevision, f.InputCiphertext)
			if err != nil {
				return nil, err
			}
			// Completion records work already performed. A token expiring after
			// submission must not prevent the operator from recording that result.
			if p.InputFormat == "session_json" && f.InputSubmittedAt != nil {
				if _, err = inspectShopSession(content, *f.InputSubmittedAt); err != nil {
					return nil, err
				}
			}
		} else {
			if strings.TrimSpace(in.DeliveryContent) == "" {
				return nil, infraerrors.BadRequest("SHOP_DELIVERY_REQUIRED", "请填写账号交付信息")
			}
			ciphertext, err := s.encrypt(o.ID, "delivery", 1, in.DeliveryContent)
			if err != nil {
				return nil, err
			}
			b.SetDeliveryCiphertext(ciphertext)
		}
		now := time.Now()
		b.SetStatus(ShopCompleted).SetCompletedAt(now).SetStatusNote(strings.TrimSpace(in.Note)).ClearInputCiphertext()
		if _, err = client.PaymentOrder.UpdateOne(o).Where(paymentorder.StatusEQ(OrderStatusPaid)).SetStatus(OrderStatusCompleted).SetCompletedAt(now).Save(ctx); err != nil {
			return nil, fmt.Errorf("complete shop payment: %w", err)
		}
	default:
		return nil, infraerrors.BadRequest("SHOP_ACTION_INVALID", "不支持的订单操作")
	}
	f, err = b.Save(ctx)
	if dbent.IsNotFound(err) {
		return nil, shopConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("update shop fulfillment: %w", err)
	}
	if in.Action == "complete" || in.Action == "request_input" {
		if err = queueShopNotification(ctx, client, f, in.Action, "email"); err != nil {
			return nil, err
		}
	}
	if err = writePaymentAuditTx(ctx, client, o.ID, fmt.Sprintf("SHOP_%s_%d", strings.ToUpper(in.Action), f.Revision), fmt.Sprintf("admin:%d", adminID), map[string]any{"revision": f.Revision, "input_revision": f.InputRevision}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit shop action: %w", err)
	}
	return s.Order(ctx, orderID, 0, true)
}
