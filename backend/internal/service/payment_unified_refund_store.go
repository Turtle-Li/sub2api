package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/google/uuid"
)

const unifiedRefundPending = "PENDING"

// The attempt is written before HTTP and finalized under the same order lock as
// every other attempt. Raw SQL avoids making the financial correlation table a
// publicly editable provider/order DTO. The additive migration owns its schema.
type unifiedRefundAttempt struct {
	ProductRefundNo, PaymentOrderID, IdempotencyKey                              string
	OrderID                                                                      int64
	Environment, OrganizationID, ProductID, AppID, PaymentMethod                 string
	AmountFen, BalanceAmountMinor                                                int64
	DeductBalance, Force                                                         bool
	ReasonSummary, Status, RefundRequestID, ChannelOutRefundNo, ProviderRefundID string
	NeedsManualReview                                                            bool
}

func lockUnifiedRefundOrder(ctx context.Context, client *dbent.Client, id int64) (*dbent.PaymentOrder, error) {
	query := client.PaymentOrder.Query().Where(paymentorder.IDEQ(id))
	if paymentAuditDialect(client) == dialect.Postgres {
		query.ForUpdate()
	}
	return query.Only(ctx)
}

func loadUnifiedRefundAttempt(ctx context.Context, client *dbent.Client, orderID int64, refundNo string) (*unifiedRefundAttempt, error) {
	query := `SELECT product_refund_no, order_id, payment_order_id, idempotency_key,
	 environment, organization_id, product_id, app_id, payment_method, amount_fen,
	 balance_amount_minor, deduct_balance, force_refund, reason_summary, status,
	 COALESCE(CAST(refund_request_id AS TEXT), ''), COALESCE(channel_out_refund_no, ''),
	 COALESCE(provider_refund_id, ''), needs_manual_review
	 FROM unified_payment_refund_attempts WHERE order_id = $1`
	args := []any{orderID}
	if refundNo != "" {
		query += ` AND product_refund_no = $2`
		args = append(args, refundNo)
	} else {
		query += ` ORDER BY CASE WHEN status = 'PENDING' THEN 0 ELSE 1 END, created_at DESC, product_refund_no DESC LIMIT 1`
	}
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	a := &unifiedRefundAttempt{}
	err = rows.Scan(&a.ProductRefundNo, &a.OrderID, &a.PaymentOrderID, &a.IdempotencyKey,
		&a.Environment, &a.OrganizationID, &a.ProductID, &a.AppID, &a.PaymentMethod,
		&a.AmountFen, &a.BalanceAmountMinor, &a.DeductBalance, &a.Force, &a.ReasonSummary,
		&a.Status, &a.RefundRequestID, &a.ChannelOutRefundNo, &a.ProviderRefundID, &a.NeedsManualReview)
	return a, err
}

func unifiedRefundOrderNeedsReview(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	rows, err := client.QueryContext(ctx, `SELECT
	 (SELECT COUNT(*) FROM unified_payment_refund_attempts WHERE order_id = $1 AND needs_manual_review = TRUE)
	 + (SELECT COUNT(*) FROM unified_payment_refund_events WHERE order_id = $1
	    AND action IN ('UNIFIED_REFUND_UNCORRELATED', 'UNIFIED_PAYMENT_EVENT_REJECTED'))`, orderID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return false, errors.New("refund review state unavailable")
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, rows.Err()
}

func insertUnifiedRefundAttempt(ctx context.Context, client *dbent.Client, a *unifiedRefundAttempt) error {
	_, err := client.ExecContext(ctx, `INSERT INTO unified_payment_refund_attempts
	 (product_refund_no, order_id, payment_order_id, idempotency_key, environment,
	 organization_id, product_id, app_id, payment_method, amount_fen, balance_amount_minor,
	 deduct_balance, force_refund, reason_summary, status)
	 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		a.ProductRefundNo, a.OrderID, a.PaymentOrderID, a.IdempotencyKey, a.Environment,
		a.OrganizationID, a.ProductID, a.AppID, a.PaymentMethod, a.AmountFen, a.BalanceAmountMinor,
		a.DeductBalance, a.Force, a.ReasonSummary, a.Status)
	return err
}

func saveUnifiedRefundAttempt(ctx context.Context, client *dbent.Client, a *unifiedRefundAttempt) error {
	_, err := client.ExecContext(ctx, `UPDATE unified_payment_refund_attempts SET
	 status=$2, refund_request_id=$3, channel_out_refund_no=$4, provider_refund_id=$5,
	 needs_manual_review=$6, updated_at=CURRENT_TIMESTAMP WHERE product_refund_no=$1`,
		a.ProductRefundNo, a.Status, nullableUnifiedRefundID(a.RefundRequestID),
		nullableUnifiedRefundID(a.ChannelOutRefundNo), nullableUnifiedRefundID(a.ProviderRefundID), a.NeedsManualReview)
	return err
}

func nullableUnifiedRefundID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func writeUnifiedRefundAudit(ctx context.Context, client *dbent.Client, orderID int64, action string, detail map[string]any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = client.ExecContext(ctx, `INSERT INTO unified_payment_refund_events (id, order_id, action, detail) VALUES ($1,$2,$3,$4)`, uuid.NewString(), orderID, action, string(encoded))
	return err
}
