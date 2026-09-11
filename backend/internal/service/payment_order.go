package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/paymentinvoicerequest"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionplan"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/shopspring/decimal"
)

// --- Order Creation ---

func (s *PaymentService) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if req.OrderType == "" {
		req.OrderType = payment.OrderTypeBalance
	}
	if normalized := NormalizeVisibleMethod(req.PaymentType); normalized != "" {
		req.PaymentType = normalized
	}
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get payment config: %w", err)
	}
	if !cfg.Enabled {
		return nil, infraerrors.Forbidden("PAYMENT_DISABLED", "payment system is disabled")
	}
	return s.createOrderWithConfig(ctx, req, cfg, nil)
}

// createOrderWithConfig is the shared, private creation core. The regular
// customer path supplies nil options and retains its existing configuration and
// provider-selection behavior. Narrow administrative flows can supply an
// immutable, already-validated configuration copy and a pinned provider
// selection without changing persisted payment settings.
func (s *PaymentService) createOrderWithConfig(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig, opts *createOrderOptions) (*CreateOrderResponse, error) {
	if cfg == nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_CONFIG_UNAVAILABLE", "payment configuration is unavailable")
	}
	var (
		plan *dbent.SubscriptionPlan
		err  error
	)
	if opts != nil && opts.ownerTest != nil {
		plan, err = validateOwnerTestOrderInput(req, cfg, opts.ownerTest)
	} else {
		plan, err = s.validateOrderInput(ctx, req, cfg)
	}
	if err != nil {
		return nil, err
	}
	if opts == nil || opts.ownerTest == nil {
		if err := s.checkCancelRateLimit(ctx, req.UserID, cfg); err != nil {
			return nil, err
		}
	}
	user, err := s.userRepo.GetByID(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if user.Status != payment.EntityStatusActive {
		return nil, infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
	}
	if opts != nil && opts.ownerTest != nil && !user.IsAdmin() {
		return nil, infraerrors.Forbidden("OWNER_TEST_ADMIN_REQUIRED", "an active administrator is required for an owner test order")
	}
	if (opts == nil || opts.ownerTest == nil) && s.notificationEmailService != nil {
		s.notificationEmailService.RememberRecipientLocale(ctx, req.UserID, user.Email, req.Locale)
	}
	orderAmount := req.Amount
	limitAmount := req.Amount
	if plan != nil {
		orderAmount = plan.Price
		limitAmount = plan.Price
	} else if req.OrderType == payment.OrderTypeBalance {
		orderAmount = calculateRechargeCreditedAmount(req.Amount, cfg.BalanceRechargeMultiplier, cfg.RechargeOptions)
	}
	feeRate := cfg.RechargeFeeRate
	methodCurrency := payment.DefaultPaymentCurrency
	if opts != nil && opts.ownerTest != nil {
		methodCurrency = payment.DefaultPaymentCurrency
	} else if s.configService != nil {
		methodCurrency, err = s.configService.ValidateMethodCurrencyConsistency(ctx, req.PaymentType)
		if err != nil {
			return nil, err
		}
	}
	payAmountStr := ""
	payAmount := float64(0)
	if opts != nil && opts.ownerTest != nil {
		payAmountStr = opts.ownerTest.amountDecimal
		payAmount = opts.ownerTest.amount
	} else {
		payAmountStr, payAmount, err = calculateCreateOrderPayAmountForOrderType(limitAmount, feeRate, methodCurrency, req.OrderType, cfg.SubscriptionUSDToCNYRate)
		if err != nil {
			return nil, err
		}
	}
	var sel *payment.InstanceSelection
	if opts != nil && opts.ownerTest != nil {
		sel = opts.ownerTest.selection
	} else {
		sel, err = s.selectCreateOrderInstance(ctx, req, cfg, payAmount)
		if err != nil {
			return nil, err
		}
	}
	if err := s.validateSelectedCreateOrderInstance(ctx, req, sel); err != nil {
		return nil, err
	}
	selectedCurrency := payment.DefaultPaymentCurrency
	if sel != nil {
		selectedCurrency = paymentProviderConfigCurrency(sel.ProviderKey, sel.Config)
	}
	if selectedCurrency != methodCurrency {
		if opts != nil && opts.ownerTest != nil {
			return nil, infraerrors.ServiceUnavailable("OWNER_TEST_UNIFIED_PAYMENT_UNAVAILABLE", "owner test payment must use the configured CNY unified gateway")
		}
		payAmountStr, payAmount, err = calculateCreateOrderPayAmountForOrderType(limitAmount, feeRate, selectedCurrency, req.OrderType, cfg.SubscriptionUSDToCNYRate)
		if err != nil {
			return nil, err
		}
	}
	if err := validateSelectedCreateOrderAmountCurrency(payAmountStr, sel); err != nil {
		return nil, err
	}
	if opts == nil || opts.ownerTest == nil {
		oauthResp, oauthErr := s.maybeBuildWeChatOAuthRequiredResponseForSelection(ctx, req, limitAmount, payAmount, feeRate, sel)
		if oauthErr != nil {
			return nil, oauthErr
		}
		if oauthResp != nil {
			return oauthResp, nil
		}
	}
	order, created, err := s.createOrderInTxWithOptions(ctx, req, user, plan, cfg, orderAmount, limitAmount, feeRate, payAmount, sel, createOrderDatabaseOptionsFrom(opts))
	if err != nil {
		if opts != nil && opts.ownerTest != nil && isOwnerTestOrderInsertConflict(err) {
			return s.replayOwnerTestOrder(ctx, opts.ownerTest)
		}
		return nil, err
	}
	if opts != nil && opts.ownerTest != nil {
		if !created {
			return s.replayOwnerTestOrderRecord(ctx, order, opts.ownerTest)
		}
		s.writeAuditLog(ctx, order.ID, "OWNER_TEST_ORDER_CREATED", fmt.Sprintf("admin:%d", opts.ownerTest.input.AdminUserID), ownerTestAuditDetail(opts.ownerTest))
		return s.invokeOwnerTestProvider(ctx, order, opts.ownerTest)
	}
	resp, err := s.invokeProvider(ctx, order, req, cfg, limitAmount, payAmountStr, payAmount, plan, sel)
	if err != nil {
		if errors.Is(err, unifiedpay.ErrCreateStateUnconfirmed) {
			s.writeAuditLog(ctx, order.ID, "UNIFIED_PAYMENT_CREATE_UNCONFIRMED", payment.TypeUnifiedPay, map[string]any{
				"out_trade_no": order.OutTradeNo,
			})
			return nil, err
		}
		_, _ = s.entClient.PaymentOrder.UpdateOneID(order.ID).
			SetStatus(OrderStatusFailed).
			Save(ctx)
		return nil, err
	}
	return resp, nil
}

func (s *PaymentService) validateOrderInput(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig) (*dbent.SubscriptionPlan, error) {
	useUnified, err := s.usesUnifiedPayment(ctx, req.PaymentType)
	if err != nil {
		return nil, err
	}
	if useUnified {
		timeoutMinutes := cfg.OrderTimeoutMin
		if timeoutMinutes <= 0 {
			timeoutMinutes = defaultOrderTimeoutMin
		}
		if timeoutMinutes < 5 || timeoutMinutes > 120 {
			return nil, infraerrors.ServiceUnavailable("UNIFIED_PAYMENT_INVALID_TIMEOUT", "unified payment order timeout must be between 5 and 120 minutes")
		}
	}
	if req.OrderType == payment.OrderTypeBalance && cfg.BalanceDisabled {
		return nil, infraerrors.Forbidden("BALANCE_PAYMENT_DISABLED", "balance recharge has been disabled")
	}
	if req.OrderType == payment.OrderTypeSubscription {
		return s.validateSubOrder(ctx, req)
	}
	if math.IsNaN(req.Amount) || math.IsInf(req.Amount, 0) || req.Amount <= 0 {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "amount must be a positive number")
	}
	if (cfg.MinAmount > 0 && req.Amount < cfg.MinAmount) || (cfg.MaxAmount > 0 && req.Amount > cfg.MaxAmount) {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "amount out of range").
			WithMetadata(map[string]string{"min": fmt.Sprintf("%.2f", cfg.MinAmount), "max": fmt.Sprintf("%.2f", cfg.MaxAmount)})
	}
	// Fail closed on a corrupted preset list. Falling through would drop the
	// fixed-tier requirement below and quietly reopen arbitrary top-up amounts
	// that carry no entitlements.
	if cfg.RechargeOptionsInvalid {
		return nil, infraerrors.BadRequest("RECHARGE_OPTIONS_UNAVAILABLE", "recharge tiers are misconfigured; balance top-up is temporarily unavailable")
	}
	if len(EnabledRechargeOptionsForCheckout(cfg.RechargeOptions)) > 0 {
		option, ok := rechargeOptionForAmount(cfg.RechargeOptions, req.Amount)
		if !ok {
			return nil, infraerrors.BadRequest("INVALID_RECHARGE_OPTION", "amount must match an enabled recharge option")
		}
		if err := validatePurchaseRulesForUser(ctx, s.paymentEligibilityClient(), req.UserID, option.PurchaseRules); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// Recharge pricing modes reported to clients so they stop inferring the mode
// from an empty tier list. "fixed" means the server accepts only the tiers it
// returned; "custom" means any amount inside the configured min/max is valid.
const (
	RechargeModeFixed  = "fixed"
	RechargeModeCustom = "custom"
)

// RechargeModeForConfig mirrors the rule validateOrderInput enforces, so the
// checkout contract and order validation can never drift apart.
func RechargeModeForConfig(cfg *PaymentConfig) string {
	if cfg == nil {
		return RechargeModeCustom
	}
	if cfg.RechargeOptionsInvalid || len(EnabledRechargeOptionsForCheckout(cfg.RechargeOptions)) > 0 {
		return RechargeModeFixed
	}
	return RechargeModeCustom
}

func (s *PaymentService) validateSubOrder(ctx context.Context, req CreateOrderRequest) (*dbent.SubscriptionPlan, error) {
	if req.PlanID == 0 {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "subscription order requires a plan")
	}
	plan, err := s.configService.GetPlan(ctx, req.PlanID)
	if err != nil || !plan.ForSale {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}
	group, err := s.groupRepo.GetByID(ctx, plan.GroupID)
	if err != nil || group.Status != payment.EntityStatusActive {
		return nil, infraerrors.NotFound("GROUP_NOT_FOUND", "subscription group is no longer available")
	}
	if !isNewSubscriptionCheckoutPlatform(group.Platform) {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan is not available for new subscription checkout")
	}
	if !group.IsSubscriptionType() {
		return nil, infraerrors.BadRequest("GROUP_TYPE_MISMATCH", "group is not a subscription type")
	}
	_, entitlements, err := normalizePlanEntitlements(plan.Entitlements)
	if err != nil {
		return nil, ErrPurchaseRulesUnavailable
	}
	if err := validatePurchaseRulesForUser(ctx, s.paymentEligibilityClient(), req.UserID, entitlements.PurchaseRules); err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *PaymentService) paymentEligibilityClient() *dbent.Client {
	if s != nil && s.entClient != nil {
		return s.entClient
	}
	if s != nil && s.configService != nil {
		return s.configService.entClient
	}
	return nil
}

// revalidateSubscriptionOrderInTx closes the interval between the optimistic
// catalog read and durable order creation. Locking group then plan matches the
// reset-card flow's policy lock order. The initial group ID is intentionally
// used for the first lock: if the plan moved, the second locked read detects
// it and rejects the stale checkout instead of pairing a new plan group with
// an old price or snapshot.
func (s *PaymentService) revalidateSubscriptionOrderInTx(ctx context.Context, tx *dbent.Tx, req CreateOrderRequest, expected *dbent.SubscriptionPlan) (*dbent.SubscriptionPlan, *Group, error) {
	if tx == nil || expected == nil {
		return nil, nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}

	groupQuery := tx.Group.Query().Where(group.IDEQ(expected.GroupID), group.DeletedAtIsNil())
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		groupQuery.ForUpdate()
	}
	currentGroup, err := groupQuery.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil, infraerrors.NotFound("GROUP_NOT_FOUND", "subscription group is no longer available")
		}
		return nil, nil, fmt.Errorf("lock subscription checkout group: %w", err)
	}

	planQuery := tx.SubscriptionPlan.Query().Where(subscriptionplan.IDEQ(expected.ID))
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		planQuery.ForUpdate()
	}
	currentPlan, err := planQuery.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
		}
		return nil, nil, fmt.Errorf("lock subscription checkout plan: %w", err)
	}
	if currentPlan.GroupID != expected.GroupID || !currentPlan.ForSale {
		return nil, nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}
	if currentGroup.Status != payment.EntityStatusActive {
		return nil, nil, infraerrors.NotFound("GROUP_NOT_FOUND", "subscription group is no longer available")
	}
	if !isNewSubscriptionCheckoutPlatform(currentGroup.Platform) {
		return nil, nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan is not available for new subscription checkout")
	}
	if currentGroup.SubscriptionType != SubscriptionTypeSubscription {
		return nil, nil, infraerrors.BadRequest("GROUP_TYPE_MISMATCH", "group is not a subscription type")
	}
	if !subscriptionPlanCheckoutSourceEqual(expected, currentPlan) {
		return nil, nil, infraerrors.Conflict("PLAN_CHANGED", "plan changed; request current checkout information")
	}

	_, entitlements, err := normalizePlanEntitlements(currentPlan.Entitlements)
	if err != nil {
		return nil, nil, ErrPurchaseRulesUnavailable
	}
	if err := validatePurchaseRulesForUser(ctx, tx.Client(), req.UserID, entitlements.PurchaseRules); err != nil {
		return nil, nil, err
	}
	return currentPlan, paymentOrderSnapshotGroup(currentGroup), nil
}

// subscriptionPlanCheckoutSourceEqual covers every plan field frozen into an
// order snapshot or used to calculate the charged amount. A configuration
// edit therefore produces a fresh catalog/order attempt instead of silently
// charging against one version and recording another.
func subscriptionPlanCheckoutSourceEqual(expected, current *dbent.SubscriptionPlan) bool {
	if expected == nil || current == nil {
		return false
	}
	return expected.ID == current.ID &&
		expected.GroupID == current.GroupID &&
		expected.Name == current.Name &&
		expected.Description == current.Description &&
		expected.Price == current.Price &&
		sameOptionalCheckoutPrice(expected.OriginalPrice, current.OriginalPrice) &&
		expected.Currency == current.Currency &&
		expected.ValidityDays == current.ValidityDays &&
		expected.ValidityUnit == current.ValidityUnit &&
		expected.Features == current.Features &&
		subscriptionPlanSnapshotEntitlementsEqual(expected.Entitlements, current.Entitlements) &&
		expected.ProductName == current.ProductName &&
		expected.ForSale == current.ForSale &&
		expected.SortOrder == current.SortOrder
}

// Audience and threshold rules are admission policy, not purchased benefits.
// Their current values are deliberately revalidated below rather than treated
// as a stale-product conflict. Everything frozen in the product snapshot must
// still match before an order can be created.
func subscriptionPlanSnapshotEntitlementsEqual(left, right map[string]any) bool {
	return reflect.DeepEqual(paymentSnapshotEntitlements(left), paymentSnapshotEntitlements(right))
}

func sameOptionalCheckoutPrice(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func paymentOrderSnapshotGroup(current *dbent.Group) *Group {
	if current == nil {
		return nil
	}
	return &Group{
		ID:              current.ID,
		Name:            current.Name,
		DailyLimitUSD:   current.DailyLimitUsd,
		WeeklyLimitUSD:  current.WeeklyLimitUsd,
		MonthlyLimitUSD: current.MonthlyLimitUsd,
	}
}

// revalidateRechargeOrderInTx locks and rereads the persisted preset row for
// every ordinary balance checkout. The outer configuration may have been in
// custom mode when the request was validated, then switched to fixed mode
// before this transaction began. Compare modes first so that change cannot
// fall through as an unrestricted custom top-up; fixed tiers then revalidate
// their current product and purchase rules under the same lock.
func (s *PaymentService) revalidateRechargeOrderInTx(ctx context.Context, tx *dbent.Tx, req CreateOrderRequest, cfg *PaymentConfig) error {
	if tx == nil || cfg == nil {
		return ErrPurchaseRulesUnavailable
	}
	if cfg.RechargeOptionsInvalid {
		return ErrPurchaseRulesUnavailable
	}

	// An absent row is the legacy custom-amount default. Materialize that exact
	// default inside this transaction before locking it: PostgreSQL cannot lock a
	// missing row, while this INSERT ... ON CONFLICT DO NOTHING holds the unique
	// key against a concurrent first admin insert until the checkout commits.
	if err := tx.Setting.Create().
		SetKey(SettingRechargeOptions).
		SetValue("[]").
		OnConflictColumns(setting.FieldKey).
		DoNothing().
		Exec(ctx); err != nil && !isSQLNoRowsError(err) {
		return fmt.Errorf("ensure recharge options setting: %w", err)
	}

	settingQuery := tx.Setting.Query().Where(setting.KeyEQ(SettingRechargeOptions))
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		settingQuery.ForUpdate()
	}
	stored, err := settingQuery.Only(ctx)
	if err != nil {
		return fmt.Errorf("lock recharge options: %w", err)
	}
	currentOptions, intact := normalizeRechargeOptions(stored.Value)
	if !intact {
		return ErrPurchaseRulesUnavailable
	}
	outerMode := RechargeModeForConfig(cfg)
	currentMode := RechargeModeForConfig(&PaymentConfig{RechargeOptions: currentOptions})
	if outerMode != currentMode {
		return infraerrors.Conflict("RECHARGE_OPTION_CHANGED", "recharge option changed; request current checkout information")
	}
	if currentMode != RechargeModeFixed {
		return nil
	}

	expected, expectedFixedTier := rechargeOptionForAmount(cfg.RechargeOptions, req.Amount)
	if !expectedFixedTier {
		return infraerrors.Conflict("RECHARGE_OPTION_CHANGED", "recharge option changed; request current checkout information")
	}
	current, found := rechargeOptionForAmount(currentOptions, req.Amount)
	if !found {
		return infraerrors.Conflict("RECHARGE_OPTION_CHANGED", "recharge option changed; request current checkout information")
	}
	if !rechargeOptionProductEqual(expected, current) {
		return infraerrors.Conflict("RECHARGE_OPTION_CHANGED", "recharge option changed; request current checkout information")
	}
	return validatePurchaseRulesForUser(ctx, tx.Client(), req.UserID, current.PurchaseRules)
}

func rechargeOptionProductEqual(expected, current RechargeOption) bool {
	return expected.Amount == current.Amount &&
		expected.OriginalPrice == current.OriginalPrice &&
		expected.Label == current.Label &&
		expected.Description == current.Description &&
		expected.BalanceBonus == current.BalanceBonus &&
		expected.EstimatedRateMultiplier == current.EstimatedRateMultiplier &&
		expected.EstimatedTokens == current.EstimatedTokens &&
		expected.Concurrency == current.Concurrency &&
		expected.Recommended == current.Recommended &&
		expected.SortOrder == current.SortOrder &&
		expected.Enabled == current.Enabled
}

// createOrderDatabaseOptions applies only to private creation paths. It keeps
// the normal customer order builder's data shape and transaction boundaries
// intact while allowing an already-persisted owner-test ledger to use a
// deterministic out_trade_no.
type createOrderDatabaseOptions struct {
	fixedOutTradeNo   string
	providerSnapshot  map[string]any
	lockOwnerTestUser bool
}

func (s *PaymentService) createOrderInTx(ctx context.Context, req CreateOrderRequest, user *User, plan *dbent.SubscriptionPlan, cfg *PaymentConfig, orderAmount, limitAmount, feeRate, payAmount float64, sel *payment.InstanceSelection) (*dbent.PaymentOrder, error) {
	order, _, err := s.createOrderInTxWithOptions(ctx, req, user, plan, cfg, orderAmount, limitAmount, feeRate, payAmount, sel, nil)
	return order, err
}

// createOrderInTxWithOptions writes all locally authoritative order data,
// including an owner-test idempotency ledger, before any provider call. A
// deterministic owner-test key is checked while holding the actor row lock so
// replays bypass ordinary pending-order checks.
func (s *PaymentService) createOrderInTxWithOptions(ctx context.Context, req CreateOrderRequest, userRecord *User, plan *dbent.SubscriptionPlan, cfg *PaymentConfig, orderAmount, limitAmount, feeRate, payAmount float64, sel *payment.InstanceSelection, opts *createOrderDatabaseOptions) (*dbent.PaymentOrder, bool, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if opts != nil && opts.lockOwnerTestUser {
		lockedUserQuery := tx.User.Query().Where(user.IDEQ(req.UserID))
		if tx.Client().Driver().Dialect() == dialect.Postgres {
			lockedUserQuery.ForUpdate()
		}
		lockedUser, lockErr := lockedUserQuery.Only(ctx)
		if lockErr != nil {
			return nil, false, fmt.Errorf("lock owner test user: %w", lockErr)
		}
		if lockedUser.Status != StatusActive {
			return nil, false, infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
		}
		if lockedUser.Role != RoleAdmin {
			return nil, false, infraerrors.Forbidden("OWNER_TEST_ADMIN_REQUIRED", "an active administrator is required for an owner test order")
		}
		if opts.fixedOutTradeNo == "" {
			return nil, false, fmt.Errorf("owner test order missing deterministic out_trade_no")
		}
		existing, lookupErr := tx.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(opts.fixedOutTradeNo)).Only(ctx)
		if lookupErr == nil {
			return existing, false, nil
		}
		if !dbent.IsNotFound(lookupErr) {
			return nil, false, fmt.Errorf("lookup owner test order: %w", lookupErr)
		}
	}
	var lockedSnapshotGroup *Group
	if opts == nil || !opts.lockOwnerTestUser {
		if plan != nil && req.OrderType == payment.OrderTypeSubscription {
			lockedPlan, lockedGroup, lockErr := s.revalidateSubscriptionOrderInTx(ctx, tx, req, plan)
			if lockErr != nil {
				return nil, false, lockErr
			}
			if orderAmount != lockedPlan.Price || limitAmount != lockedPlan.Price {
				return nil, false, infraerrors.Conflict("PLAN_CHANGED", "plan changed; request current checkout information")
			}
			// Keep the existing snapshot contract for narrow internal callers that
			// deliberately omit GroupRepository: policy still revalidates against
			// the locked Ent row, but those callers did not previously promise group
			// display evidence in their immutable product snapshots.
			if s.groupRepo != nil {
				lockedSnapshotGroup = lockedGroup
			}
		} else if plan == nil && req.OrderType == payment.OrderTypeBalance {
			if lockErr := s.revalidateRechargeOrderInTx(ctx, tx, req, cfg); lockErr != nil {
				return nil, false, lockErr
			}
		}
	}
	if err := s.checkPendingLimit(ctx, tx, req.UserID, cfg.MaxPendingOrders); err != nil {
		return nil, false, err
	}
	if err := s.checkDailyLimit(ctx, tx, req.UserID, limitAmount, cfg.DailyLimit); err != nil {
		return nil, false, err
	}
	tm := cfg.OrderTimeoutMin
	if tm <= 0 {
		tm = defaultOrderTimeoutMin
	}
	exp := time.Now().Add(time.Duration(tm) * time.Minute)
	outTradeNo := ""
	if opts != nil && opts.fixedOutTradeNo != "" {
		outTradeNo = opts.fixedOutTradeNo
	} else {
		outTradeNo, err = s.allocateOutTradeNo(ctx, tx)
		if err != nil {
			return nil, false, err
		}
	}
	providerSnapshot := buildPaymentOrderProviderSnapshot(sel, req)
	if opts != nil && opts.providerSnapshot != nil {
		providerSnapshot = clonePaymentOrderSnapshot(opts.providerSnapshot)
	}
	selectedInstanceID := ""
	selectedProviderKey := ""
	if sel != nil {
		selectedInstanceID = strings.TrimSpace(sel.InstanceID)
		selectedProviderKey = strings.TrimSpace(sel.ProviderKey)
	}
	b := tx.PaymentOrder.Create().
		SetUserID(req.UserID).
		SetUserEmail(userRecord.Email).
		SetUserName(userRecord.Username).
		SetNillableUserNotes(psNilIfEmpty(userRecord.Notes)).
		SetAmount(orderAmount).
		SetPayAmount(payAmount).
		SetFeeRate(feeRate).
		SetRechargeCode("").
		SetOutTradeNo(outTradeNo).
		SetPaymentType(req.PaymentType).
		SetPaymentTradeNo("").
		SetOrderType(req.OrderType).
		SetStatus(OrderStatusPending).
		SetExpiresAt(exp).
		SetClientIP(req.ClientIP).
		SetSrcHost(req.SrcHost)
	if req.SrcURL != "" {
		b.SetSrcURL(req.SrcURL)
	}
	if selectedInstanceID != "" {
		b.SetProviderInstanceID(selectedInstanceID)
	}
	if selectedProviderKey != "" {
		b.SetProviderKey(selectedProviderKey)
	}
	if providerSnapshot != nil {
		b.SetProviderSnapshot(providerSnapshot)
	}
	if plan != nil {
		subscriptionDays := psComputeValidityDays(plan.ValidityDays, plan.ValidityUnit)
		b.SetPlanID(plan.ID).SetSubscriptionGroupID(plan.GroupID).SetSubscriptionDays(subscriptionDays)
		// The plan is already the immutable product selected for this order. A
		// small current-group read records the promised limits alongside it, so a
		// later group edit cannot rewrite what this purchase represented.
		snapshotGroup := lockedSnapshotGroup
		if snapshotGroup == nil && s.groupRepo != nil {
			if group, groupErr := s.groupRepo.GetByID(ctx, plan.GroupID); groupErr == nil {
				snapshotGroup = group
			}
		}
		b.SetProductSnapshot(buildPaymentProductSnapshotWithGroup(plan, orderAmount, payAmount, subscriptionDays, snapshotGroup))
	} else if req.OrderType == payment.OrderTypeBalance {
		// Resolve balance entitlements only from the server-side configured
		// preset. The client-provided amount can select a preset, but can never
		// provide the concurrency target itself.
		b.SetProductSnapshot(buildPaymentBalanceProductSnapshot(req.Amount, orderAmount, payAmount, cfg.RechargeOptions))
	}
	order, err := b.Save(ctx)
	if err != nil {
		if opts != nil && opts.fixedOutTradeNo != "" && dbent.IsConstraintError(err) {
			return nil, false, errOwnerTestOrderInsertConflict
		}
		return nil, false, fmt.Errorf("create order: %w", err)
	}
	code := fmt.Sprintf("PAY-%d-%d", order.ID, time.Now().UnixNano()%100000)
	order, err = tx.PaymentOrder.UpdateOneID(order.ID).SetRechargeCode(code).Save(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("set recharge code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit order transaction: %w", err)
	}
	return order, true, nil
}

func buildPaymentBalanceProductSnapshot(requestAmount, creditedAmount, payAmount float64, options []RechargeOption) map[string]any {
	option, _ := rechargeOptionForAmount(options, requestAmount)
	return map[string]any{
		"kind":                      "balance",
		"label":                     option.Label,
		"description":               option.Description,
		"request_amount":            requestAmount,
		"list_price":                option.OriginalPrice,
		"price":                     requestAmount,
		"discount_percent":          rechargeOptionDiscountPercent(option),
		"credited_amount":           creditedAmount,
		"pay_amount":                payAmount,
		"estimated_rate_multiplier": option.EstimatedRateMultiplier,
		"estimated_tokens":          option.EstimatedTokens,
		"entitlements": map[string]any{
			"balance_bonus": option.BalanceBonus,
			"concurrency":   option.Concurrency,
		},
	}
}

func buildPaymentProductSnapshot(plan *dbent.SubscriptionPlan, orderAmount, payAmount float64, subscriptionDays int) map[string]any {
	return buildPaymentProductSnapshotWithGroup(plan, orderAmount, payAmount, subscriptionDays, nil)
}

// buildPaymentProductSnapshotWithGroup freezes the purchasable plan and the
// limited group evidence consulted while the order was created. It does not
// pull mutable entitlement state at read time.
func buildPaymentProductSnapshotWithGroup(plan *dbent.SubscriptionPlan, orderAmount, payAmount float64, subscriptionDays int, group *Group) map[string]any {
	if plan == nil {
		return nil
	}
	snapshot := map[string]any{
		"kind":              "subscription",
		"plan_id":           plan.ID,
		"group_id":          plan.GroupID,
		"name":              plan.Name,
		"product_name":      plan.ProductName,
		"description":       plan.Description,
		"features":          paymentSnapshotFeatures(plan.Features),
		"currency":          plan.Currency,
		"list_price":        plan.OriginalPrice,
		"price":             plan.Price,
		"order_amount":      orderAmount,
		"pay_amount":        payAmount,
		"discount_percent":  PlanDiscountPercent(plan.Price, plan.OriginalPrice),
		"validity_days":     plan.ValidityDays,
		"validity_unit":     plan.ValidityUnit,
		"subscription_days": subscriptionDays,
		"entitlements":      paymentSnapshotEntitlements(plan.Entitlements),
	}
	if group != nil {
		snapshot["group_name"] = group.Name
		if group.DailyLimitUSD != nil {
			snapshot["daily_limit_usd"] = *group.DailyLimitUSD
		}
		if group.WeeklyLimitUSD != nil {
			snapshot["weekly_limit_usd"] = *group.WeeklyLimitUSD
		}
		if group.MonthlyLimitUSD != nil {
			snapshot["monthly_limit_usd"] = *group.MonthlyLimitUSD
		}
	}
	return snapshot
}

// paymentSnapshotEntitlements preserves fulfillment benefits while omitting
// administrator-only audience metadata. A historical order needs its benefit
// snapshot, never a list of users who could have purchased it at the time.
func paymentSnapshotEntitlements(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	copy := make(map[string]any, len(raw))
	for key, value := range raw {
		copy[key] = value
	}
	delete(copy, "purchase_rules")
	delete(copy, "reset_card_purchase_rules")
	return copy
}

func paymentSnapshotFeatures(raw string) []string {
	var features []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &features); err != nil {
		return nil
	}
	out := make([]string, 0, len(features))
	for _, feature := range features {
		feature = strings.TrimSpace(feature)
		if feature != "" {
			out = append(out, feature)
		}
	}
	return out
}

func (s *PaymentService) allocateOutTradeNo(ctx context.Context, tx *dbent.Tx) (string, error) {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		candidate := generateOutTradeNo()
		exists, err := tx.PaymentOrder.Query().Where(paymentorder.OutTradeNo(candidate)).Exist(ctx)
		if err != nil {
			return "", fmt.Errorf("check out_trade_no uniqueness: %w", err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("generate unique out_trade_no: exhausted %d attempts", maxAttempts)
}

func (s *PaymentService) checkPendingLimit(ctx context.Context, tx *dbent.Tx, userID int64, max int) error {
	if max <= 0 {
		max = defaultMaxPendingOrders
	}
	c, err := tx.PaymentOrder.Query().Where(paymentorder.UserIDEQ(userID), paymentorder.StatusEQ(OrderStatusPending)).Count(ctx)
	if err != nil {
		return fmt.Errorf("count pending orders: %w", err)
	}
	if c >= max {
		return infraerrors.TooManyRequests("TOO_MANY_PENDING", "too_many_pending").
			WithMetadata(map[string]string{"max": strconv.Itoa(max)})
	}
	return nil
}

func buildPaymentOrderProviderSnapshot(sel *payment.InstanceSelection, req CreateOrderRequest) map[string]any {
	if sel == nil {
		return nil
	}

	snapshot := map[string]any{}
	snapshot["schema_version"] = 2

	instanceID := strings.TrimSpace(sel.InstanceID)
	if instanceID != "" {
		snapshot["provider_instance_id"] = instanceID
	}

	providerKey := strings.TrimSpace(sel.ProviderKey)
	if providerKey != "" {
		snapshot["provider_key"] = providerKey
	}

	paymentMode := strings.TrimSpace(sel.PaymentMode)
	if paymentMode != "" {
		snapshot["payment_mode"] = paymentMode
	}

	if providerKey == payment.TypeWxpay {
		if merchantAppID := paymentOrderSnapshotWxpayAppID(sel, req); merchantAppID != "" {
			snapshot["merchant_app_id"] = merchantAppID
		}
		if merchantID := strings.TrimSpace(sel.Config["mchId"]); merchantID != "" {
			snapshot["merchant_id"] = merchantID
		}
		snapshot["currency"] = payment.DefaultPaymentCurrency
	}
	if providerKey == payment.TypeAlipay {
		if merchantAppID := strings.TrimSpace(sel.Config["appId"]); merchantAppID != "" {
			snapshot["merchant_app_id"] = merchantAppID
		}
	}
	if providerKey == payment.TypeEasyPay {
		if merchantID := strings.TrimSpace(sel.Config["pid"]); merchantID != "" {
			snapshot["merchant_id"] = merchantID
		}
	}
	if providerKey == payment.TypeStripe {
		snapshot["currency"] = paymentProviderConfigCurrency(providerKey, sel.Config)
	}
	if providerKey == payment.TypeAirwallex {
		if accountID := strings.TrimSpace(sel.Config["accountId"]); accountID != "" {
			snapshot["merchant_id"] = accountID
		}
		snapshot["currency"] = paymentProviderConfigCurrency(providerKey, sel.Config)
	}

	if len(snapshot) == 1 {
		return nil
	}
	if providerKey == payment.TypeUnifiedPay {
		snapshot["currency"] = payment.DefaultPaymentCurrency
		for _, key := range []string{"environment", "organization_id", "product_id", "app_id"} {
			if value := strings.TrimSpace(sel.Config[key]); value != "" {
				snapshot[key] = value
			}
		}
	}
	return snapshot
}

func paymentOrderSnapshotWxpayAppID(sel *payment.InstanceSelection, req CreateOrderRequest) string {
	if sel == nil || strings.TrimSpace(sel.ProviderKey) != payment.TypeWxpay {
		return ""
	}
	if strings.TrimSpace(req.OpenID) != "" {
		return strings.TrimSpace(provider.ResolveWxpayJSAPIAppID(sel.Config))
	}
	return strings.TrimSpace(sel.Config["appId"])
}

func (s *PaymentService) checkDailyLimit(ctx context.Context, tx *dbent.Tx, userID int64, amount, limit float64) error {
	if limit <= 0 {
		return nil
	}
	ts := psStartOfDayUTC(time.Now())
	orders, err := tx.PaymentOrder.Query().Where(paymentorder.UserIDEQ(userID), paymentorder.StatusIn(OrderStatusPaid, OrderStatusRecharging, OrderStatusCompleted), paymentorder.PaidAtGTE(ts)).All(ctx)
	if err != nil {
		return fmt.Errorf("query daily usage: %w", err)
	}
	var used float64
	for _, o := range orders {
		if o.OrderType == payment.OrderTypeBalance {
			used += o.PayAmount
			continue
		}
		used += o.Amount
	}
	if used+amount > limit {
		return infraerrors.TooManyRequests("DAILY_LIMIT_EXCEEDED", "daily_limit_exceeded").
			WithMetadata(map[string]string{"remaining": fmt.Sprintf("%.2f", math.Max(0, limit-used))})
	}
	return nil
}

func (s *PaymentService) selectCreateOrderInstance(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig, payAmount float64) (*payment.InstanceSelection, error) {
	providerKey, err := s.createOrderProviderKey(ctx, req.PaymentType)
	if err != nil {
		return nil, err
	}
	if providerKey == payment.TypeUnifiedPay {
		return s.unifiedPayment.Selection(req.PaymentType), nil
	}
	selectCtx, err := s.prepareCreateOrderSelectionContext(ctx, req)
	if err != nil {
		return nil, err
	}
	sel, err := s.loadBalancer.SelectInstance(selectCtx, providerKey, req.PaymentType, payment.Strategy(cfg.LoadBalanceStrategy), payAmount)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_GATEWAY_ERROR", "method_not_configured").
			WithMetadata(map[string]string{"payment_type": req.PaymentType})
	}
	if sel == nil {
		return nil, infraerrors.TooManyRequests("NO_AVAILABLE_INSTANCE", "no_available_instance")
	}
	return sel, nil
}

func (s *PaymentService) prepareCreateOrderSelectionContext(ctx context.Context, req CreateOrderRequest) (context.Context, error) {
	if !requestNeedsWeChatJSAPICompatibility(req) {
		return ctx, nil
	}
	if !s.usesOfficialWxpayVisibleMethod(ctx) {
		return ctx, nil
	}
	expectedAppID, _, err := s.getWeChatPaymentOAuthCredential(ctx)
	if err != nil {
		return nil, err
	}
	return payment.WithWxpayJSAPIAppID(ctx, expectedAppID), nil
}

func requestNeedsWeChatJSAPICompatibility(req CreateOrderRequest) bool {
	if payment.GetBasePaymentType(req.PaymentType) != payment.TypeWxpay {
		return false
	}
	return req.IsWeChatBrowser || strings.TrimSpace(req.OpenID) != ""
}

func (s *PaymentService) usesOfficialWxpayVisibleMethod(ctx context.Context) bool {
	if s == nil || s.configService == nil {
		return false
	}
	inst, err := s.configService.resolveEnabledVisibleMethodInstance(ctx, payment.TypeWxpay)
	if err != nil {
		return false
	}
	if inst == nil {
		return false
	}
	return inst.ProviderKey == payment.TypeWxpay
}

func (s *PaymentService) invokeProvider(ctx context.Context, order *dbent.PaymentOrder, req CreateOrderRequest, cfg *PaymentConfig, limitAmount float64, payAmountStr string, payAmount float64, plan *dbent.SubscriptionPlan, sel *payment.InstanceSelection) (*CreateOrderResponse, error) {
	var prov payment.Provider
	var err error
	if sel != nil && sel.ProviderKey == payment.TypeUnifiedPay {
		if s.unifiedPayment == nil || !s.unifiedPayment.Enabled() {
			return nil, unifiedpay.ErrDisabled
		}
		prov = s.unifiedPayment
	} else {
		prov, err = provider.CreateProvider(sel.ProviderKey, sel.InstanceID, sel.Config)
	}
	if err != nil {
		slog.Error("[PaymentService] CreateProvider failed", "provider", sel.ProviderKey, "instance", sel.InstanceID, "error", err)
		// If the provider returned a structured ApplicationError (e.g. WXPAY_CONFIG_MISSING_KEY),
		// pass it through with provider context added to metadata. Otherwise wrap as PAYMENT_PROVIDER_MISCONFIGURED.
		if appErr := new(infraerrors.ApplicationError); errors.As(err, &appErr) {
			md := map[string]string{"provider": sel.ProviderKey, "instance_id": sel.InstanceID}
			for k, v := range appErr.Metadata {
				md[k] = v
			}
			return nil, appErr.WithMetadata(md)
		}
		return nil, infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_MISCONFIGURED", "provider_misconfigured").
			WithMetadata(map[string]string{"provider": sel.ProviderKey, "instance_id": sel.InstanceID})
	}
	subject := s.buildPaymentSubject(plan, limitAmount, cfg, sel)
	outTradeNo := order.OutTradeNo
	canonicalReturnURL, err := CanonicalizeReturnURL(req.ReturnURL, req.SrcHost, req.SrcURL)
	if err != nil {
		return nil, err
	}
	resumeToken := ""
	if resume := s.paymentResume(); resume != nil {
		if canonicalReturnURL != "" && resume.isSigningConfigured() {
			resumeToken, err = resume.CreateToken(ResumeTokenClaims{
				OrderID:            order.ID,
				UserID:             order.UserID,
				ProviderInstanceID: sel.InstanceID,
				ProviderKey:        sel.ProviderKey,
				PaymentType:        req.PaymentType,
				CanonicalReturnURL: canonicalReturnURL,
			})
			if err != nil {
				return nil, fmt.Errorf("create payment resume token: %w", err)
			}
		}
	}
	providerReturnURL := canonicalReturnURL
	if sel.ProviderKey == payment.TypeUnifiedPay {
		if providerReturnURL == "" || providerReturnURL != s.unifiedPayment.ReturnURL() {
			return nil, infraerrors.BadRequest("INVALID_RETURN_URL", "return URL must match the configured Sub2 payment result page")
		}
	} else {
		providerReturnURL, err = buildPaymentReturnURL(canonicalReturnURL, order.ID, outTradeNo, resumeToken)
		if err != nil {
			return nil, err
		}
	}
	providerReq := buildProviderCreatePaymentRequest(CreateOrderRequest{
		PaymentType: req.PaymentType,
		OrderType:   req.OrderType,
		OpenID:      req.OpenID,
		ClientIP:    req.ClientIP,
		IsMobile:    req.IsMobile,
		ReturnURL:   providerReturnURL,
	}, sel, outTradeNo, payAmountStr, subject)
	providerReq.ExpiresInSeconds = int(math.Ceil(order.ExpiresAt.Sub(order.CreatedAt).Seconds()))
	providerReq.AlipayMobilePrecreate = shouldUseAlipayMobilePrecreate(req, cfg, sel)
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	pr, err := prov.CreatePayment(ctx, providerReq)
	finishProviderCall()
	if err != nil {
		slog.Error("[PaymentService] CreatePayment failed", "provider", sel.ProviderKey, "instance", sel.InstanceID, "error", err)
		if sel.ProviderKey == payment.TypeUnifiedPay && errors.Is(err, unifiedpay.ErrCreateStateUnconfirmed) {
			return nil, err
		}
		if appErr := new(infraerrors.ApplicationError); errors.As(err, &appErr) {
			return nil, appErr
		}
		return nil, classifyCreatePaymentError(req, sel.ProviderKey, err)
	}
	sanitizeCreatePaymentResponseDetails(pr)
	providerSnapshot := order.ProviderSnapshot
	if sel.ProviderKey == payment.TypeUnifiedPay {
		if providerSnapshot == nil {
			providerSnapshot = make(map[string]any)
		}
		providerSnapshot["payment_order_id"] = strings.TrimSpace(pr.TradeNo)
	}
	update := s.entClient.PaymentOrder.UpdateOneID(order.ID).
		SetNillablePaymentTradeNo(psNilIfEmpty(pr.TradeNo)).
		SetNillablePayURL(psNilIfEmpty(pr.PayURL)).
		SetNillableQrCode(psNilIfEmpty(pr.QRCode)).
		SetNillableProviderInstanceID(psNilIfEmpty(sel.InstanceID)).
		SetNillableProviderKey(psNilIfEmpty(sel.ProviderKey))
	if providerSnapshot != nil {
		update.SetProviderSnapshot(providerSnapshot)
	}
	_, err = update.Save(ctx)
	if err != nil {
		if sel.ProviderKey == payment.TypeUnifiedPay {
			return nil, fmt.Errorf("%w: persist remote order binding", unifiedpay.ErrCreateStateUnconfirmed)
		}
		return nil, fmt.Errorf("update order with payment details: %w", err)
	}
	s.writeAuditLog(ctx, order.ID, "ORDER_CREATED", fmt.Sprintf("user:%d", req.UserID), map[string]any{
		"paymentAmount":  req.Amount,
		"creditedAmount": order.Amount,
		"payAmount":      order.PayAmount,
		"paymentType":    req.PaymentType,
		"orderType":      req.OrderType,
		"paymentSource":  NormalizePaymentSource(req.PaymentSource),
	})
	resultType := pr.ResultType
	if resultType == "" {
		resultType = payment.CreatePaymentResultOrderCreated
	}
	resp := buildCreateOrderResponse(order, req, payAmount, sel, pr, resultType)
	resp.ResumeToken = resumeToken
	resp.AlipayMobilePrecreateDeepLink = providerReq.AlipayMobilePrecreate && strings.TrimSpace(pr.QRCode) != ""
	return resp, nil
}

func shouldUseAlipayMobilePrecreate(req CreateOrderRequest, cfg *PaymentConfig, sel *payment.InstanceSelection) bool {
	return cfg != nil &&
		cfg.AlipayMobilePrecreateDeepLink &&
		req.IsMobile &&
		sel != nil &&
		strings.EqualFold(strings.TrimSpace(sel.ProviderKey), payment.TypeAlipay)
}

func sanitizeCreatePaymentResponseDetails(pr *payment.CreatePaymentResponse) {
	if pr == nil {
		return
	}
	pr.TradeNo = removePostgresTextNUL(pr.TradeNo)
	pr.PayURL = removePostgresTextNUL(pr.PayURL)
	pr.QRCode = removePostgresTextNUL(pr.QRCode)
}

func removePostgresTextNUL(value string) string {
	if !strings.ContainsRune(value, 0) {
		return value
	}
	return strings.ReplaceAll(value, "\x00", "")
}

func buildProviderCreatePaymentRequest(req CreateOrderRequest, sel *payment.InstanceSelection, orderID, amount, subject string) payment.CreatePaymentRequest {
	return payment.CreatePaymentRequest{
		OrderID:            orderID,
		Amount:             amount,
		PaymentType:        req.PaymentType,
		OrderType:          req.OrderType,
		Subject:            subject,
		ReturnURL:          req.ReturnURL,
		OpenID:             strings.TrimSpace(req.OpenID),
		ClientIP:           req.ClientIP,
		IsMobile:           req.IsMobile,
		InstanceSubMethods: selectedInstanceSupportedTypes(sel),
	}
}

func selectedInstanceSupportedTypes(sel *payment.InstanceSelection) string {
	if sel == nil {
		return ""
	}
	return sel.SupportedTypes
}

func (s *PaymentService) buildPaymentSubject(plan *dbent.SubscriptionPlan, limitAmount float64, cfg *PaymentConfig, sel *payment.InstanceSelection) string {
	if plan != nil {
		productName := plan.ProductName
		if productName == "" {
			productName = "Sub2API Subscription " + plan.Name
		}
		return applyPaymentProductNameAffix(productName, cfg)
	}
	currency := payment.DefaultPaymentCurrency
	if sel != nil {
		currency = paymentProviderConfigCurrency(sel.ProviderKey, sel.Config)
	}
	amountStr := payment.FormatAmountForCurrency(limitAmount, currency)
	if hasPaymentProductNameAffix(cfg) {
		return applyPaymentProductNameAffix(amountStr, cfg)
	}
	return "Sub2API " + amountStr + " " + currency
}

func hasPaymentProductNameAffix(cfg *PaymentConfig) bool {
	if cfg == nil {
		return false
	}
	pf := strings.TrimSpace(cfg.ProductNamePrefix)
	sf := strings.TrimSpace(cfg.ProductNameSuffix)
	return pf != "" || sf != ""
}

func applyPaymentProductNameAffix(productName string, cfg *PaymentConfig) string {
	if !hasPaymentProductNameAffix(cfg) {
		return productName
	}
	pf := strings.TrimSpace(cfg.ProductNamePrefix)
	sf := strings.TrimSpace(cfg.ProductNameSuffix)
	return strings.TrimSpace(pf + " " + productName + " " + sf)
}

func (s *PaymentService) maybeBuildWeChatOAuthRequiredResponse(ctx context.Context, req CreateOrderRequest, amount, payAmount, feeRate float64) (*CreateOrderResponse, error) {
	return s.maybeBuildWeChatOAuthRequiredResponseForSelection(ctx, req, amount, payAmount, feeRate, nil)
}

func (s *PaymentService) maybeBuildWeChatOAuthRequiredResponseForSelection(ctx context.Context, req CreateOrderRequest, amount, payAmount, feeRate float64, sel *payment.InstanceSelection) (*CreateOrderResponse, error) {
	if sel != nil && sel.ProviderKey != "" && sel.ProviderKey != payment.TypeWxpay {
		return nil, nil
	}
	if strings.TrimSpace(req.OpenID) != "" || !req.IsWeChatBrowser || payment.GetBasePaymentType(req.PaymentType) != payment.TypeWxpay {
		return nil, nil
	}
	return s.buildWeChatOAuthRequiredResponse(ctx, req, amount, payAmount, feeRate)
}

func (s *PaymentService) buildWeChatOAuthRequiredResponse(ctx context.Context, req CreateOrderRequest, amount, payAmount, feeRate float64) (*CreateOrderResponse, error) {
	appID, _, err := s.getWeChatPaymentOAuthCredential(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.paymentResume().ensureSigningKey(); err != nil {
		return nil, err
	}

	authorizeURL, err := buildWeChatPaymentOAuthStartURL(req, "snsapi_base")
	if err != nil {
		return nil, err
	}

	return &CreateOrderResponse{
		Amount:      amount,
		PayAmount:   payAmount,
		FeeRate:     feeRate,
		ResultType:  payment.CreatePaymentResultOAuthRequired,
		PaymentType: req.PaymentType,
		OAuth: &payment.WechatOAuthInfo{
			AuthorizeURL: authorizeURL,
			AppID:        appID,
			Scope:        "snsapi_base",
			RedirectURL:  "/auth/wechat/payment/callback",
		},
	}, nil
}

func (s *PaymentService) validateSelectedCreateOrderInstance(ctx context.Context, req CreateOrderRequest, sel *payment.InstanceSelection) error {
	if !requiresWeChatJSAPICompatibleSelection(req, sel) {
		return nil
	}
	expectedAppID, _, err := s.getWeChatPaymentOAuthCredential(ctx)
	if err != nil {
		return err
	}
	selectedAppID := provider.ResolveWxpayJSAPIAppID(sel.Config)
	if selectedAppID == "" || selectedAppID != expectedAppID {
		return infraerrors.TooManyRequests("NO_AVAILABLE_INSTANCE", "selected payment instance is not compatible with the current WeChat OAuth app")
	}
	return nil
}

func calculateCreateOrderPayAmount(limitAmount, feeRate float64, currency string) (string, float64, error) {
	if err := validateCreateOrderAmountCurrency(limitAmount, currency); err != nil {
		return "", 0, err
	}
	payAmountStr := payment.CalculatePayAmountForCurrency(limitAmount, feeRate, currency)
	if _, err := payment.AmountToMinorUnit(payAmountStr, currency); err != nil {
		return "", 0, infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	payAmount, err := strconv.ParseFloat(payAmountStr, 64)
	if err != nil {
		return "", 0, infraerrors.BadRequest("INVALID_AMOUNT", "invalid payment amount").
			WithMetadata(map[string]string{"currency": currency})
	}
	return payAmountStr, payAmount, nil
}

func calculateCreateOrderPayAmountForOrderType(limitAmount, feeRate float64, currency, orderType string, usdToCnyRate float64) (string, float64, error) {
	paymentAmount := limitAmount
	if orderType == payment.OrderTypeSubscription {
		paymentAmount = calculateSubscriptionGatewayBaseAmount(limitAmount, usdToCnyRate, currency)
	}
	return calculateCreateOrderPayAmount(paymentAmount, feeRate, currency)
}

// calculateSubscriptionGatewayBaseAmount 计算订阅订单的网关扣款基数。
// 换算是显式 opt-in：仅当管理员配置了订阅汇率（rate > 0，1 USD = rate CNY）
// 且网关币种为 CNY 时，按 price × rate 换算；未配置时保持 price 直付的存量行为。
func calculateSubscriptionGatewayBaseAmount(amount, usdToCnyRate float64, currency string) float64 {
	rate := normalizeSubscriptionUSDToCNYRate(usdToCnyRate)
	if rate <= 0 || currency != payment.DefaultPaymentCurrency {
		return amount
	}
	return decimal.NewFromFloat(amount).
		Mul(decimal.NewFromFloat(rate)).
		Round(int32(payment.CurrencyMaxFractionDigits(currency))).
		InexactFloat64()
}

func validateCreateOrderAmountCurrency(amount float64, currency string) error {
	amountStr := strconv.FormatFloat(amount, 'f', -1, 64)
	if _, err := payment.AmountToMinorUnit(amountStr, currency); err != nil {
		return infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	return nil
}

func validateSelectedCreateOrderAmountCurrency(payAmount string, sel *payment.InstanceSelection) error {
	if sel == nil {
		return nil
	}
	currency := paymentProviderConfigCurrency(sel.ProviderKey, sel.Config)
	if _, err := payment.AmountToMinorUnit(payAmount, currency); err != nil {
		return infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	return nil
}

func requiresWeChatJSAPICompatibleSelection(req CreateOrderRequest, sel *payment.InstanceSelection) bool {
	if sel == nil || sel.ProviderKey != payment.TypeWxpay || payment.GetBasePaymentType(req.PaymentType) != payment.TypeWxpay {
		return false
	}
	return req.IsWeChatBrowser || strings.TrimSpace(req.OpenID) != ""
}

func (s *PaymentService) getWeChatPaymentOAuthCredential(ctx context.Context) (string, string, error) {
	if s == nil || s.configService == nil || s.configService.settingRepo == nil {
		return "", "", infraerrors.ServiceUnavailable(
			"WECHAT_PAYMENT_MP_NOT_CONFIGURED",
			"wechat in-app payment requires a complete WeChat MP OAuth credential",
		)
	}
	cfg, err := (&SettingService{settingRepo: s.configService.settingRepo}).GetWeChatConnectOAuthConfig(ctx)
	appID := strings.TrimSpace(cfg.AppIDForMode("mp"))
	appSecret := strings.TrimSpace(cfg.AppSecretForMode("mp"))
	if err != nil || !cfg.SupportsMode("mp") || appID == "" || appSecret == "" {
		return "", "", infraerrors.ServiceUnavailable(
			"WECHAT_PAYMENT_MP_NOT_CONFIGURED",
			"wechat in-app payment requires a complete WeChat MP OAuth credential",
		)
	}
	return appID, appSecret, nil
}

func classifyCreatePaymentError(req CreateOrderRequest, providerKey string, err error) error {
	if err == nil {
		return nil
	}
	if providerKey == payment.TypeWxpay &&
		payment.GetBasePaymentType(req.PaymentType) == payment.TypeWxpay &&
		strings.Contains(err.Error(), "wxpay h5 payments are not authorized for this merchant") {
		return infraerrors.ServiceUnavailable(
			"WECHAT_H5_NOT_AUTHORIZED",
			"wechat h5 payment is not available for this merchant",
		).WithMetadata(map[string]string{
			"action": "open_in_wechat_or_scan_qr",
		})
	}
	return infraerrors.ServiceUnavailable("PAYMENT_GATEWAY_ERROR", fmt.Sprintf("payment gateway error: %s", err.Error()))
}

func buildCreateOrderResponse(order *dbent.PaymentOrder, req CreateOrderRequest, payAmount float64, sel *payment.InstanceSelection, pr *payment.CreatePaymentResponse, resultType payment.CreatePaymentResultType) *CreateOrderResponse {
	return &CreateOrderResponse{
		OrderID:      order.ID,
		Amount:       order.Amount,
		PayAmount:    payAmount,
		FeeRate:      order.FeeRate,
		Status:       OrderStatusPending,
		ResultType:   resultType,
		PaymentType:  req.PaymentType,
		OutTradeNo:   order.OutTradeNo,
		PayURL:       pr.PayURL,
		QRCode:       pr.QRCode,
		ClientSecret: pr.ClientSecret,
		IntentID:     pr.IntentID,
		Currency:     pr.Currency,
		CountryCode:  pr.CountryCode,
		PaymentEnv:   pr.PaymentEnv,
		OAuth:        pr.OAuth,
		JSAPI:        pr.JSAPI,
		JSAPIPayload: pr.JSAPI,
		ExpiresAt:    order.ExpiresAt,
		PaymentMode:  sel.PaymentMode,
	}
}

func buildWeChatPaymentOAuthStartURL(req CreateOrderRequest, scope string) (string, error) {
	u, err := url.Parse("/api/v1/auth/oauth/wechat/payment/start")
	if err != nil {
		return "", fmt.Errorf("build wechat payment oauth start url: %w", err)
	}
	q := u.Query()
	q.Set("payment_type", strings.TrimSpace(req.PaymentType))
	if req.Amount > 0 {
		q.Set("amount", strconv.FormatFloat(req.Amount, 'f', -1, 64))
	}
	if orderType := strings.TrimSpace(req.OrderType); orderType != "" {
		q.Set("order_type", orderType)
	}
	if req.PlanID > 0 {
		q.Set("plan_id", strconv.FormatInt(req.PlanID, 10))
	}
	if scope = strings.TrimSpace(scope); scope != "" {
		q.Set("scope", scope)
	}
	if redirectTo := paymentRedirectPathFromURL(req.SrcURL); redirectTo != "" {
		q.Set("redirect", redirectTo)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func paymentRedirectPathFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "/purchase"
	}
	if strings.HasPrefix(rawURL, "/") && !strings.HasPrefix(rawURL, "//") {
		return normalizePaymentRedirectPath(rawURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "/purchase"
	}
	path := strings.TrimSpace(u.EscapedPath())
	if path == "" {
		path = strings.TrimSpace(u.Path)
	}
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "/purchase"
	}
	if strings.TrimSpace(u.RawQuery) != "" {
		path += "?" + u.RawQuery
	}
	return normalizePaymentRedirectPath(path)
}

func normalizePaymentRedirectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/purchase"
	}
	if path == "/payment" {
		return "/purchase"
	}
	if strings.HasPrefix(path, "/payment?") {
		return "/purchase" + strings.TrimPrefix(path, "/payment")
	}
	return path
}

// --- Order Queries ---

func (s *PaymentService) GetOrder(ctx context.Context, orderID, userID int64) (*dbent.PaymentOrder, error) {
	o, err := s.entClient.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID)).WithInvoiceRequest().Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	return o, nil
}

func (s *PaymentService) GetOrderByID(ctx context.Context, orderID int64) (*dbent.PaymentOrder, error) {
	o, err := s.entClient.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID)).WithInvoiceRequest().Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	return o, nil
}

func (s *PaymentService) GetUserOrders(ctx context.Context, userID int64, p OrderListParams) ([]*dbent.PaymentOrder, int, error) {
	q := s.entClient.PaymentOrder.Query().Where(paymentorder.UserIDEQ(userID))
	if err := applyPaymentOrderListFilters(q, p, false); err != nil {
		return nil, 0, err
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count user orders: %w", err)
	}
	ps, pg := applyPagination(p.PageSize, p.Page)
	orders, err := q.WithInvoiceRequest().Order(dbent.Desc(paymentorder.FieldCreatedAt)).Limit(ps).Offset((pg - 1) * ps).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("query user orders: %w", err)
	}
	return orders, total, nil
}

// AdminListOrders returns a paginated list of orders. If userID > 0, filters by user.
func (s *PaymentService) AdminListOrders(ctx context.Context, userID int64, p OrderListParams) ([]*dbent.PaymentOrder, int, error) {
	q := s.entClient.PaymentOrder.Query()
	if userID > 0 {
		q = q.Where(paymentorder.UserIDEQ(userID))
	}
	if err := applyPaymentOrderListFilters(q, p, true); err != nil {
		return nil, 0, err
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count admin orders: %w", err)
	}
	ps, pg := applyPagination(p.PageSize, p.Page)
	orders, err := q.WithInvoiceRequest().Order(dbent.Desc(paymentorder.FieldCreatedAt)).Limit(ps).Offset((pg - 1) * ps).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("query admin orders: %w", err)
	}
	return orders, total, nil
}

// applyPaymentOrderListFilters adds all predicates before Count and pagination.
// User routes call it with adminOnly=false, so only the user-approved invoice
// and fulfillment filters apply there; admin routes additionally get payment
// and customer-email delivery filters.
func applyPaymentOrderListFilters(q *dbent.PaymentOrderQuery, p OrderListParams, adminOnly bool) error {
	if q == nil {
		return errors.New("nil payment order query")
	}
	if p.Status != "" {
		q = q.Where(paymentorder.StatusEQ(p.Status))
	}
	if p.OrderType != "" {
		q = q.Where(paymentorder.OrderTypeEQ(p.OrderType))
	}
	if p.PaymentType != "" {
		q = q.Where(paymentorder.PaymentTypeEQ(p.PaymentType))
	}
	invoiceStatus, err := normalizeInvoiceStatusFilter(p.InvoiceStatus)
	if err != nil {
		return err
	}
	switch invoiceStatus {
	case InvoiceStatusFilterNone:
		q.Where(paymentorder.Not(paymentorder.HasInvoiceRequest()))
	case InvoiceStatusFilterAny:
		q.Where(paymentorder.HasInvoiceRequest())
	case InvoiceStatusPending, InvoiceStatusProcessing, InvoiceStatusIssued, InvoiceStatusRejected:
		q.Where(paymentorder.HasInvoiceRequestWith(paymentinvoicerequest.StatusEQ(invoiceStatus)))
	}
	fulfillmentStatus, err := normalizeInvoiceFulfillmentStatusFilter(p.FulfillmentStatus)
	if err != nil {
		return err
	}
	if fulfillmentStatus != "" {
		q.Where(paymentOrderFulfillmentStatusPredicate(fulfillmentStatus))
	}
	if adminOnly {
		paymentStatus, err := normalizeInvoicePaymentStatusFilter(p.PaymentStatus)
		if err != nil {
			return err
		}
		switch paymentStatus {
		case InvoicePaymentStatusPaid:
			q.Where(paymentorder.PaidAtNotNil())
		case InvoicePaymentStatusUnpaid:
			q.Where(paymentorder.PaidAtIsNil())
		}
		emailStatus, err := normalizeInvoiceEmailStatusFilter(p.InvoiceEmailStatus)
		if err != nil {
			return err
		}
		if emailStatus != "" {
			q.Where(paymentorder.HasInvoiceRequestWith(paymentinvoicerequest.EmailDeliveryStatusEQ(emailStatus)))
		}
	}
	if adminOnly && strings.TrimSpace(p.Keyword) != "" {
		keyword := strings.TrimSpace(p.Keyword)
		if orderID, ok := exactPaymentOrderIDKeyword(keyword); ok {
			q.Where(paymentorder.IDEQ(orderID))
			return nil
		}
		q.Where(paymentorder.Or(
			paymentorder.OutTradeNoContainsFold(keyword),
			paymentorder.UserEmailContainsFold(keyword),
			paymentorder.UserNameContainsFold(keyword),
		))
	}
	return nil
}

func paymentOrderFulfillmentStatusPredicate(status string) predicate.PaymentOrder {
	switch status {
	case InvoiceFulfillmentStatusFulfilled:
		return paymentorder.CompletedAtNotNil()
	case InvoiceFulfillmentStatusNotStarted:
		return paymentorder.PaidAtIsNil()
	case InvoiceFulfillmentStatusFailed:
		return paymentorder.And(paymentorder.PaidAtNotNil(), paymentorder.CompletedAtIsNil(), paymentorder.StatusEQ(OrderStatusFailed))
	case InvoiceFulfillmentStatusManualReview:
		return paymentorder.And(paymentorder.PaidAtNotNil(), paymentorder.CompletedAtIsNil(), paymentOrderHasUnifiedRefundReview())
	case InvoiceFulfillmentStatusPending:
		return paymentorder.And(
			paymentorder.PaidAtNotNil(),
			paymentorder.CompletedAtIsNil(),
			paymentorder.StatusNEQ(OrderStatusFailed),
			paymentorder.Not(paymentOrderHasUnifiedRefundReview()),
		)
	default:
		return nil
	}
}

func exactPaymentOrderIDKeyword(keyword string) (int64, bool) {
	if !strings.HasPrefix(keyword, "#") || len(keyword) == 1 {
		return 0, false
	}
	for _, r := range keyword[1:] {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(keyword[1:], 10, 64)
	return id, err == nil && id > 0
}
