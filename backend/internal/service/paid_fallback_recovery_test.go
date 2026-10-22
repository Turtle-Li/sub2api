//go:build unit

package service

import (
	"context"
	"testing"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestPaidFallbackSendRechecksOrdinaryRecovery(t *testing.T) {
	for _, transport := range []string{"http", "ws_ctx_pool", "ws_passthrough", "ws_http_bridge"} {
		for _, recovery := range []string{"before_send", "during_audit", "later_turn", "dependency_failure"} {
			if transport == "http" && recovery == "later_turn" {
				continue
			}
			t.Run(transport+"/"+recovery, func(t *testing.T) {
				var accountRepo paidFallbackAccountRepo
				writes, unsent := 0, 0
				recoverOrdinary := func() {
					accountRepo.accounts[0].Status = StatusActive
					accountRepo.accounts[0].Schedulable = true
				}
				store := &paidAttemptTestStore{
					start: func(context.Context, PaidFallbackAttempt) error {
						writes++
						if recovery == "during_audit" {
							recoverOrdinary()
						}
						return nil
					},
					finish: func(_ context.Context, _ string, outcome string, _ int) error {
						require.Equal(t, "not_sent", outcome)
						unsent++
						return nil
					},
				}
				ctx, _ := paidAttemptContext(t, store)
				permit := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
				accountRepo = paidFallbackTestAccountRepo(t, permit.gateway)
				turn := 1
				switch recovery {
				case "before_send":
					recoverOrdinary()
				case "later_turn":
					recoverOrdinary()
					turn = 2
					permit.request.ExcludedIDs = map[int64]struct{}{1: {}}
					ctx = WithPaidFallbackFailure(ctx, &Account{ID: 1}, 503)
				case "dependency_failure":
					permit.gateway.accountRepo = nil
				}
				var finish func(int, error)
				var err error
				switch transport {
				case "http":
					finish, err = BeginPaidFallbackHTTPAttempt(ctx, 23)
				case "ws_http_bridge":
					finish, err = BeginPaidFallbackHTTPAttempt(WithPaidFallbackBridgeTurn(ctx, "public-ds", turn), 23)
				default:
					finish, err = BeginPaidFallbackWSSend(ctx, 23, turn, "public-ds", "upstream-ds", transport)
				}
				require.ErrorIs(t, err, ErrPaidFallbackRestartRequired)
				if transport != "http" {
					var closeErr *OpenAIWSClientCloseError
					require.ErrorAs(t, err, &closeErr)
					require.Contains(t, closeErr.reason, "reconnect with full conversation history")
				}
				require.Nil(t, finish)
				if recovery == "during_audit" {
					require.Equal(t, 1, writes)
					require.Equal(t, 1, unsent)
				} else {
					require.Zero(t, writes)
					require.Zero(t, unsent)
				}
			})
		}
	}
}

func TestPaidFallbackPassthroughRecoveryStopsBothFrameTypes(t *testing.T) {
	for _, messageType := range []coderws.MessageType{coderws.MessageText, coderws.MessageBinary} {
		t.Run(messageType.String(), func(t *testing.T) {
			store := &paidAttemptTestStore{start: func(context.Context, PaidFallbackAttempt) error {
				t.Fatal("recovered ordinary pool must veto before audit and transport")
				return nil
			}}
			ctx, _ := paidAttemptContext(t, store)
			permit := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
			repo := paidFallbackTestAccountRepo(t, permit.gateway)
			repo.accounts[0].Status, repo.accounts[0].Schedulable = StatusActive, true
			inner := newStagedPassthroughConn()
			defer func() { _ = inner.Close() }()
			conn := &openAIWSPassthroughFirstOutputFrameConn{inner: inner, beforeModelWrite: func(ctx context.Context, _ []byte) (func(int, error), error) {
				return BeginPaidFallbackWSSend(ctx, 23, 2, "public-ds", "upstream-ds", "ws_passthrough")
			}}
			err := conn.WriteFrame(ctx, messageType, []byte(`{"type":"response.create","model":"public-ds"}`))
			require.ErrorIs(t, err, ErrPaidFallbackRestartRequired)
			require.Empty(t, inner.writes)
			require.False(t, conn.state.armed)
		})
	}
}
