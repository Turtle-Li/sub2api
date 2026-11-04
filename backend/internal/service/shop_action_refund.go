package service

import (
	"context"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

// This changes the operator's warning, never the manual-only refund boundary.
func shopManualRefundReview(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, now time.Time) (*RefundReview, error) {
	review := manualRefundReview(order, now, "SHOP_REFUND_MANUAL_ONLY", "商城订单需要人工售后核对交付，不能使用余额或订阅退款")
	task, err := client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(order.ID)).Only(ctx)
	if dbent.IsNotFound(err) {
		return review, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeShopAction(task)
	if err != nil || snapshot.Binding.Adapter != shopaction.Native {
		return review, nil
	}
	if task.Status != shopaction.Revoked && task.Status != shopaction.Cancelled {
		review.ReasonCode = "SHOP_REFUND_REVOKE_REQUIRED"
		review.Reason = "此订单的自动授权尚未确认撤销。请先打开商城订单撤销授权，确认显示“授权已撤销”后再人工核对退款。撤销不会自动退款。"
	}
	return review, nil
}
