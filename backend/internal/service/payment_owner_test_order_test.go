//go:build unit

package service

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type ownerTestOrderUserRepo struct {
	UserRepository
	client *dbent.Client
}

func (r *ownerTestOrderUserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	entity, err := r.client.User.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &User{ID: entity.ID, Email: entity.Email, Username: entity.Username, Notes: entity.Notes, Role: entity.Role, Status: entity.Status}, nil
}

type ownerTestCentralCreateRequest struct {
	ProductOrderNo   string            `json:"product_order_no"`
	OrderType        string            `json:"order_type"`
	AmountFen        int64             `json:"amount_fen"`
	Currency         string            `json:"currency"`
	Subject          string            `json:"subject"`
	PaymentMethod    string            `json:"payment_method"`
	ExpiresInSeconds int               `json:"expires_in_seconds"`
	ReturnURL        *string           `json:"return_url"`
	Metadata         map[string]string `json:"metadata"`
}

type ownerTestCentralOrder struct {
	request   ownerTestCentralCreateRequest
	id        string
	expiresAt time.Time
}

// ownerTestCentralHarness is a memory-only signed-client peer. It never
// contacts a merchant; it only records how the product invokes pay-v1.
type ownerTestCentralHarness struct {
	t *testing.T

	server *httptest.Server
	mu     sync.Mutex
	orders map[string]ownerTestCentralOrder
	posts  []string
	keys   []string

	loseFirstResponse bool
	lookupFailures    int
	firstPostArrived  chan struct{}
	releaseFirstPost  chan struct{}
	firstPostFailure  bool
}

func newOwnerTestCentralHarness(t *testing.T) *ownerTestCentralHarness {
	t.Helper()
	h := &ownerTestCentralHarness{t: t, orders: make(map[string]ownerTestCentralOrder)}
	h.server = httptest.NewTLSServer(http.HandlerFunc(h.serveHTTP))
	t.Cleanup(h.server.Close)
	return h
}

func (h *ownerTestCentralHarness) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/payment-orders":
		h.serveCreate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/payment-orders":
		h.serveLookup(w, r)
	default:
		http.Error(w, "unexpected endpoint", http.StatusNotFound)
	}
}

func (h *ownerTestCentralHarness) serveCreate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	var request ownerTestCentralCreateRequest
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, "json", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	h.posts = append(h.posts, string(body))
	h.keys = append(h.keys, r.Header.Get(unifiedpay.HeaderIdempotencyKey))
	if _, exists := h.orders[request.ProductOrderNo]; !exists {
		h.orders[request.ProductOrderNo] = ownerTestCentralOrder{
			request:   request,
			id:        uuid.NewString(),
			expiresAt: time.Now().UTC().Truncate(time.Microsecond).Add(5 * time.Minute),
		}
	}
	order := h.orders[request.ProductOrderNo]
	postNumber := len(h.posts)
	arrived := h.firstPostArrived
	release := h.releaseFirstPost
	fail := h.firstPostFailure && postNumber == 1
	lose := h.loseFirstResponse && postNumber == 1
	if lose {
		h.lookupFailures++
	}
	h.mu.Unlock()
	if arrived != nil && postNumber == 1 {
		close(arrived)
	}
	if release != nil && postNumber == 1 {
		<-release
	}
	if fail {
		http.Error(w, "definite test rejection", http.StatusBadRequest)
		return
	}
	if lose {
		http.Error(w, "response lost after commit", http.StatusServiceUnavailable)
		return
	}
	h.writeOrder(w, order)
}

func (h *ownerTestCentralHarness) serveLookup(w http.ResponseWriter, r *http.Request) {
	productOrderNo := r.URL.Query().Get("product_order_no")
	h.mu.Lock()
	if h.lookupFailures > 0 {
		h.lookupFailures--
		h.mu.Unlock()
		http.Error(w, "lookup unavailable", http.StatusServiceUnavailable)
		return
	}
	order, ok := h.orders[productOrderNo]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h.writeOrder(w, order)
}

func (h *ownerTestCentralHarness) writeOrder(w http.ResponseWriter, order ownerTestCentralOrder) {
	now := time.Now().UTC().Truncate(time.Second)
	checkoutURL := h.server.URL + "/checkout/" + order.id
	var checkoutCodeURL any
	if order.request.PaymentMethod == unifiedpay.PaymentMethodWechatPay {
		checkoutCodeURL = "weixin://wxpay/bizpayurl?pr=owner-test"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"environment":                unifiedpay.EnvironmentLive,
		"organization_id":            ownerTestOrganizationID,
		"product_id":                 ownerTestProductID,
		"app_id":                     ownerTestAppID,
		"payment_order_id":           order.id,
		"product_order_no":           order.request.ProductOrderNo,
		"order_type":                 order.request.OrderType,
		"amount_fen":                 order.request.AmountFen,
		"paid_amount_fen":            0,
		"refunded_amount_fen":        0,
		"reserved_refund_amount_fen": 0,
		"refundable_amount_fen":      0,
		"currency":                   payment.DefaultPaymentCurrency,
		"payment_method":             order.request.PaymentMethod,
		"status":                     unifiedpay.StatusPendingPayment,
		"checkout_url":               checkoutURL,
		"checkout_code_url":          checkoutCodeURL,
		"needs_manual_review":        false,
		"created_at":                 now,
		"updated_at":                 now,
		"expires_at":                 order.expiresAt,
	})
}

func (h *ownerTestCentralHarness) gateway(t *testing.T) *unifiedpay.Gateway {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	gateway, err := unifiedpay.New(unifiedpay.Config{
		Enabled: true, BaseURL: h.server.URL, Environment: unifiedpay.EnvironmentLive,
		OrganizationID: ownerTestOrganizationID, ProductID: ownerTestProductID, AppID: ownerTestAppID,
		RequestKeyID: ownerTestRequestKeyID, RequestPrivateKey: key,
		WebhookPublicKeys: map[string]ed25519.PublicKey{"sub2.webhook.live.v1": key.Public().(ed25519.PublicKey)},
		ReturnURL:         ownerTestReturnURL, SupportedMethods: []string{payment.TypeAlipay, payment.TypeWxpay}, HTTPClient: h.server.Client(),
	})
	require.NoError(t, err)
	return gateway
}

func (h *ownerTestCentralHarness) postSnapshot() ([]string, []string, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.posts...), append([]string(nil), h.keys...), len(h.orders)
}

type ownerTestOrderFixture struct {
	service  *PaymentService
	client   *dbent.Client
	userID   int64
	settings *paymentConfigSettingRepoStub
	central  *ownerTestCentralHarness
}

func newOwnerTestOrderFixture(t *testing.T) *ownerTestOrderFixture {
	t.Helper()
	client := newPaymentConfigServiceTestClient(t)
	userEntity, err := client.User.Create().
		SetEmail("owner-test-" + uuid.NewString() + "@example.test").
		SetPasswordHash("test-only-password-hash").
		SetUsername("owner-test-admin").
		SetRole(RoleAdmin).
		SetStatus(StatusActive).
		Save(context.Background())
	require.NoError(t, err)
	settings := &paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentEnabled:                   "false",
		SettingOrderTimeoutMinutes:              "5",
		SettingMaxPendingOrders:                 "3",
		SettingBalancePayDisabled:               "false",
		SettingBalanceRechargeMult:              "1",
		SettingRechargeFeeRate:                  "0",
		SettingPaymentVisibleMethodAlipaySource: VisibleMethodSourceEasyPayAlipay,
	}}
	configService := &PaymentConfigService{entClient: client, settingRepo: settings}
	central := newOwnerTestCentralHarness(t)
	svc := NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, configService, &ownerTestOrderUserRepo{client: client}, nil, nil)
	svc.SetUnifiedPayment(central.gateway(t), nil)
	return &ownerTestOrderFixture{service: svc, client: client, userID: userEntity.ID, settings: settings, central: central}
}

func (f *ownerTestOrderFixture) request(key string, fen int64, paymentType string) OwnerTestOrderRequest {
	return OwnerTestOrderRequest{AdminUserID: f.userID, AmountFen: fen, PaymentType: paymentType, IdempotencyKey: key, ClientIP: "127.0.0.1"}
}

func TestOwnerTestOrderPinsLiveGatewayAndPreservesDisabledCustomerCheckout(t *testing.T) {
	fixture := newOwnerTestOrderFixture(t)
	ctx := context.Background()
	_, err := fixture.service.CreateOrder(ctx, CreateOrderRequest{UserID: fixture.userID, Amount: 1, PaymentType: payment.TypeAlipay, OrderType: payment.OrderTypeBalance})
	require.Error(t, err)
	_, status := infraerrors.ToHTTP(err)
	require.Equal(t, "PAYMENT_DISABLED", status.Reason)

	response, err := fixture.service.CreateOwnerTestOrder(ctx, fixture.request("owner-test-live-key-0001", 1, payment.TypeAlipay))
	require.NoError(t, err)
	require.Equal(t, float64(0.01), response.Amount)
	require.Equal(t, float64(0.01), response.PayAmount)
	require.Equal(t, float64(0), response.FeeRate)
	require.NotEmpty(t, response.PayURL)
	posts, keys, remoteOrders := fixture.central.postSnapshot()
	require.Len(t, posts, 1)
	require.Equal(t, 1, remoteOrders)
	require.Equal(t, "sub2:create:"+response.OutTradeNo, keys[0])
	var sent ownerTestCentralCreateRequest
	require.NoError(t, json.Unmarshal([]byte(posts[0]), &sent))
	require.Equal(t, int64(1), sent.AmountFen)
	require.Equal(t, "CNY", sent.Currency)
	require.Equal(t, unifiedpay.PaymentMethodAlipay, sent.PaymentMethod)
	require.Equal(t, ownerTestReturnURL, *sent.ReturnURL)
	require.Equal(t, "sub2", sent.Metadata["source"])

	order, err := fixture.client.PaymentOrder.Get(ctx, response.OrderID)
	require.NoError(t, err)
	require.Equal(t, payment.TypeUnifiedPay, valueOrEmpty(order.ProviderKey))
	ledger, err := ownerTestLedgerFromOrder(order)
	require.NoError(t, err)
	require.Equal(t, int64(1), ledger.ProviderRequest.AmountFen)
	require.Equal(t, ownerTestRequestKeyID, ledger.RuntimeScope.RequestKeyID)
	require.Equal(t, fixture.central.server.URL, ledger.RuntimeScope.BaseURL)
	require.NotNil(t, ledger.AuthoritativeExpiresAt)
	require.True(t, order.ExpiresAt.Equal(*ledger.AuthoritativeExpiresAt))
	require.True(t, response.ExpiresAt.Equal(order.ExpiresAt))
	encoded, err := json.Marshal(order.ProviderSnapshot)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "owner-test-live-key-0001")
	require.Equal(t, "false", fixture.settings.values[SettingPaymentEnabled])
}

func TestOwnerTestOrderRejectsInvalidInputBeforeLocalOrCentralCreate(t *testing.T) {
	fixture := newOwnerTestOrderFixture(t)
	ctx := context.Background()
	for _, request := range []OwnerTestOrderRequest{
		fixture.request("owner-test-live-key-0002", 3, payment.TypeAlipay),
		fixture.request("owner-test-live-key-0003", 1, payment.TypeEasyPay),
		fixture.request("short", 1, payment.TypeAlipay),
	} {
		_, err := fixture.service.CreateOwnerTestOrder(ctx, request)
		require.Error(t, err)
	}
	count, err := fixture.client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	posts, _, remoteOrders := fixture.central.postSnapshot()
	require.Empty(t, posts)
	require.Zero(t, remoteOrders)
}

func TestOwnerTestOrderRetryReusesDurableCentralRequestAfterLostResponse(t *testing.T) {
	fixture := newOwnerTestOrderFixture(t)
	fixture.central.loseFirstResponse = true
	ctx := context.Background()
	request := fixture.request("owner-test-live-key-0004", 2, payment.TypeWxpay)
	_, err := fixture.service.CreateOwnerTestOrder(ctx, request)
	require.ErrorIs(t, err, unifiedpay.ErrCreateStateUnconfirmed)
	order, err := fixture.client.PaymentOrder.Query().Where(paymentorder.UserIDEQ(fixture.userID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, order.Status)
	require.Empty(t, order.PaymentTradeNo)
	ledger, err := ownerTestLedgerFromOrder(order)
	require.NoError(t, err)
	require.Equal(t, int64(2), ledger.ProviderRequest.AmountFen)
	require.Equal(t, "0.02", ledger.ProviderRequest.Amount)

	response, err := fixture.service.CreateOwnerTestOrder(ctx, request)
	require.NoError(t, err)
	require.Equal(t, order.ID, response.OrderID)
	require.NotEmpty(t, response.PayURL)
	require.NotEmpty(t, response.QRCode)
	posts, keys, remoteOrders := fixture.central.postSnapshot()
	require.Len(t, posts, 2)
	require.Equal(t, posts[0], posts[1])
	require.Equal(t, keys[0], keys[1])
	require.Equal(t, 1, remoteOrders)

	_, err = fixture.service.CreateOwnerTestOrder(ctx, fixture.request("owner-test-live-key-0004", 1, payment.TypeWxpay))
	require.Error(t, err)
	_, status := infraerrors.ToHTTP(err)
	require.Equal(t, "OWNER_TEST_IDEMPOTENCY_CONFLICT", status.Reason)

	_, err = fixture.client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusFailed).Save(ctx)
	require.NoError(t, err)
	terminal, err := fixture.service.CreateOwnerTestOrder(ctx, request)
	require.NoError(t, err)
	require.Equal(t, OrderStatusFailed, terminal.Status)
	require.Empty(t, terminal.PayURL)
	require.Empty(t, terminal.QRCode)
	postsAfterTerminal, _, _ := fixture.central.postSnapshot()
	require.Len(t, postsAfterTerminal, 2)
}

func TestOwnerTestOrderStaleFailureCannotOverwriteReclaimedCheckout(t *testing.T) {
	fixture := newOwnerTestOrderFixture(t)
	fixture.central.firstPostArrived = make(chan struct{})
	fixture.central.releaseFirstPost = make(chan struct{})
	fixture.central.firstPostFailure = true
	ctx := context.Background()
	request := fixture.request("owner-test-live-key-0005", 1, payment.TypeAlipay)
	first := make(chan struct {
		response *CreateOrderResponse
		err      error
	}, 1)
	go func() {
		response, err := fixture.service.CreateOwnerTestOrder(ctx, request)
		first <- struct {
			response *CreateOrderResponse
			err      error
		}{response: response, err: err}
	}()
	select {
	case <-fixture.central.firstPostArrived:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for first owner-test create")
	}
	order, err := fixture.client.PaymentOrder.Query().Where(paymentorder.UserIDEQ(fixture.userID)).Only(ctx)
	require.NoError(t, err)
	ledger, err := ownerTestLedgerFromOrder(order)
	require.NoError(t, err)
	past := ownerTestTimestamp(time.Now().Add(-time.Second))
	ledger.Dispatch.LeaseExpiresAt = &past
	snapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
	snapshot[ownerTestProviderSnapshotKey] = *ledger
	_, err = fixture.client.PaymentOrder.UpdateOneID(order.ID).SetProviderSnapshot(snapshot).SetUpdatedAt(ownerTestNextVersion(order.UpdatedAt, time.Now())).Save(ctx)
	require.NoError(t, err)

	fixture.central.firstPostFailure = false
	second, err := fixture.service.CreateOwnerTestOrder(ctx, request)
	require.NoError(t, err)
	require.NotEmpty(t, second.PayURL)
	close(fixture.central.releaseFirstPost)
	firstResult := <-first
	require.NoError(t, firstResult.err)
	require.NotNil(t, firstResult.response)
	require.Equal(t, second.OrderID, firstResult.response.OrderID)
	require.Equal(t, second.PayURL, firstResult.response.PayURL)
	stored, err := fixture.client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, stored.Status)
	require.NotEmpty(t, stored.PaymentTradeNo)
	posts, _, remoteOrders := fixture.central.postSnapshot()
	require.Len(t, posts, 2)
	require.Equal(t, 1, remoteOrders)
}
