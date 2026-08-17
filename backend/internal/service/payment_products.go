package service

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
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
	return math.Round((credited+option.BalanceBonus)*100) / 100
}

func normalizePlanEntitlements(raw map[string]interface{}) (map[string]interface{}, PlanEntitlements, error) {
	if raw == nil {
		raw = map[string]interface{}{}
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
	var normalized map[string]interface{}
	if err := json.Unmarshal(canonical, &normalized); err != nil {
		return nil, PlanEntitlements{}, fmt.Errorf("decode canonical plan entitlements: %w", err)
	}
	return normalized, entitlements, nil
}

func PlanEntitlementsFromRaw(raw map[string]interface{}) PlanEntitlements {
	_, entitlements, err := normalizePlanEntitlements(raw)
	if err != nil {
		return PlanEntitlements{}
	}
	return entitlements
}

// paymentEntitlementsRequireManualRefund reports benefits that cannot yet be
// reversed by the refund ledger. Refusing an automatic refund is safer than
// returning the payment while leaving a bonus or account upgrade behind.
func paymentEntitlementsRequireManualRefund(entitlements PlanEntitlements) bool {
	return entitlements.BalanceBonus > 0 || entitlements.ResetCardCount > 0 || entitlements.Concurrency > 0
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

func normalizeRechargeOptions(raw string) []RechargeOption {
	if strings.TrimSpace(raw) == "" {
		return []RechargeOption{}
	}
	var options []RechargeOption
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return []RechargeOption{}
	}
	valid := make([]RechargeOption, 0, len(options))
	for _, option := range options {
		if validateRechargeOption(option) != nil {
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
	return valid
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
