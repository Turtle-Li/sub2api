package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/google/uuid"
)

const (
	ownerTestLedgerVersion = 1

	ownerTestEnvironment    = "live"
	ownerTestOrganizationID = "84fc3e66-e959-4bc8-8d78-6f8c3d3483fb"
	ownerTestProductID      = "00da03c5-bc5c-4edb-9d4c-c77da0e969d5"
	ownerTestAppID          = "app.sub2.live"
	ownerTestRequestKeyID   = "sub2.request.live.v1"
	ownerTestReturnURL      = "https://www.turtleligpt.com/payment/result"

	ownerTestProviderSnapshotKey   = "owner_test"
	ownerTestDispatchLeaseDuration = 30 * time.Second
)

var errOwnerTestOrderInsertConflict = errors.New("owner test order insert conflict")

// OwnerTestOrderRequest contains only server-derived identity and the narrow
// 1–2 fen pilot inputs. It deliberately has no user, provider-source, return
// URL, subject, or fulfillment fields that an HTTP caller could influence.
type OwnerTestOrderRequest struct {
	AdminUserID    int64
	AmountFen      int64
	PaymentType    string
	IdempotencyKey string
	ClientIP       string
}

type ownerTestRuntimeScope struct {
	Environment    string `json:"environment"`
	OrganizationID string `json:"organization_id"`
	ProductID      string `json:"product_id"`
	AppID          string `json:"app_id"`
	BaseURL        string `json:"base_url"`
	RequestKeyID   string `json:"request_key_id"`
	ReturnURL      string `json:"return_url"`
	PaymentType    string `json:"payment_type"`
	Currency       string `json:"currency"`
}

type ownerTestProviderRequest struct {
	ProductOrderNo  string `json:"product_order_no"`
	OrderType       string `json:"order_type"`
	AmountFen       int64  `json:"amount_fen"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	PaymentType     string `json:"payment_type"`
	Subject         string `json:"subject"`
	ReturnURL       string `json:"return_url"`
	ExpiresInSecond int    `json:"expires_in_seconds"`
	IdempotencyKey  string `json:"idempotency_key"`
	MetadataSource  string `json:"metadata_source"`
}

// ownerTestDispatch is an on-row fenced lease. Its generation and token are
// audit-visible only; the exact persisted UpdatedAt version is the finalizer
// CAS condition. No database transaction remains open while calling pay-v1.
type ownerTestDispatch struct {
	Generation     int64      `json:"generation"`
	ClaimToken     string     `json:"claim_token,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
}

// ownerTestOrderLedger is stored inside payment_orders.provider_snapshot in
// the same transaction as the order. It never contains the raw idempotency
// key or a private credential. The original central request is intentionally
// immutable so a retry cannot inherit a later global-config change.
type ownerTestOrderLedger struct {
	Version              int                      `json:"version"`
	OwnerUserID          int64                    `json:"owner_user_id"`
	IdempotencyKeySHA256 string                   `json:"idempotency_key_sha256"`
	PayloadSHA256        string                   `json:"payload_sha256"`
	RuntimeScope         ownerTestRuntimeScope    `json:"runtime_scope"`
	RuntimeScopeSHA256   string                   `json:"runtime_scope_sha256"`
	ProviderRequest      ownerTestProviderRequest `json:"provider_request"`
	// AuthoritativeExpiresAt is copied only from a validated pay-v1 create
	// response in the same fenced write as the checkout. It replaces the
	// provisional local timeout before a checkout can be returned.
	AuthoritativeExpiresAt *time.Time        `json:"authoritative_expires_at,omitempty"`
	Dispatch               ownerTestDispatch `json:"dispatch"`
}

type ownerTestOrderContext struct {
	input              OwnerTestOrderRequest
	amount             float64
	amountDecimal      string
	outTradeNo         string
	idempotencyKeyHash string
	payloadHash        string
	scope              ownerTestRuntimeScope
	scopeHash          string
	selection          *payment.InstanceSelection
	ledger             ownerTestOrderLedger
}

type createOrderOptions struct {
	ownerTest *ownerTestOrderContext
}

type ownerTestDispatchLease struct {
	version    time.Time
	generation int64
	token      string
}

func createOrderDatabaseOptionsFrom(opts *createOrderOptions) *createOrderDatabaseOptions {
	if opts == nil || opts.ownerTest == nil {
		return nil
	}
	return &createOrderDatabaseOptions{
		fixedOutTradeNo:   opts.ownerTest.outTradeNo,
		providerSnapshot:  ownerTestProviderSnapshot(opts.ownerTest),
		lockOwnerTestUser: true,
	}
}

func isOwnerTestOrderInsertConflict(err error) bool {
	return errors.Is(err, errOwnerTestOrderInsertConflict)
}

// CreateOwnerTestOrder creates a controlled, full-balance payment order for
// the authenticated financial administrator. It is intentionally separate
// from ordinary checkout: it never reads visible provider-source routing and
// never turns on the persisted customer-purchase switch.
func (s *PaymentService) CreateOwnerTestOrder(ctx context.Context, input OwnerTestOrderRequest) (*CreateOrderResponse, error) {
	ownerCtx, err := s.newOwnerTestOrderContext(input)
	if err != nil {
		return nil, err
	}
	if err := s.requireActiveOwnerTestAdmin(ctx, input.AdminUserID); err != nil {
		return nil, err
	}

	if existing, lookupErr := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(ownerCtx.outTradeNo)).Only(ctx); lookupErr == nil {
		return s.replayOwnerTestOrderRecord(ctx, existing, ownerCtx)
	} else if !dbent.IsNotFound(lookupErr) {
		return nil, fmt.Errorf("lookup owner test order: %w", lookupErr)
	}

	if s.configService == nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_CONFIG_UNAVAILABLE", "payment configuration is unavailable")
	}
	storedCfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get payment config: %w", err)
	}
	cfg, err := ownerTestPaymentConfigCopy(storedCfg)
	if err != nil {
		return nil, err
	}
	if err := ownerCtx.attachLedger(cfg); err != nil {
		return nil, err
	}

	req := CreateOrderRequest{
		UserID:      input.AdminUserID,
		Amount:      ownerCtx.amount,
		PaymentType: input.PaymentType,
		ClientIP:    input.ClientIP,
		OrderType:   payment.OrderTypeBalance,
		ReturnURL:   ownerTestReturnURL,
	}
	return s.createOrderWithConfig(ctx, req, cfg, &createOrderOptions{ownerTest: ownerCtx})
}

func (s *PaymentService) newOwnerTestOrderContext(input OwnerTestOrderRequest) (*ownerTestOrderContext, error) {
	if input.AdminUserID <= 0 {
		return nil, infraerrors.Unauthorized("UNAUTHORIZED", "administrator authentication is required")
	}
	if !validOwnerTestIdempotencyKey(input.IdempotencyKey) {
		return nil, infraerrors.BadRequest("INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must be 16 to 80 safe characters")
	}
	amount, amountDecimal, err := ownerTestCanonicalAmount(input.AmountFen)
	if err != nil {
		return nil, err
	}
	if input.PaymentType != payment.TypeAlipay && input.PaymentType != payment.TypeWxpay {
		return nil, infraerrors.BadRequest("INVALID_PAYMENT_TYPE", "payment_type must be alipay or wxpay")
	}
	selection, scope, err := s.ownerTestUnifiedSelection(input.PaymentType)
	if err != nil {
		return nil, err
	}
	keyHash := ownerTestSHA256(input.IdempotencyKey)
	payloadHash := ownerTestPayloadHash(input.AdminUserID, input.AmountFen, input.PaymentType)
	return &ownerTestOrderContext{
		input:              input,
		amount:             amount,
		amountDecimal:      amountDecimal,
		outTradeNo:         ownerTestOutTradeNo(input.AdminUserID, keyHash),
		idempotencyKeyHash: keyHash,
		payloadHash:        payloadHash,
		scope:              scope,
		scopeHash:          ownerTestRuntimeScopeHash(scope),
		selection:          selection,
	}, nil
}

func (s *PaymentService) requireActiveOwnerTestAdmin(ctx context.Context, userID int64) error {
	if s == nil || s.userRepo == nil {
		return infraerrors.ServiceUnavailable("OWNER_TEST_UNAVAILABLE", "owner test user validation is unavailable")
	}
	actor, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get owner test administrator: %w", err)
	}
	if actor == nil || !actor.IsActive() {
		return infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
	}
	if !actor.IsAdmin() {
		return infraerrors.Forbidden("OWNER_TEST_ADMIN_REQUIRED", "an active administrator is required for an owner test order")
	}
	return nil
}

func (s *PaymentService) ownerTestUnifiedSelection(paymentType string) (*payment.InstanceSelection, ownerTestRuntimeScope, error) {
	if s == nil || s.unifiedPayment == nil || !s.unifiedPayment.Enabled() {
		return nil, ownerTestRuntimeScope{}, infraerrors.ServiceUnavailable("OWNER_TEST_UNIFIED_PAYMENT_UNAVAILABLE", "live unified payment is not enabled")
	}
	if !s.unifiedPayment.SupportsPaymentType(paymentType) {
		return nil, ownerTestRuntimeScope{}, infraerrors.ServiceUnavailable("OWNER_TEST_UNIFIED_METHOD_UNAVAILABLE", "selected unified payment method is not enabled")
	}
	scopeValues := s.unifiedPayment.ScopeMetadata()
	scope := ownerTestRuntimeScope{
		Environment:    strings.TrimSpace(scopeValues["environment"]),
		OrganizationID: strings.TrimSpace(scopeValues["organization_id"]),
		ProductID:      strings.TrimSpace(scopeValues["product_id"]),
		AppID:          strings.TrimSpace(scopeValues["app_id"]),
		BaseURL:        strings.TrimSpace(scopeValues["base_url"]),
		RequestKeyID:   strings.TrimSpace(scopeValues["request_key_id"]),
		ReturnURL:      strings.TrimSpace(s.unifiedPayment.ReturnURL()),
		PaymentType:    paymentType,
		Currency:       payment.DefaultPaymentCurrency,
	}
	if scope.Environment != ownerTestEnvironment ||
		scope.OrganizationID != ownerTestOrganizationID ||
		scope.ProductID != ownerTestProductID ||
		scope.AppID != ownerTestAppID ||
		scope.RequestKeyID != ownerTestRequestKeyID ||
		scope.BaseURL == "" ||
		scope.ReturnURL != ownerTestReturnURL ||
		scope.Currency != "CNY" {
		return nil, ownerTestRuntimeScope{}, infraerrors.ServiceUnavailable("OWNER_TEST_UNIFIED_SCOPE_UNAVAILABLE", "live unified payment scope is not the approved owner-test binding")
	}
	selection := s.unifiedPayment.Selection(paymentType)
	if selection == nil || selection.ProviderKey != payment.TypeUnifiedPay || !paymentTypeInSelection(paymentType, selection.SupportedTypes) {
		return nil, ownerTestRuntimeScope{}, infraerrors.ServiceUnavailable("OWNER_TEST_UNIFIED_METHOD_UNAVAILABLE", "selected unified payment method is not routed through the unified gateway")
	}
	return selection, scope, nil
}

func paymentTypeInSelection(paymentType, supportedTypes string) bool {
	for _, candidate := range strings.Split(supportedTypes, ",") {
		if strings.TrimSpace(candidate) == paymentType {
			return true
		}
	}
	return false
}

func ownerTestCanonicalAmount(amountFen int64) (float64, string, error) {
	switch amountFen {
	case 1:
		return 0.01, "0.01", nil
	case 2:
		return 0.02, "0.02", nil
	default:
		return 0, "", infraerrors.BadRequest("INVALID_OWNER_TEST_AMOUNT", "amount_fen must be exactly 1 or 2")
	}
}

func validOwnerTestIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 80 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:-", r) {
			continue
		}
		return false
	}
	return true
}

func ownerTestSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func ownerTestPayloadHash(ownerID, amountFen int64, paymentType string) string {
	payload, _ := json.Marshal(struct {
		Version     int    `json:"version"`
		OwnerUserID int64  `json:"owner_user_id"`
		AmountFen   int64  `json:"amount_fen"`
		PaymentType string `json:"payment_type"`
	}{Version: ownerTestLedgerVersion, OwnerUserID: ownerID, AmountFen: amountFen, PaymentType: paymentType})
	return ownerTestSHA256(string(payload))
}

func ownerTestOutTradeNo(ownerID int64, idempotencyKeyHash string) string {
	digest := sha256.Sum256([]byte("sub2-owner-test-v1\n" + strconv.FormatInt(ownerID, 10) + "\n" + idempotencyKeyHash))
	// 13-byte prefix + 48 hexadecimal chars leaves room under the provider's
	// 64-character product-order identifier limit while retaining 192 bits.
	return "sub2_ownert_" + hex.EncodeToString(digest[:24])
}

func ownerTestRuntimeScopeHash(scope ownerTestRuntimeScope) string {
	payload, _ := json.Marshal(scope)
	return ownerTestSHA256(string(payload))
}

func ownerTestPaymentConfigCopy(source *PaymentConfig) (*PaymentConfig, error) {
	if source == nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_CONFIG_UNAVAILABLE", "payment configuration is unavailable")
	}
	copy := *source
	copy.Enabled = true
	copy.BalanceDisabled = false
	copy.MinAmount = 0.01
	copy.MaxAmount = 0.02
	copy.RechargeFeeRate = 0
	copy.BalanceRechargeMultiplier = 1
	copy.RechargeOptions = nil
	if copy.OrderTimeoutMin <= 0 {
		copy.OrderTimeoutMin = defaultOrderTimeoutMin
	}
	if copy.OrderTimeoutMin < 5 || copy.OrderTimeoutMin > 120 {
		return nil, infraerrors.ServiceUnavailable("UNIFIED_PAYMENT_INVALID_TIMEOUT", "unified payment order timeout must be between 5 and 120 minutes")
	}
	return &copy, nil
}

func (c *ownerTestOrderContext) attachLedger(cfg *PaymentConfig) error {
	if c == nil || cfg == nil || c.selection == nil {
		return fmt.Errorf("owner test order context is incomplete")
	}
	ttl := cfg.OrderTimeoutMin * 60
	if ttl < 300 || ttl > 7200 {
		return infraerrors.ServiceUnavailable("UNIFIED_PAYMENT_INVALID_TIMEOUT", "unified payment order timeout must be between 5 and 120 minutes")
	}
	c.ledger = ownerTestOrderLedger{
		Version:              ownerTestLedgerVersion,
		OwnerUserID:          c.input.AdminUserID,
		IdempotencyKeySHA256: c.idempotencyKeyHash,
		PayloadSHA256:        c.payloadHash,
		RuntimeScope:         c.scope,
		RuntimeScopeSHA256:   c.scopeHash,
		ProviderRequest: ownerTestProviderRequest{
			ProductOrderNo:  c.outTradeNo,
			OrderType:       payment.OrderTypeBalance,
			AmountFen:       c.input.AmountFen,
			Amount:          c.amountDecimal,
			Currency:        payment.DefaultPaymentCurrency,
			PaymentType:     c.input.PaymentType,
			Subject:         "Sub2API admin test " + c.amountDecimal + " CNY",
			ReturnURL:       ownerTestReturnURL,
			ExpiresInSecond: ttl,
			IdempotencyKey:  "sub2:create:" + c.outTradeNo,
			MetadataSource:  "sub2",
		},
	}
	return nil
}

func ownerTestProviderSnapshot(c *ownerTestOrderContext) map[string]any {
	if c == nil {
		return nil
	}
	snapshot := buildPaymentOrderProviderSnapshot(c.selection, CreateOrderRequest{PaymentType: c.input.PaymentType})
	if snapshot == nil {
		snapshot = make(map[string]any)
	}
	snapshot[ownerTestProviderSnapshotKey] = c.ledger
	return snapshot
}

func clonePaymentOrderSnapshot(snapshot map[string]any) map[string]any {
	if snapshot == nil {
		return nil
	}
	copy := make(map[string]any, len(snapshot))
	for key, value := range snapshot {
		copy[key] = value
	}
	return copy
}

func validateOwnerTestOrderInput(req CreateOrderRequest, cfg *PaymentConfig, owner *ownerTestOrderContext) (*dbent.SubscriptionPlan, error) {
	if owner == nil || cfg == nil || req.UserID != owner.input.AdminUserID || req.OrderType != payment.OrderTypeBalance || req.PaymentType != owner.input.PaymentType {
		return nil, infraerrors.BadRequest("INVALID_OWNER_TEST_ORDER", "owner test order input is invalid")
	}
	if req.Amount != owner.amount || cfg.MinAmount != 0.01 || cfg.MaxAmount != 0.02 ||
		cfg.RechargeFeeRate != 0 || cfg.BalanceRechargeMultiplier != 1 || cfg.BalanceDisabled || !cfg.Enabled || len(cfg.RechargeOptions) != 0 {
		return nil, infraerrors.ServiceUnavailable("OWNER_TEST_CONFIGURATION_INVALID", "owner test payment configuration is invalid")
	}
	return nil, nil
}

func (s *PaymentService) replayOwnerTestOrder(ctx context.Context, owner *ownerTestOrderContext) (*CreateOrderResponse, error) {
	if owner == nil {
		return nil, infraerrors.ServiceUnavailable("OWNER_TEST_UNAVAILABLE", "owner test order context is unavailable")
	}
	order, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(owner.outTradeNo)).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, infraerrors.ServiceUnavailable("OWNER_TEST_ORDER_RETRY", "owner test order is still being recorded; retry with the same Idempotency-Key")
		}
		return nil, fmt.Errorf("reload owner test order: %w", err)
	}
	return s.replayOwnerTestOrderRecord(ctx, order, owner)
}

func (s *PaymentService) replayOwnerTestOrderRecord(ctx context.Context, order *dbent.PaymentOrder, owner *ownerTestOrderContext) (*CreateOrderResponse, error) {
	ledger, err := validateOwnerTestOrderRecord(order, owner)
	if err != nil {
		return nil, err
	}
	if ownerTestOrderHasCheckout(order) || order.Status != OrderStatusPending || !order.ExpiresAt.After(time.Now()) {
		s.writeAuditLog(ctx, order.ID, "OWNER_TEST_ORDER_REPLAYED", fmt.Sprintf("admin:%d", owner.input.AdminUserID), ownerTestAuditDetail(owner))
		return buildOwnerTestOrderResponse(order, owner, ledger), nil
	}
	return s.invokeOwnerTestProvider(ctx, order, owner)
}

func validateOwnerTestOrderRecord(order *dbent.PaymentOrder, owner *ownerTestOrderContext) (*ownerTestOrderLedger, error) {
	if order == nil || owner == nil || order.OutTradeNo != owner.outTradeNo {
		return nil, infraerrors.Conflict("OWNER_TEST_IDEMPOTENCY_METADATA_MISMATCH", "owner test order metadata does not match the idempotency key")
	}
	ledger, err := ownerTestLedgerFromOrder(order)
	if err != nil {
		return nil, infraerrors.Conflict("OWNER_TEST_IDEMPOTENCY_METADATA_MISMATCH", "owner test order metadata is invalid")
	}
	if ledger.OwnerUserID != owner.input.AdminUserID || ledger.IdempotencyKeySHA256 != owner.idempotencyKeyHash {
		return nil, infraerrors.Conflict("OWNER_TEST_IDEMPOTENCY_METADATA_MISMATCH", "owner test order belongs to a different idempotency request")
	}
	if ledger.PayloadSHA256 != owner.payloadHash || ledger.ProviderRequest.AmountFen != owner.input.AmountFen || ledger.ProviderRequest.PaymentType != owner.input.PaymentType {
		return nil, infraerrors.Conflict("OWNER_TEST_IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with different owner test order data")
	}
	if ledger.RuntimeScopeSHA256 != owner.scopeHash || ownerTestRuntimeScopeHash(ledger.RuntimeScope) != ledger.RuntimeScopeSHA256 || ledger.RuntimeScope != owner.scope {
		return nil, infraerrors.Conflict("OWNER_TEST_IDEMPOTENCY_SCOPE_CONFLICT", "Idempotency-Key belongs to a different unified payment runtime scope")
	}
	request := ledger.ProviderRequest
	if request.ProductOrderNo != order.OutTradeNo || request.OrderType != payment.OrderTypeBalance || request.Amount != owner.amountDecimal ||
		request.Currency != payment.DefaultPaymentCurrency || request.ReturnURL != ownerTestReturnURL ||
		request.IdempotencyKey != "sub2:create:"+order.OutTradeNo || request.MetadataSource != "sub2" ||
		request.ExpiresInSecond < 300 || request.ExpiresInSecond > 7200 || request.Subject == "" ||
		order.UserID != owner.input.AdminUserID || order.OrderType != payment.OrderTypeBalance || order.PaymentType != owner.input.PaymentType ||
		math.Abs(order.Amount-owner.amount) > 1e-9 || math.Abs(order.PayAmount-owner.amount) > 1e-9 || order.FeeRate != 0 || !paymentOrderUsesUnifiedPay(order) {
		return nil, infraerrors.Conflict("OWNER_TEST_IDEMPOTENCY_METADATA_MISMATCH", "owner test order no longer matches its durable request")
	}
	if ledger.Dispatch.Generation < 0 || (ledger.Dispatch.ClaimToken == "" && ledger.Dispatch.LeaseExpiresAt != nil && ledger.Dispatch.LeaseExpiresAt.After(ownerTestTimestamp(time.Now()))) {
		return nil, infraerrors.Conflict("OWNER_TEST_IDEMPOTENCY_METADATA_MISMATCH", "owner test order dispatch metadata is invalid")
	}
	if ledger.AuthoritativeExpiresAt != nil {
		authoritativeExpiry := ownerTestTimestamp(*ledger.AuthoritativeExpiresAt)
		if authoritativeExpiry.IsZero() || !ownerTestTimestamp(order.ExpiresAt).Equal(authoritativeExpiry) || strings.TrimSpace(order.PaymentTradeNo) == "" {
			return nil, infraerrors.Conflict("OWNER_TEST_IDEMPOTENCY_METADATA_MISMATCH", "owner test order checkout deadline is invalid")
		}
	}
	return ledger, nil
}

func ownerTestLedgerFromOrder(order *dbent.PaymentOrder) (*ownerTestOrderLedger, error) {
	if order == nil || order.ProviderSnapshot == nil {
		return nil, errors.New("missing owner test snapshot")
	}
	raw, ok := order.ProviderSnapshot[ownerTestProviderSnapshotKey]
	if !ok {
		return nil, errors.New("missing owner test ledger")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var ledger ownerTestOrderLedger
	if err := json.Unmarshal(encoded, &ledger); err != nil || ledger.Version != ownerTestLedgerVersion {
		return nil, errors.New("invalid owner test ledger")
	}
	return &ledger, nil
}

func ownerTestOrderHasCheckout(order *dbent.PaymentOrder) bool {
	return order != nil && (strings.TrimSpace(order.PaymentTradeNo) != "" || strings.TrimSpace(psStringValue(order.PayURL)) != "" || strings.TrimSpace(psStringValue(order.QrCode)) != "")
}

// invokeOwnerTestProvider claims a short, fenced dispatch lease before the
// network call. A reclaimer updates the persisted UpdatedAt version, so a
// late executor can never turn a newer checkout into FAILED.
func (s *PaymentService) invokeOwnerTestProvider(ctx context.Context, order *dbent.PaymentOrder, owner *ownerTestOrderContext) (*CreateOrderResponse, error) {
	claimed, ledger, lease, dispatch, err := s.claimOwnerTestDispatch(ctx, order, owner)
	if err != nil {
		return nil, err
	}
	if !dispatch {
		return buildOwnerTestOrderResponse(claimed, owner, ledger), nil
	}
	providerReq := payment.CreatePaymentRequest{
		OrderID:          ledger.ProviderRequest.ProductOrderNo,
		Amount:           ledger.ProviderRequest.Amount,
		PaymentType:      ledger.ProviderRequest.PaymentType,
		OrderType:        ledger.ProviderRequest.OrderType,
		Subject:          ledger.ProviderRequest.Subject,
		ReturnURL:        ledger.ProviderRequest.ReturnURL,
		ExpiresInSeconds: ledger.ProviderRequest.ExpiresInSecond,
	}
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	providerResp, providerErr := s.unifiedPayment.CreatePayment(ctx, providerReq)
	finishProviderCall()
	if providerErr != nil {
		if ownerTestCreateMayHaveCommitted(providerErr) {
			released, releaseErr := s.releaseOwnerTestDispatch(ctx, claimed, owner, lease)
			if releaseErr != nil {
				return nil, releaseErr
			}
			if released {
				s.writeAuditLog(ctx, claimed.ID, "OWNER_TEST_CREATE_UNCONFIRMED", payment.TypeUnifiedPay, ownerTestAuditDetail(owner))
			}
			return nil, fmt.Errorf("%w: %v", unifiedpay.ErrCreateStateUnconfirmed, providerErr)
		}
		return s.finishOwnerTestProviderFailure(ctx, claimed, owner, lease, providerErr)
	}
	sanitizeCreatePaymentResponseDetails(providerResp)
	if providerResp == nil || strings.TrimSpace(providerResp.TradeNo) == "" || strings.TrimSpace(providerResp.PayURL) == "" {
		released, releaseErr := s.releaseOwnerTestDispatch(ctx, claimed, owner, lease)
		if releaseErr != nil {
			return nil, releaseErr
		}
		if released {
			s.writeAuditLog(ctx, claimed.ID, "OWNER_TEST_CREATE_UNCONFIRMED", payment.TypeUnifiedPay, ownerTestAuditDetail(owner))
		}
		return nil, fmt.Errorf("%w: checkout response was incomplete", unifiedpay.ErrCreateStateUnconfirmed)
	}
	expiresAt, validExpiry := ownerTestAuthoritativeExpiry(providerResp.ExpiresAt)
	if !validExpiry {
		released, releaseErr := s.releaseOwnerTestDispatch(ctx, claimed, owner, lease)
		if releaseErr != nil {
			return nil, releaseErr
		}
		if released {
			s.writeAuditLog(ctx, claimed.ID, "OWNER_TEST_CREATE_UNCONFIRMED", payment.TypeUnifiedPay, ownerTestAuditDetail(owner))
		}
		return nil, fmt.Errorf("%w: checkout response did not contain a future expiry", unifiedpay.ErrCreateStateUnconfirmed)
	}
	return s.finishOwnerTestProviderSuccess(ctx, claimed, owner, ledger, lease, providerResp, expiresAt)
}

// ownerTestAuthoritativeExpiry normalizes the deadline to the database's
// microsecond precision before it is used as both the row value and the
// fenced-ledger value. An expired or missing deadline must never publish a
// checkout, because the gateway could otherwise revive a locally stale order.
func ownerTestAuthoritativeExpiry(value time.Time) (time.Time, bool) {
	expiresAt := ownerTestTimestamp(value)
	return expiresAt, !expiresAt.IsZero() && expiresAt.After(ownerTestTimestamp(time.Now()))
}

// claimOwnerTestDispatch uses a short row lock only to replace an expired
// claim. The persisted timestamp is reloaded after the write because the
// database, rather than Go's clock precision, defines the finalizer version.
func (s *PaymentService) claimOwnerTestDispatch(ctx context.Context, initial *dbent.PaymentOrder, owner *ownerTestOrderContext) (*dbent.PaymentOrder, *ownerTestOrderLedger, *ownerTestDispatchLease, bool, error) {
	if initial == nil || owner == nil {
		return nil, nil, nil, false, infraerrors.ServiceUnavailable("OWNER_TEST_UNAVAILABLE", "owner test order is unavailable")
	}
	for attempts := 0; attempts < 4; attempts++ {
		tx, err := s.entClient.Tx(ctx)
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("begin owner test dispatch claim: %w", err)
		}
		claimed := false
		retryClaim := false
		var terminalOrder *dbent.PaymentOrder
		var terminalLedger *ownerTestOrderLedger
		var claimToken string
		var claimGeneration int64
		func() {
			defer func() { _ = tx.Rollback() }()
			query := tx.PaymentOrder.Query().Where(paymentorder.IDEQ(initial.ID))
			if tx.Client().Driver().Dialect() == "postgres" {
				query.ForUpdate()
			}
			current, queryErr := query.Only(ctx)
			if queryErr != nil {
				err = fmt.Errorf("lock owner test order: %w", queryErr)
				return
			}
			ledger, validationErr := validateOwnerTestOrderRecord(current, owner)
			if validationErr != nil {
				err = validationErr
				return
			}
			if ownerTestOrderHasCheckout(current) || current.Status != OrderStatusPending || !current.ExpiresAt.After(time.Now()) {
				terminalOrder = current
				terminalLedger = ledger
				return
			}
			now := ownerTestTimestamp(time.Now())
			if ownerTestDispatchActive(ledger.Dispatch, now) {
				err = infraerrors.Conflict("OWNER_TEST_ORDER_IN_PROGRESS", "owner test payment checkout is still being created; retry with the same Idempotency-Key")
				return
			}
			nextLedger := *ledger
			leaseExpiresAt := ownerTestTimestamp(now.Add(ownerTestDispatchLeaseDuration))
			claimToken = uuid.NewString()
			claimGeneration = ledger.Dispatch.Generation + 1
			nextLedger.Dispatch = ownerTestDispatch{
				Generation:     claimGeneration,
				ClaimToken:     claimToken,
				LeaseExpiresAt: &leaseExpiresAt,
			}
			nextSnapshot := clonePaymentOrderSnapshot(current.ProviderSnapshot)
			nextSnapshot[ownerTestProviderSnapshotKey] = nextLedger
			nextVersion := ownerTestNextVersion(current.UpdatedAt, now)
			updated, updateErr := tx.PaymentOrder.Update().Where(
				paymentorder.IDEQ(current.ID),
				paymentorder.StatusEQ(OrderStatusPending),
				paymentorder.PaymentTradeNoEQ(""),
			).SetProviderSnapshot(nextSnapshot).SetUpdatedAt(nextVersion).Save(ctx)
			if updateErr != nil {
				err = fmt.Errorf("claim owner test dispatch: %w", updateErr)
				return
			}
			if updated != 1 {
				retryClaim = true
				return
			}
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("commit owner test dispatch claim: %w", commitErr)
				return
			}
			claimed = true
		}()
		if err != nil {
			return nil, nil, nil, false, err
		}
		if terminalOrder != nil {
			return terminalOrder, terminalLedger, nil, false, nil
		}
		if retryClaim {
			continue
		}
		if !claimed {
			continue
		}
		current, getErr := s.entClient.PaymentOrder.Get(ctx, initial.ID)
		if getErr != nil {
			return nil, nil, nil, false, fmt.Errorf("reload owner test dispatch claim: %w", getErr)
		}
		ledger, validationErr := validateOwnerTestOrderRecord(current, owner)
		if validationErr != nil {
			return nil, nil, nil, false, validationErr
		}
		if ledger.Dispatch.ClaimToken == "" || ledger.Dispatch.LeaseExpiresAt == nil ||
			ledger.Dispatch.ClaimToken != claimToken || ledger.Dispatch.Generation != claimGeneration {
			return nil, nil, nil, false, infraerrors.Conflict("OWNER_TEST_DISPATCH_CONFLICT", "owner test dispatch claim was lost")
		}
		return current, ledger, &ownerTestDispatchLease{
			version: current.UpdatedAt, generation: ledger.Dispatch.Generation, token: ledger.Dispatch.ClaimToken,
		}, true, nil
	}
	return nil, nil, nil, false, infraerrors.Conflict("OWNER_TEST_DISPATCH_CONFLICT", "owner test dispatch changed concurrently")
}

func ownerTestTimestamp(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func ownerTestNextVersion(previous, now time.Time) time.Time {
	previous = ownerTestTimestamp(previous)
	now = ownerTestTimestamp(now)
	if !now.After(previous) {
		return previous.Add(time.Microsecond)
	}
	return now
}

func ownerTestDispatchActive(dispatch ownerTestDispatch, now time.Time) bool {
	return dispatch.ClaimToken != "" && dispatch.LeaseExpiresAt != nil && dispatch.LeaseExpiresAt.After(now)
}

// releaseOwnerTestDispatch makes a potentially-created central order retriable
// immediately. The same version fence prevents an old network error from
// clearing a later claimant's lease.
func (s *PaymentService) releaseOwnerTestDispatch(ctx context.Context, order *dbent.PaymentOrder, owner *ownerTestOrderContext, lease *ownerTestDispatchLease) (bool, error) {
	if order == nil || lease == nil {
		return false, infraerrors.ServiceUnavailable("OWNER_TEST_UNAVAILABLE", "owner test dispatch lease is unavailable")
	}
	ledger, err := validateOwnerTestOrderRecord(order, owner)
	if err != nil {
		return false, err
	}
	if ledger.Dispatch.Generation != lease.generation || ledger.Dispatch.ClaimToken != lease.token {
		return false, nil
	}
	now := ownerTestTimestamp(time.Now())
	nextLedger := *ledger
	nextLedger.Dispatch.ClaimToken = ""
	nextLedger.Dispatch.LeaseExpiresAt = &now
	nextSnapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
	nextSnapshot[ownerTestProviderSnapshotKey] = nextLedger
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.PaymentTradeNoEQ(""),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetProviderSnapshot(nextSnapshot).SetUpdatedAt(ownerTestNextVersion(lease.version, now)).Save(ctx)
	if err != nil {
		return false, fmt.Errorf("release owner test dispatch: %w", err)
	}
	return updated == 1, nil
}

func ownerTestCreateMayHaveCommitted(err error) bool {
	if errors.Is(err, unifiedpay.ErrCreateStateUnconfirmed) || errors.Is(err, unifiedpay.ErrInvalidResponse) ||
		errors.Is(err, unifiedpay.ErrRequestFailed) || errors.Is(err, unifiedpay.ErrResponseTooLarge) {
		return true
	}
	var apiErr *unifiedpay.APIError
	return errors.As(err, &apiErr) && apiErr.Retryable
}

func (s *PaymentService) finishOwnerTestProviderSuccess(ctx context.Context, order *dbent.PaymentOrder, owner *ownerTestOrderContext, ledger *ownerTestOrderLedger, lease *ownerTestDispatchLease, providerResp *payment.CreatePaymentResponse, expiresAt time.Time) (*CreateOrderResponse, error) {
	if lease == nil {
		return nil, infraerrors.ServiceUnavailable("OWNER_TEST_UNAVAILABLE", "owner test dispatch lease is unavailable")
	}
	if ledger == nil || ledger.Dispatch.Generation != lease.generation || ledger.Dispatch.ClaimToken != lease.token {
		return s.replayOwnerTestOrder(ctx, owner)
	}
	if normalizedExpiry, validExpiry := ownerTestAuthoritativeExpiry(expiresAt); !validExpiry {
		released, releaseErr := s.releaseOwnerTestDispatch(ctx, order, owner, lease)
		if releaseErr != nil {
			return nil, releaseErr
		}
		if released {
			s.writeAuditLog(ctx, order.ID, "OWNER_TEST_CREATE_UNCONFIRMED", payment.TypeUnifiedPay, ownerTestAuditDetail(owner))
		}
		return nil, fmt.Errorf("%w: checkout response did not contain a future expiry", unifiedpay.ErrCreateStateUnconfirmed)
	} else {
		expiresAt = normalizedExpiry
	}
	nextLedger := *ledger
	nextLedger.AuthoritativeExpiresAt = &expiresAt
	snapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
	snapshot["payment_order_id"] = strings.TrimSpace(providerResp.TradeNo)
	snapshot[ownerTestProviderSnapshotKey] = nextLedger
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.PaymentTradeNoEQ(""),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetPaymentTradeNo(strings.TrimSpace(providerResp.TradeNo)).
		SetPayURL(strings.TrimSpace(providerResp.PayURL)).
		SetNillableQrCode(psNilIfEmpty(providerResp.QRCode)).
		SetExpiresAt(expiresAt).
		SetProviderSnapshot(snapshot).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("persist owner test checkout: %w", err)
	}
	if updated == 0 {
		return s.replayOwnerTestOrder(ctx, owner)
	}
	reloaded, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
	if err != nil {
		return nil, fmt.Errorf("reload owner test checkout: %w", err)
	}
	s.writeAuditLog(ctx, order.ID, "OWNER_TEST_CHECKOUT_CREATED", fmt.Sprintf("admin:%d", owner.input.AdminUserID), ownerTestAuditDetail(owner))
	return buildOwnerTestOrderResponse(reloaded, owner, ledger), nil
}

func (s *PaymentService) finishOwnerTestProviderFailure(ctx context.Context, order *dbent.PaymentOrder, owner *ownerTestOrderContext, lease *ownerTestDispatchLease, providerErr error) (*CreateOrderResponse, error) {
	if lease == nil {
		return nil, infraerrors.ServiceUnavailable("OWNER_TEST_UNAVAILABLE", "owner test dispatch lease is unavailable")
	}
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.PaymentTradeNoEQ(""),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetStatus(OrderStatusFailed).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark owner test checkout failed: %w", err)
	}
	if updated == 0 {
		return s.replayOwnerTestOrder(ctx, owner)
	}
	s.writeAuditLog(ctx, order.ID, "OWNER_TEST_CHECKOUT_FAILED", payment.TypeUnifiedPay, ownerTestAuditDetail(owner))
	return nil, infraerrors.ServiceUnavailable("OWNER_TEST_PAYMENT_GATEWAY_ERROR", fmt.Sprintf("owner test payment gateway error: %s", providerErr.Error()))
}

func buildOwnerTestOrderResponse(order *dbent.PaymentOrder, owner *ownerTestOrderContext, ledger *ownerTestOrderLedger) *CreateOrderResponse {
	paymentType := ""
	paymentMode := ""
	payURL := ""
	qrCode := ""
	if ledger != nil {
		paymentType = ledger.ProviderRequest.PaymentType
	}
	if owner != nil && owner.selection != nil {
		paymentMode = owner.selection.PaymentMode
	}
	// A completed, failed, closed, or expired record is authoritative status,
	// not an invitation to reuse a stale browser checkout or QR payload.
	if order.Status == OrderStatusPending && order.ExpiresAt.After(time.Now()) {
		payURL = psStringValue(order.PayURL)
		qrCode = psStringValue(order.QrCode)
	}
	return &CreateOrderResponse{
		OrderID:     order.ID,
		Amount:      order.Amount,
		PayAmount:   order.PayAmount,
		FeeRate:     order.FeeRate,
		Status:      order.Status,
		ResultType:  payment.CreatePaymentResultOrderCreated,
		PaymentType: paymentType,
		OutTradeNo:  order.OutTradeNo,
		PayURL:      payURL,
		QRCode:      qrCode,
		Currency:    payment.DefaultPaymentCurrency,
		ExpiresAt:   order.ExpiresAt,
		PaymentMode: paymentMode,
	}
}

func ownerTestAuditDetail(owner *ownerTestOrderContext) map[string]any {
	if owner == nil {
		return map[string]any{"classification": "owner_test"}
	}
	return map[string]any{
		"classification":         "owner_test",
		"amount_fen":             owner.input.AmountFen,
		"payment_type":           owner.input.PaymentType,
		"out_trade_no":           owner.outTradeNo,
		"payload_sha256":         owner.payloadHash,
		"runtime_scope_sha256":   owner.scopeHash,
		"idempotency_key_sha256": owner.idempotencyKeyHash,
	}
}
