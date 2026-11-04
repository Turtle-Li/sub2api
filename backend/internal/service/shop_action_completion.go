package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

func (s *ShopService) completeShopAction(ctx context.Context, c *shopOrderActionClaim, result shopaction.Result) error {
	if !runtimegate.SharedWorkAllowed() || ctx.Err() != nil {
		return context.Canceled
	}
	ciphertext := ""
	var err error
	if result.Delivery.Kind == "text" {
		ciphertext, err = s.encrypt(c.row.OrderID, "delivery", 1, result.Delivery.Content)
		if err != nil {
			return err
		}
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(ctx, client, c.row.OrderID)
	if err != nil {
		return err
	}
	f, err := lockShopFulfillment(ctx, client, o.ID)
	if err != nil {
		return err
	}
	row, err := lockShopOrderAction(ctx, client, o.ID)
	if err != nil {
		return err
	}
	if !ownedShopAction(row, c) || row.Status != shopaction.Running {
		return errShopActionLeaseLost
	}
	fence := ensureShopFinancialFence(ctx, client, o.ID)
	if fence != nil && infraerrors.Reason(fence) != "SHOP_FINANCIAL_REVIEW" {
		return fence
	}
	reason := ""
	if !shopPaidActionAllowed(o) || fence != nil || o.PaidAt == nil || !o.PaidAt.Equal(c.snapshot.Request.Order.PaidAt) || f.Status != ShopProcessing {
		reason = "FINANCIAL_FENCE"
	} else if !time.Now().Before(row.DeadlineAt) {
		reason = "DELIVERY_EXPIRED"
	}
	if reason != "" {
		if err = client.ShopOrderAction.UpdateOne(row).SetStatus(shopaction.Compensating).SetCompensationReason(reason).SetErrorKind(reason).SetReceiptRef(result.ReceiptID).Exec(ctx); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		c.row.Status, c.row.CompensationReason = shopaction.Compensating, reason
		return errShopActionCompensate
	}
	now := time.Now()
	f, err = client.ShopFulfillment.UpdateOne(f).SetStatus(ShopCompleted).SetDeliveryCiphertext(ciphertext).SetCompletedAt(now).
		SetStatusNote("").ClearInputCiphertext().ClearProcessedBy().AddRevision(1).Save(ctx)
	if err != nil {
		return err
	}
	if err = client.PaymentOrder.UpdateOne(o).SetStatus(OrderStatusCompleted).SetCompletedAt(now).Exec(ctx); err != nil {
		return err
	}
	if err = client.ShopOrderAction.UpdateOne(row).Where(shoporderaction.ClaimRevisionEQ(c.row.ClaimRevision)).SetStatus(shopaction.Succeeded).
		SetResultKind(result.Delivery.Kind).SetReceiptRef(result.ReceiptID).SetCompletedAt(now).SetErrorKind("").ClearLeaseUntil().Exec(ctx); err != nil {
		return err
	}
	if err = queueShopNotification(ctx, client, f, "complete", "email"); err != nil {
		return err
	}
	if err = writePaymentAuditTx(ctx, client, o.ID, "SHOP_ACTION_COMPLETE_"+row.ActionKey, "system:shop-actions", map[string]any{"action_id": row.ActionKey, "receipt_ref": result.ReceiptID}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ShopService) finishShopRevocation(ctx context.Context, c *shopOrderActionClaim) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	client := tx.Client()
	if _, err = lockUnifiedRefundOrder(ctx, client, c.row.OrderID); err != nil {
		return err
	}
	f, err := lockShopFulfillment(ctx, client, c.row.OrderID)
	if err != nil {
		return err
	}
	row, err := lockShopOrderAction(ctx, client, c.row.OrderID)
	if err != nil {
		return err
	}
	if !ownedShopAction(row, c) || row.Status != shopaction.Compensating {
		return errShopActionLeaseLost
	}
	if err = client.ShopOrderAction.UpdateOne(row).SetStatus(shopaction.Revoked).SetCompletedAt(time.Now()).ClearLeaseUntil().SetErrorKind("").Exec(ctx); err != nil {
		return err
	}
	if err = client.ShopFulfillment.UpdateOne(f).SetStatus(ShopClosed).SetStatusNote("此订单的授权已撤销，请联系客服核对售后。").AddRevision(1).Exec(ctx); err != nil {
		return err
	}
	if err = writePaymentAuditTx(ctx, client, row.OrderID, fmt.Sprintf("SHOP_ACTION_REVOKED_%s", row.ActionKey), "system:shop-actions", map[string]any{"action_id": row.ActionKey}); err != nil {
		return err
	}
	return tx.Commit()
}
