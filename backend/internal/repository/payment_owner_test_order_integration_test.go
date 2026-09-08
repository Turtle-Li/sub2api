//go:build integration

package repository

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

	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	ownerTestPGOrganizationID = "84fc3e66-e959-4bc8-8d78-6f8c3d3483fb"
	ownerTestPGProductID      = "00da03c5-bc5c-4edb-9d4c-c77da0e969d5"
	ownerTestPGAppID          = "app.sub2.live"
	ownerTestPGRequestKeyID   = "sub2.request.live.v1"
	ownerTestPGReturnURL      = "https://www.turtleligpt.com/payment/result"
)

// ownerTestPGSettings supplies an immutable disabled customer payment config.
// Embedding preserves the complete repository interface while GetPaymentConfig
// only reads GetMultiple for this bounded integration fixture.
type ownerTestPGSettings struct {
	service.SettingRepository
	values map[string]string
}

func (r *ownerTestPGSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = r.values[key]
	}
	return result, nil
}

type ownerTestPGCentralRequest struct {
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

type ownerTestPGCentralOrder struct {
	request   ownerTestPGCentralRequest
	id        string
	expiresAt time.Time
}

type ownerTestPGCentralBlock struct {
	arrived chan struct{}
	release chan struct{}
}

type ownerTestPGCentral struct {
	server *httptest.Server
	mu     sync.Mutex
	orders map[string]ownerTestPGCentralOrder
	bodies []string
	keys   []string

	loseFirstResponse bool
	lookupFailures    int
	firstPostArrived  chan struct{}
	releaseFirstPost  chan struct{}
	blocks            map[int]*ownerTestPGCentralBlock
}

func newOwnerTestPGCentral(t *testing.T) *ownerTestPGCentral {
	t.Helper()
	c := &ownerTestPGCentral{orders: make(map[string]ownerTestPGCentralOrder), blocks: make(map[int]*ownerTestPGCentralBlock)}
	c.server = httptest.NewTLSServer(http.HandlerFunc(c.serveHTTP))
	t.Cleanup(c.server.Close)
	return c
}

func (c *ownerTestPGCentral) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/payment-orders":
		c.serveCreate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/payment-orders":
		c.serveLookup(w, r)
	default:
		http.Error(w, "unexpected pay-v1 endpoint", http.StatusNotFound)
	}
}

func (c *ownerTestPGCentral) serveCreate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var request ownerTestPGCentralRequest
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.bodies = append(c.bodies, string(body))
	c.keys = append(c.keys, r.Header.Get(unifiedpay.HeaderIdempotencyKey))
	if _, exists := c.orders[request.ProductOrderNo]; !exists {
		c.orders[request.ProductOrderNo] = ownerTestPGCentralOrder{
			request:   request,
			id:        uuid.NewString(),
			expiresAt: time.Now().UTC().Truncate(time.Microsecond).Add(5 * time.Minute),
		}
	}
	order := c.orders[request.ProductOrderNo]
	postNumber := len(c.bodies)
	first := postNumber == 1
	lose := first && c.loseFirstResponse
	if lose {
		c.lookupFailures++
	}
	arrived, release := c.firstPostArrived, c.releaseFirstPost
	block := c.blocks[postNumber]
	c.mu.Unlock()
	if first && arrived != nil {
		close(arrived)
	}
	if first && release != nil {
		<-release
	}
	if block != nil {
		close(block.arrived)
		<-block.release
	}
	if lose {
		http.Error(w, "simulated lost response after durable central create", http.StatusServiceUnavailable)
		return
	}
	c.writeOrder(w, order)
}

func (c *ownerTestPGCentral) serveLookup(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	if c.lookupFailures > 0 {
		c.lookupFailures--
		c.mu.Unlock()
		http.Error(w, "simulated unavailable lookup", http.StatusServiceUnavailable)
		return
	}
	order, ok := c.orders[r.URL.Query().Get("product_order_no")]
	c.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	c.writeOrder(w, order)
}

func (c *ownerTestPGCentral) writeOrder(w http.ResponseWriter, order ownerTestPGCentralOrder) {
	now := time.Now().UTC().Truncate(time.Second)
	checkoutURL := c.server.URL + "/checkout/" + order.id
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"environment":                unifiedpay.EnvironmentLive,
		"organization_id":            ownerTestPGOrganizationID,
		"product_id":                 ownerTestPGProductID,
		"app_id":                     ownerTestPGAppID,
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
		"checkout_code_url":          nil,
		"needs_manual_review":        false,
		"created_at":                 now,
		"updated_at":                 now,
		"expires_at":                 order.expiresAt,
	})
}

func (c *ownerTestPGCentral) gateway(t *testing.T) *unifiedpay.Gateway {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	gateway, err := unifiedpay.New(unifiedpay.Config{
		Enabled: true, BaseURL: c.server.URL, Environment: unifiedpay.EnvironmentLive,
		OrganizationID: ownerTestPGOrganizationID, ProductID: ownerTestPGProductID, AppID: ownerTestPGAppID,
		RequestKeyID: ownerTestPGRequestKeyID, RequestPrivateKey: key,
		WebhookPublicKeys: map[string]ed25519.PublicKey{"sub2.webhook.live.v1": key.Public().(ed25519.PublicKey)},
		ReturnURL:         ownerTestPGReturnURL, SupportedMethods: []string{payment.TypeAlipay}, HTTPClient: c.server.Client(),
	})
	require.NoError(t, err)
	return gateway
}

func (c *ownerTestPGCentral) snapshot() ([]string, []string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.bodies...), append([]string(nil), c.keys...), len(c.orders)
}

func (c *ownerTestPGCentral) blockPost(number int) *ownerTestPGCentralBlock {
	c.mu.Lock()
	defer c.mu.Unlock()
	block := &ownerTestPGCentralBlock{arrived: make(chan struct{}), release: make(chan struct{})}
	c.blocks[number] = block
	return block
}

func (c *ownerTestPGCentral) expiryFor(productOrderNo string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	order, ok := c.orders[productOrderNo]
	return order.expiresAt, ok
}

type ownerTestPGFixture struct {
	service *service.PaymentService
	central *ownerTestPGCentral
	userID  int64
}

func newOwnerTestPGFixture(t *testing.T) *ownerTestPGFixture {
	t.Helper()
	central := newOwnerTestPGCentral(t)
	settings := &ownerTestPGSettings{values: map[string]string{
		service.SettingPaymentEnabled:                   "false",
		service.SettingOrderTimeoutMinutes:              "5",
		service.SettingMaxPendingOrders:                 "3",
		service.SettingBalancePayDisabled:               "false",
		service.SettingBalanceRechargeMult:              "1",
		service.SettingRechargeFeeRate:                  "0",
		service.SettingPaymentVisibleMethodAlipaySource: service.VisibleMethodSourceEasyPayAlipay,
	}}
	configService := service.NewPaymentConfigService(integrationEntClient, settings, nil)
	paymentService := service.NewPaymentService(integrationEntClient, payment.NewRegistry(), nil, nil, nil, configService, NewUserRepository(integrationEntClient, integrationDB), nil, nil)
	paymentService.SetUnifiedPayment(central.gateway(t), nil)
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email: "owner-test-" + uuid.NewString() + "@integration.test", PasswordHash: "test-only-password-hash",
		Username: "owner-test-admin", Role: service.RoleAdmin, Status: service.StatusActive,
	})
	fixture := &ownerTestPGFixture{service: paymentService, central: central, userID: user.ID}
	t.Cleanup(func() { fixture.cleanup(t) })
	return fixture
}

func (f *ownerTestPGFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `DELETE FROM payment_audit_logs WHERE order_id IN (SELECT id::text FROM payment_orders WHERE user_id = $1)`, f.userID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM payment_orders WHERE user_id = $1`, f.userID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, f.userID)
	require.NoError(t, err)
}

func (f *ownerTestPGFixture) request(key string) service.OwnerTestOrderRequest {
	return service.OwnerTestOrderRequest{AdminUserID: f.userID, AmountFen: 1, PaymentType: payment.TypeAlipay, IdempotencyKey: key, ClientIP: "127.0.0.1"}
}

// expireDispatchForReclaim simulates elapsed time after a dispatcher has
// committed its claim but before its network response returns. It deliberately
// changes the lease and provisional expiry in PostgreSQL, which fences the
// first executor and lets the retry claim the exact same durable local order.
func (f *ownerTestPGFixture) expireDispatchForReclaim(t *testing.T, orderID int64, provisionalExpiry time.Time) {
	t.Helper()
	order, err := integrationEntClient.PaymentOrder.Get(context.Background(), orderID)
	require.NoError(t, err)
	encoded, err := json.Marshal(order.ProviderSnapshot)
	require.NoError(t, err)
	var snapshot map[string]any
	require.NoError(t, json.Unmarshal(encoded, &snapshot))
	ledger, ok := snapshot["owner_test"].(map[string]any)
	require.True(t, ok)
	dispatch, ok := ledger["dispatch"].(map[string]any)
	require.True(t, ok)
	dispatch["lease_expires_at"] = time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond).Format(time.RFC3339Nano)
	_, err = integrationEntClient.PaymentOrder.UpdateOneID(orderID).
		SetProviderSnapshot(snapshot).
		SetExpiresAt(provisionalExpiry).
		Save(context.Background())
	require.NoError(t, err)
}

func TestOwnerTestOrderPostgresConcurrencyAndLostResponse(t *testing.T) {
	t.Run("concurrent same key creates one local and central order", func(t *testing.T) {
		fixture := newOwnerTestPGFixture(t)
		fixture.central.firstPostArrived = make(chan struct{})
		fixture.central.releaseFirstPost = make(chan struct{})
		request := fixture.request("owner-test-postgres-key-0001")
		firstResult := make(chan struct {
			response *service.CreateOrderResponse
			err      error
		}, 1)
		go func() {
			response, err := fixture.service.CreateOwnerTestOrder(context.Background(), request)
			firstResult <- struct {
				response *service.CreateOrderResponse
				err      error
			}{response, err}
		}()
		select {
		case <-fixture.central.firstPostArrived:
		case <-time.After(15 * time.Second):
			t.Fatal("timed out waiting for first central create")
		}

		const contenders = 6
		errs := make(chan error, contenders)
		var wg sync.WaitGroup
		for i := 0; i < contenders; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := fixture.service.CreateOwnerTestOrder(context.Background(), request)
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.Error(t, err)
			_, status := infraerrors.ToHTTP(err)
			require.Equal(t, "OWNER_TEST_ORDER_IN_PROGRESS", status.Reason)
		}
		close(fixture.central.releaseFirstPost)
		outcome := <-firstResult
		require.NoError(t, outcome.err)
		require.NotNil(t, outcome.response)
		require.NotEmpty(t, outcome.response.PayURL)
		count, err := integrationEntClient.PaymentOrder.Query().Where(paymentorder.UserIDEQ(fixture.userID)).Count(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, count)
		bodies, keys, remoteOrders := fixture.central.snapshot()
		require.Len(t, bodies, 1)
		require.Len(t, keys, 1)
		require.Equal(t, "sub2:create:"+outcome.response.OutTradeNo, keys[0])
		require.Equal(t, 1, remoteOrders)
	})

	t.Run("restart retry reuses exact persisted central request", func(t *testing.T) {
		fixture := newOwnerTestPGFixture(t)
		fixture.central.loseFirstResponse = true
		request := fixture.request("owner-test-postgres-key-0002")
		_, err := fixture.service.CreateOwnerTestOrder(context.Background(), request)
		require.ErrorIs(t, err, unifiedpay.ErrCreateStateUnconfirmed)
		pending, err := integrationEntClient.PaymentOrder.Query().Where(paymentorder.UserIDEQ(fixture.userID)).Only(context.Background())
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusPending, pending.Status)
		require.Empty(t, pending.PaymentTradeNo)

		// Construct a fresh service to model process loss after the central write.
		settings := &ownerTestPGSettings{values: map[string]string{
			service.SettingPaymentEnabled: "false", service.SettingOrderTimeoutMinutes: "5", service.SettingMaxPendingOrders: "3",
			service.SettingBalancePayDisabled: "false", service.SettingBalanceRechargeMult: "1", service.SettingRechargeFeeRate: "0",
		}}
		restarted := service.NewPaymentService(integrationEntClient, payment.NewRegistry(), nil, nil, nil, service.NewPaymentConfigService(integrationEntClient, settings, nil), NewUserRepository(integrationEntClient, integrationDB), nil, nil)
		restarted.SetUnifiedPayment(fixture.central.gateway(t), nil)
		response, err := restarted.CreateOwnerTestOrder(context.Background(), request)
		require.NoError(t, err)
		require.Equal(t, pending.ID, response.OrderID)
		require.NotEmpty(t, response.PayURL)
		bodies, keys, remoteOrders := fixture.central.snapshot()
		require.Len(t, bodies, 2)
		require.Equal(t, bodies[0], bodies[1])
		require.Equal(t, keys[0], keys[1])
		require.Equal(t, 1, remoteOrders)
		stored, err := integrationEntClient.PaymentOrder.Get(context.Background(), pending.ID)
		require.NoError(t, err)
		require.NotEmpty(t, stored.PaymentTradeNo)
		encoded, err := json.Marshal(stored.ProviderSnapshot)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), request.IdempotencyKey)
	})
}

func TestOwnerTestOrderPostgresReclaimedDispatcherUsesAuthoritativeCentralExpiry(t *testing.T) {
	fixture := newOwnerTestPGFixture(t)
	firstBlock := fixture.central.blockPost(1)
	request := fixture.request("owner-test-postgres-key-expiry-0001")
	type result struct {
		response *service.CreateOrderResponse
		err      error
	}
	firstResult := make(chan result, 1)
	go func() {
		response, err := fixture.service.CreateOwnerTestOrder(context.Background(), request)
		firstResult <- result{response: response, err: err}
	}()
	firstReleased := false
	defer func() {
		if !firstReleased {
			close(firstBlock.release)
		}
	}()
	select {
	case <-firstBlock.arrived:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for first delayed central create")
	}

	initial, err := integrationEntClient.PaymentOrder.Query().Where(paymentorder.UserIDEQ(fixture.userID)).Only(context.Background())
	require.NoError(t, err)
	// The first claimant is now stale. Keep the provisional local deadline
	// briefly in the future so the retry can reclaim before it elapses, then
	// hold its response until that deadline has passed.
	provisionalExpiry := time.Now().UTC().Add(2 * time.Second).Truncate(time.Microsecond)
	fixture.expireDispatchForReclaim(t, initial.ID, provisionalExpiry)

	secondBlock := fixture.central.blockPost(2)
	secondResult := make(chan result, 1)
	go func() {
		response, err := fixture.service.CreateOwnerTestOrder(context.Background(), request)
		secondResult <- result{response: response, err: err}
	}()
	secondReleased := false
	defer func() {
		if !secondReleased {
			close(secondBlock.release)
		}
	}()
	select {
	case <-secondBlock.arrived:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for reclaimed central create")
	}
	if wait := time.Until(provisionalExpiry.Add(50 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	require.True(t, time.Now().After(provisionalExpiry), "test must release the checkout only after the provisional local deadline")
	close(secondBlock.release)
	secondReleased = true
	second := <-secondResult
	require.NoError(t, second.err)
	require.NotNil(t, second.response)
	require.NotEmpty(t, second.response.PayURL)

	centralExpiry, ok := fixture.central.expiryFor(second.response.OutTradeNo)
	require.True(t, ok)
	stored, err := integrationEntClient.PaymentOrder.Get(context.Background(), second.response.OrderID)
	require.NoError(t, err)
	require.True(t, stored.ExpiresAt.Equal(centralExpiry))
	require.True(t, second.response.ExpiresAt.Equal(centralExpiry))
	require.True(t, stored.ExpiresAt.After(time.Now()))
	require.False(t, stored.ExpiresAt.Equal(provisionalExpiry))

	close(firstBlock.release)
	firstReleased = true
	first := <-firstResult
	require.NoError(t, first.err)
	require.NotNil(t, first.response)
	require.Equal(t, second.response.OrderID, first.response.OrderID)
	require.Equal(t, second.response.ExpiresAt, first.response.ExpiresAt)
	bodies, keys, remoteOrders := fixture.central.snapshot()
	require.Len(t, bodies, 2)
	require.Equal(t, bodies[0], bodies[1])
	require.Equal(t, keys[0], keys[1])
	require.Equal(t, 1, remoteOrders)
}
