package service

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/shopspring/decimal"
)

// PlanEntitlements is the public, structured set of benefits delivered after
// a subscription order is confirmed. Unknown JSON fields are intentionally
// ignored so the schema can grow without breaking older clients.
type PlanEntitlements struct {
	BalanceBonus   float64 `json:"balance_bonus"`
	ResetCardCount int     `json:"reset_card_count"`
	// PurchaseRules controls whether a customer may see and buy a new
	// subscription. It is administrator-only configuration and is removed from
	// every customer projection and immutable product snapshot.
	PurchaseRules *PurchaseRules `json:"purchase_rules,omitempty"`
	// ResetCardPurchaseRules is evaluated independently when a customer buys a
	// reset card for an existing subscription. The monthly plan's audience
	// fence still applies, but its recharge threshold does not carry over.
	ResetCardPurchaseRules *PurchaseRules `json:"reset_card_purchase_rules,omitempty"`
	// ResetCardPurchasePrice optionally overrides the wallet price for a reset
	// card bought against this monthly plan. It is only a price-source metadata
	// field: it neither grants a card nor changes subscription fulfillment.
	// Omission preserves the established monthly-price fallback.
	ResetCardPurchasePrice *float64 `json:"reset_card_purchase_price,omitempty"`
	// ResetCardExpiryDays is a count in ResetCardExpiryUnit, not necessarily a
	// number of days — the field keeps its name for compatibility with plans
	// stored before units existed, which were all in days. This mirrors the
	// subscription plan's own validity_days/validity_unit pair.
	// Use ResetCardValidityDays for the real duration.
	ResetCardExpiryDays int `json:"reset_card_expiry_days"`
	// ResetCardExpiryUnit is day, week, or month (singular or plural, matching
	// what the admin form has always saved for plan validity). Empty means day,
	// which is what every pre-unit plan meant.
	ResetCardExpiryUnit string `json:"reset_card_expiry_unit"`
	// Concurrency is the minimum target for the user's concurrent request cap.
	// Payment fulfillment will never lower an already higher cap. Zero means this
	// product does not change the cap.
	Concurrency int    `json:"concurrency"`
	Message     string `json:"message"`
	// ResetCardTitle and ResetCardDescription let an administrator describe the
	// optional reset-card offer without changing subscription fulfillment.
	ResetCardTitle       string `json:"reset_card_title,omitempty"`
	ResetCardDescription string `json:"reset_card_description,omitempty"`
	// Recommended controls the single, admin-selected presentation highlight.
	// It has no effect on pricing or fulfillment.
	Recommended bool `json:"recommended,omitempty"`
}

const resetCardPurchasePriceScale int32 = 2

const (
	maxResetCardTitleLength       = 200
	maxResetCardDescriptionLength = 1000
)

// resetCardPurchaseConfiguredPrice validates the optional plan metadata once
// for both plan administration and the locked purchase path. A nil value means
// the plan deliberately uses the legacy monthly-price calculation.
func resetCardPurchaseConfiguredPrice(value *float64) (decimal.Decimal, bool, error) {
	if value == nil {
		return decimal.Zero, false, nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value <= 0 {
		return decimal.Zero, false, fmt.Errorf("reset_card_purchase_price must be a positive finite amount")
	}
	price := decimal.NewFromFloat(*value)
	if !price.Equal(price.Round(resetCardPurchasePriceScale)) {
		return decimal.Zero, false, fmt.Errorf("reset_card_purchase_price must have at most %d decimal places", resetCardPurchasePriceScale)
	}
	return price, true, nil
}

// Reset card expiry units. Deliberately a narrower set than subscription
// validity: a reset card that outlives its subscription has no meaning, so
// quarters and years are not offered.
const (
	resetCardExpiryUnitDay   = "day"
	resetCardExpiryUnitWeek  = "week"
	resetCardExpiryUnitMonth = "month"
)

// maxResetCardValidityDays bounds the computed duration rather than the raw
// count, so "36 months" is rejected for the same reason "1100 days" is.
const maxResetCardValidityDays = 3650

// invalidResetCardValidityDays is returned when a raw count cannot be safely
// converted to days. It is deliberately negative so fulfillment cannot turn a
// malformed snapshot into an immediately usable card if validation is bypassed.
const invalidResetCardValidityDays = -1

// normalizeResetCardExpiryUnit accepts singular and plural spellings because
// the admin form saves plural for plan validity and the database default for
// that field is singular. Anything unrecognized falls back to days, matching
// psComputeValidityDays.
func normalizeResetCardExpiryUnit(unit string) string {
	base := strings.ToLower(strings.TrimSpace(unit))
	base = strings.TrimSuffix(base, "s")
	switch base {
	case resetCardExpiryUnitWeek:
		return resetCardExpiryUnitWeek
	case resetCardExpiryUnitMonth:
		return resetCardExpiryUnitMonth
	default:
		return resetCardExpiryUnitDay
	}
}

// ResetCardValidityDays converts the count/unit pair into the real number of
// days a granted reset card stays usable. A month is 30 days, matching
// psComputeValidityDays so subscription and reset-card periods do not drift.
func (e PlanEntitlements) ResetCardValidityDays() int {
	multiplier := 1
	switch normalizeResetCardExpiryUnit(e.ResetCardExpiryUnit) {
	case resetCardExpiryUnitWeek:
		multiplier = 7
	case resetCardExpiryUnitMonth:
		multiplier = 30
	}
	if e.ResetCardExpiryDays < 0 || e.ResetCardExpiryDays > maxResetCardValidityDays/multiplier {
		return invalidResetCardValidityDays
	}
	return e.ResetCardExpiryDays * multiplier
}

// RechargeOption is a server-configured balance purchase preset.
type RechargeOption struct {
	Amount                  float64 `json:"amount"`
	OriginalPrice           float64 `json:"original_price,omitempty"`
	Label                   string  `json:"label,omitempty"`
	Description             string  `json:"description,omitempty"`
	BalanceBonus            float64 `json:"balance_bonus,omitempty"`
	EstimatedRateMultiplier float64 `json:"estimated_rate_multiplier,omitempty"`
	EstimatedTokens         int64   `json:"estimated_tokens,omitempty"`
	// Concurrency is the minimum target applied after a successful order for this
	// exact configured amount. Zero means no concurrency entitlement.
	Concurrency int `json:"concurrency,omitempty"`
	// Recommended controls the single, admin-selected presentation highlight.
	// It has no effect on pricing or fulfillment.
	Recommended bool `json:"recommended,omitempty"`
	SortOrder   int  `json:"sort_order"`
	Enabled     bool `json:"enabled"`
	// PurchaseRules stays in administrator configuration only. Customer API
	// projections always clear it before serialization.
	PurchaseRules *PurchaseRules `json:"purchase_rules,omitempty"`
	// Eligibility is computed for the authenticated customer at read time. It
	// is deliberately never accepted from, or retained in, admin configuration.
	Eligibility *PurchaseEligibility `json:"eligibility,omitempty"`
}

// UnmarshalJSON treats an omitted enabled flag as enabled. This keeps a hand-
// written legacy preset such as {"amount": 20} usable while still allowing
// admins to explicitly disable a preset without losing it on the next save.
func (o *RechargeOption) UnmarshalJSON(data []byte) error {
	type optionJSON struct {
		Amount                  float64        `json:"amount"`
		OriginalPrice           float64        `json:"original_price"`
		Label                   string         `json:"label"`
		Description             string         `json:"description"`
		BalanceBonus            float64        `json:"balance_bonus"`
		EstimatedRateMultiplier float64        `json:"estimated_rate_multiplier"`
		EstimatedTokens         int64          `json:"estimated_tokens"`
		Concurrency             int            `json:"concurrency"`
		Recommended             bool           `json:"recommended"`
		SortOrder               int            `json:"sort_order"`
		Enabled                 *bool          `json:"enabled"`
		PurchaseRules           *PurchaseRules `json:"purchase_rules"`
	}
	var raw optionJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	o.Amount = raw.Amount
	o.OriginalPrice = raw.OriginalPrice
	o.Label = raw.Label
	o.Description = raw.Description
	o.BalanceBonus = raw.BalanceBonus
	o.EstimatedRateMultiplier = raw.EstimatedRateMultiplier
	o.EstimatedTokens = raw.EstimatedTokens
	o.Concurrency = raw.Concurrency
	o.Recommended = raw.Recommended
	o.SortOrder = raw.SortOrder
	o.Enabled = raw.Enabled == nil || *raw.Enabled
	o.PurchaseRules = raw.PurchaseRules
	o.Eligibility = nil
	return nil
}

func validateRechargeOption(option RechargeOption) error {
	if math.IsNaN(option.Amount) || math.IsInf(option.Amount, 0) || option.Amount <= 0 {
		return fmt.Errorf("recharge option amount must be positive")
	}
	if math.IsNaN(option.OriginalPrice) || math.IsInf(option.OriginalPrice, 0) || option.OriginalPrice < 0 {
		return fmt.Errorf("recharge option original_price must be >= 0")
	}
	if math.IsNaN(option.BalanceBonus) || math.IsInf(option.BalanceBonus, 0) || option.BalanceBonus < 0 {
		return fmt.Errorf("recharge option balance_bonus must be >= 0")
	}
	if math.IsNaN(option.EstimatedRateMultiplier) || math.IsInf(option.EstimatedRateMultiplier, 0) || option.EstimatedRateMultiplier < 0 {
		return fmt.Errorf("recharge option estimated_rate_multiplier must be >= 0")
	}
	if option.EstimatedTokens < 0 {
		return fmt.Errorf("recharge option estimated_tokens must be >= 0")
	}
	if option.Concurrency < 0 || option.Concurrency > 10000 {
		return fmt.Errorf("recharge option concurrency must be between 0 and 10000")
	}
	if _, err := normalizePurchaseRules(option.PurchaseRules); err != nil {
		return err
	}
	return nil
}

func normalizeRechargeOption(option RechargeOption) (RechargeOption, error) {
	if err := validateRechargeOption(option); err != nil {
		return RechargeOption{}, err
	}
	rules, err := normalizePurchaseRules(option.PurchaseRules)
	if err != nil {
		return RechargeOption{}, err
	}
	option.Label = strings.TrimSpace(option.Label)
	option.Description = strings.TrimSpace(option.Description)
	option.PurchaseRules = rules
	option.Eligibility = nil
	return option, nil
}

func rechargeOptionForAmount(options []RechargeOption, amount float64) (RechargeOption, bool) {
	for _, option := range options {
		if option.Enabled && math.Abs(option.Amount-amount) <= 0.000001 {
			return option, true
		}
	}
	return RechargeOption{}, false
}

func rechargeOptionDiscountPercent(option RechargeOption) float64 {
	if option.OriginalPrice <= option.Amount || option.OriginalPrice <= 0 {
		return 0
	}
	return math.Round((1-option.Amount/option.OriginalPrice)*10000) / 100
}

func calculateRechargeCreditedAmount(paymentAmount, multiplier float64, options []RechargeOption) float64 {
	credited := calculateCreditedBalance(paymentAmount, multiplier)
	option, ok := rechargeOptionForAmount(options, paymentAmount)
	if !ok || option.BalanceBonus <= 0 {
		return credited
	}
	// Stay on decimal for the bonus step too. Every other amount in this package
	// is computed with shopspring/decimal; a float round here would drift from
	// the rest of the ledger once bonuses stop being whole numbers.
	return decimal.NewFromFloat(credited).
		Add(decimal.NewFromFloat(option.BalanceBonus)).
		Round(2).
		InexactFloat64()
}

func normalizePlanEntitlements(raw map[string]any) (map[string]any, PlanEntitlements, error) {
	if raw == nil {
		raw = map[string]any{}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, PlanEntitlements{}, fmt.Errorf("encode plan entitlements: %w", err)
	}
	var entitlements PlanEntitlements
	if err := json.Unmarshal(encoded, &entitlements); err != nil {
		return nil, PlanEntitlements{}, fmt.Errorf("decode plan entitlements: %w", err)
	}
	if math.IsNaN(entitlements.BalanceBonus) || math.IsInf(entitlements.BalanceBonus, 0) || entitlements.BalanceBonus < 0 {
		return nil, PlanEntitlements{}, fmt.Errorf("balance_bonus must be >= 0")
	}
	if entitlements.ResetCardCount < 0 || entitlements.ResetCardCount > MaxResetCardsPerGrant {
		return nil, PlanEntitlements{}, fmt.Errorf("reset_card_count must be between 0 and %d", MaxResetCardsPerGrant)
	}
	if _, _, err := resetCardPurchaseConfiguredPrice(entitlements.ResetCardPurchasePrice); err != nil {
		return nil, PlanEntitlements{}, err
	}
	purchaseRules, err := normalizePurchaseRules(entitlements.PurchaseRules)
	if err != nil {
		return nil, PlanEntitlements{}, err
	}
	resetCardPurchaseRules, err := normalizePurchaseRules(entitlements.ResetCardPurchaseRules)
	if err != nil {
		return nil, PlanEntitlements{}, err
	}
	entitlements.PurchaseRules = purchaseRules
	entitlements.ResetCardPurchaseRules = resetCardPurchaseRules
	entitlements.ResetCardExpiryUnit = normalizeResetCardExpiryUnit(entitlements.ResetCardExpiryUnit)
	if entitlements.ResetCardCount > 0 {
		if entitlements.ResetCardExpiryDays <= 0 {
			return nil, PlanEntitlements{}, fmt.Errorf("reset_card_expiry_days must be positive when reset cards are granted")
		}
		// Bound the resolved duration, not the raw count: 36 months and 1100
		// days are the same mistake and must fail the same way.
		if validity := entitlements.ResetCardValidityDays(); validity <= 0 || validity > maxResetCardValidityDays {
			return nil, PlanEntitlements{}, fmt.Errorf("reset card validity must not exceed %d days", maxResetCardValidityDays)
		}
	}
	if entitlements.ResetCardCount == 0 {
		entitlements.ResetCardExpiryDays = 0
		entitlements.ResetCardExpiryUnit = resetCardExpiryUnitDay
	}
	if entitlements.Concurrency < 0 || entitlements.Concurrency > 10000 {
		return nil, PlanEntitlements{}, fmt.Errorf("concurrency must be between 0 and 10000")
	}
	entitlements.Message = strings.TrimSpace(entitlements.Message)
	entitlements.ResetCardTitle = strings.TrimSpace(entitlements.ResetCardTitle)
	entitlements.ResetCardDescription = strings.TrimSpace(entitlements.ResetCardDescription)
	if utf8.RuneCountInString(entitlements.ResetCardTitle) > maxResetCardTitleLength {
		return nil, PlanEntitlements{}, fmt.Errorf("reset_card_title must be at most %d characters", maxResetCardTitleLength)
	}
	if utf8.RuneCountInString(entitlements.ResetCardDescription) > maxResetCardDescriptionLength {
		return nil, PlanEntitlements{}, fmt.Errorf("reset_card_description must be at most %d characters", maxResetCardDescriptionLength)
	}
	canonical, err := json.Marshal(entitlements)
	if err != nil {
		return nil, PlanEntitlements{}, fmt.Errorf("encode canonical plan entitlements: %w", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(canonical, &normalized); err != nil {
		return nil, PlanEntitlements{}, fmt.Errorf("decode canonical plan entitlements: %w", err)
	}
	return normalized, entitlements, nil
}

func PlanEntitlementsFromRaw(raw map[string]any) PlanEntitlements {
	_, entitlements, err := normalizePlanEntitlements(raw)
	if err != nil {
		return PlanEntitlements{}
	}
	return entitlements
}

// paymentSnapshotKindBalance is the product snapshot kind written for a
// balance top-up. Balance-tier bonuses are folded into the credited amount
// (PaymentOrder.amount), so the ordinary balance refund deduction already
// reclaims them. Subscription bonuses are added separately and therefore must
// remain behind the manual-reclaim fence.
const paymentSnapshotKindBalance = "balance"

// paymentEntitlementsRequireManualRefund reports benefits that cannot yet be
// reversed by the refund ledger. Refusing an automatic refund is safer than
// returning the payment while leaving a bonus or account upgrade behind.
func paymentEntitlementsRequireManualRefund(entitlements PlanEntitlements) bool {
	return entitlements.BalanceBonus > 0 || entitlements.ResetCardCount > 0 || entitlements.Concurrency > 0
}

// paymentOrderRequiresManualRefund resolves the refund fence from the
// immutable purchase snapshot. Live plan edits must never change whether an
// already-paid order is refundable.
func paymentOrderRequiresManualRefund(order *dbent.PaymentOrder) (bool, error) {
	if order == nil {
		// A missing order cannot be proven to have had its entitlements reclaimed.
		return true, nil
	}
	entitlements, err := paymentOrderEntitlementsStrict(order)
	if err != nil {
		return false, err
	}
	if entitlements.ResetCardCount > 0 || entitlements.Concurrency > 0 {
		return true, nil
	}
	if entitlements.BalanceBonus <= 0 {
		return false, nil
	}
	kind := ""
	if order.ProductSnapshot != nil {
		kind, _ = order.ProductSnapshot["kind"].(string)
	}
	if kind != paymentSnapshotKindBalance {
		return true, nil
	}

	// A balance bonus is only automatically reclaimable when the immutable
	// snapshot proves that it was folded into the credited balance (and thus
	// into PaymentOrder.amount).  Treat missing, malformed, or inconsistent
	// evidence as manual review rather than risking a refund that leaves the
	// bonus behind.
	if !isFinitePositiveRefundAmount(order.Amount) || order.ProductSnapshot == nil {
		return true, nil
	}
	credited, ok := paymentSnapshotFloat(order.ProductSnapshot["credited_amount"])
	if !ok || !isFinitePositiveRefundAmount(credited) {
		return true, nil
	}
	// Snapshot evidence is a refund-safety fence, not a provider-notification
	// comparison. Use the currency's half-minor-unit equality here so a
	// one-cent discrepancy cannot accidentally classify a bonus as already
	// reclaimed.
	tolerance := paymentAmountZeroTolerance(PaymentOrderCurrency(order))
	return math.Abs(credited-order.Amount) > tolerance, nil
}

// paymentSnapshotFloat accepts the numeric representations used by JSON
// snapshots before and after a database round trip. Strings are deliberately
// accepted only when they parse as finite numbers so legacy rows can still be
// evaluated without weakening the safety fence.
func paymentSnapshotFloat(value any) (float64, bool) {
	var n float64
	switch typed := value.(type) {
	case float64:
		n = typed
	case float32:
		n = float64(typed)
	case int:
		n = float64(typed)
	case int8:
		n = float64(typed)
	case int16:
		n = float64(typed)
	case int32:
		n = float64(typed)
	case int64:
		n = float64(typed)
	case uint:
		n = float64(typed)
	case uint8:
		n = float64(typed)
	case uint16:
		n = float64(typed)
	case uint32:
		n = float64(typed)
	case uint64:
		n = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		n = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		n = parsed
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}

func PlanDiscountPercent(price float64, original *float64) float64 {
	if original == nil || *original <= 0 || price >= *original {
		return 0
	}
	return math.Round((1-price/(*original))*10000) / 100
}

func PlanPeriodLabel(days int, unit string) string {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "quarter", "quarters":
		return "quarter"
	case "year", "years":
		return "year"
	case "month", "months":
		if days == 3 {
			return "quarter"
		}
		if days == 12 {
			return "year"
		}
		return "month"
	default:
		if days == 90 {
			return "quarter"
		}
		if days == 365 {
			return "year"
		}
		return "custom"
	}
}

// normalizeRechargeOptions parses the stored preset list. The bool reports
// whether the stored value was fully understood.
//
// This distinction matters for safety, not just diagnostics: order validation
// only enforces fixed tiers when at least one enabled tier survives parsing. A
// corrupted setting that silently parsed to an empty list would therefore
// disable the whole product layer and quietly reopen free-amount top-ups. The
// caller turns a false here into a hard rejection instead.
func normalizeRechargeOptions(raw string) ([]RechargeOption, bool) {
	if strings.TrimSpace(raw) == "" {
		return []RechargeOption{}, true
	}
	var options []RechargeOption
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return []RechargeOption{}, false
	}
	intact := true
	valid := make([]RechargeOption, 0, len(options))
	for _, option := range options {
		normalized, err := normalizeRechargeOption(option)
		if err != nil {
			intact = false
			continue
		}
		valid = append(valid, normalized)
	}
	sort.SliceStable(valid, func(i, j int) bool {
		if valid[i].SortOrder == valid[j].SortOrder {
			return valid[i].Amount < valid[j].Amount
		}
		return valid[i].SortOrder < valid[j].SortOrder
	})
	return valid, intact
}

// EnabledRechargeOptionsForCheckout keeps disabled admin presets out of the
// public checkout contract while retaining them in the admin settings view.
func EnabledRechargeOptionsForCheckout(options []RechargeOption) []RechargeOption {
	result := make([]RechargeOption, 0, len(options))
	for _, option := range options {
		if option.Enabled {
			result = append(result, option)
		}
	}
	return result
}

func encodeRechargeOptions(options []RechargeOption) (string, error) {
	if options == nil {
		options = []RechargeOption{}
	}
	normalizedOptions := make([]RechargeOption, 0, len(options))
	for i, option := range options {
		normalized, err := normalizeRechargeOption(option)
		if err != nil {
			return "", err
		}
		for _, previous := range normalizedOptions[:i] {
			if math.Abs(previous.Amount-normalized.Amount) <= 0.000001 {
				return "", fmt.Errorf("recharge option amount %g is duplicated", normalized.Amount)
			}
		}
		normalizedOptions = append(normalizedOptions, normalized)
	}
	encoded, err := json.Marshal(normalizedOptions)
	if err != nil {
		return "", fmt.Errorf("encode recharge options: %w", err)
	}
	return string(encoded), nil
}
