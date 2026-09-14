package server

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CompareAndSetReviewedRefunds is the narrow operator transition. Host topology
// is proved by the maintenance-lock-owning rollout helper; this process proves
// admission and financial quiescence again inside the serialized transaction.
func (s *HealthService) CompareAndSetReviewedRefunds(ctx context.Context, expected string, enabled bool) (bool, error) {
	if (enabled && expected != "" && expected != "false") || (!enabled && expected != "true") {
		return false, errors.New("invalid refund rollout transition")
	}
	if s == nil || s.db == nil || s.trafficStateFile == "" || strings.TrimSpace(os.Getenv(runtimegate.StateFileEnv)) == "" {
		return false, errors.New("explicit runtime state required")
	}
	eligible := func() bool {
		if enabled {
			return runtimegate.SharedWorkAllowed() && s.Ready(ctx)
		}
		// A planned node drain also parks background claims in standby.
		// Durable recovery still runs there; disabling creates no new work.
		if !runtimegate.DurableRecoveryWorkAllowed() {
			return false
		}
		state, err := os.ReadFile(s.trafficStateFile)
		return err == nil && strings.TrimSpace(string(state)) == "draining" && s.InFlightRequests() == 0
	}
	if !eligible() {
		return false, errors.New("refund rollout runtime not eligible")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", service.ReviewedRefundRolloutLockID); err != nil {
		return false, err
	}
	if !eligible() {
		return false, errors.New("refund rollout runtime changed")
	}
	var pending int64
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_payment_refund_attempts
  WHERE entitlement_reserved = TRUE AND quote_revision <> '' AND refund_kind IN ('balance', 'subscription')`).Scan(&pending)
	if err != nil {
		return false, err
	}
	if pending != 0 {
		return false, errors.New("reviewed refund reservations remain")
	}
	value := "false"
	if enabled {
		value = "true"
	}
	query := `UPDATE settings SET value = $3, updated_at = NOW() WHERE key = $1 AND value = $2`
	args := []any{service.SettingPaymentReviewedRefundsEnabled, expected, value}
	if expected == "" {
		query = `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW()) ON CONFLICT (key) DO NOTHING`
		args = []any{service.SettingPaymentReviewedRefundsEnabled, value}
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
