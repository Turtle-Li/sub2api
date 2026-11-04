package service

import (
	"context"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shopnotification"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

func (s *ShopService) Order(ctx context.Context, orderID, userID int64, admin bool) (*ShopOrderDetail, error) {
	q := s.client.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID), paymentorder.OrderTypeEQ(payment.OrderTypeManualShop))
	if !admin {
		q.Where(paymentorder.UserIDEQ(userID))
	}
	o, err := q.Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, infraerrors.NotFound("SHOP_ORDER_NOT_FOUND", "商城订单不存在")
	}
	if err != nil {
		return nil, fmt.Errorf("get shop order: %w", err)
	}
	f, err := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("get shop fulfillment: %w", err)
	}
	detail, err := s.orderDTO(ctx, o, f, admin, !admin)
	if err != nil {
		return nil, err
	}
	if p := detail.Product; p.InputFormat == "session_json" && f.InputCiphertext != "" && f.InputSubmittedAt != nil {
		content, err := s.decrypt(o.ID, "input", f.InputRevision, f.InputCiphertext)
		if err != nil {
			return nil, err
		}
		// Reconstruct the submission-time hints; expiry remains visible to the operator.
		detail.InputCheck, _ = inspectShopSession(content, *f.InputSubmittedAt)
	}
	return detail, nil
}

func (s *ShopService) orderDTO(ctx context.Context, o *dbent.PaymentOrder, f *dbent.ShopFulfillment, admin, includeDelivery bool) (*ShopOrderDetail, error) {
	p, err := shopSnapshot(f)
	snapshotValid := err == nil
	if !snapshotValid {
		p = ShopProduct{ID: f.ProductID, Name: "商品配置待复核", Currency: "CNY", Price: o.PayAmount, FulfillmentMode: ShopAccountDelivery}
	}
	state := shopEffectiveStatus(o, f)
	active := shopPaidActionAllowed(o)
	if active {
		if err := ensureShopFinancialFence(ctx, s.client, o.ID); err != nil {
			active = false
		}
	}
	canSubmit := active && p.FulfillmentMode == ShopSessionTopup && (state == ShopWaitingInput || state == ShopNeedsInput || state == ShopQueued)
	canWork := snapshotValid && active && !shopAutomaticFulfillment(p) && (state == ShopQueued || state == ShopProcessing) && (p.FulfillmentMode == ShopAccountDelivery || f.InputCiphertext != "")
	d := &ShopOrderDetail{OrderID: o.ID, OutTradeNo: o.OutTradeNo, Product: p, UserID: o.UserID, PayAmount: o.PayAmount, Currency: "CNY", PaymentStatus: o.Status, FulfillmentStatus: state, Revision: f.Revision, InputRevision: f.InputRevision, InputAccount: f.InputAccount, InputSubmittedAt: f.InputSubmittedAt, CreatedAt: o.CreatedAt, PaidAt: o.PaidAt, CompletedAt: f.CompletedAt, StatusNote: f.StatusNote, EmailStatus: "NOT_SENT", AdminNotificationStatus: "NOT_SENT", CanSubmit: canSubmit, CanStart: admin && canWork && state == ShopQueued, CanComplete: admin && canWork, CanRevealInput: admin && canWork && p.FulfillmentMode == ShopSessionTopup}
	if admin {
		d.UserEmail = o.UserEmail
	}
	if err = s.attachShopAutomation(ctx, d, admin); err != nil {
		return nil, err
	}
	if !admin {
		automatic := d.Product.AutomaticFulfillment
		d.Product = publicShopProduct(d.Product)
		d.Product.AutomaticFulfillment = automatic
	}
	if d.Automation != nil && d.Automation.Status != shopaction.Succeeded {
		includeDelivery = false
	}
	if includeDelivery && f.Status == ShopCompleted && o.Status == OrderStatusCompleted && f.DeliveryCiphertext != "" {
		d.DeliveryContent, err = s.decrypt(o.ID, "delivery", 1, f.DeliveryCiphertext)
		if err != nil {
			return nil, err
		}
	}
	jobs, err := s.client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID)).Order(dbent.Desc(shopnotification.FieldID)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("load shop notification status: %w", err)
	}
	for _, j := range jobs {
		if j.Channel == "email" && d.EmailStatus == "NOT_SENT" {
			d.EmailStatus = j.Status
		}
		if j.Channel == "feishu" && d.AdminNotificationStatus == "NOT_SENT" {
			d.AdminNotificationStatus = j.Status
		}
	}
	return d, nil
}

func (s *ShopService) Orders(ctx context.Context, page, pageSize int, status string) (*ShopOrderList, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	q := s.client.ShopFulfillment.Query()
	if status != "" {
		q.Where(shopStatusFilter(status))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count shop orders: %w", err)
	}
	rows, err := q.Order(dbent.Desc(shopfulfillment.FieldCreatedAt), dbent.Desc(shopfulfillment.FieldID)).Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list shop orders: %w", err)
	}
	out := &ShopOrderList{Items: make([]ShopOrderDetail, 0, len(rows)), Total: total, Page: page, PageSize: pageSize}
	for _, f := range rows {
		o, err := s.client.PaymentOrder.Get(ctx, f.OrderID)
		if err != nil {
			return nil, fmt.Errorf("load shop payment: %w", err)
		}
		d, err := s.orderDTO(ctx, o, f, true, false)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, *d)
	}
	return out, nil
}
