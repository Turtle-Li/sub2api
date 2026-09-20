package service

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func installRefundAccountingSQLiteTables(t *testing.T, client *dbent.Client) {
	t.Helper()
	_, err := client.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS payment_wallet_fundings (
			payment_order_id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			paid_credit_amount NUMERIC NOT NULL,
			gift_credit_amount NUMERIC NOT NULL DEFAULT 0,
			cash_paid_minor INTEGER NOT NULL,
			currency TEXT NOT NULL,
			refunded_paid_amount NUMERIC NOT NULL DEFAULT 0,
			reclaimed_gift_amount NUMERIC NOT NULL DEFAULT 0,
			refunded_cash_minor INTEGER NOT NULL DEFAULT 0,
			reserved_paid_amount NUMERIC NOT NULL DEFAULT 0,
			reserved_gift_amount NUMERIC NOT NULL DEFAULT 0,
			reserved_cash_minor INTEGER NOT NULL DEFAULT 0,
			version INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS payment_subscription_grants (
			payment_order_id INTEGER PRIMARY KEY,
			subscription_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			group_id INTEGER NOT NULL,
			term_start_at TIMESTAMP NOT NULL,
			original_term_end_at TIMESTAMP NOT NULL,
			current_term_end_at TIMESTAMP NOT NULL,
			refunded_seconds INTEGER NOT NULL DEFAULT 0,
			reserved_seconds INTEGER NOT NULL DEFAULT 0,
			refunded_cash_minor INTEGER NOT NULL DEFAULT 0,
			reserved_cash_minor INTEGER NOT NULL DEFAULT 0,
			balance_bonus NUMERIC NOT NULL DEFAULT 0,
			reset_card_count INTEGER NOT NULL DEFAULT 0,
			concurrency_target INTEGER NOT NULL DEFAULT 0,
			version INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	require.NoError(t, err)
}

func installUnifiedRefundAccountingSQLiteColumns(t *testing.T, client *dbent.Client) {
	t.Helper()
	for _, statement := range []string{
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN reason_code TEXT NOT NULL DEFAULT 'other'`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN refund_kind TEXT NOT NULL DEFAULT 'legacy_balance'`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN quote_revision TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN benefit_proof_digest TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN wallet_paid_amount NUMERIC NOT NULL DEFAULT 0`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN wallet_gift_amount NUMERIC NOT NULL DEFAULT 0`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN subscription_seconds INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN subscription_grant_order_id INTEGER NULL`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN entitlement_reserved BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN valuation_at TIMESTAMP NULL`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN provider_status TEXT NULL`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN failure_code TEXT NULL`,
		`ALTER TABLE unified_payment_refund_attempts ADD COLUMN provider_updated_at TIMESTAMP NULL`,
	} {
		_, err := client.ExecContext(context.Background(), statement)
		require.NoError(t, err)
	}
}
