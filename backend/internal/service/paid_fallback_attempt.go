package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
)

type paidFallbackPublicModelKey struct{}
type paidFallbackModelSendKey struct{}
type paidFallbackUpstreamModelKey struct{}
type paidFallbackBridgeTurnKey struct{}

func WithPaidFallbackPublicModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, paidFallbackPublicModelKey{}, strings.TrimSpace(model))
}

func WithPaidFallbackModelSend(ctx context.Context) context.Context {
	return context.WithValue(ctx, paidFallbackModelSendKey{}, true)
}

func WithPaidFallbackBridgeTurn(ctx context.Context, model string, turn int) context.Context {
	return context.WithValue(WithPaidFallbackPublicModel(ctx, model), paidFallbackBridgeTurnKey{}, turn)
}

func BeginPaidFallbackHTTPAttempt(ctx context.Context, accountID int64) (func(int, error), error) {
	marked, _ := ctx.Value(paidFallbackModelSendKey{}).(bool)
	if !marked {
		if permit, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit); permit != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if accountID != permit.accountID {
				return nil, errors.New("paid fallback send account mismatch")
			}
			if !permit.settings.paidFallbackAllowed(WithPaidFallbackAPIKey(ctx, permit.apiKeyID), &permit.groupID) {
				return nil, errors.New("paid fallback admission revoked")
			}
		}
		return func(int, error) {}, nil
	}
	transport, turn := "http", 0
	if value, ok := ctx.Value(paidFallbackBridgeTurnKey{}).(int); ok {
		transport, turn = "ws_http_bridge", value
	}
	model, _ := ctx.Value(paidFallbackPublicModelKey{}).(string)
	upstream, _ := ctx.Value(paidFallbackUpstreamModelKey{}).(string)
	return beginPaidFallbackSend(ctx, accountID, turn, model, upstream, transport)
}

func BeginPaidFallbackWSSend(ctx context.Context, accountID int64, turn int, publicModel, upstreamModel, transport string) (func(int, error), error) {
	return beginPaidFallbackSend(ctx, accountID, turn, publicModel, upstreamModel, transport)
}

func beginPaidFallbackSend(ctx context.Context, accountID int64, turn int, publicModel, upstreamModel, transport string) (func(int, error), error) {
	permit, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
	if permit == nil {
		return func(int, error) {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if permit.accountID != accountID {
		return nil, errors.New("paid fallback send account mismatch")
	}
	if !permit.settings.paidFallbackAllowed(WithPaidFallbackAPIKey(ctx, permit.apiKeyID), &permit.groupID) {
		return nil, errors.New("paid fallback admission revoked")
	}
	store := permit.settings.paidFallbackOpsStore
	if store == nil {
		return nil, errors.New("paid fallback audit store unavailable")
	}
	publicModel = strings.TrimSpace(publicModel)
	if publicModel == "" {
		return nil, errors.New("paid fallback public model missing")
	}
	if !paidFallbackStillRequired(ctx, permit, turn, publicModel) {
		return nil, paidFallbackRestartError(transport)
	}
	inbound, _ := ctx.Value(ctxkey.ClientRequestID).(string)
	if inbound == "" {
		inbound = uuid.NewString()
	}
	id := uuid.NewString()
	forwardModel := permit.request.RequestedModel
	if transport != "http" {
		forwardModel = ""
	}
	writeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	err := store.StartAttempt(writeCtx, PaidFallbackAttempt{ID: id, InboundRequestID: inbound, APIKeyID: permit.apiKeyID, AccountID: accountID, GroupID: permit.groupID, PublicModel: publicModel, ForwardModel: forwardModel, UpstreamModel: upstreamModel, Transport: transport, Turn: turn})
	cancel()
	if err != nil {
		finishUnsentPaidFallbackAttempt(store, id)
		return nil, errors.New("paid fallback attempt could not be persisted")
	}
	if err := ctx.Err(); err != nil {
		finishUnsentPaidFallbackAttempt(store, id)
		return nil, err
	}
	if !permit.settings.paidFallbackAllowed(WithPaidFallbackAPIKey(ctx, permit.apiKeyID), &permit.groupID) {
		finishUnsentPaidFallbackAttempt(store, id)
		return nil, errors.New("paid fallback admission revoked")
	}
	if !paidFallbackStillRequired(ctx, permit, turn, publicModel) {
		finishUnsentPaidFallbackAttempt(store, id)
		return nil, paidFallbackRestartError(transport)
	}
	slog.WarnContext(ctx, "paid_fallback_upstream_attempt", "group_id", permit.groupID, "api_key_id", permit.apiKeyID, "account_id", accountID, "attempt_id", id, "transport", transport, "cost_known", false)
	var once sync.Once
	return func(status int, sendErr error) {
		once.Do(func() {
			slog.WarnContext(ctx, "paid_fallback_upstream_result", "group_id", permit.groupID, "account_id", accountID, "attempt_id", id, "status", status, "transport_failed", sendErr != nil, "cost_known", false)
			outcome := "http_headers"
			if strings.HasPrefix(transport, "ws_") && transport != "ws_http_bridge" {
				outcome = "ws_frame_written"
			}
			if sendErr != nil {
				outcome = "unknown"
			}
			go func() {
				resultCtx, resultCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer resultCancel()
				if err := store.FinishAttempt(resultCtx, id, outcome, status); err != nil {
					slog.Warn("paid_fallback_result_write_failed", "attempt_id", id)
				}
			}()
		})
	}, nil
}

func paidFallbackRestartError(transport string) error {
	if strings.HasPrefix(transport, "ws_") {
		return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "paid fallback is no longer eligible; reconnect with full conversation history", ErrPaidFallbackRestartRequired)
	}
	return ErrPaidFallbackRestartRequired
}

func paidFallbackStillRequired(ctx context.Context, permit *paidFallbackPermit, turn int, publicModel string) bool {
	if permit.gateway == nil {
		return false
	}
	req := permit.request
	if turn > 1 {
		req.ExcludedIDs = nil
		model := publicModel
		if resolved, ok := ResolvedUpstreamModelFromContext(ctx); ok {
			model = resolved
		}
		mapping, _ := permit.gateway.ResolveChannelMappingAndRestrict(ctx, req.GroupID, model)
		req.RequestedModel = mapping.MappedModel
	}
	return permit.gateway.ordinaryPoolUnavailableForPaidFallback(ctx, req)
}

func finishUnsentPaidFallbackAttempt(store PaidFallbackOpsStore, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	if err := store.FinishAttempt(ctx, id, "not_sent", 0); err != nil {
		slog.Warn("paid_fallback_unsent_write_failed", "attempt_id", id)
	}
}

func RecordPaidFallbackWSAdmission(ctx context.Context, accountID int64, turn int) {
	permit, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
	if permit != nil && permit.accountID == accountID {
		slog.WarnContext(ctx, "paid_fallback_websocket_turn_admitted", "group_id", permit.groupID, "account_id", accountID, "turn", turn, "cost_known", false)
	}
}
