package service

import (
	"context"
	"errors"
	"log/slog"
)

func BeginPaidFallbackAttempt(ctx context.Context) (func(int, error), error) {
	permit, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
	if permit == nil {
		return func(int, error) {}, nil
	}
	if !permit.settings.paidFallbackAllowed(WithPaidFallbackAPIKey(ctx, permit.apiKeyID), &permit.groupID) {
		return nil, errors.New("paid fallback admission revoked")
	}
	slog.WarnContext(ctx, "paid_fallback_upstream_attempt", "group_id", permit.groupID, "api_key_id", permit.apiKeyID, "account_id", permit.accountID, "cost_known", false)
	return func(status int, err error) {
		slog.WarnContext(ctx, "paid_fallback_upstream_result", "group_id", permit.groupID, "account_id", permit.accountID, "status", status, "transport_failed", err != nil, "cost_known", false)
	}, nil
}

func RecordPaidFallbackWSAdmission(ctx context.Context, accountID int64, turn int) {
	permit, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
	if permit != nil && permit.accountID == accountID {
		slog.WarnContext(ctx, "paid_fallback_websocket_turn_admitted", "group_id", permit.groupID, "account_id", accountID, "turn", turn, "cost_known", false)
	}
}
