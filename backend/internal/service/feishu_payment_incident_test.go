//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type feishuPaymentRoundTripper func(*http.Request) (*http.Response, error)

func (fn feishuPaymentRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func feishuPaymentDeliveryFixture() FeishuPaymentDelivery {
	return FeishuPaymentDelivery{
		ID:             "delivery-test-id",
		IncidentID:     "incident-test-id",
		IncidentKind:   FeishuPaymentIncidentRefundReview,
		SubjectOrderID: 42,
		Generation:     1,
		OpenedAt:       time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Kind:           FeishuPaymentDeliveryOpen,
		Sequence:       1,
	}
}

func TestFeishuPaymentSenderAcceptsCurrentAndLegacySuccess(t *testing.T) {
	t.Parallel()
	for name, response := range map[string]string{
		"current": `{"code":0}`,
		"legacy":  `{"StatusCode":0}`,
		"both":    `{"code":0,"StatusCode":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var body string
			sender := newFeishuPaymentIncidentSender(&http.Client{Transport: feishuPaymentRoundTripper(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, request.Method)
				require.Equal(t, "https", request.URL.Scheme)
				require.Equal(t, "open.feishu.cn", request.URL.Host)
				require.Equal(t, "/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111", request.URL.Path)
				encoded, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				body = string(encoded)
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
			})}, func(context.Context) (string, error) {
				return "https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111", nil
			}, func() time.Time {
				return time.Date(2026, 9, 10, 12, 23, 0, 0, time.UTC)
			})

			require.NoError(t, sender.Send(context.Background(), feishuPaymentDeliveryFixture()))
			require.Contains(t, body, "incident-test-id")
			require.Contains(t, body, "订单：42")
			require.Contains(t, body, "持续时间：23m0s")
			require.Contains(t, body, "【Sub2】支付事件告警")
			require.Contains(t, body, "https://www.turtleligpt.com/admin/orders")
			require.NotContains(t, body, "@")
		})
	}
}

func TestFeishuPaymentSenderRejectsUnsafeOrUnacknowledgedResponsesWithoutLeaking(t *testing.T) {
	t.Parallel()
	secret := "https://example.test/open-apis/bot/v2/hook/never-print-this-token"
	invalidURLSender := newFeishuPaymentIncidentSender(nil, func(context.Context) (string, error) { return secret, nil }, nil)
	err := invalidURLSender.Send(context.Background(), feishuPaymentDeliveryFixture())
	require.ErrorIs(t, err, errFeishuPaymentWebhookDelivery)
	require.NotContains(t, err.Error(), "never-print")

	for name, response := range map[string]string{
		"nonzero_current":  `{"code":1,"msg":"never-print-this-response"}`,
		"nonzero_legacy":   `{"StatusCode":2,"message":"never-print-this-response"}`,
		"conflicting":      `{"code":0,"StatusCode":1}`,
		"duplicate":        `{"code":1,"code":0}`,
		"missing":          `{"message":"never-print-this-response"}`,
		"null_current":     `{"code":null}`,
		"null_legacy":      `{"StatusCode":null}`,
		"null_conflicting": `{"code":0,"StatusCode":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sender := newFeishuPaymentIncidentSender(&http.Client{Transport: feishuPaymentRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
			})}, func(context.Context) (string, error) {
				return "https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111", nil
			}, nil)
			err := sender.Send(context.Background(), feishuPaymentDeliveryFixture())
			require.ErrorIs(t, err, errFeishuPaymentWebhookDelivery)
			require.NotContains(t, err.Error(), "never-print")
		})
	}
}

func TestFeishuPaymentWebhookURLAndVaultReferenceAreStrict(t *testing.T) {
	t.Parallel()
	_, err := validateFeishuPaymentWebhookURL("https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111")
	require.NoError(t, err)
	for _, value := range []string{
		"http://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111",
		"https://open.feishu.cn:443/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111",
		"https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111?x=1",
		"https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111?",
		"https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111/extra",
		"https://open.feishu.cn/open-apis/bot/v2/hook/not-a-uuid",
	} {
		_, err := validateFeishuPaymentWebhookURL(value)
		require.ErrorIs(t, err, errFeishuPaymentWebhookDelivery, value)
	}

	path, field, err := parseFeishuPaymentVaultReference(feishuPaymentVaultReference)
	require.NoError(t, err)
	require.Equal(t, "secret/data/ops/feishu/payment", path)
	require.Equal(t, "webhook_url", field)
	_, err = loadFeishuPaymentVaultText(context.Background(), "/tmp/not-the-fixed-socket", feishuPaymentVaultReference, "webhook_url")
	require.ErrorIs(t, err, errFeishuPaymentWebhookDelivery)
	webhook, err := loadFeishuPaymentVaultTextWithClient(context.Background(), feishuPaymentVaultSocket, feishuPaymentVaultReference, "webhook_url", &http.Client{Transport: feishuPaymentRoundTripper(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "vault", request.URL.Host)
		require.Equal(t, feishuPaymentVaultRequestPath, request.URL.Path)
		require.Empty(t, request.Header.Get("Authorization"))
		require.Empty(t, request.Header.Get("X-Vault-Token"))
		require.Empty(t, request.Header.Get("Cookie"))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":{"data":{"webhook_url":"https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111"}}}`)), Header: make(http.Header)}, nil
	})})
	require.NoError(t, err)
	require.Equal(t, "https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-4111-8111-111111111111", webhook)
	_, err = parseFeishuPaymentVaultEnvelope([]byte(`{"data":{"data":{"webhook_url":"value","other":"no"}}}`), "webhook_url")
	require.ErrorIs(t, err, errFeishuPaymentWebhookDelivery)
}

func TestFeishuPaymentBackoffIsCappedAndNeverDrops(t *testing.T) {
	t.Parallel()
	require.Equal(t, 5*time.Second, feishuPaymentIncidentBackoff(FeishuPaymentDelivery{}))
	require.Equal(t, 20*time.Second, feishuPaymentIncidentBackoff(FeishuPaymentDelivery{Attempts: 3}))
	require.Equal(t, time.Hour, feishuPaymentIncidentBackoff(FeishuPaymentDelivery{Attempts: 99}))
}

func TestFeishuPaymentRecheckOnlyResolvesKnownTerminalStates(t *testing.T) {
	t.Parallel()
	store := &feishuPaymentRecheckStore{}
	svc := NewFeishuPaymentIncidentService(store, feishuPaymentNoopSender{}, &config.Config{FeishuPaymentAlerts: config.FeishuPaymentAlertsConfig{Enabled: true}}, nil, nil)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	store.refund = FeishuPaymentRefundFenceState{Exists: true, Fenced: false, RefundTerminal: false}
	require.NoError(t, svc.recheckOpen(context.Background(), FeishuPaymentIncident{ID: "refund", Kind: FeishuPaymentIncidentRefundReview, SubjectOrderID: 1}, now))
	require.Equal(t, []string{"touch:refund"}, store.actions())

	store.reset()
	store.refund = FeishuPaymentRefundFenceState{Exists: true, Fenced: true, RefundTerminal: true}
	require.NoError(t, svc.recheckOpen(context.Background(), FeishuPaymentIncident{ID: "refund-terminal", Kind: FeishuPaymentIncidentRefundReview, SubjectOrderID: 1}, now))
	require.Equal(t, []string{"resolve:refund-terminal"}, store.actions())

	store.reset()
	store.order = FeishuPaymentOrderState{Exists: true, Status: OrderStatusRefundPending}
	require.NoError(t, svc.recheckOpen(context.Background(), FeishuPaymentIncident{ID: "pending", Kind: FeishuPaymentIncidentPaidIncomplete, SubjectOrderID: 1}, now))
	require.Equal(t, []string{"touch:pending"}, store.actions())

	store.reset()
	store.order = FeishuPaymentOrderState{Exists: true, Status: OrderStatusCompleted}
	require.NoError(t, svc.recheckOpen(context.Background(), FeishuPaymentIncident{ID: "completed", Kind: FeishuPaymentIncidentPaidIncomplete, SubjectOrderID: 1}, now))
	require.Equal(t, []string{"resolve:completed"}, store.actions())
}

func TestFeishuPaymentSyntheticNotificationUsesOnlyItsOwnDelivery(t *testing.T) {
	t.Parallel()
	store := &feishuPaymentTestDeliveryStore{}
	sender := &feishuPaymentCountingSender{}
	svc := NewFeishuPaymentIncidentService(store, sender, &config.Config{FeishuPaymentAlerts: config.FeishuPaymentAlertsConfig{Enabled: true}}, nil, nil)
	require.NoError(t, svc.SendTestNotification(context.Background()))
	require.Equal(t, 1, sender.count())
	require.Equal(t, feishuPaymentDeliveryStatusDelivered, store.status)
	require.True(t, store.enqueued)
}

func TestFeishuPaymentSyntheticNotificationAcceptsDeliveryByNormalProcessor(t *testing.T) {
	t.Parallel()
	store := &feishuPaymentTestDeliveryStore{deliveredByPeer: true}
	sender := &feishuPaymentCountingSender{}
	svc := NewFeishuPaymentIncidentService(store, sender, &config.Config{FeishuPaymentAlerts: config.FeishuPaymentAlertsConfig{Enabled: true}}, nil, nil)
	require.NoError(t, svc.SendTestNotification(context.Background()))
	require.Equal(t, 0, sender.count(), "CLI must accept its own delivery when the normal owner processor already acknowledged it")
}

func TestFeishuPaymentSyntheticNotificationRechecksAfterLeaseLostToPeer(t *testing.T) {
	t.Parallel()
	store := &feishuPaymentTestDeliveryStore{deliveredByPeerOnCompletion: true}
	sender := &feishuPaymentCountingSender{}
	svc := NewFeishuPaymentIncidentService(store, sender, &config.Config{FeishuPaymentAlerts: config.FeishuPaymentAlertsConfig{Enabled: true}}, nil, nil)
	require.NoError(t, svc.SendTestNotification(context.Background()))
	require.Equal(t, 1, sender.count(), "the CLI must recheck its own delivery after a peer takes the terminal CAS")
	require.Equal(t, feishuPaymentDeliveryStatusDelivered, store.status)
}

func TestFeishuPaymentBackgroundLeaseCancelsWhenGenerationBecomesStandby(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "background-state")
	require.NoError(t, os.WriteFile(stateFile, []byte("active\n"), 0o600))
	t.Setenv("SUB2API_BACKGROUND_STATE_FILE", stateFile)
	svc := NewFeishuPaymentIncidentService(&feishuPaymentRecheckStore{}, feishuPaymentNoopSender{}, &config.Config{FeishuPaymentAlerts: config.FeishuPaymentAlertsConfig{Enabled: true}}, nil, nil)
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- svc.withBackgroundLease(context.Background(), func(leaseCtx context.Context) error {
			close(started)
			<-leaseCtx.Done()
			return leaseCtx.Err()
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background lease was not acquired while active")
	}
	require.NoError(t, os.WriteFile(stateFile, []byte("standby\n"), 0o600))
	select {
	case err := <-finished:
		require.NoError(t, err, "normal standby handoff is cancellation, not a failed pass")
	case <-time.After(2 * time.Second):
		t.Fatal("background lease was not canceled after standby handoff")
	}
}

type feishuPaymentNoopSender struct{}

func (feishuPaymentNoopSender) Send(context.Context, FeishuPaymentDelivery) error { return nil }

type feishuPaymentRecheckStore struct {
	FeishuPaymentIncidentStore
	mu     sync.Mutex
	refund FeishuPaymentRefundFenceState
	order  FeishuPaymentOrderState
	calls  []string
}

func (s *feishuPaymentRecheckStore) LoadRefundFenceState(context.Context, int64) (FeishuPaymentRefundFenceState, error) {
	return s.refund, nil
}

func (s *feishuPaymentRecheckStore) LoadPaymentOrderState(context.Context, int64) (FeishuPaymentOrderState, error) {
	return s.order, nil
}

func (s *feishuPaymentRecheckStore) TouchOpen(_ context.Context, id string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "touch:"+id)
	return nil
}

func (s *feishuPaymentRecheckStore) Resolve(_ context.Context, id string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "resolve:"+id)
	return nil
}

func (s *feishuPaymentRecheckStore) actions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *feishuPaymentRecheckStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

type feishuPaymentTestDeliveryStore struct {
	FeishuPaymentIncidentStore
	enqueued                    bool
	deliveredByPeer             bool
	deliveredByPeerOnCompletion bool
	status                      string
}

func (s *feishuPaymentTestDeliveryStore) EnqueueTest(context.Context, time.Time) (string, error) {
	s.enqueued = true
	s.status = feishuPaymentDeliveryStatusPending
	if s.deliveredByPeer {
		s.status = feishuPaymentDeliveryStatusDelivered
	}
	return "test-delivery", nil
}

func (s *feishuPaymentTestDeliveryStore) DeliveryStatus(context.Context, string) (string, error) {
	return s.status, nil
}

func (s *feishuPaymentTestDeliveryStore) ClaimExact(_ context.Context, id string, _ time.Duration) (*FeishuPaymentDelivery, error) {
	if id != "test-delivery" || s.status != feishuPaymentDeliveryStatusPending {
		return nil, nil
	}
	s.status = feishuPaymentDeliveryStatusClaimed
	return &FeishuPaymentDelivery{ID: id, IncidentID: "test-incident", IncidentKind: FeishuPaymentIncidentTest, Kind: FeishuPaymentDeliveryTest, ClaimToken: "test-token"}, nil
}

func (s *feishuPaymentTestDeliveryStore) CanSend(context.Context, string, string) (bool, error) {
	return true, nil
}

func (s *feishuPaymentTestDeliveryStore) MarkDelivered(context.Context, string, string) error {
	s.status = feishuPaymentDeliveryStatusDelivered
	if s.deliveredByPeerOnCompletion {
		return ErrFeishuPaymentDeliveryLeaseLost
	}
	return nil
}

func (s *feishuPaymentTestDeliveryStore) MarkFailed(context.Context, string, string, time.Duration, string) error {
	return errors.New("unexpected test delivery failure")
}

type feishuPaymentCountingSender struct {
	mu    sync.Mutex
	calls int
}

func (s *feishuPaymentCountingSender) Send(context.Context, FeishuPaymentDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return nil
}

func (s *feishuPaymentCountingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestFeishuPaymentSenderRejectsNullAcknowledgement(t *testing.T) {
	for _, body := range []string{`{"code":null}`, `{"StatusCode":null}`, `{"code":0,"StatusCode":null}`} {
		if isFeishuPaymentSuccessResponse([]byte(body)) {
			t.Fatalf("null provider acknowledgement accepted: %s", body)
		}
	}
}
