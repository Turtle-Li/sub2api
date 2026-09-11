package service

import (
	"context"
	"fmt"
	"math"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const (
	maxPurchaseRuleVisibleUserIDs       = 1000
	purchaseRuleMoneyScale        int32 = 2
	purchaseEligibilityPageSize         = 500
)

var (
	// ErrPurchaseNotAllowed deliberately does not disclose which audience rule
	// hid a product. Customer catalog reads omit hidden products altogether;
	// direct checkout attempts receive the same authorization boundary.
	ErrPurchaseNotAllowed = infraerrors.Forbidden("PURCHASE_NOT_ALLOWED", "product is not available to this user")
	// ErrMinimumRechargeRequired is returned only after the caller has passed
	// the audience check. The public catalog supplies the matching required and
	// current values so a client can render the disabled state without treating
	// that projection as authority.
	ErrMinimumRechargeRequired = infraerrors.Forbidden("MINIMUM_RECHARGE_REQUIRED", "minimum total recharge is required")
	// ErrPurchaseRulesUnavailable keeps malformed persisted rule metadata from
	// silently opening a checkout path. Administrators receive the detailed
	// validation error when saving configuration; customers receive this safe
	// availability error instead.
	ErrPurchaseRulesUnavailable = infraerrors.ServiceUnavailable("PURCHASE_RULES_UNAVAILABLE", "purchase eligibility is temporarily unavailable")
)

// PurchaseRules controls customer visibility and admission for one payment
// card. An empty visible_user_ids list means every user can see the card. A
// nil or zero threshold is equivalent to no threshold and preserves legacy
// availability.
type PurchaseRules struct {
	VisibleUserIDs   []int64  `json:"visible_user_ids,omitempty"`
	MinTotalRecharge *float64 `json:"min_total_recharge,omitempty"`
}

// PurchaseEligibility is the public, server-derived admission projection for
// a visible card. It deliberately never contains audience IDs or rule source
// metadata.
type PurchaseEligibility struct {
	CanPurchase           bool     `json:"can_purchase"`
	RequiredTotalRecharge *float64 `json:"required_total_recharge,omitempty"`
	CurrentTotalRecharge  *float64 `json:"current_total_recharge,omitempty"`
	Reason                string   `json:"reason,omitempty"`
}

// ResetCardEligibility extends the normal public projection with visibility:
// reset cards have their own optional rule set even when their monthly plan is
// visible in the regular subscription catalog.
type ResetCardEligibility struct {
	Visible bool `json:"visible"`
	PurchaseEligibility
}

// CustomerSubscriptionPlan is the customer-safe projection of one sale plan.
// The embedded ent plan is internal-only; callers must serialize Entitlements
// and eligibility fields rather than the raw plan entitlement map.
type CustomerSubscriptionPlan struct {
	plan                 *dbent.SubscriptionPlan
	Entitlements         PlanEntitlements
	Eligibility          PurchaseEligibility
	ResetCardEligibility ResetCardEligibility
}

// Plan returns the source entity for server-side response mapping. It is kept
// out of the struct's JSON fields so a future customer endpoint cannot
// accidentally serialize raw entitlement metadata.
func (p CustomerSubscriptionPlan) Plan() *dbent.SubscriptionPlan {
	return p.plan
}

// CustomerRechargeOption is the customer-safe projection of a recharge tier.
// Option has its PurchaseRules field removed before it is returned.
type CustomerRechargeOption struct {
	Option      RechargeOption
	Eligibility PurchaseEligibility
}

// CustomerPaymentCatalog lets checkout derive plan and recharge eligibility
// from one cumulative-recharge read. Standalone plans/config endpoints use the
// same projection with the irrelevant input slice empty.
type CustomerPaymentCatalog struct {
	Plans           []CustomerSubscriptionPlan
	RechargeOptions []CustomerRechargeOption
}

func normalizePurchaseRules(input *PurchaseRules) (*PurchaseRules, error) {
	if input == nil {
		return nil, nil
	}

	normalized := PurchaseRules{}
	if len(input.VisibleUserIDs) > 0 {
		seen := make(map[int64]struct{}, len(input.VisibleUserIDs))
		normalized.VisibleUserIDs = make([]int64, 0, len(input.VisibleUserIDs))
		for _, userID := range input.VisibleUserIDs {
			if userID <= 0 {
				return nil, fmt.Errorf("purchase_rules visible_user_ids must contain positive user IDs")
			}
			if _, exists := seen[userID]; exists {
				continue
			}
			seen[userID] = struct{}{}
			normalized.VisibleUserIDs = append(normalized.VisibleUserIDs, userID)
			if len(normalized.VisibleUserIDs) > maxPurchaseRuleVisibleUserIDs {
				return nil, fmt.Errorf("purchase_rules visible_user_ids must contain at most %d users", maxPurchaseRuleVisibleUserIDs)
			}
		}
	}

	if input.MinTotalRecharge != nil {
		value := *input.MinTotalRecharge
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return nil, fmt.Errorf("purchase_rules min_total_recharge must be a finite amount >= 0")
		}
		amount := decimal.NewFromFloat(value)
		if !amount.Equal(amount.Round(purchaseRuleMoneyScale)) {
			return nil, fmt.Errorf("purchase_rules min_total_recharge must have at most %d decimal places", purchaseRuleMoneyScale)
		}
		if amount.IsPositive() {
			canonical := amount.InexactFloat64()
			normalized.MinTotalRecharge = &canonical
		}
	}

	if len(normalized.VisibleUserIDs) == 0 && normalized.MinTotalRecharge == nil {
		return nil, nil
	}
	return &normalized, nil
}

func purchaseRulesAudienceVisible(rules *PurchaseRules, userID int64) bool {
	if rules == nil || len(rules.VisibleUserIDs) == 0 {
		return true
	}
	for _, candidate := range rules.VisibleUserIDs {
		if candidate == userID {
			return true
		}
	}
	return false
}

func purchaseRulesNeedsRechargeTotal(rules *PurchaseRules) bool {
	return rules != nil && rules.MinTotalRecharge != nil && *rules.MinTotalRecharge > 0
}

func projectPurchaseEligibility(rules *PurchaseRules, userID int64, total *decimal.Decimal) (bool, PurchaseEligibility, error) {
	if !purchaseRulesAudienceVisible(rules, userID) {
		return false, PurchaseEligibility{CanPurchase: false}, nil
	}
	eligibility := PurchaseEligibility{CanPurchase: true}
	if !purchaseRulesNeedsRechargeTotal(rules) {
		return true, eligibility, nil
	}
	if total == nil {
		return false, PurchaseEligibility{}, fmt.Errorf("purchase eligibility requires a recharge total")
	}
	required := *rules.MinTotalRecharge
	if total.LessThan(decimal.NewFromFloat(required)) {
		current := total.InexactFloat64()
		eligibility.CanPurchase = false
		eligibility.RequiredTotalRecharge = &required
		eligibility.CurrentTotalRecharge = &current
		eligibility.Reason = "minimum_recharge"
	}
	return true, eligibility, nil
}

func validatePurchaseRulesForUser(ctx context.Context, client *dbent.Client, userID int64, rules *PurchaseRules) error {
	normalized, err := normalizePurchaseRules(rules)
	if err != nil {
		return ErrPurchaseRulesUnavailable
	}
	if !purchaseRulesAudienceVisible(normalized, userID) {
		return ErrPurchaseNotAllowed
	}
	if !purchaseRulesNeedsRechargeTotal(normalized) {
		return nil
	}
	total, err := completedCNYBalanceRechargeTotal(ctx, client, userID)
	if err != nil {
		return ErrPurchaseRulesUnavailable
	}
	_, eligibility, err := projectPurchaseEligibility(normalized, userID, &total)
	if err != nil {
		return ErrPurchaseRulesUnavailable
	}
	if !eligibility.CanPurchase {
		return ErrMinimumRechargeRequired
	}
	return nil
}

// validateResetCardPurchaseRules applies the monthly plan's audience fence
// first, then the reset card's independent audience/threshold rules. The
// ordinary plan threshold is intentionally not inherited by reset cards: it
// controls new subscription admission, not a user's existing subscription.
func validateResetCardPurchaseRules(ctx context.Context, client *dbent.Client, userID int64, entitlements PlanEntitlements) error {
	monthlyRules, err := normalizePurchaseRules(entitlements.PurchaseRules)
	if err != nil {
		return ErrPurchaseRulesUnavailable
	}
	if !purchaseRulesAudienceVisible(monthlyRules, userID) {
		return ErrPurchaseNotAllowed
	}
	return validatePurchaseRulesForUser(ctx, client, userID, entitlements.ResetCardPurchaseRules)
}

func projectResetCardEligibility(entitlements PlanEntitlements, userID int64, total *decimal.Decimal) (ResetCardEligibility, error) {
	monthlyRules, err := normalizePurchaseRules(entitlements.PurchaseRules)
	if err != nil {
		return ResetCardEligibility{}, err
	}
	if !purchaseRulesAudienceVisible(monthlyRules, userID) {
		return ResetCardEligibility{Visible: false, PurchaseEligibility: PurchaseEligibility{CanPurchase: false}}, nil
	}
	resetRules, err := normalizePurchaseRules(entitlements.ResetCardPurchaseRules)
	if err != nil {
		return ResetCardEligibility{}, err
	}
	visible, eligibility, err := projectPurchaseEligibility(resetRules, userID, total)
	if err != nil {
		return ResetCardEligibility{}, err
	}
	return ResetCardEligibility{Visible: visible, PurchaseEligibility: eligibility}, nil
}

// completedCNYBalanceRechargeTotal returns the net successful balance recharge
// paid through CNY providers. completed_at is the durable fulfillment fact;
// paid/recharging status alone is deliberately excluded. Refund amounts use
// internal-credit units, so their CNY channel value must be derived with the
// existing gateway-refund conversion helper instead of direct subtraction.
func completedCNYBalanceRechargeTotal(ctx context.Context, client *dbent.Client, userID int64) (decimal.Decimal, error) {
	if client == nil || userID <= 0 {
		return decimal.Zero, fmt.Errorf("purchase eligibility payment-order reader is unavailable")
	}

	total := decimal.Zero
	var afterID int64
	for {
		var rows []paymentEligibilityOrderRow
		err := client.PaymentOrder.Query().
			Where(
				paymentorder.UserIDEQ(userID),
				paymentorder.OrderTypeEQ(payment.OrderTypeBalance),
				paymentorder.CompletedAtNotNil(),
				paymentorder.IDGT(afterID),
			).
			Order(paymentorder.ByID()).
			Limit(purchaseEligibilityPageSize).
			Select(
				paymentorder.FieldID,
				paymentorder.FieldAmount,
				paymentorder.FieldPayAmount,
				paymentorder.FieldRefundAmount,
				paymentorder.FieldProviderSnapshot,
			).
			Scan(ctx, &rows)
		if err != nil {
			return decimal.Zero, fmt.Errorf("list completed balance orders for purchase eligibility: %w", err)
		}
		if len(rows) == 0 {
			return total, nil
		}

		for _, row := range rows {
			afterID = row.ID
			order := &dbent.PaymentOrder{
				Amount:           row.Amount,
				PayAmount:        row.PayAmount,
				RefundAmount:     row.RefundAmount,
				ProviderSnapshot: row.ProviderSnapshot,
			}
			if PaymentOrderCurrency(order) != payment.DefaultPaymentCurrency {
				continue
			}
			if !isFinitePositiveRefundAmount(order.Amount) || !isFinitePositiveRefundAmount(order.PayAmount) || !isFiniteNonNegativeRefundAmount(order.RefundAmount) {
				return decimal.Zero, fmt.Errorf("completed balance order %d has invalid payment amounts", row.ID)
			}
			if decimal.NewFromFloat(order.RefundAmount).GreaterThan(decimal.NewFromFloat(order.Amount)) {
				return decimal.Zero, fmt.Errorf("completed balance order %d has refund amount above credited amount", row.ID)
			}
			gatewayRefund := calculateGatewayRefundAmount(order.Amount, order.PayAmount, order.RefundAmount, payment.DefaultPaymentCurrency)
			if !isFiniteNonNegativeRefundAmount(gatewayRefund) || decimal.NewFromFloat(gatewayRefund).GreaterThan(decimal.NewFromFloat(order.PayAmount)) {
				return decimal.Zero, fmt.Errorf("completed balance order %d has invalid gateway refund amount", row.ID)
			}
			net := decimal.NewFromFloat(order.PayAmount).Sub(decimal.NewFromFloat(gatewayRefund))
			if net.IsNegative() {
				return decimal.Zero, fmt.Errorf("completed balance order %d has negative net payment", row.ID)
			}
			total = total.Add(net)
		}
		if len(rows) < purchaseEligibilityPageSize {
			return total, nil
		}
	}
}

type paymentEligibilityOrderRow struct {
	ID               int64          `json:"id"`
	Amount           float64        `json:"amount"`
	PayAmount        float64        `json:"pay_amount"`
	RefundAmount     float64        `json:"refund_amount"`
	ProviderSnapshot map[string]any `json:"provider_snapshot"`
}

// CustomerPaymentCatalogForUser projects both card families from one snapshot
// of sale plans and recharge options. It makes at most one payment-order scan,
// even when several cards have thresholds.
func (s *PaymentConfigService) CustomerPaymentCatalogForUser(ctx context.Context, userID int64, rechargeOptions []RechargeOption) (*CustomerPaymentCatalog, error) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return nil, ErrPurchaseRulesUnavailable
	}
	plans, err := s.ListPlansForSale(ctx)
	if err != nil {
		return nil, err
	}
	return projectCustomerPaymentCatalog(ctx, s.entClient, userID, plans, rechargeOptions)
}

// CustomerRechargeOptionsForUser is the customer-safe projection used by the
// legacy public payment-config endpoint. It intentionally does not load plans,
// while still deriving a threshold total once when a visible recharge tier
// needs one.
func (s *PaymentConfigService) CustomerRechargeOptionsForUser(ctx context.Context, userID int64, rechargeOptions []RechargeOption) ([]RechargeOption, error) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return nil, ErrPurchaseRulesUnavailable
	}
	catalog, err := projectCustomerPaymentCatalog(ctx, s.entClient, userID, nil, rechargeOptions)
	if err != nil {
		return nil, err
	}
	options := make([]RechargeOption, 0, len(catalog.RechargeOptions))
	for _, candidate := range catalog.RechargeOptions {
		options = append(options, candidate.Option)
	}
	return options, nil
}

func projectCustomerPaymentCatalog(ctx context.Context, client *dbent.Client, userID int64, plans []*dbent.SubscriptionPlan, rechargeOptions []RechargeOption) (*CustomerPaymentCatalog, error) {
	type planCandidate struct {
		plan         *dbent.SubscriptionPlan
		entitlements PlanEntitlements
	}
	type rechargeCandidate struct {
		option RechargeOption
	}

	planCandidates := make([]planCandidate, 0, len(plans))
	rechargeCandidates := make([]rechargeCandidate, 0, len(rechargeOptions))
	needsTotal := false

	for _, plan := range plans {
		if plan == nil {
			return nil, ErrPurchaseRulesUnavailable
		}
		_, entitlements, err := normalizePlanEntitlements(plan.Entitlements)
		if err != nil {
			return nil, ErrPurchaseRulesUnavailable
		}
		planCandidates = append(planCandidates, planCandidate{plan: plan, entitlements: entitlements})
		if purchaseRulesAudienceVisible(entitlements.PurchaseRules, userID) {
			if purchaseRulesNeedsRechargeTotal(entitlements.PurchaseRules) {
				needsTotal = true
			}
			if purchaseRulesAudienceVisible(entitlements.ResetCardPurchaseRules, userID) && purchaseRulesNeedsRechargeTotal(entitlements.ResetCardPurchaseRules) {
				needsTotal = true
			}
		}
	}

	for _, option := range rechargeOptions {
		normalized, err := normalizeRechargeOption(option)
		if err != nil {
			return nil, ErrPurchaseRulesUnavailable
		}
		if !normalized.Enabled {
			continue
		}
		rechargeCandidates = append(rechargeCandidates, rechargeCandidate{option: normalized})
		if purchaseRulesAudienceVisible(normalized.PurchaseRules, userID) && purchaseRulesNeedsRechargeTotal(normalized.PurchaseRules) {
			needsTotal = true
		}
	}

	var total *decimal.Decimal
	if needsTotal {
		calculated, err := completedCNYBalanceRechargeTotal(ctx, client, userID)
		if err != nil {
			return nil, ErrPurchaseRulesUnavailable
		}
		total = &calculated
	}

	catalog := &CustomerPaymentCatalog{
		Plans:           make([]CustomerSubscriptionPlan, 0, len(planCandidates)),
		RechargeOptions: make([]CustomerRechargeOption, 0, len(rechargeCandidates)),
	}
	for _, candidate := range planCandidates {
		visible, eligibility, err := projectPurchaseEligibility(candidate.entitlements.PurchaseRules, userID, total)
		if err != nil {
			return nil, ErrPurchaseRulesUnavailable
		}
		if !visible {
			continue
		}
		resetEligibility, err := projectResetCardEligibility(candidate.entitlements, userID, total)
		if err != nil {
			return nil, ErrPurchaseRulesUnavailable
		}
		publicEntitlements := candidate.entitlements
		publicEntitlements.PurchaseRules = nil
		publicEntitlements.ResetCardPurchaseRules = nil
		catalog.Plans = append(catalog.Plans, CustomerSubscriptionPlan{
			plan:                 candidate.plan,
			Entitlements:         publicEntitlements,
			Eligibility:          eligibility,
			ResetCardEligibility: resetEligibility,
		})
	}
	for _, candidate := range rechargeCandidates {
		visible, eligibility, err := projectPurchaseEligibility(candidate.option.PurchaseRules, userID, total)
		if err != nil {
			return nil, ErrPurchaseRulesUnavailable
		}
		if !visible {
			continue
		}
		publicOption := candidate.option
		publicOption.PurchaseRules = nil
		publicOption.Eligibility = &eligibility
		catalog.RechargeOptions = append(catalog.RechargeOptions, CustomerRechargeOption{
			Option:      publicOption,
			Eligibility: eligibility,
		})
	}
	return catalog, nil
}
