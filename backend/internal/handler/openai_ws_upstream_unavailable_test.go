package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	coderws "github.com/coder/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestResponsesWSCtxPool_UnavailableReleasesBothSlotsWithoutReplaying(t *testing.T) {
	const failure = `{"type":"error","status":502,"request_id":"req_gateway","headers":{"x-request-id":"req_upstream","x-gateway-request-id":"req_gateway"},"error":{"code":"upstream_unavailable","type":"server_error","message":"Upstream service is unavailable."}}`
	const partial = `{"type":"response.output_text.delta","delta":"partial"}`
	const metered = `{"type":"response.in_progress","response":{"id":"resp_metered","usage":{"input_tokens":13,"output_tokens":2}}}`
	const image = `{"type":"response.output_item.done","item":{"id":"ig_1","type":"image_generation_call","status":"completed","result":"final-image"}}`
	for _, tc := range []struct {
		name, first string
		heartbeat   bool
	}{{"silent", "", false}, {"keepalive_after_output", partial, true}, {"previously_metered", metered, false}, {"completed_image", image, false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var submissions atomic.Int32
			accepted := make(chan struct{})
			sendFailure := make(chan struct{})
			discarded := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				if _, _, err = conn.Read(ctx); err != nil {
					return
				}
				if submissions.Add(1) != 1 {
					return
				}
				defer close(discarded)
				close(accepted)
				if tc.first != "" {
					if err := conn.Write(ctx, coderws.MessageText, []byte(tc.first)); err != nil {
						return
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-sendFailure:
				}
				if err := conn.Write(ctx, coderws.MessageText, []byte(failure)); err != nil {
					return
				}
				if tc.heartbeat {
					pulseCtx, stop := context.WithCancel(ctx)
					defer stop()
					go sendWSUnavailableTestHeartbeats(pulseCtx, conn)
				}
				for {
					if _, _, err := conn.Read(ctx); err != nil {
						return
					}
					submissions.Add(1) // catch a replay on the same connection too
				}
			}))
			t.Cleanup(upstream.Close)
			// Exercise the same configured CPR endpoint identity as production, not just a matching frame.
			settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{}}, nil)
			dummy := "local-test-only"
			_, err := settings.UpdateCPRQuotaSettings(ctx, upstream.URL, &dummy)
			require.NoError(t, err)
			t.Cleanup(func() {
				empty := ""
				_, err := settings.UpdateCPRQuotaSettings(context.Background(), "", &empty)
				require.NoError(t, err)
			})
			rdb := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			slots := repository.NewConcurrencyCache(rdb, 30, 60)
			h := newOpenAIWSModeHandlerHarness(t, upstream.URL, service.OpenAIWSIngressModeCtxPool, 900, slots)
			request := []byte(`{"type":"response.create","model":"gpt-5.1","input":"hello"}`)
			require.NoError(t, h.clientConn.Write(ctx, coderws.MessageText, request))
			counts := func() (int, int) {
				u, ue := slots.GetUserConcurrency(ctx, h.apiKey.UserID)
				a, ae := slots.GetAccountConcurrency(ctx, 9951)
				require.NoError(t, ue)
				require.NoError(t, ae)
				return u, a
			}
			select {
			case <-accepted:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream did not receive the turn")
			}
			u, a := counts()
			require.Equal(t, 1, u)
			require.Equal(t, 1, a)
			close(sendFailure)
			readCtx, stopRead := context.WithTimeout(ctx, 4*time.Second)
			defer stopRead()
			if tc.first != "" {
				_, first, err := h.clientConn.Read(readCtx)
				require.NoError(t, err)
				require.JSONEq(t, tc.first, string(first))
			}
			_, frame, err := h.clientConn.Read(readCtx)
			require.NoError(t, err)
			require.JSONEq(t, failure, string(frame))
			for {
				_, trailing, err := h.clientConn.Read(readCtx)
				if err != nil {
					require.Equal(t, coderws.StatusInternalError, coderws.CloseStatus(err), "expected gateway close, not the test read deadline: %v", err)
					break
				}
				require.JSONEq(t, `{"type":"keepalive"}`, string(trailing), "do not emit the error twice")
			}
			select {
			case <-discarded:
			case <-readCtx.Done():
				t.Fatal("upstream socket was not discarded")
			}
			u, a = counts()
			require.Zero(t, u, "user slot")
			require.Zero(t, a, "shared account slot")
			uOK, err := slots.AcquireUserSlot(ctx, h.apiKey.UserID, 1, "next-turn")
			require.NoError(t, err)
			require.True(t, uOK)
			aOK, err := slots.AcquireAccountSlot(ctx, 9951, 1, "next-turn")
			require.NoError(t, err)
			require.True(t, aOK)
			require.NoError(t, slots.ReleaseAccountSlot(ctx, 9951, "next-turn"))
			require.NoError(t, slots.ReleaseUserSlot(ctx, h.apiKey.UserID, "next-turn"))
			require.EqualValues(t, 1, submissions.Load())
			select {
			case <-h.handlerDone:
			case <-readCtx.Done():
				t.Fatal("handler did not exit")
			}
			if tc.first == metered || tc.first == image {
				select {
				case usage := <-h.usageLogs:
					if tc.first == image {
						require.Equal(t, "req_gateway", usage.RequestID)
						require.EqualValues(t, 1, usage.ImageCount)
					} else {
						require.Equal(t, "resp_metered", usage.RequestID)
						require.EqualValues(t, 13, usage.InputTokens)
						require.EqualValues(t, 2, usage.OutputTokens)
					}
				case <-readCtx.Done():
					t.Fatal("previously reported usage was lost")
				}
			}
			require.Empty(t, h.usageLogs, "do not invent usage or settle twice")
		})
	}
}

func sendWSUnavailableTestHeartbeats(ctx context.Context, conn *coderws.Conn) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := conn.Write(ctx, coderws.MessageText, []byte(`{"type":"keepalive"}`)); err != nil {
				return
			}
		}
	}
}

func TestResponsesWSCtxPool_LateFailureUsageSettlesAndReleasesBothSlots(t *testing.T) {
	const bare = `{"type":"error","error":{"type":"server_error","code":"upstream_unavailable"}}`
	const failed = `{"type":"response.failed","response":{"id":"resp_late","status":"failed","usage":{"input_tokens":13,"output_tokens":2}}}`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	terminal := make(chan struct{})
	var submissions atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
		submissions.Add(1)
		if err := conn.Write(ctx, coderws.MessageText, []byte(bare)); err != nil {
			return
		}
		select {
		case <-terminal:
		case <-ctx.Done():
			return
		}
		if err := conn.Write(ctx, coderws.MessageText, []byte(failed)); err != nil {
			return
		}
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
			submissions.Add(1)
		}
	}))
	t.Cleanup(upstream.Close)
	rdb := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	slots := repository.NewConcurrencyCache(rdb, 30, 60)
	h := newOpenAIWSModeHandlerHarness(t, upstream.URL, service.OpenAIWSIngressModeCtxPool, 900, slots)
	require.NoError(t, h.clientConn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":"hello"}`)))
	_, msg, err := h.clientConn.Read(ctx)
	require.NoError(t, err)
	require.JSONEq(t, bare, string(msg))
	select {
	case <-h.handlerDone:
		t.Fatal("ambiguous error ended the turn prematurely")
	case <-time.After(2500 * time.Millisecond):
	}
	checkSlots := func(want int) {
		t.Helper()
		u, err := slots.GetUserConcurrency(ctx, h.apiKey.UserID)
		require.NoError(t, err)
		a, err := slots.GetAccountConcurrency(ctx, 9951)
		require.NoError(t, err)
		require.Equal(t, want, u, "user slot")
		require.Equal(t, want, a, "shared account slot")
	}
	checkSlots(1)
	require.Empty(t, h.usageLogs)
	close(terminal)
	_, msg, err = h.clientConn.Read(ctx)
	require.NoError(t, err)
	require.JSONEq(t, failed, string(msg))
	select {
	case usage := <-h.usageLogs:
		require.Equal(t, "resp_late", usage.RequestID)
		require.EqualValues(t, 13, usage.InputTokens)
		require.EqualValues(t, 2, usage.OutputTokens)
	case <-ctx.Done():
		t.Fatal("late usage was not recorded")
	}
	checkSlots(0)
	_ = h.clientConn.CloseNow()
	select {
	case <-h.handlerDone:
	case <-ctx.Done():
		t.Fatal("handler did not exit")
	}
	require.EqualValues(t, 1, submissions.Load())
	require.Empty(t, h.usageLogs)
}
