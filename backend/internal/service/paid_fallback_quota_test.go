//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPaidFallbackAccountQuotaExhaustionAndRecovery(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek} {
		for _, advanced := range []bool{false, true} {
			for _, quota := range []string{"total", "daily", "weekly"} {
				t.Run(fmt.Sprintf("%s/advanced=%t/%s", platform, advanced, quota), func(t *testing.T) {
					svc, ctx, _ := newPaidFallbackScheduler(t, platform, advanced)
					repo := paidFallbackTestAccountRepo(t, svc)
					extra := map[string]any{"quota_limit": 1.0, "quota_used": 1.0}
					if quota != "total" {
						extra = map[string]any{
							"quota_" + quota + "_limit": 1.0,
							"quota_" + quota + "_used":  1.0,
							"quota_" + quota + "_start": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
						}
					}
					repo.accounts[0].Extra = extra
					require.True(t, repo.accounts[0].IsQuotaExceeded())
					selection, err := selectPaidFallbackTestAccount(svc, ctx, platform, nil)
					require.NoError(t, err)
					require.EqualValues(t, 2, selection.Account.ID)
					selection.ReleaseFunc()

					model := selection.paidFallbackPermit.request.RequestedModel
					sendCtx := WithPaidFallbackModelSend(WithPaidFallbackPublicModel(ContextWithPaidFallbackSelection(ctx, selection), model))
					attempts := 0
					finished := make(chan struct{}, 1)
					svc.settingService.paidFallbackOpsStore = &paidAttemptTestStore{
						start:  func(context.Context, PaidFallbackAttempt) error { attempts++; return nil },
						finish: func(context.Context, string, string, int) error { finished <- struct{}{}; return nil },
					}
					finish, err := BeginPaidFallbackHTTPAttempt(sendCtx, 2)
					require.NoError(t, err)
					finish(200, nil)
					select {
					case <-finished:
					case <-time.After(time.Second):
						t.Fatal("paid attempt result was not recorded")
					}
					require.Equal(t, 1, attempts)

					groupID := int64(7)
					require.NoError(t, svc.BindStickySession(ctx, &groupID, "test-session", 2))
					if quota == "total" {
						extra["quota_used"] = 0.0
					} else {
						extra["quota_"+quota+"_start"] = time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339)
					}
					require.False(t, repo.accounts[0].IsQuotaExceeded())
					finish, err = BeginPaidFallbackHTTPAttempt(sendCtx, 2)
					require.ErrorIs(t, err, ErrPaidFallbackRestartRequired)
					require.Nil(t, finish)
					finish, err = BeginPaidFallbackWSSend(sendCtx, 2, 2, model, model, "ws_ctx_pool")
					require.ErrorIs(t, err, ErrPaidFallbackRestartRequired)
					require.Nil(t, finish)
					require.Equal(t, 1, attempts, "recovered ordinary capacity blocks paid sends before audit")
					selection, err = selectPaidFallbackTestAccount(svc, ctx, platform, nil)
					require.NoError(t, err)
					require.EqualValues(t, 1, selection.Account.ID, "ordinary recovery overrides paid affinity")
					selection.ReleaseFunc()
				})
			}
		}
	}
}

func TestPaidFallbackQuotaRecoveryDuringAuditStopsSend(t *testing.T) {
	for _, transport := range []string{"http", "ws_ctx_pool", "ws_passthrough", "ws_http_bridge"} {
		t.Run(transport, func(t *testing.T) {
			var ordinary *Account
			attempts, unsent := 0, 0
			store := &paidAttemptTestStore{
				start: func(context.Context, PaidFallbackAttempt) error {
					attempts++
					ordinary.Extra["quota_used"] = 0.0
					return nil
				},
				finish: func(_ context.Context, _ string, outcome string, status int) error {
					require.Equal(t, "not_sent", outcome)
					require.Zero(t, status)
					unsent++
					return nil
				},
			}
			ctx, _ := paidAttemptContext(t, store)
			permit := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
			repo := paidFallbackTestAccountRepo(t, permit.gateway)
			ordinary = &repo.accounts[0]
			ordinary.Status, ordinary.Schedulable = StatusActive, true
			ordinary.Extra = map[string]any{"quota_limit": 1.0, "quota_used": 1.0}
			var finish func(int, error)
			var err error
			switch transport {
			case "http":
				finish, err = BeginPaidFallbackHTTPAttempt(ctx, 23)
			case "ws_http_bridge":
				finish, err = BeginPaidFallbackHTTPAttempt(WithPaidFallbackBridgeTurn(ctx, "public-ds", 2), 23)
			default:
				finish, err = BeginPaidFallbackWSSend(ctx, 23, 2, "public-ds", "upstream-ds", transport)
			}
			require.ErrorIs(t, err, ErrPaidFallbackRestartRequired)
			require.Nil(t, finish)
			require.Equal(t, 1, attempts, "quota exhaustion must permit the initial audit")
			require.Equal(t, 1, unsent, "recovery during audit must cancel the send")
		})
	}
}
