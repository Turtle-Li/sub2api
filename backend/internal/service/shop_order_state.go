package service

import (
	"context"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func createShopFulfillmentTx(ctx context.Context, client *dbent.Client, o *dbent.PaymentOrder, p ShopProduct) error {
	snapshot, err := shopProductMap(p)
	if err != nil {
		return err
	}
	_, err = client.ShopFulfillment.Create().SetOrderID(o.ID).SetUserID(o.UserID).SetProductID(p.ID).SetProductSnapshot(snapshot).Save(ctx)
	return err
}

func lockShopFulfillment(ctx context.Context, client *dbent.Client, orderID int64) (*dbent.ShopFulfillment, error) {
	q := client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(orderID))
	if client.Driver().Dialect() == dialect.Postgres {
		q.ForUpdate()
	}
	f, err := q.Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, infraerrors.NotFound("SHOP_ORDER_NOT_FOUND", "商城订单不存在")
	}
	if err != nil {
		return nil, fmt.Errorf("lock shop fulfillment: %w", err)
	}
	return f, nil
}

func queueShopNotification(ctx context.Context, client *dbent.Client, f *dbent.ShopFulfillment, event, channel string) error {
	return client.ShopNotification.Create().SetOrderID(f.OrderID).SetEvent(event).SetRevision(f.Revision).SetChannel(channel).OnConflict().DoNothing().Exec(ctx)
}

// markShopPaidTx runs under the payment row lock in the payment-confirmation transaction.
func markShopPaidTx(ctx context.Context, client *dbent.Client, o *dbent.PaymentOrder) error {
	f, err := lockShopFulfillment(ctx, client, o.ID)
	if err != nil {
		return err
	}
	if f.Status != ShopWaitingPayment {
		return nil
	}
	p, err := shopSnapshot(f)
	snapshotErr := err
	state := ShopQueued
	if snapshotErr == nil && p.FulfillmentMode == ShopSessionTopup {
		state = ShopWaitingInput
	}
	f, err = client.ShopFulfillment.UpdateOne(f).SetStatus(state).AddRevision(1).Save(ctx)
	if err != nil {
		return fmt.Errorf("queue paid shop order: %w", err)
	}
	if err := enqueueShopOrderActionTx(ctx, client, o, f); err != nil {
		return err
	}
	return queueShopNotification(ctx, client, f, "paid", "feishu")
}

func shopPaidActionAllowed(o *dbent.PaymentOrder) bool {
	return o.OrderType == payment.OrderTypeManualShop && o.PaidAt != nil && o.Status == OrderStatusPaid
}

func shopEffectiveStatus(o *dbent.PaymentOrder, f *dbent.ShopFulfillment) string {
	if !shopPaidActionAllowed(o) && o.Status != OrderStatusCompleted && (o.PaidAt != nil || o.Status != OrderStatusPending) {
		return ShopClosed
	}
	return f.Status
}

// Apply the same payment fence as shopEffectiveStatus before counting or paging.
func shopStatusFilter(status string) predicate.ShopFulfillment {
	return func(s *sql.Selector) {
		p := sql.Table(paymentorder.Table)
		preserved := sql.Select(p.C(paymentorder.FieldID)).From(p).
			Where(sql.ColumnsEQ(p.C(paymentorder.FieldID), s.C(shopfulfillment.FieldOrderID))).
			Where(sql.Or(
				sql.EQ(p.C(paymentorder.FieldStatus), OrderStatusCompleted),
				sql.And(sql.EQ(p.C(paymentorder.FieldOrderType), payment.OrderTypeManualShop), sql.EQ(p.C(paymentorder.FieldStatus), OrderStatusPaid), sql.NotNull(p.C(paymentorder.FieldPaidAt))),
				sql.And(sql.EQ(p.C(paymentorder.FieldStatus), OrderStatusPending), sql.IsNull(p.C(paymentorder.FieldPaidAt))),
			))
		if status == ShopClosed {
			s.Where(sql.Or(sql.EQ(s.C(shopfulfillment.FieldStatus), ShopClosed), sql.Not(sql.Exists(preserved))))
		} else {
			s.Where(sql.And(sql.EQ(s.C(shopfulfillment.FieldStatus), status), sql.Exists(preserved)))
		}
	}
}

func ensureShopFinancialFence(ctx context.Context, client *dbent.Client, orderID int64) error {
	held, err := unifiedRefundOrderNeedsReview(ctx, client, orderID)
	if err != nil {
		return fmt.Errorf("read shop financial review fence: %w", err)
	}
	if held {
		return infraerrors.Conflict("SHOP_FINANCIAL_REVIEW", "订单存在支付或退款复核，请先核对资金状态")
	}
	return nil
}
