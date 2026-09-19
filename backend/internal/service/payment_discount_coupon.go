package service

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	stdsql "database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
)

const (
	paymentDiscountTypeFixed   = "fixed"
	paymentDiscountTypePercent = "percent"

	paymentDiscountUseReserved   = "reserved"
	paymentDiscountUseConsumed   = "consumed"
	paymentDiscountUseReleased   = "released"
	paymentDiscountUsePaidReview = "paid_review"

	paymentDiscountDefaultPageSize = 20
	paymentDiscountMaximumPageSize = 100
	paymentDiscountMaximumPage     = 1_000_000
	paymentDiscountRateLimit       = 20
	paymentDiscountMaximumPlanIDs  = 100
)

var (
	// ErrPaymentDiscountInvalid is deliberately shared for an unknown, expired,
	// disabled, exhausted, or audience-restricted code. Customer-facing callers
	// must not distinguish those conditions.
	ErrPaymentDiscountInvalid         = infraerrors.BadRequest("COUPON_INVALID", "coupon is invalid or unavailable")
	ErrPaymentDiscountRateLimited     = infraerrors.TooManyRequests("COUPON_RATE_LIMITED", "too many coupon attempts, please try again later")
	ErrPaymentDiscountVersionConflict = infraerrors.Conflict("PAYMENT_DISCOUNT_VERSION_CONFLICT", "coupon configuration changed; reload before saving")
	ErrPaymentDiscountReplay          = errors.New("payment discount idempotency replay")

	errPaymentDiscountLedgerMissing = errors.New("payment discount ledger is missing")
)

// PaymentDiscountCode is the administrative projection of a durable payment
// coupon. Counts include only capacity-holding reserved and consumed uses.
type PaymentDiscountCode struct {
	ID             int64     `json:"id"`
	Code           string    `json:"code"`
	DiscountType   string    `json:"discount_type"`
	DiscountValue  string    `json:"discount_value"`
	Currency       string    `json:"currency"`
	MaxUses        int       `json:"max_uses"`
	PerUserMaxUses int       `json:"per_user_max_uses"`
	TargetUserID   *int64    `json:"target_user_id,omitempty"`
	StartsAt       time.Time `json:"starts_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Enabled        bool      `json:"enabled"`
	Version        int64     `json:"version"`
	Notes          string    `json:"notes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ReservedUses   int       `json:"reserved_uses"`
	ConsumedUses   int       `json:"consumed_uses"`
	OrderTypes     []string  `json:"order_types"`
	PlanIDs        []int64   `json:"plan_ids"`
}

// PaymentDiscountCodeInput contains the editable administrative state. A nil
// PerUserMaxUses means the product default of one; a pointer to zero means no
// per-user cap, which is distinct from an omitted field in JSON.
type PaymentDiscountCodeInput struct {
	Code           string    `json:"code,omitempty"`
	DiscountType   string    `json:"discount_type"`
	DiscountValue  string    `json:"discount_value"`
	Currency       string    `json:"currency"`
	MaxUses        int       `json:"max_uses"`
	PerUserMaxUses *int      `json:"per_user_max_uses,omitempty"`
	TargetUserID   *int64    `json:"target_user_id,omitempty"`
	StartsAt       time.Time `json:"starts_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Enabled        bool      `json:"enabled"`
	Notes          string    `json:"notes"`
	// Nil slices mean the field was omitted. Create defaults omitted order
	// types to both supported purchase kinds; update preserves the stored
	// scope. An explicit empty order_types array is rejected.
	OrderTypes []string `json:"order_types,omitempty"`
	// An empty plan_ids array permits every subscription plan. On update a nil
	// slice preserves the stored list, while an explicit empty array clears it.
	PlanIDs []int64 `json:"plan_ids,omitempty"`
}

// PaymentDiscountUse is the customer-order-safe ledger projection. Request
// hashes, idempotency hashes, quote JSON, and provider response JSON remain
// internal to the service layer.
type PaymentDiscountUse struct {
	ID      int64 `json:"id"`
	CodeID  int64 `json:"code_id"`
	OrderID int64 `json:"order_id"`
	UserID  int64 `json:"user_id"`
	// Username is projected only for administrative usage history. The list
	// query deliberately retains soft-deleted user profiles for attribution.
	Username       string     `json:"username,omitempty"`
	Status         string     `json:"status"`
	OriginalAmount string     `json:"original_amount"`
	DiscountAmount string     `json:"discount_amount"`
	PayAmount      string     `json:"pay_amount"`
	Currency       string     `json:"currency"`
	QuoteVersion   int64      `json:"quote_version"`
	QuoteRevision  string     `json:"quote_revision"`
	ReservedAt     time.Time  `json:"reserved_at"`
	ConsumedAt     *time.Time `json:"consumed_at,omitempty"`
	ReleasedAt     *time.Time `json:"released_at,omitempty"`
	PaidReviewAt   *time.Time `json:"paid_review_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// PaymentDiscountCodeAudit records an administrative write without exposing
// operational sessions or payment credentials.
type PaymentDiscountCodeAudit struct {
	ID          int64           `json:"id"`
	CodeID      int64           `json:"code_id"`
	AdminUserID int64           `json:"admin_user_id"`
	Action      string          `json:"action"`
	Detail      json.RawMessage `json:"detail"`
	CreatedAt   time.Time       `json:"created_at"`
}

// PaymentDiscountQuote is the exact money boundary used by payment creation.
// Decimal values remain strings so JSON cannot silently round a cash amount.
type PaymentDiscountQuote struct {
	CodeID         int64  `json:"code_id"`
	Code           string `json:"code"`
	Version        int64  `json:"version"`
	OriginalAmount string `json:"original_amount"`
	DiscountAmount string `json:"discount_amount"`
	PayAmount      string `json:"pay_amount"`
	Currency       string `json:"currency"`
	Revision       string `json:"revision"`
}

type normalizedPaymentDiscountInput struct {
	input              PaymentDiscountCodeInput
	discountValue      decimal.Decimal
	orderTypesProvided bool
	planIDsProvided    bool
}

type paymentDiscountUsageCounts struct {
	reserved     int64
	consumed     int64
	userReserved int64
	userConsumed int64
}

type paymentDiscountUseRecord struct {
	PaymentDiscountUse
	RequestHash     string
	IdempotencyHash string
}

func normalizePaymentDiscountCode(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if len(code) < 8 || len(code) > 32 {
		return "", errors.New("payment discount code has an invalid length")
	}
	buf := make([]byte, len(code))
	for i := range code {
		ch := code[i]
		switch {
		case ch >= 'a' && ch <= 'z':
			buf[i] = ch - ('a' - 'A')
		case (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-':
			buf[i] = ch
		default:
			return "", errors.New("payment discount code contains an invalid character")
		}
	}
	return string(buf), nil
}

func generatePaymentDiscountCode() (string, error) {
	var raw [16]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(raw[:])), nil
}

func normalizePaymentDiscountCurrency(raw string) (string, error) {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if currency != "CNY" && currency != "USD" {
		return "", errors.New("payment discount currency is invalid")
	}
	return currency, nil
}

func parsePaymentDiscountAmount(raw, currency string, allowZero bool) (decimal.Decimal, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return decimal.Zero, errors.New("payment discount amount is required")
	}
	dotSeen := false
	for i := range text {
		ch := text[i]
		switch {
		case ch >= '0' && ch <= '9':
		case ch == '.' && !dotSeen:
			dotSeen = true
		default:
			return decimal.Zero, errors.New("payment discount amount is invalid")
		}
	}
	if text[0] == '.' || text[len(text)-1] == '.' {
		return decimal.Zero, errors.New("payment discount amount is invalid")
	}
	value, err := decimal.NewFromString(text)
	if err != nil || value.IsNegative() || (!allowZero && !value.IsPositive()) {
		return decimal.Zero, errors.New("payment discount amount is invalid")
	}
	precision := int32(payment.CurrencyMaxFractionDigits(currency))
	if !value.Equal(value.Round(precision)) {
		return decimal.Zero, errors.New("payment discount amount has unsupported precision")
	}
	return value, nil
}

func canonicalPaymentDiscountAmount(value decimal.Decimal, currency string) string {
	return value.StringFixed(int32(payment.CurrencyMaxFractionDigits(currency)))
}

func paymentDiscountDefaultOrderTypes() []string {
	return []string{payment.OrderTypeBalance, payment.OrderTypeSubscription}
}

func clonePaymentDiscountOrderTypes(values []string) []string {
	return append([]string(nil), values...)
}

func clonePaymentDiscountPlanIDs(values []int64) []int64 {
	return append([]int64(nil), values...)
}

func normalizePaymentDiscountOrderTypes(raw []string, defaultWhenNil bool) ([]string, error) {
	if raw == nil {
		if defaultWhenNil {
			return paymentDiscountDefaultOrderTypes(), nil
		}
		return nil, errors.New("payment discount order types are missing")
	}
	if len(raw) == 0 {
		return nil, errors.New("payment discount order types are empty")
	}
	if len(raw) > paymentDiscountMaximumPlanIDs {
		return nil, errors.New("payment discount order types exceed the limit")
	}
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		orderType := strings.ToLower(strings.TrimSpace(value))
		switch orderType {
		case payment.OrderTypeBalance, payment.OrderTypeSubscription:
			seen[orderType] = struct{}{}
		default:
			return nil, errors.New("payment discount order type is invalid")
		}
	}
	orderTypes := make([]string, 0, len(seen))
	for _, orderType := range paymentDiscountDefaultOrderTypes() {
		if _, ok := seen[orderType]; ok {
			orderTypes = append(orderTypes, orderType)
		}
	}
	if len(orderTypes) == 0 {
		return nil, errors.New("payment discount order types are empty")
	}
	return orderTypes, nil
}

func normalizePaymentDiscountPlanIDs(raw []int64) ([]int64, error) {
	if len(raw) > paymentDiscountMaximumPlanIDs {
		return nil, errors.New("payment discount plan ids exceed the limit")
	}
	seen := make(map[int64]struct{}, len(raw))
	for _, planID := range raw {
		if planID <= 0 {
			return nil, errors.New("payment discount plan id is invalid")
		}
		seen[planID] = struct{}{}
	}
	planIDs := make([]int64, 0, len(seen))
	for planID := range seen {
		planIDs = append(planIDs, planID)
	}
	sort.Slice(planIDs, func(i, j int) bool { return planIDs[i] < planIDs[j] })
	return planIDs, nil
}

func paymentDiscountOrderTypesContain(orderTypes []string, wanted string) bool {
	for _, orderType := range orderTypes {
		if orderType == wanted {
			return true
		}
	}
	return false
}

func normalizePaymentDiscountScope(orderTypes []string, planIDs []int64, defaultOrderTypes bool) ([]string, []int64, error) {
	normalizedOrderTypes, err := normalizePaymentDiscountOrderTypes(orderTypes, defaultOrderTypes)
	if err != nil {
		return nil, nil, err
	}
	normalizedPlanIDs, err := normalizePaymentDiscountPlanIDs(planIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(normalizedPlanIDs) > 0 && !paymentDiscountOrderTypesContain(normalizedOrderTypes, payment.OrderTypeSubscription) {
		return nil, nil, errors.New("payment discount plan ids require subscription scope")
	}
	return normalizedOrderTypes, normalizedPlanIDs, nil
}

func paymentDiscountScopeJSON(orderTypes []string, planIDs []int64) (string, string, error) {
	orderTypesJSON, err := json.Marshal(orderTypes)
	if err != nil {
		return "", "", err
	}
	planIDsJSON, err := json.Marshal(planIDs)
	if err != nil {
		return "", "", err
	}
	return string(orderTypesJSON), string(planIDsJSON), nil
}

func parsePaymentDiscountStoredScope(orderTypesJSON, planIDsJSON string) ([]string, []int64, error) {
	var orderTypes []string
	if err := json.Unmarshal([]byte(orderTypesJSON), &orderTypes); err != nil {
		return nil, nil, err
	}
	var planIDs []int64
	if err := json.Unmarshal([]byte(planIDsJSON), &planIDs); err != nil {
		return nil, nil, err
	}
	return normalizePaymentDiscountScope(orderTypes, planIDs, false)
}

func normalizePaymentDiscountInput(input PaymentDiscountCodeInput, allowEmptyCode bool, now time.Time) (normalizedPaymentDiscountInput, error) {
	normalized := input
	if strings.TrimSpace(input.Code) != "" {
		code, err := normalizePaymentDiscountCode(input.Code)
		if err != nil {
			return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_CODE", "coupon code is invalid")
		}
		normalized.Code = code
	} else if !allowEmptyCode {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_CODE", "coupon code is required")
	} else {
		normalized.Code = ""
	}

	normalized.DiscountType = strings.ToLower(strings.TrimSpace(input.DiscountType))
	if normalized.DiscountType != paymentDiscountTypeFixed && normalized.DiscountType != paymentDiscountTypePercent {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_TYPE", "coupon discount type is invalid")
	}
	currency, err := normalizePaymentDiscountCurrency(input.Currency)
	if err != nil {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_CURRENCY", "coupon currency must be CNY or USD")
	}
	normalized.Currency = currency
	value, err := parsePaymentDiscountAmount(input.DiscountValue, currency, false)
	if err != nil || (normalized.DiscountType == paymentDiscountTypePercent && value.GreaterThan(decimal.NewFromInt(100))) {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_VALUE", "coupon discount value is invalid")
	}
	normalized.DiscountValue = canonicalPaymentDiscountAmount(value, currency)

	if normalized.MaxUses < 0 {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_CAP", "coupon maximum uses cannot be negative")
	}
	perUserMaxUses := 1
	if input.PerUserMaxUses != nil {
		perUserMaxUses = *input.PerUserMaxUses
	}
	if perUserMaxUses < 0 {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_CAP", "coupon per-user maximum cannot be negative")
	}
	normalized.PerUserMaxUses = &perUserMaxUses
	orderTypes, planIDs, err := normalizePaymentDiscountScope(input.OrderTypes, input.PlanIDs, true)
	if err != nil {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_SCOPE", "coupon scope is invalid")
	}
	normalized.OrderTypes = orderTypes
	normalized.PlanIDs = planIDs

	if normalized.TargetUserID != nil && *normalized.TargetUserID <= 0 {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_TARGET_USER", "coupon target user is invalid")
	}
	if normalized.TargetUserID != nil {
		target := *normalized.TargetUserID
		normalized.TargetUserID = &target
	}

	normalized.StartsAt = input.StartsAt.UTC()
	normalized.ExpiresAt = input.ExpiresAt.UTC()
	if normalized.StartsAt.IsZero() || normalized.ExpiresAt.IsZero() ||
		!normalized.ExpiresAt.After(normalized.StartsAt) || !normalized.ExpiresAt.After(now.UTC()) {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_SCHEDULE", "coupon expiry must be after its start and in the future")
	}

	if !utf8.ValidString(input.Notes) || strings.Contains(input.Notes, "\x00") {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_NOTES", "coupon notes are invalid")
	}
	normalized.Notes = strings.TrimSpace(input.Notes)
	if utf8.RuneCountInString(normalized.Notes) > 2000 {
		return normalizedPaymentDiscountInput{}, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_NOTES", "coupon notes are too long")
	}

	return normalizedPaymentDiscountInput{
		input:              normalized,
		discountValue:      value,
		orderTypesProvided: input.OrderTypes != nil,
		planIDsProvided:    input.PlanIDs != nil,
	}, nil
}

func paymentDiscountInputTargetUserID(input PaymentDiscountCodeInput) any {
	if input.TargetUserID == nil {
		return nil
	}
	return *input.TargetUserID
}

func paymentDiscountCodeColumns() string {
	return `id, code, discount_type, CAST(discount_value AS TEXT), currency,
		max_uses, per_user_max_uses, target_user_id, starts_at, expires_at,
		enabled, version, notes, CAST(order_types AS TEXT), CAST(plan_ids AS TEXT),
		created_at, updated_at`
}

func scanPaymentDiscountCode(rows *stdsql.Rows, includeCounts bool) (*PaymentDiscountCode, error) {
	code := &PaymentDiscountCode{}
	var (
		discountValue  string
		targetUserID   stdsql.NullInt64
		maxUses        int64
		perUserMax     int64
		orderTypesJSON string
		planIDsJSON    string
		reserved       int64
		consumed       int64
	)
	args := []any{
		&code.ID, &code.Code, &code.DiscountType, &discountValue, &code.Currency,
		&maxUses, &perUserMax, &targetUserID, &code.StartsAt, &code.ExpiresAt,
		&code.Enabled, &code.Version, &code.Notes, &orderTypesJSON, &planIDsJSON,
		&code.CreatedAt, &code.UpdatedAt,
	}
	if includeCounts {
		args = append(args, &reserved, &consumed)
	}
	if err := rows.Scan(args...); err != nil {
		return nil, err
	}
	var err error
	if code.MaxUses, err = paymentDiscountInt(maxUses); err != nil {
		return nil, err
	}
	if code.PerUserMaxUses, err = paymentDiscountInt(perUserMax); err != nil {
		return nil, err
	}
	if includeCounts {
		if code.ReservedUses, err = paymentDiscountInt(reserved); err != nil {
			return nil, err
		}
		if code.ConsumedUses, err = paymentDiscountInt(consumed); err != nil {
			return nil, err
		}
	}
	if targetUserID.Valid {
		target := targetUserID.Int64
		code.TargetUserID = &target
	}
	value, err := parsePaymentDiscountAmount(discountValue, code.Currency, false)
	if err != nil {
		return nil, errors.New("stored payment discount value is invalid")
	}
	code.DiscountValue = canonicalPaymentDiscountAmount(value, code.Currency)
	code.OrderTypes, code.PlanIDs, err = parsePaymentDiscountStoredScope(orderTypesJSON, planIDsJSON)
	if err != nil {
		return nil, errors.New("stored payment discount scope is invalid")
	}
	return code, nil
}

func paymentDiscountInt(value int64) (int, error) {
	converted := int(value)
	if value < 0 || int64(converted) != value {
		return 0, errors.New("payment discount integer is out of range")
	}
	return converted, nil
}

func loadPaymentDiscountCodeByID(ctx context.Context, client *dbent.Client, id int64, lock bool) (*PaymentDiscountCode, bool, error) {
	if client == nil || id <= 0 {
		return nil, false, errors.New("invalid payment discount code lookup")
	}
	query := `SELECT ` + paymentDiscountCodeColumns() + ` FROM payment_discount_codes WHERE id = $1`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, id)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	code, err := scanPaymentDiscountCode(rows, false)
	if err != nil {
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	return code, true, nil
}

func loadPaymentDiscountCodeByCode(ctx context.Context, client *dbent.Client, code string, lock bool) (*PaymentDiscountCode, bool, error) {
	if client == nil || code == "" {
		return nil, false, errors.New("invalid payment discount code lookup")
	}
	query := `SELECT ` + paymentDiscountCodeColumns() + ` FROM payment_discount_codes WHERE code = $1`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, code)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	loaded, err := scanPaymentDiscountCode(rows, false)
	if err != nil {
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	return loaded, true, nil
}

func paymentDiscountUserExists(ctx context.Context, client *dbent.Client, userID int64) (bool, error) {
	if client == nil || userID <= 0 {
		return false, nil
	}
	rows, err := client.QueryContext(ctx, `SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL LIMIT 1`, userID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, rows.Err()
	}
	return true, rows.Err()
}

func requirePaymentDiscountUser(ctx context.Context, client *dbent.Client, userID int64, reason string) error {
	exists, err := paymentDiscountUserExists(ctx, client, userID)
	if err != nil {
		return err
	}
	if !exists {
		return infraerrors.BadRequest(reason, "coupon target user does not exist")
	}
	return nil
}

func requirePaymentDiscountPlans(ctx context.Context, client *dbent.Client, planIDs []int64) error {
	if len(planIDs) == 0 {
		return nil
	}
	if client == nil {
		return errors.New("payment discount plan lookup has no client")
	}
	var query strings.Builder
	_, _ = query.WriteString(`SELECT id FROM subscription_plans WHERE id IN (`)
	args := make([]any, 0, len(planIDs))
	for index, planID := range planIDs {
		if index > 0 {
			_ = query.WriteByte(',')
		}
		_ = query.WriteByte('$')
		_, _ = query.WriteString(strconv.Itoa(index + 1))
		args = append(args, planID)
	}
	_ = query.WriteByte(')')
	rows, err := client.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	found := make(map[int64]struct{}, len(planIDs))
	for rows.Next() {
		var planID int64
		if err := rows.Scan(&planID); err != nil {
			return err
		}
		found[planID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(found) != len(planIDs) {
		return infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_PLAN", "coupon subscription plan does not exist")
	}
	return nil
}

func paymentDiscountStoredValue(code *PaymentDiscountCode) (decimal.Decimal, error) {
	if code == nil || code.Version <= 0 || code.MaxUses < 0 || code.PerUserMaxUses < 0 ||
		code.StartsAt.IsZero() || code.ExpiresAt.IsZero() || !code.ExpiresAt.After(code.StartsAt) {
		return decimal.Zero, errors.New("stored payment discount configuration is invalid")
	}
	if _, err := normalizePaymentDiscountCurrency(code.Currency); err != nil {
		return decimal.Zero, err
	}
	if _, _, err := normalizePaymentDiscountScope(code.OrderTypes, code.PlanIDs, false); err != nil {
		return decimal.Zero, err
	}
	value, err := parsePaymentDiscountAmount(code.DiscountValue, code.Currency, false)
	if err != nil {
		return decimal.Zero, err
	}
	switch code.DiscountType {
	case paymentDiscountTypeFixed:
	case paymentDiscountTypePercent:
		if value.GreaterThan(decimal.NewFromInt(100)) {
			return decimal.Zero, errors.New("stored payment discount percent is invalid")
		}
	default:
		return decimal.Zero, errors.New("stored payment discount type is invalid")
	}
	return value, nil
}

func paymentDiscountUsageCount(ctx context.Context, client *dbent.Client, codeID, userID int64) (paymentDiscountUsageCounts, error) {
	if client == nil || codeID <= 0 || userID <= 0 {
		return paymentDiscountUsageCounts{}, errors.New("invalid payment discount usage count")
	}
	rows, err := client.QueryContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status = 'reserved' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'consumed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN user_id = $2 AND status = 'reserved' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN user_id = $2 AND status = 'consumed' THEN 1 ELSE 0 END), 0)
		FROM payment_discount_uses
		WHERE code_id = $1`, codeID, userID)
	if err != nil {
		return paymentDiscountUsageCounts{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return paymentDiscountUsageCounts{}, err
		}
		return paymentDiscountUsageCounts{}, errors.New("payment discount usage count has no row")
	}
	counts := paymentDiscountUsageCounts{}
	if err := rows.Scan(&counts.reserved, &counts.consumed, &counts.userReserved, &counts.userConsumed); err != nil {
		return paymentDiscountUsageCounts{}, err
	}
	return counts, rows.Err()
}

func paymentDiscountActiveCapacity(ctx context.Context, client *dbent.Client, codeID int64) (int64, int64, error) {
	if client == nil || codeID <= 0 {
		return 0, 0, errors.New("invalid payment discount capacity lookup")
	}
	rows, err := client.QueryContext(ctx, `SELECT COALESCE(SUM(active_uses), 0), COALESCE(MAX(active_uses), 0)
		FROM (
			SELECT user_id, COUNT(*) AS active_uses
			FROM payment_discount_uses
			WHERE code_id = $1 AND status IN ('reserved', 'consumed')
			GROUP BY user_id
		) AS active_by_user`, codeID)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, 0, err
		}
		return 0, 0, errors.New("payment discount capacity lookup has no row")
	}
	var total, maximum int64
	if err := rows.Scan(&total, &maximum); err != nil {
		return 0, 0, err
	}
	return total, maximum, rows.Err()
}

func paymentDiscountCapacityAvailable(code *PaymentDiscountCode, counts paymentDiscountUsageCounts) bool {
	if code == nil {
		return false
	}
	if code.MaxUses > 0 && counts.reserved+counts.consumed >= int64(code.MaxUses) {
		return false
	}
	if code.PerUserMaxUses > 0 && counts.userReserved+counts.userConsumed >= int64(code.PerUserMaxUses) {
		return false
	}
	return true
}

func validatePaymentDiscountAvailability(ctx context.Context, client *dbent.Client, code *PaymentDiscountCode, userID int64, now time.Time) error {
	if _, err := paymentDiscountStoredValue(code); err != nil {
		return ErrPaymentDiscountInvalid
	}
	if !code.Enabled || now.Before(code.StartsAt) || !now.Before(code.ExpiresAt) ||
		(code.TargetUserID != nil && *code.TargetUserID != userID) {
		return ErrPaymentDiscountInvalid
	}
	counts, err := paymentDiscountUsageCount(ctx, client, code.ID, userID)
	if err != nil {
		return err
	}
	if !paymentDiscountCapacityAvailable(code, counts) {
		return ErrPaymentDiscountInvalid
	}
	return nil
}

func normalizePaymentDiscountQuoteScope(orderType string, planID int64) (string, int64, error) {
	normalizedOrderType := strings.ToLower(strings.TrimSpace(orderType))
	switch normalizedOrderType {
	case payment.OrderTypeBalance:
		if planID != 0 {
			return "", 0, errors.New("balance coupon quote has a plan id")
		}
		return normalizedOrderType, 0, nil
	case payment.OrderTypeSubscription:
		if planID <= 0 {
			return "", 0, errors.New("subscription coupon quote has no plan id")
		}
		return normalizedOrderType, planID, nil
	default:
		return "", 0, errors.New("coupon quote order type is invalid")
	}
}

func validatePaymentDiscountScope(code *PaymentDiscountCode, orderType string, planID int64) error {
	if code == nil {
		return ErrPaymentDiscountInvalid
	}
	normalizedOrderType, normalizedPlanID, err := normalizePaymentDiscountQuoteScope(orderType, planID)
	if err != nil || !paymentDiscountOrderTypesContain(code.OrderTypes, normalizedOrderType) {
		return ErrPaymentDiscountInvalid
	}
	if normalizedOrderType != payment.OrderTypeSubscription || len(code.PlanIDs) == 0 {
		return nil
	}
	for _, allowedPlanID := range code.PlanIDs {
		if allowedPlanID == normalizedPlanID {
			return nil
		}
	}
	return ErrPaymentDiscountInvalid
}

func paymentDiscountRevision(binding string, code *PaymentDiscountCode, userID int64, orderType string, planID int64, original, discount, payAmount string) string {
	canonical := strings.Join([]string{
		"payment-discount-v1", binding, strconv.FormatInt(code.ID, 10), code.Code,
		strconv.FormatInt(code.Version, 10), strconv.FormatInt(userID, 10), code.Currency,
		orderType, strconv.FormatInt(planID, 10),
		original, discount, payAmount,
	}, "\x1f")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// quotePaymentDiscount performs only server-authoritative validation and cash
// math. Callers creating an order pass lock=true from their transaction so the
// quote and the subsequent reservation share the coupon parent-row lock.
func quotePaymentDiscount(ctx context.Context, client *dbent.Client, userID int64, rawCode string, original decimal.Decimal, currency, orderType string, planID int64, binding string, lock bool) (*PaymentDiscountQuote, error) {
	if client == nil || userID <= 0 {
		return nil, ErrPaymentDiscountInvalid
	}
	orderType, planID, err := normalizePaymentDiscountQuoteScope(orderType, planID)
	if err != nil {
		return nil, ErrPaymentDiscountInvalid
	}
	codeText, err := normalizePaymentDiscountCode(rawCode)
	if err != nil {
		return nil, ErrPaymentDiscountInvalid
	}
	currency, err = normalizePaymentDiscountCurrency(currency)
	if err != nil {
		return nil, ErrPaymentDiscountInvalid
	}
	originalText := canonicalPaymentDiscountAmount(original, currency)
	canonicalOriginal, err := parsePaymentDiscountAmount(originalText, currency, false)
	if err != nil || !canonicalOriginal.Equal(original) {
		return nil, ErrPaymentDiscountInvalid
	}

	code, found, err := loadPaymentDiscountCodeByCode(ctx, client, codeText, lock)
	if err != nil {
		return nil, err
	}
	if !found || code.Currency != currency {
		return nil, ErrPaymentDiscountInvalid
	}
	value, err := paymentDiscountStoredValue(code)
	if err != nil {
		return nil, ErrPaymentDiscountInvalid
	}
	if err := validatePaymentDiscountAvailability(ctx, client, code, userID, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := validatePaymentDiscountScope(code, orderType, planID); err != nil {
		return nil, err
	}

	var rawPayAmount decimal.Decimal
	switch code.DiscountType {
	case paymentDiscountTypeFixed:
		rawPayAmount = canonicalOriginal.Sub(value)
	case paymentDiscountTypePercent:
		rawPayAmount = canonicalOriginal.Mul(decimal.NewFromInt(100).Sub(value)).Div(decimal.NewFromInt(100))
	default:
		return nil, ErrPaymentDiscountInvalid
	}
	// Coupon reductions settle to a whole currency unit. Ceiling happens before
	// the one-unit floor so a fractional discount can never make the customer
	// pay more than the non-coupon amount.
	payAmount := rawPayAmount.Ceil()
	minimum := decimal.NewFromInt(1)
	if payAmount.LessThan(minimum) {
		payAmount = minimum
	}
	if !payAmount.LessThan(canonicalOriginal) {
		return nil, ErrPaymentDiscountInvalid
	}
	discount := canonicalOriginal.Sub(payAmount)
	if !discount.IsPositive() {
		return nil, ErrPaymentDiscountInvalid
	}

	quote := &PaymentDiscountQuote{
		CodeID:         code.ID,
		Code:           code.Code,
		Version:        code.Version,
		OriginalAmount: canonicalPaymentDiscountAmount(canonicalOriginal, currency),
		DiscountAmount: canonicalPaymentDiscountAmount(discount, currency),
		PayAmount:      canonicalPaymentDiscountAmount(payAmount, currency),
		Currency:       currency,
	}
	quote.Revision = paymentDiscountRevision(binding, code, userID, orderType, planID, quote.OriginalAmount, quote.DiscountAmount, quote.PayAmount)
	return quote, nil
}

func normalizePaymentDiscountDigest(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if len(value) != sha256.Size*2 {
		return "", errors.New("payment discount digest has an invalid length")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("payment discount digest is invalid")
	}
	return value, nil
}

func parsePaymentDiscountQuote(quote *PaymentDiscountQuote) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	if quote == nil || quote.CodeID <= 0 || quote.Version <= 0 {
		return decimal.Zero, decimal.Zero, decimal.Zero, errors.New("payment discount quote is invalid")
	}
	code, err := normalizePaymentDiscountCode(quote.Code)
	if err != nil || code != quote.Code {
		return decimal.Zero, decimal.Zero, decimal.Zero, errors.New("payment discount quote code is invalid")
	}
	currency, err := normalizePaymentDiscountCurrency(quote.Currency)
	if err != nil || currency != quote.Currency {
		return decimal.Zero, decimal.Zero, decimal.Zero, errors.New("payment discount quote currency is invalid")
	}
	if _, err := normalizePaymentDiscountDigest(quote.Revision); err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, errors.New("payment discount quote revision is invalid")
	}
	original, err := parsePaymentDiscountAmount(quote.OriginalAmount, currency, false)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, err
	}
	discount, err := parsePaymentDiscountAmount(quote.DiscountAmount, currency, true)
	if err != nil || !discount.IsPositive() {
		return decimal.Zero, decimal.Zero, decimal.Zero, errors.New("payment discount quote discount is invalid")
	}
	payAmount, err := parsePaymentDiscountAmount(quote.PayAmount, currency, false)
	if err != nil || !original.Equal(discount.Add(payAmount)) {
		return decimal.Zero, decimal.Zero, decimal.Zero, errors.New("payment discount quote amounts are invalid")
	}
	return original, discount, payAmount, nil
}

func paymentDiscountAuditConfig(code *PaymentDiscountCode) map[string]any {
	if code == nil {
		return nil
	}
	return map[string]any{
		"code":              code.Code,
		"discount_type":     code.DiscountType,
		"discount_value":    code.DiscountValue,
		"currency":          code.Currency,
		"max_uses":          code.MaxUses,
		"per_user_max_uses": code.PerUserMaxUses,
		"target_user_id":    code.TargetUserID,
		"starts_at":         code.StartsAt.UTC().Format(time.RFC3339Nano),
		"expires_at":        code.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"enabled":           code.Enabled,
		"version":           code.Version,
		"notes":             code.Notes,
		"order_types":       clonePaymentDiscountOrderTypes(code.OrderTypes),
		"plan_ids":          clonePaymentDiscountPlanIDs(code.PlanIDs),
	}
}

func paymentDiscountAuditDetail(before, after *PaymentDiscountCode) ([]byte, error) {
	return json.Marshal(map[string]any{
		"before": paymentDiscountAuditConfig(before),
		"after":  paymentDiscountAuditConfig(after),
	})
}

func insertPaymentDiscountAudit(ctx context.Context, client *dbent.Client, codeID, adminID int64, action string, detail []byte) error {
	if client == nil || codeID <= 0 || adminID <= 0 || len(detail) == 0 {
		return errors.New("invalid payment discount audit input")
	}
	_, err := client.ExecContext(ctx, `INSERT INTO payment_discount_code_audits
		(code_id, admin_user_id, action, detail) VALUES ($1, $2, $3, $4)`,
		codeID, adminID, action, string(detail))
	return err
}

func insertPaymentDiscountCode(ctx context.Context, client *dbent.Client, input PaymentDiscountCodeInput) (int64, error) {
	if client == nil || input.PerUserMaxUses == nil || input.OrderTypes == nil || input.PlanIDs == nil {
		return 0, errors.New("invalid payment discount code insert")
	}
	orderTypesJSON, planIDsJSON, err := paymentDiscountScopeJSON(input.OrderTypes, input.PlanIDs)
	if err != nil {
		return 0, err
	}
	rows, err := client.QueryContext(ctx, `INSERT INTO payment_discount_codes
		(code, discount_type, discount_value, currency, max_uses, per_user_max_uses,
		 target_user_id, starts_at, expires_at, enabled, version, notes, order_types, plan_ids)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$12,$13)
		RETURNING id`,
		input.Code, input.DiscountType, input.DiscountValue, input.Currency,
		input.MaxUses, *input.PerUserMaxUses, paymentDiscountInputTargetUserID(input),
		input.StartsAt, input.ExpiresAt, input.Enabled, input.Notes, orderTypesJSON, planIDsJSON)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, errors.New("payment discount code insert returned no id")
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	return id, nil
}

func paymentDiscountCodeUniqueViolation(err error) bool {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && string(pgErr.Code) == "23505" {
		return pgErr.Constraint == "payment_discount_codes_code_key" || pgErr.Constraint == "payment_discount_codes_code_unique"
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") && strings.Contains(message, "payment_discount_codes") && strings.Contains(message, "code")
}

func (s *PaymentService) createPaymentDiscountCode(ctx context.Context, adminID int64, normalized normalizedPaymentDiscountInput) (*PaymentDiscountCode, error) {
	generated := normalized.input.Code == ""
	attempts := 1
	if generated {
		attempts = 8
	}
	for attempt := 0; attempt < attempts; attempt++ {
		input := normalized.input
		if generated {
			code, err := generatePaymentDiscountCode()
			if err != nil {
				return nil, fmt.Errorf("generate payment discount code: %w", err)
			}
			input.Code = code
		}
		tx, err := s.entClient.Tx(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin payment discount create transaction: %w", err)
		}
		txCtx := dbent.NewTxContext(ctx, tx)
		if err := requirePaymentDiscountUser(txCtx, tx.Client(), adminID, "INVALID_PAYMENT_DISCOUNT_ADMIN"); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if input.TargetUserID != nil {
			if err := requirePaymentDiscountUser(txCtx, tx.Client(), *input.TargetUserID, "INVALID_PAYMENT_DISCOUNT_TARGET_USER"); err != nil {
				_ = tx.Rollback()
				return nil, err
			}
		}
		if err := requirePaymentDiscountPlans(txCtx, tx.Client(), input.PlanIDs); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		id, err := insertPaymentDiscountCode(txCtx, tx.Client(), input)
		if err != nil {
			_ = tx.Rollback()
			if generated && paymentDiscountCodeUniqueViolation(err) {
				continue
			}
			return nil, fmt.Errorf("insert payment discount code: %w", err)
		}
		created, found, err := loadPaymentDiscountCodeByID(txCtx, tx.Client(), id, true)
		if err != nil || !found {
			_ = tx.Rollback()
			if err != nil {
				return nil, fmt.Errorf("load created payment discount code: %w", err)
			}
			return nil, errors.New("created payment discount code is missing")
		}
		detail, err := paymentDiscountAuditDetail(nil, created)
		if err == nil {
			err = insertPaymentDiscountAudit(txCtx, tx.Client(), created.ID, adminID, "created", detail)
		}
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("record payment discount create audit: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit payment discount create: %w", err)
		}
		return created, nil
	}
	return nil, errors.New("could not allocate a unique payment discount code")
}

// SavePaymentDiscountCode creates or updates an append-only-audited code. A
// caller may never change the code text itself, and the version CAS prevents
// an administrator from overwriting a checkout-visible configuration.
func (s *PaymentService) SavePaymentDiscountCode(ctx context.Context, adminID, id int64, input PaymentDiscountCodeInput, expectedVersion int64) (*PaymentDiscountCode, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_DISCOUNT_UNAVAILABLE", "payment discounts are unavailable")
	}
	if adminID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_ADMIN", "coupon administrator is invalid")
	}
	normalized, err := normalizePaymentDiscountInput(input, true, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if id == 0 {
		if expectedVersion != 0 {
			return nil, ErrPaymentDiscountVersionConflict
		}
		return s.createPaymentDiscountCode(ctx, adminID, normalized)
	}
	if id < 0 || expectedVersion <= 0 {
		return nil, ErrPaymentDiscountVersionConflict
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin payment discount update transaction: %w", err)
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	rollback := func(result error) (*PaymentDiscountCode, error) {
		_ = tx.Rollback()
		return nil, result
	}
	if err := requirePaymentDiscountUser(txCtx, tx.Client(), adminID, "INVALID_PAYMENT_DISCOUNT_ADMIN"); err != nil {
		return rollback(err)
	}
	before, found, err := loadPaymentDiscountCodeByID(txCtx, tx.Client(), id, true)
	if err != nil {
		return rollback(fmt.Errorf("load payment discount code for update: %w", err))
	}
	if !found {
		return rollback(infraerrors.NotFound("PAYMENT_DISCOUNT_CODE_NOT_FOUND", "coupon code was not found"))
	}
	if before.Version != expectedVersion {
		return rollback(ErrPaymentDiscountVersionConflict)
	}
	if normalized.input.Code != "" && normalized.input.Code != before.Code {
		return rollback(infraerrors.BadRequest("PAYMENT_DISCOUNT_CODE_IMMUTABLE", "coupon code cannot be changed"))
	}
	normalized.input.Code = before.Code
	if !normalized.orderTypesProvided {
		normalized.input.OrderTypes = clonePaymentDiscountOrderTypes(before.OrderTypes)
	}
	if !normalized.planIDsProvided {
		normalized.input.PlanIDs = clonePaymentDiscountPlanIDs(before.PlanIDs)
	}
	orderTypes, planIDs, scopeErr := normalizePaymentDiscountScope(normalized.input.OrderTypes, normalized.input.PlanIDs, false)
	if scopeErr != nil {
		return rollback(infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_SCOPE", "coupon scope is invalid"))
	}
	normalized.input.OrderTypes = orderTypes
	normalized.input.PlanIDs = planIDs
	if normalized.input.TargetUserID != nil {
		if err := requirePaymentDiscountUser(txCtx, tx.Client(), *normalized.input.TargetUserID, "INVALID_PAYMENT_DISCOUNT_TARGET_USER"); err != nil {
			return rollback(err)
		}
	}
	if err := requirePaymentDiscountPlans(txCtx, tx.Client(), normalized.input.PlanIDs); err != nil {
		return rollback(err)
	}

	activeTotal, activePerUserMaximum, err := paymentDiscountActiveCapacity(txCtx, tx.Client(), before.ID)
	if err != nil {
		return rollback(fmt.Errorf("load payment discount active capacity: %w", err))
	}
	if normalized.input.MaxUses > 0 && int64(normalized.input.MaxUses) < activeTotal {
		return rollback(infraerrors.BadRequest("PAYMENT_DISCOUNT_CAP_BELOW_ACTIVE_USES", "coupon maximum uses cannot be below active reservations"))
	}
	if *normalized.input.PerUserMaxUses > 0 && int64(*normalized.input.PerUserMaxUses) < activePerUserMaximum {
		return rollback(infraerrors.BadRequest("PAYMENT_DISCOUNT_CAP_BELOW_ACTIVE_USES", "coupon per-user maximum cannot be below active reservations"))
	}

	orderTypesJSON, planIDsJSON, err := paymentDiscountScopeJSON(normalized.input.OrderTypes, normalized.input.PlanIDs)
	if err != nil {
		return rollback(err)
	}
	result, err := tx.Client().ExecContext(txCtx, `UPDATE payment_discount_codes SET
		discount_type = $1, discount_value = $2, currency = $3, max_uses = $4,
		per_user_max_uses = $5, target_user_id = $6, starts_at = $7, expires_at = $8,
		enabled = $9, notes = $10, order_types = $11, plan_ids = $12,
		version = version + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $13 AND version = $14`,
		normalized.input.DiscountType, normalized.input.DiscountValue, normalized.input.Currency,
		normalized.input.MaxUses, *normalized.input.PerUserMaxUses, paymentDiscountInputTargetUserID(normalized.input),
		normalized.input.StartsAt, normalized.input.ExpiresAt, normalized.input.Enabled, normalized.input.Notes,
		orderTypesJSON, planIDsJSON, id, expectedVersion)
	if err != nil {
		return rollback(fmt.Errorf("update payment discount code: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return rollback(err)
	}
	if affected != 1 {
		return rollback(ErrPaymentDiscountVersionConflict)
	}
	after, found, err := loadPaymentDiscountCodeByID(txCtx, tx.Client(), id, true)
	if err != nil || !found {
		if err != nil {
			return rollback(fmt.Errorf("load updated payment discount code: %w", err))
		}
		return rollback(errors.New("updated payment discount code is missing"))
	}
	detail, err := paymentDiscountAuditDetail(before, after)
	if err == nil {
		err = insertPaymentDiscountAudit(txCtx, tx.Client(), id, adminID, "updated", detail)
	}
	if err != nil {
		return rollback(fmt.Errorf("record payment discount update audit: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit payment discount update: %w", err)
	}
	return after, nil
}

func normalizePaymentDiscountPage(page, size int) (int, int, error) {
	if page <= 0 {
		page = 1
	}
	if page > paymentDiscountMaximumPage {
		return 0, 0, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_PAGE", "coupon page is invalid")
	}
	if size <= 0 {
		size = paymentDiscountDefaultPageSize
	}
	if size > paymentDiscountMaximumPageSize {
		size = paymentDiscountMaximumPageSize
	}
	return page, size, nil
}

func normalizePaymentDiscountSearch(raw string) (string, error) {
	search := strings.ToUpper(strings.TrimSpace(raw))
	if len(search) > 32 {
		return "", infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_SEARCH", "coupon search is too long")
	}
	return search, nil
}

// ListPaymentDiscountCodes returns a bounded administrative page. The count
// is not authorization state and may change immediately after the read.
func (s *PaymentService) ListPaymentDiscountCodes(ctx context.Context, page, size int, search string) ([]PaymentDiscountCode, int, error) {
	if s == nil || s.entClient == nil {
		return nil, 0, infraerrors.ServiceUnavailable("PAYMENT_DISCOUNT_UNAVAILABLE", "payment discounts are unavailable")
	}
	page, size, err := normalizePaymentDiscountPage(page, size)
	if err != nil {
		return nil, 0, err
	}
	search, err = normalizePaymentDiscountSearch(search)
	if err != nil {
		return nil, 0, err
	}
	pattern := "%" + search + "%"
	countRows, err := s.entClient.QueryContext(ctx, `SELECT COUNT(*) FROM payment_discount_codes WHERE UPPER(code) LIKE $1`, pattern)
	if err != nil {
		return nil, 0, err
	}
	if !countRows.Next() {
		_ = countRows.Close()
		if err := countRows.Err(); err != nil {
			return nil, 0, err
		}
		return nil, 0, errors.New("payment discount code count has no row")
	}
	var total64 int64
	if err := countRows.Scan(&total64); err != nil {
		_ = countRows.Close()
		return nil, 0, err
	}
	if err := countRows.Close(); err != nil {
		return nil, 0, err
	}
	total, err := paymentDiscountInt(total64)
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * size
	rows, err := s.entClient.QueryContext(ctx, `SELECT
		code.id, code.code, code.discount_type, CAST(code.discount_value AS TEXT), code.currency,
		code.max_uses, code.per_user_max_uses, code.target_user_id, code.starts_at, code.expires_at,
		code.enabled, code.version, code.notes, CAST(code.order_types AS TEXT), CAST(code.plan_ids AS TEXT),
		code.created_at, code.updated_at,
		COALESCE(SUM(CASE WHEN use_row.status = 'reserved' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN use_row.status = 'consumed' THEN 1 ELSE 0 END), 0)
		FROM payment_discount_codes AS code
		LEFT JOIN payment_discount_uses AS use_row ON use_row.code_id = code.id
		WHERE UPPER(code.code) LIKE $1
		GROUP BY code.id
		ORDER BY code.created_at DESC, code.id DESC
		LIMIT $2 OFFSET $3`, pattern, size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]PaymentDiscountCode, 0, size)
	for rows.Next() {
		code, err := scanPaymentDiscountCode(rows, true)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *code)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func paymentDiscountUseColumns(tableAlias ...string) string {
	prefix := ""
	if len(tableAlias) > 0 && tableAlias[0] != "" {
		prefix = tableAlias[0] + "."
	}
	return fmt.Sprintf(`%sid, %scode_id, %sorder_id, %suser_id, %sstatus,
		CAST(%soriginal_amount AS TEXT), CAST(%sdiscount_amount AS TEXT), CAST(%spay_amount AS TEXT),
		%scurrency, %squote_version, %squote_revision, %srequest_hash, %sidempotency_hash,
		%sreserved_at, %sconsumed_at, %sreleased_at, %spaid_review_at, %screated_at, %supdated_at`,
		prefix, prefix, prefix, prefix, prefix,
		prefix, prefix, prefix,
		prefix, prefix, prefix, prefix, prefix,
		prefix, prefix, prefix, prefix, prefix, prefix)
}

func scanPaymentDiscountUse(rows *stdsql.Rows) (*paymentDiscountUseRecord, error) {
	return scanPaymentDiscountUseRow(rows, false)
}

func scanPaymentDiscountUseWithUsername(rows *stdsql.Rows) (*paymentDiscountUseRecord, error) {
	return scanPaymentDiscountUseRow(rows, true)
}

type paymentDiscountUseScanner interface {
	Scan(dest ...any) error
}

func scanPaymentDiscountUseRow(rows paymentDiscountUseScanner, includeUsername bool) (*paymentDiscountUseRecord, error) {
	record := &paymentDiscountUseRecord{}
	var (
		original, discount, payAmount  string
		consumed, released, paidReview stdsql.NullTime
		username                       stdsql.NullString
	)
	scanTargets := []any{
		&record.ID, &record.CodeID, &record.OrderID, &record.UserID, &record.Status,
		&original, &discount, &payAmount, &record.Currency, &record.QuoteVersion,
		&record.QuoteRevision, &record.RequestHash, &record.IdempotencyHash,
		&record.ReservedAt, &consumed, &released, &paidReview, &record.CreatedAt, &record.UpdatedAt,
	}
	if includeUsername {
		scanTargets = append(scanTargets, &username)
	}
	err := rows.Scan(scanTargets...)
	if err != nil {
		return nil, err
	}
	currency, err := normalizePaymentDiscountCurrency(record.Currency)
	if err != nil || currency != record.Currency {
		return nil, errors.New("stored payment discount use currency is invalid")
	}
	originalAmount, err := parsePaymentDiscountAmount(original, currency, false)
	if err != nil {
		return nil, errors.New("stored payment discount use original amount is invalid")
	}
	discountAmount, err := parsePaymentDiscountAmount(discount, currency, true)
	if err != nil || discountAmount.IsNegative() {
		return nil, errors.New("stored payment discount use discount amount is invalid")
	}
	paidAmount, err := parsePaymentDiscountAmount(payAmount, currency, false)
	if err != nil || !originalAmount.Equal(discountAmount.Add(paidAmount)) {
		return nil, errors.New("stored payment discount use amounts are invalid")
	}
	if _, err := normalizePaymentDiscountDigest(record.QuoteRevision); err != nil {
		return nil, errors.New("stored payment discount quote revision is invalid")
	}
	if _, err := normalizePaymentDiscountDigest(record.RequestHash); err != nil {
		return nil, errors.New("stored payment discount request hash is invalid")
	}
	if _, err := normalizePaymentDiscountDigest(record.IdempotencyHash); err != nil {
		return nil, errors.New("stored payment discount idempotency hash is invalid")
	}
	record.OriginalAmount = canonicalPaymentDiscountAmount(originalAmount, currency)
	record.DiscountAmount = canonicalPaymentDiscountAmount(discountAmount, currency)
	record.PayAmount = canonicalPaymentDiscountAmount(paidAmount, currency)
	if consumed.Valid {
		value := consumed.Time
		record.ConsumedAt = &value
	}
	if released.Valid {
		value := released.Time
		record.ReleasedAt = &value
	}
	if paidReview.Valid {
		value := paidReview.Time
		record.PaidReviewAt = &value
	}
	if username.Valid {
		record.Username = username.String
	}
	return record, nil
}

func loadPaymentDiscountUseByOrder(ctx context.Context, client *dbent.Client, orderID int64, lock bool) (*paymentDiscountUseRecord, bool, error) {
	if client == nil || orderID <= 0 {
		return nil, false, errors.New("invalid payment discount order lookup")
	}
	query := `SELECT ` + paymentDiscountUseColumns() + ` FROM payment_discount_uses WHERE order_id = $1`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, orderID)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	record, err := scanPaymentDiscountUse(rows)
	if err != nil {
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	return record, true, nil
}

func loadPaymentDiscountUseByIdempotency(ctx context.Context, client *dbent.Client, userID int64, idempotencyHash string, lock bool) (*paymentDiscountUseRecord, bool, error) {
	if client == nil || userID <= 0 || idempotencyHash == "" {
		return nil, false, errors.New("invalid payment discount idempotency lookup")
	}
	query := `SELECT ` + paymentDiscountUseColumns() + ` FROM payment_discount_uses
		WHERE user_id = $1 AND idempotency_hash = $2`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, userID, idempotencyHash)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	record, err := scanPaymentDiscountUse(rows)
	if err != nil {
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	return record, true, nil
}

// ListPaymentDiscountUses returns the immutable ledger rows for one code.
func (s *PaymentService) ListPaymentDiscountUses(ctx context.Context, codeID int64, page, size int) ([]PaymentDiscountUse, int, error) {
	if s == nil || s.entClient == nil {
		return nil, 0, infraerrors.ServiceUnavailable("PAYMENT_DISCOUNT_UNAVAILABLE", "payment discounts are unavailable")
	}
	if codeID <= 0 {
		return nil, 0, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_CODE", "coupon code is invalid")
	}
	page, size, err := normalizePaymentDiscountPage(page, size)
	if err != nil {
		return nil, 0, err
	}
	countRows, err := s.entClient.QueryContext(ctx, `SELECT COUNT(*) FROM payment_discount_uses WHERE code_id = $1`, codeID)
	if err != nil {
		return nil, 0, err
	}
	if !countRows.Next() {
		_ = countRows.Close()
		if err := countRows.Err(); err != nil {
			return nil, 0, err
		}
		return nil, 0, errors.New("payment discount use count has no row")
	}
	var total64 int64
	if err := countRows.Scan(&total64); err != nil {
		_ = countRows.Close()
		return nil, 0, err
	}
	if err := countRows.Close(); err != nil {
		return nil, 0, err
	}
	total, err := paymentDiscountInt(total64)
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * size
	rows, err := s.entClient.QueryContext(ctx, `SELECT `+paymentDiscountUseColumns("use_row")+`, user_row.username
		FROM payment_discount_uses AS use_row
		LEFT JOIN users AS user_row ON user_row.id = use_row.user_id
		WHERE use_row.code_id = $1
		ORDER BY use_row.id DESC LIMIT $2 OFFSET $3`, codeID, size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]PaymentDiscountUse, 0, size)
	for rows.Next() {
		record, err := scanPaymentDiscountUseWithUsername(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, record.PaymentDiscountUse)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListPaymentDiscountAudits exposes administrative history separately from
// order-use history, keeping the audit table append-only and query-bounded.
func (s *PaymentService) ListPaymentDiscountAudits(ctx context.Context, codeID int64, page, size int) ([]PaymentDiscountCodeAudit, int, error) {
	if s == nil || s.entClient == nil {
		return nil, 0, infraerrors.ServiceUnavailable("PAYMENT_DISCOUNT_UNAVAILABLE", "payment discounts are unavailable")
	}
	if codeID <= 0 {
		return nil, 0, infraerrors.BadRequest("INVALID_PAYMENT_DISCOUNT_CODE", "coupon code is invalid")
	}
	page, size, err := normalizePaymentDiscountPage(page, size)
	if err != nil {
		return nil, 0, err
	}
	countRows, err := s.entClient.QueryContext(ctx, `SELECT COUNT(*) FROM payment_discount_code_audits WHERE code_id = $1`, codeID)
	if err != nil {
		return nil, 0, err
	}
	if !countRows.Next() {
		_ = countRows.Close()
		if err := countRows.Err(); err != nil {
			return nil, 0, err
		}
		return nil, 0, errors.New("payment discount audit count has no row")
	}
	var total64 int64
	if err := countRows.Scan(&total64); err != nil {
		_ = countRows.Close()
		return nil, 0, err
	}
	if err := countRows.Close(); err != nil {
		return nil, 0, err
	}
	total, err := paymentDiscountInt(total64)
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * size
	rows, err := s.entClient.QueryContext(ctx, `SELECT id, code_id, admin_user_id, action, CAST(detail AS TEXT), created_at
		FROM payment_discount_code_audits WHERE code_id = $1
		ORDER BY id DESC LIMIT $2 OFFSET $3`, codeID, size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]PaymentDiscountCodeAudit, 0, size)
	for rows.Next() {
		var item PaymentDiscountCodeAudit
		var detail string
		if err := rows.Scan(&item.ID, &item.CodeID, &item.AdminUserID, &item.Action, &detail, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		if !json.Valid([]byte(detail)) {
			return nil, 0, errors.New("stored payment discount audit detail is invalid")
		}
		item.Detail = json.RawMessage(detail)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func paymentDiscountReservationMatches(record *paymentDiscountUseRecord, orderID, userID int64, quote *PaymentDiscountQuote, original, discount, payAmount decimal.Decimal, requestHash, idempotencyHash string) error {
	if record == nil || record.OrderID != orderID || record.UserID != userID ||
		record.CodeID != quote.CodeID || record.QuoteVersion != quote.Version ||
		record.QuoteRevision != quote.Revision || record.RequestHash != requestHash ||
		record.IdempotencyHash != idempotencyHash || record.Currency != quote.Currency {
		return ErrIdempotencyKeyConflict
	}
	storedOriginal, err := parsePaymentDiscountAmount(record.OriginalAmount, record.Currency, false)
	if err != nil {
		return err
	}
	storedDiscount, err := parsePaymentDiscountAmount(record.DiscountAmount, record.Currency, true)
	if err != nil {
		return err
	}
	storedPayAmount, err := parsePaymentDiscountAmount(record.PayAmount, record.Currency, false)
	if err != nil || !storedOriginal.Equal(original) || !storedDiscount.Equal(discount) || !storedPayAmount.Equal(payAmount) {
		return ErrIdempotencyKeyConflict
	}
	return nil
}

// loadPaymentDiscountReservationScope reads the freshly persisted order rather
// than trusting request fields or a caller-provided quote for applicability.
// The order caller already holds its row while creating it; FOR UPDATE keeps
// this invariant for other transactional internal callers as well.
func loadPaymentDiscountReservationScope(ctx context.Context, client *dbent.Client, orderID, userID int64, currency string) (string, int64, decimal.Decimal, error) {
	if client == nil || orderID <= 0 || userID <= 0 {
		return "", 0, decimal.Zero, ErrPaymentDiscountInvalid
	}
	query := `SELECT order_type, plan_id, CAST(pay_amount AS TEXT)
		FROM payment_orders WHERE id = $1 AND user_id = $2`
	if paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, orderID, userID)
	if err != nil {
		return "", 0, decimal.Zero, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", 0, decimal.Zero, err
		}
		return "", 0, decimal.Zero, ErrPaymentDiscountInvalid
	}
	var (
		orderType string
		planID    stdsql.NullInt64
		payRaw    string
	)
	if err := rows.Scan(&orderType, &planID, &payRaw); err != nil {
		return "", 0, decimal.Zero, err
	}
	if err := rows.Err(); err != nil {
		return "", 0, decimal.Zero, err
	}
	resolvedPlanID := int64(0)
	if planID.Valid {
		resolvedPlanID = planID.Int64
	}
	orderType, resolvedPlanID, err = normalizePaymentDiscountQuoteScope(orderType, resolvedPlanID)
	if err != nil {
		return "", 0, decimal.Zero, ErrPaymentDiscountInvalid
	}
	payAmount, err := parsePaymentDiscountAmount(payRaw, currency, false)
	if err != nil {
		return "", 0, decimal.Zero, ErrPaymentDiscountInvalid
	}
	return orderType, resolvedPlanID, payAmount, nil
}

// reservePaymentDiscount writes the immutable quote and takes one capacity
// slot. The caller must re-quote with lock=true in its order transaction.
func reservePaymentDiscount(ctx context.Context, client *dbent.Client, orderID, userID int64, quote *PaymentDiscountQuote, requestHash, idempotencyHash string) error {
	if client == nil || orderID <= 0 || userID <= 0 {
		return errors.New("invalid payment discount reservation input")
	}
	original, discount, payAmount, err := parsePaymentDiscountQuote(quote)
	if err != nil {
		return err
	}
	requestHash, err = normalizePaymentDiscountDigest(requestHash)
	if err != nil {
		return err
	}
	idempotencyHash, err = normalizePaymentDiscountDigest(idempotencyHash)
	if err != nil {
		return err
	}

	if existing, found, err := loadPaymentDiscountUseByOrder(ctx, client, orderID, false); err != nil {
		return err
	} else if found {
		return paymentDiscountReservationMatches(existing, orderID, userID, quote, original, discount, payAmount, requestHash, idempotencyHash)
	}
	if existing, found, err := loadPaymentDiscountUseByIdempotency(ctx, client, userID, idempotencyHash, false); err != nil {
		return err
	} else if found {
		if existing.RequestHash != requestHash {
			return ErrIdempotencyKeyConflict
		}
		return ErrPaymentDiscountReplay
	}

	code, found, err := loadPaymentDiscountCodeByID(ctx, client, quote.CodeID, true)
	if err != nil {
		return err
	}
	if !found || code.Code != quote.Code || code.Version != quote.Version || code.Currency != quote.Currency {
		return ErrPaymentDiscountInvalid
	}
	if err := validatePaymentDiscountAvailability(ctx, client, code, userID, time.Now().UTC()); err != nil {
		return err
	}
	orderType, planID, persistedPayAmount, err := loadPaymentDiscountReservationScope(ctx, client, orderID, userID, quote.Currency)
	if err != nil {
		return err
	}
	if !persistedPayAmount.Equal(payAmount) {
		return ErrPaymentDiscountInvalid
	}
	if err := validatePaymentDiscountScope(code, orderType, planID); err != nil {
		return err
	}
	snapshot, err := json.Marshal(quote)
	if err != nil {
		return err
	}
	insertRows, err := client.QueryContext(ctx, `INSERT INTO payment_discount_uses
		(code_id, order_id, user_id, status, original_amount, discount_amount, pay_amount,
		 currency, quote_version, quote_revision, request_hash, idempotency_hash, quote_snapshot)
		VALUES ($1,$2,$3,'reserved',$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		quote.CodeID, orderID, userID,
		canonicalPaymentDiscountAmount(original, quote.Currency), canonicalPaymentDiscountAmount(discount, quote.Currency), canonicalPaymentDiscountAmount(payAmount, quote.Currency),
		quote.Currency, quote.Version, quote.Revision, requestHash, idempotencyHash, string(snapshot))
	if err != nil {
		return err
	}
	defer func() { _ = insertRows.Close() }()
	if insertRows.Next() {
		var insertedID int64
		if err := insertRows.Scan(&insertedID); err != nil {
			return err
		}
		return insertRows.Err()
	}
	if err := insertRows.Err(); err != nil {
		return err
	}
	if err := insertRows.Close(); err != nil {
		return err
	}
	// ON CONFLICT keeps this transaction usable for durable replay lookups.
	if existing, found, lookupErr := loadPaymentDiscountUseByOrder(ctx, client, orderID, false); lookupErr == nil && found {
		return paymentDiscountReservationMatches(existing, orderID, userID, quote, original, discount, payAmount, requestHash, idempotencyHash)
	}
	if existing, found, lookupErr := loadPaymentDiscountUseByIdempotency(ctx, client, userID, idempotencyHash, false); lookupErr == nil && found {
		if existing.RequestHash != requestHash {
			return ErrIdempotencyKeyConflict
		}
		return ErrPaymentDiscountReplay
	}
	return errors.New("payment discount reservation conflict could not be resolved")
}

// findPaymentDiscountReplay binds an idempotency key to its original request
// fingerprint and authenticated user before exposing its payment order.
func findPaymentDiscountReplay(ctx context.Context, client *dbent.Client, userID int64, idempotencyHash, requestHash string) (*dbent.PaymentOrder, bool, error) {
	if client == nil || userID <= 0 {
		return nil, false, errors.New("invalid payment discount replay input")
	}
	var err error
	idempotencyHash, err = normalizePaymentDiscountDigest(idempotencyHash)
	if err != nil {
		return nil, false, err
	}
	requestHash, err = normalizePaymentDiscountDigest(requestHash)
	if err != nil {
		return nil, false, err
	}
	use, found, err := loadPaymentDiscountUseByIdempotency(ctx, client, userID, idempotencyHash, false)
	if err != nil || !found {
		return nil, found, err
	}
	if use.RequestHash != requestHash {
		return nil, false, ErrIdempotencyKeyConflict
	}
	order, err := client.PaymentOrder.Query().Where(
		paymentorder.IDEQ(use.OrderID),
		paymentorder.UserIDEQ(userID),
	).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, false, errors.New("payment discount replay order is missing")
		}
		return nil, false, err
	}
	return order, true, nil
}

// consumePaymentDiscount is called only while the order row is locked. A
// reserved use already owns capacity, so edits to the code cannot invalidate a
// sale in flight. A released late-paid order performs only the current hard
// cap check; dates, enabled state, audience, and price remain frozen.
func consumePaymentDiscount(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	if client == nil || orderID <= 0 {
		return false, errors.New("invalid payment discount consumption input")
	}
	use, found, err := loadPaymentDiscountUseByOrder(ctx, client, orderID, true)
	if err != nil {
		return false, err
	}
	if !found {
		return false, errPaymentDiscountLedgerMissing
	}
	switch use.Status {
	case paymentDiscountUseReserved:
		if _, found, err := loadPaymentDiscountCodeByID(ctx, client, use.CodeID, true); err != nil {
			return false, err
		} else if !found {
			return false, errors.New("payment discount code is missing for reserved use")
		}
		result, err := client.ExecContext(ctx, `UPDATE payment_discount_uses
			SET status = 'consumed', consumed_at = CURRENT_TIMESTAMP,
				paid_review_at = NULL, updated_at = CURRENT_TIMESTAMP
			WHERE id = $1 AND status = 'reserved'`, use.ID)
		if err != nil {
			return false, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected != 1 {
			return false, errors.New("payment discount reserved consumption lost its transition")
		}
		return true, nil
	case paymentDiscountUseConsumed:
		return true, nil
	case paymentDiscountUseReleased:
		code, found, err := loadPaymentDiscountCodeByID(ctx, client, use.CodeID, true)
		if err != nil {
			return false, err
		}
		if !found || code.MaxUses < 0 || code.PerUserMaxUses < 0 {
			return false, errors.New("payment discount code is invalid for released use")
		}
		counts, err := paymentDiscountUsageCount(ctx, client, code.ID, use.UserID)
		if err != nil {
			return false, err
		}
		if paymentDiscountCapacityAvailable(code, counts) {
			result, err := client.ExecContext(ctx, `UPDATE payment_discount_uses
				SET status = 'consumed', consumed_at = CURRENT_TIMESTAMP,
					paid_review_at = NULL, updated_at = CURRENT_TIMESTAMP
				WHERE id = $1 AND status = 'released'`, use.ID)
			if err != nil {
				return false, err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return false, err
			}
			if affected != 1 {
				return false, errors.New("payment discount late-paid consumption lost its transition")
			}
			return true, nil
		}
		result, err := client.ExecContext(ctx, `UPDATE payment_discount_uses
			SET status = 'paid_review', paid_review_at = CURRENT_TIMESTAMP,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $1 AND status = 'released'`, use.ID)
		if err != nil {
			return false, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected != 1 {
			return false, errors.New("payment discount late-paid review lost its transition")
		}
		return false, nil
	case paymentDiscountUsePaidReview:
		return false, nil
	default:
		return false, errors.New("payment discount use status is invalid")
	}
}

// releasePaymentDiscount releases only a still-pending reservation. The root
// caller owns the order closure proof and holds that order's lock first.
func releasePaymentDiscount(ctx context.Context, client *dbent.Client, orderID int64) error {
	if client == nil || orderID <= 0 {
		return errors.New("invalid payment discount release input")
	}
	use, found, err := loadPaymentDiscountUseByOrder(ctx, client, orderID, true)
	if err != nil {
		return err
	}
	if !found {
		return errPaymentDiscountLedgerMissing
	}
	if use.Status != paymentDiscountUseReserved {
		return nil
	}
	if _, found, err := loadPaymentDiscountCodeByID(ctx, client, use.CodeID, true); err != nil {
		return err
	} else if !found {
		return errors.New("payment discount code is missing for release")
	}
	result, err := client.ExecContext(ctx, `UPDATE payment_discount_uses
		SET status = 'released', released_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status = 'reserved'`, use.ID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("payment discount release lost its transition")
	}
	return nil
}

// savePaymentDiscountResponse persists a provider response once. The stored
// response is always bound to its order ID and cannot be overwritten by a
// later replay with different provider data.
func savePaymentDiscountResponse(ctx context.Context, client *dbent.Client, orderID int64, response *CreateOrderResponse) error {
	if client == nil || orderID <= 0 || response == nil || response.OrderID != orderID {
		return errors.New("invalid payment discount response snapshot")
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	result, err := client.ExecContext(ctx, `UPDATE payment_discount_uses
		SET response_snapshot = $2, updated_at = CURRENT_TIMESTAMP
		WHERE order_id = $1 AND response_snapshot IS NULL`, orderID, string(payload))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	rows, err := client.QueryContext(ctx, `SELECT CAST(response_snapshot AS TEXT)
		FROM payment_discount_uses WHERE order_id = $1`, orderID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return errPaymentDiscountLedgerMissing
	}
	var existing stdsql.NullString
	if err := rows.Scan(&existing); err != nil {
		return err
	}
	if !existing.Valid {
		return errors.New("payment discount response snapshot was not stored")
	}
	return rows.Err()
}

// loadPaymentDiscountResponse returns nil,nil only when the durable coupon
// order exists but its provider checkout response is still unconfirmed.
func loadPaymentDiscountResponse(ctx context.Context, client *dbent.Client, orderID int64) (*CreateOrderResponse, error) {
	if client == nil || orderID <= 0 {
		return nil, errors.New("invalid payment discount response lookup")
	}
	rows, err := client.QueryContext(ctx, `SELECT CAST(response_snapshot AS TEXT)
		FROM payment_discount_uses WHERE order_id = $1`, orderID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, errPaymentDiscountLedgerMissing
	}
	var snapshot stdsql.NullString
	if err := rows.Scan(&snapshot); err != nil {
		return nil, err
	}
	if !snapshot.Valid {
		return nil, rows.Err()
	}
	response := &CreateOrderResponse{}
	if err := json.Unmarshal([]byte(snapshot.String), response); err != nil {
		return nil, fmt.Errorf("decode payment discount response snapshot: %w", err)
	}
	if response.OrderID != orderID {
		return nil, errors.New("payment discount response snapshot is bound to another order")
	}
	return response, rows.Err()
}

// checkPaymentDiscountRate atomically consumes one of twenty attempts in the
// database's current minute. It is intentionally durable rather than process
// local so quote and create limits apply across every API instance.
func checkPaymentDiscountRate(ctx context.Context, client *dbent.Client, userID int64) error {
	if client == nil || userID <= 0 {
		return errors.New("invalid payment discount rate-limit input")
	}
	var query string
	if paymentAuditDialect(client) == dialect.Postgres {
		query = `INSERT INTO payment_discount_rate_limits AS rate_limit
			(user_id, window_started_at, attempt_count, updated_at)
			VALUES ($1, date_trunc('minute', CURRENT_TIMESTAMP), 1, CURRENT_TIMESTAMP)
			ON CONFLICT (user_id) DO UPDATE SET
				window_started_at = EXCLUDED.window_started_at,
				attempt_count = CASE WHEN rate_limit.window_started_at = EXCLUDED.window_started_at
					THEN rate_limit.attempt_count + 1 ELSE 1 END,
				updated_at = CURRENT_TIMESTAMP
			WHERE rate_limit.window_started_at <> EXCLUDED.window_started_at
				OR rate_limit.attempt_count < $2
			RETURNING attempt_count`
	} else {
		query = `INSERT INTO payment_discount_rate_limits
			(user_id, window_started_at, attempt_count, updated_at)
			VALUES ($1, strftime('%Y-%m-%d %H:%M:00', 'now'), 1, CURRENT_TIMESTAMP)
			ON CONFLICT (user_id) DO UPDATE SET
				window_started_at = excluded.window_started_at,
				attempt_count = CASE WHEN window_started_at = excluded.window_started_at
					THEN attempt_count + 1 ELSE 1 END,
				updated_at = CURRENT_TIMESTAMP
			WHERE window_started_at <> excluded.window_started_at OR attempt_count < $2
			RETURNING attempt_count`
	}
	rows, err := client.QueryContext(ctx, query, userID, paymentDiscountRateLimit)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return ErrPaymentDiscountRateLimited
	}
	var attempts int
	if err := rows.Scan(&attempts); err != nil {
		return err
	}
	if attempts < 1 || attempts > paymentDiscountRateLimit {
		return errors.New("payment discount rate-limit state is invalid")
	}
	return rows.Err()
}
