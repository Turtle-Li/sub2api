package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const (
	subscriptionPurchaseOperationSubscribe   = "subscribe"
	subscriptionPurchaseOperationRenew       = "renew"
	subscriptionPurchaseOperationResubscribe = "resubscribe"
	subscriptionPurchaseOperationUpgrade     = "upgrade"

	resetCardPurchaseValidityDays = 14
	maxResetCardPurchaseQuantity  = MaxResetCardPurchaseQuantity
)

// MaxResetCardPurchaseQuantity is the server-side upper bound for one
// standalone reset-card order. Keeping it exported lets HTTP resume-token
// validation use the same bound as normal order creation.
const MaxResetCardPurchaseQuantity = 100

// subscriptionPurchaseQuote is a server-derived commercial term. The browser
// can select a target plan, but never gets to decide whether it is a renewal,
// resubscription, or prorated upgrade, nor the amount charged for it.
type subscriptionPurchaseQuote struct {
	Plan                 *dbent.SubscriptionPlan
	ExistingSubscription *dbent.UserSubscription
	Operation            string
	Platform             string
	Amount               float64
	SubscriptionDays     int
}

type resetCardPurchaseQuote struct {
	Subscription *dbent.UserSubscription
	Platform     string
	Quantity     int
	UnitPrice    float64
	Amount       float64
}

type paymentOrderProduct struct {
	Subscription *subscriptionPurchaseQuote
	ResetCards   *resetCardPurchaseQuote
}

func (p *paymentOrderProduct) amount() float64 {
	if p == nil {
		return 0
	}
	if p.Subscription != nil {
		return p.Subscription.Amount
	}
	if p.ResetCards != nil {
		return p.ResetCards.Amount
	}
	return 0
}

func (p *paymentOrderProduct) plan() *dbent.SubscriptionPlan {
	if p == nil || p.Subscription == nil {
		return nil
	}
	return p.Subscription.Plan
}

func (s *PaymentService) resolvePaymentOrderProduct(ctx context.Context, req CreateOrderRequest, plan *dbent.SubscriptionPlan, lockExisting bool) (*paymentOrderProduct, error) {
	switch req.OrderType {
	case payment.OrderTypeSubscription:
		quote, err := s.quoteSubscriptionPurchase(ctx, req.UserID, plan, lockExisting)
		if err != nil {
			return nil, err
		}
		return &paymentOrderProduct{Subscription: quote}, nil
	case payment.OrderTypeSubscriptionResetCards:
		quote, err := s.quoteResetCardPurchase(ctx, req.UserID, req.ResetSubscriptionID, req.ResetCardQuantity, lockExisting)
		if err != nil {
			return nil, err
		}
		return &paymentOrderProduct{ResetCards: quote}, nil
	default:
		return nil, nil
	}
}

func samePaymentProductTerms(expected, actual *paymentOrderProduct) bool {
	if expected == nil || actual == nil {
		return expected == actual
	}
	if math.Abs(expected.amount()-actual.amount()) > 0.000001 {
		return false
	}
	if expected.Subscription != nil && actual.Subscription != nil {
		if expected.Subscription.Operation != actual.Subscription.Operation ||
			expected.Subscription.Platform != actual.Subscription.Platform ||
			expected.Subscription.SubscriptionDays != actual.Subscription.SubscriptionDays ||
			expected.Subscription.Plan == nil || actual.Subscription.Plan == nil ||
			expected.Subscription.Plan.ID != actual.Subscription.Plan.ID ||
			math.Abs(expected.Subscription.Plan.Price-actual.Subscription.Plan.Price) > 0.000001 ||
			expected.Subscription.Plan.ValidityDays != actual.Subscription.Plan.ValidityDays ||
			expected.Subscription.Plan.ValidityUnit != actual.Subscription.Plan.ValidityUnit ||
			expected.Subscription.Plan.GroupID != actual.Subscription.Plan.GroupID {
			return false
		}
		if expected.Subscription.ExistingSubscription == nil || actual.Subscription.ExistingSubscription == nil {
			return expected.Subscription.ExistingSubscription == nil && actual.Subscription.ExistingSubscription == nil
		}
		return expected.Subscription.ExistingSubscription.ID == actual.Subscription.ExistingSubscription.ID &&
			expected.Subscription.ExistingSubscription.GroupID == actual.Subscription.ExistingSubscription.GroupID
	}
	if expected.ResetCards != nil && actual.ResetCards != nil {
		return expected.ResetCards.Subscription != nil && actual.ResetCards.Subscription != nil &&
			expected.ResetCards.Subscription.ID == actual.ResetCards.Subscription.ID &&
			expected.ResetCards.Quantity == actual.ResetCards.Quantity &&
			math.Abs(expected.ResetCards.UnitPrice-actual.ResetCards.UnitPrice) <= 0.000001
	}
	return false
}

func paymentClientFromContext(ctx context.Context, fallback *dbent.Client) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	if client := dbent.FromContext(ctx); client != nil {
		return client
	}
	return fallback
}

func isSubscriptionProductOrder(orderType string) bool {
	return orderType == payment.OrderTypeSubscription || orderType == payment.OrderTypeSubscriptionResetCards
}

func (s *PaymentService) quoteSubscriptionPurchase(ctx context.Context, userID int64, plan *dbent.SubscriptionPlan, lockExisting bool) (*subscriptionPurchaseQuote, error) {
	if s == nil || s.entClient == nil || s.groupRepo == nil || plan == nil {
		return nil, infraerrors.ServiceUnavailable("SUBSCRIPTION_PURCHASE_UNAVAILABLE", "subscription purchase is unavailable")
	}
	if userID <= 0 || plan.ID <= 0 || plan.Price <= 0 {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "subscription purchase terms are invalid")
	}
	if !plan.ForSale {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}
	group, err := s.groupRepo.GetByID(ctx, plan.GroupID)
	if err != nil || group == nil || group.Status != payment.EntityStatusActive || !group.IsSubscriptionType() {
		return nil, infraerrors.NotFound("GROUP_NOT_FOUND", "subscription group is no longer available")
	}
	platform := strings.TrimSpace(group.Platform)
	if platform == "" {
		return nil, infraerrors.BadRequest("SUBSCRIPTION_PLATFORM_INVALID", "subscription platform is unavailable")
	}

	client := paymentClientFromContext(ctx, s.entClient)
	query := client.UserSubscription.Query().Where(
		usersubscription.UserIDEQ(userID),
		usersubscription.PlatformEQ(platform),
	)
	if lockExisting {
		query = query.ForUpdate()
	}
	existing, err := query.Only(ctx)
	if err != nil && !dbent.IsNotFound(err) {
		return nil, fmt.Errorf("find user subscription for platform: %w", err)
	}
	if dbent.IsNotFound(err) {
		existing = nil
	}

	days := psComputeValidityDays(plan.ValidityDays, plan.ValidityUnit)
	if days <= 0 {
		return nil, infraerrors.BadRequest("PLAN_VALIDITY_INVALID", "subscription plan has invalid validity")
	}
	quote := &subscriptionPurchaseQuote{
		Plan:                 plan,
		ExistingSubscription: existing,
		Operation:            subscriptionPurchaseOperationSubscribe,
		Platform:             platform,
		Amount:               roundUpPaymentAmount(plan.Price),
		SubscriptionDays:     days,
	}
	if existing == nil {
		return quote, nil
	}

	now := time.Now()
	if !existing.ExpiresAt.After(now) {
		quote.Operation = subscriptionPurchaseOperationResubscribe
		return quote, nil
	}
	if existing.Status != SubscriptionStatusActive {
		return nil, infraerrors.Conflict("SUBSCRIPTION_NOT_ACTIVE", "subscription is not active and cannot be changed")
	}
	if existing.GroupID == plan.GroupID {
		quote.Operation = subscriptionPurchaseOperationRenew
		return quote, nil
	}

	if existing.PlanPrice == nil || existing.PlanValidityDays == nil || *existing.PlanPrice <= 0 || *existing.PlanValidityDays <= 0 {
		return nil, infraerrors.Conflict("UPGRADE_SOURCE_TERMS_UNKNOWN", "current subscription has no immutable purchase terms; renew it before upgrading")
	}
	if !subscriptionUnitPriceHigher(plan.Price, days, *existing.PlanPrice, *existing.PlanValidityDays) {
		return nil, infraerrors.BadRequest("SUBSCRIPTION_DOWNGRADE_NOT_SUPPORTED", "only upgrades to a higher daily price are supported")
	}
	remainingDays := subscriptionRemainingDays(existing.ExpiresAt, now)
	if remainingDays <= 0 {
		quote.Operation = subscriptionPurchaseOperationResubscribe
		return quote, nil
	}
	upgradeAmount := subscriptionUpgradeAmount(
		*existing.PlanPrice,
		*existing.PlanValidityDays,
		plan.Price,
		days,
		remainingDays,
	)
	if upgradeAmount <= 0 {
		return nil, infraerrors.BadRequest("SUBSCRIPTION_UPGRADE_PRICE_INVALID", "upgrade price must be positive")
	}
	quote.Operation = subscriptionPurchaseOperationUpgrade
	quote.Amount = upgradeAmount
	return quote, nil
}

func (s *PaymentService) quoteResetCardPurchase(ctx context.Context, userID, subscriptionID int64, quantity int, lockSubscription bool) (*resetCardPurchaseQuote, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("RESET_CARD_PURCHASE_UNAVAILABLE", "reset card purchase is unavailable")
	}
	if subscriptionID <= 0 || quantity < 1 || quantity > maxResetCardPurchaseQuantity {
		return nil, infraerrors.BadRequest("RESET_CARD_PURCHASE_INVALID", "reset card quantity must be between 1 and 100")
	}
	client := paymentClientFromContext(ctx, s.entClient)
	query := client.UserSubscription.Query().Where(
		usersubscription.IDEQ(subscriptionID),
		usersubscription.UserIDEQ(userID),
		usersubscription.StatusEQ(SubscriptionStatusActive),
		usersubscription.ExpiresAtGT(time.Now()),
	)
	if lockSubscription {
		query = query.ForUpdate()
	}
	subscription, err := query.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, infraerrors.BadRequest("RESET_CARD_SUBSCRIPTION_UNAVAILABLE", "an active subscription is required to buy reset cards")
		}
		return nil, fmt.Errorf("find reset-card subscription: %w", err)
	}
	if subscription.PlanPrice == nil || *subscription.PlanPrice <= 0 {
		return nil, infraerrors.Conflict("RESET_CARD_PRICE_UNAVAILABLE", "current subscription has no immutable purchase price")
	}
	unitPrice := roundUpPaymentAmount(decimal.NewFromFloat(*subscription.PlanPrice).Div(decimal.NewFromInt(3)).InexactFloat64())
	amount := roundUpPaymentAmount(decimal.NewFromFloat(unitPrice).Mul(decimal.NewFromInt(int64(quantity))).InexactFloat64())
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return nil, infraerrors.BadRequest("RESET_CARD_PRICE_INVALID", "reset card price is invalid")
	}
	return &resetCardPurchaseQuote{
		Subscription: subscription,
		Platform:     subscription.Platform,
		Quantity:     quantity,
		UnitPrice:    unitPrice,
		Amount:       amount,
	}, nil
}

func subscriptionUnitPriceHigher(targetPrice float64, targetDays int, currentPrice float64, currentDays int) bool {
	if targetPrice <= 0 || targetDays <= 0 || currentPrice <= 0 || currentDays <= 0 {
		return false
	}
	target := decimal.NewFromFloat(targetPrice).Div(decimal.NewFromInt(int64(targetDays)))
	current := decimal.NewFromFloat(currentPrice).Div(decimal.NewFromInt(int64(currentDays)))
	return target.GreaterThan(current)
}

func subscriptionRemainingDays(expiresAt, now time.Time) int {
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	return int(math.Ceil(remaining.Hours() / 24))
}

func subscriptionUpgradeAmount(currentPrice float64, currentDays int, targetPrice float64, targetDays int, remainingDays int) float64 {
	if currentPrice <= 0 || targetPrice <= 0 || currentDays <= 0 || targetDays <= 0 || remainingDays <= 0 {
		return 0
	}
	days := decimal.NewFromInt(int64(remainingDays))
	currentValue := decimal.NewFromFloat(currentPrice).Div(decimal.NewFromInt(int64(currentDays))).Mul(days)
	targetValue := decimal.NewFromFloat(targetPrice).Div(decimal.NewFromInt(int64(targetDays))).Mul(days)
	return roundUpPaymentAmount(targetValue.Sub(currentValue).InexactFloat64())
}

func roundUpPaymentAmount(amount float64) float64 {
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0
	}
	return decimal.NewFromFloat(amount).
		Mul(decimal.NewFromInt(100)).
		Ceil().
		Div(decimal.NewFromInt(100)).
		InexactFloat64()
}

func buildSubscriptionPurchaseProductSnapshot(quote *subscriptionPurchaseQuote, payAmount float64) map[string]interface{} {
	if quote == nil || quote.Plan == nil {
		return nil
	}
	entitlements := quote.Plan.Entitlements
	if quote.Operation == subscriptionPurchaseOperationUpgrade {
		// The price only covers the remaining term. Full-term bonuses would make
		// a prorated upgrade economically unsafe, so upgrades receive the target
		// group limits but no separate balance/reset-card/concurrency grant.
		entitlements = map[string]interface{}{}
	}
	snapshot := buildPaymentProductSnapshot(quote.Plan, quote.Amount, payAmount, quote.SubscriptionDays)
	snapshot["operation"] = quote.Operation
	snapshot["platform"] = quote.Platform
	snapshot["order_amount"] = quote.Amount
	snapshot["entitlements"] = entitlements
	if quote.ExistingSubscription != nil {
		snapshot["source_subscription_id"] = quote.ExistingSubscription.ID
		snapshot["source_group_id"] = quote.ExistingSubscription.GroupID
		if quote.ExistingSubscription.PlanID != nil {
			snapshot["source_plan_id"] = *quote.ExistingSubscription.PlanID
		}
		if quote.ExistingSubscription.PlanPrice != nil {
			snapshot["source_plan_price"] = *quote.ExistingSubscription.PlanPrice
		}
		if quote.ExistingSubscription.PlanValidityDays != nil {
			snapshot["source_plan_validity_days"] = *quote.ExistingSubscription.PlanValidityDays
		}
	}
	return snapshot
}

func buildResetCardPurchaseProductSnapshot(quote *resetCardPurchaseQuote, payAmount float64) map[string]interface{} {
	if quote == nil || quote.Subscription == nil {
		return nil
	}
	snapshot := map[string]interface{}{
		"kind":            "subscription_reset_cards",
		"operation":       "purchase_reset_cards",
		"subscription_id": quote.Subscription.ID,
		"group_id":        quote.Subscription.GroupID,
		"platform":        quote.Platform,
		"quantity":        quote.Quantity,
		"unit_price":      quote.UnitPrice,
		"order_amount":    quote.Amount,
		"pay_amount":      payAmount,
		"validity_days":   resetCardPurchaseValidityDays,
	}
	if quote.Subscription.PlanID != nil {
		snapshot["source_plan_id"] = *quote.Subscription.PlanID
	}
	if quote.Subscription.PlanPrice != nil {
		snapshot["source_plan_price"] = *quote.Subscription.PlanPrice
	}
	if quote.Subscription.PlanValidityDays != nil {
		snapshot["source_plan_validity_days"] = *quote.Subscription.PlanValidityDays
	}
	return snapshot
}
