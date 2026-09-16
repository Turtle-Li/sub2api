//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestUnifiedRefundRecoveryMigrationSkipsMalformedHistoricalEvidence(t *testing.T) {
	ctx := context.Background()
	schema := fmt.Sprintf("refund_recovery_%d", time.Now().UnixNano())
	_, err := integrationDB.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
	})

	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO "`+schema+`"`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE unified_payment_refund_attempts (
			product_refund_no TEXT PRIMARY KEY,
			order_id BIGINT NOT NULL,
			refund_request_id UUID
		);
		CREATE TABLE unified_payment_refund_events (
			id UUID PRIMARY KEY,
			order_id BIGINT NOT NULL,
			action VARCHAR(50) NOT NULL,
			detail TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		);
	`)
	require.NoError(t, err)

	const refundNo = "sub2-refund-80d5f249-1a39-4b87-b86e-95fb3d46a90e"
	const requestID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	_, err = tx.ExecContext(ctx, `INSERT INTO unified_payment_refund_attempts
		(product_refund_no, order_id, refund_request_id) VALUES ($1, 4, $2)`, refundNo, requestID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO unified_payment_refund_events
		(id, order_id, action, detail, created_at) VALUES
		('11111111-1111-4111-8111-111111111111', 4, 'UNIFIED_REFUND_RESULT', $1, '2026-09-16T03:01:00Z'),
		('22222222-2222-4222-8222-222222222222', 4, 'UNIFIED_REFUND_RESULT', 'not-json', '2026-09-16T03:02:00Z'),
		('33333333-3333-4333-8333-333333333333', 4, 'UNIFIED_REFUND_RESULT', $2, '2026-09-16T03:03:00Z')`,
		`{"product_refund_no":"`+refundNo+`","refund_request_id":"`+requestID+`","provider_status":"HTTP_403_NOT_ENOUGH","failure_code":"refund_submit_provider_rejected","updated_at":"infinity"}`,
		`{"x":"\u0000"}`)
	require.NoError(t, err)

	migration, err := migrations.FS.ReadFile("251_unified_refund_recovery.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)

	var providerStatus, failureCode string
	var providerUpdatedAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT provider_status, failure_code, provider_updated_at
		FROM unified_payment_refund_attempts WHERE product_refund_no = $1`, refundNo).
		Scan(&providerStatus, &failureCode, &providerUpdatedAt)
	require.NoError(t, err)
	require.Equal(t, "HTTP_403_NOT_ENOUGH", providerStatus)
	require.Equal(t, "refund_submit_provider_rejected", failureCode)
	require.Equal(t, time.Date(2026, time.September, 16, 3, 1, 0, 0, time.UTC), providerUpdatedAt.UTC())
}
