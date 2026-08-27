package service

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/shopspring/decimal"
)

// PlanEntitlements is the public, structured set of benefits delivered after
// a subscription order is confirmed. Unknown JSON fields are intentionally
// ignored so the schema can grow without breaking older clients.
type PlanEntitlements struct {
	BalanceBonus        float64 `json:"balance_bonus"`
	ResetCardCount      int     `json:"reset_card_count"`
	ResetCardExpiryDays int     `json:"reset_card_expiry_days"`
	// Concurrency is the minimum target for the user's concurrent request cap.
	// Payment fulfillment will never lower an already higher cap. Zero means this
	// product does not change the cap.
	Concurrency int    `json:"concurrency"`
	Message     string `json:"message"`
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
	Concurrency int  `json:"concurrency,omitempty"`
	SortOrder   int  `json:"sort_order"`
	Enabled     bool `json:"enabled"`
}

// UnmarshalJSON treats an omitted enabled flag as enabled. This keeps a hand-
// written legacy preset such as {"amount": 20} usable while still allowing
// admins to explicitly disable a preset without losing it on the next save.
func (o *RechargeOption) UnmarshalJSON(data []byte) error {
	type optionJSON struct {
		Amount                  float64 `json:"amount"`
		OriginalPrice           float64 `json:"original_price"`
		Label                   string  `json:"label"`
		Description             string  `json:"description"`
		BalanceBonus            float64 `json:"balance_bonus"`
		EstimatedRateMultiplier float64 `json:"estimated_rate_multiplier"`
		EstimatedTokens         int64   `json:"estimated_tokens"`
		Concurrency             int     `json:"concurrency"`
		SortOrder               int     `json:"sort_order"`
		Enabled                 *bool   `json:"enabled"`
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
	o.SortOrder = raw.SortOrder
	o.Enabled = raw.Enabled == nil || *raw.Enabled
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
	return nil
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
	if entitlements.ResetCardCount > 0 && entitlements.ResetCardExpiryDays <= 0 {
		return nil, PlanEntitlements{}, fmt.Errorf("reset_card_expiry_days must be positive when reset cards are granted")
	}
	if entitlements.ResetCardCount == 0 {
		entitlements.ResetCardExpiryDays = 0
	}
	if entitlements.Concurrency < 0 || entitlements.Concurrency > 10000 {
		return nil, PlanEntitlements{}, fmt.Errorf("concurrency must be between 0 and 10000")
	}
	entitlements.Message = strings.TrimSpace(entitlements.Message)
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
		if validateRechargeOption(option) != nil {
			intact = false
			continue
		}
		option.Label = strings.TrimSpace(option.Label)
		option.Description = strings.TrimSpace(option.Description)
		valid = append(valid, option)
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
	for i, option := range options {
		if err := validateRechargeOption(option); err != nil {
			return "", err
		}
		for _, previous := range options[:i] {
			if math.Abs(previous.Amount-option.Amount) <= 0.000001 {
				return "", fmt.Errorf("recharge option amount %g is duplicated", option.Amount)
			}
		}
	}
	encoded, err := json.Marshal(options)
	if err != nil {
		return "", fmt.Errorf("encode recharge options: %w", err)
	}
	return string(encoded), nil
}
