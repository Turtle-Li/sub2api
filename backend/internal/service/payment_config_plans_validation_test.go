//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type subscriptionCheckoutGroupRepoStub struct {
	GroupRepository
	group *Group
}

func (s subscriptionCheckoutGroupRepoStub) GetByID(context.Context, int64) (*Group, error) {
	return s.group, nil
}

func TestListPlansForSaleUsesNewSubscriptionCheckoutPolicy(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}

	createGroup := func(name, platform, status, subscriptionType string) int64 {
		candidate, err := client.Group.Create().
			SetName(name).
			SetPlatform(platform).
			SetStatus(status).
			SetSubscriptionType(subscriptionType).
			Save(ctx)
		require.NoError(t, err)
		return int64(candidate.ID)
	}
	createPlan := func(name string, groupID int64, forSale bool) int64 {
		plan, err := client.SubscriptionPlan.Create().
			SetName(name).
			SetGroupID(groupID).
			SetPrice(120).
			SetValidityDays(1).
			SetValidityUnit("month").
			SetForSale(forSale).
			Save(ctx)
		require.NoError(t, err)
		return int64(plan.ID)
	}

	openAIPlanID := createPlan("openai", createGroup("openai", PlatformOpenAI, StatusActive, SubscriptionTypeSubscription), true)
	createPlan("anthropic", createGroup("anthropic", PlatformAnthropic, StatusActive, SubscriptionTypeSubscription), true)
	createPlan("disabled", createGroup("disabled", PlatformOpenAI, StatusDisabled, SubscriptionTypeSubscription), true)
	createPlan("standard", createGroup("standard", PlatformOpenAI, StatusActive, SubscriptionTypeStandard), true)
	createPlan("not-for-sale", createGroup("not-for-sale", PlatformOpenAI, StatusActive, SubscriptionTypeSubscription), false)

	plans, err := svc.ListPlansForSale(ctx)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Equal(t, openAIPlanID, int64(plans[0].ID))

	allPlans, err := svc.ListPlans(ctx)
	require.NoError(t, err)
	require.Len(t, allPlans, 5, "admin plan management must retain the full catalog")
}

func TestValidateSubOrderRejectsNonOpenAINewCheckout(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	plan, err := client.SubscriptionPlan.Create().
		SetName("test plan").
		SetGroupID(11).
		SetPrice(120).
		SetValidityDays(1).
		SetValidityUnit("month").
		SetForSale(true).
		Save(ctx)
	require.NoError(t, err)

	configService := &PaymentConfigService{entClient: client}
	request := CreateOrderRequest{PlanID: int64(plan.ID)}

	openAISvc := &PaymentService{
		configService: configService,
		groupRepo: subscriptionCheckoutGroupRepoStub{group: &Group{
			ID:               11,
			Platform:         PlatformOpenAI,
			Status:           StatusActive,
			SubscriptionType: SubscriptionTypeSubscription,
		}},
	}
	resolved, err := openAISvc.validateSubOrder(ctx, request)
	require.NoError(t, err)
	require.Equal(t, plan.ID, resolved.ID)

	nonOpenAISvc := &PaymentService{
		configService: configService,
		groupRepo: subscriptionCheckoutGroupRepoStub{group: &Group{
			ID:               11,
			Platform:         PlatformAnthropic,
			Status:           StatusActive,
			SubscriptionType: SubscriptionTypeSubscription,
		}},
	}
	_, err = nonOpenAISvc.validateSubOrder(ctx, request)
	require.Error(t, err)
	require.ErrorContains(t, err, "not available for new subscription checkout")
}

func TestCreateOrderInTxRevalidatesSubscriptionPlatformUnderWriteBoundary(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("locked-platform@example.test").
		SetPasswordHash("hash").
		SetUsername("locked-platform").
		Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().
		SetName("locked platform group").
		SetPlatform(PlatformOpenAI).
		SetStatus(StatusActive).
		SetSubscriptionType(SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().
		SetGroupID(group.ID).
		SetName("locked platform plan").
		SetPrice(120).
		SetValidityDays(1).
		SetValidityUnit("month").
		SetForSale(true).
		Save(ctx)
	require.NoError(t, err)

	svc := &PaymentService{entClient: client}
	request := CreateOrderRequest{UserID: user.ID, PaymentType: payment.TypeAlipay, OrderType: payment.OrderTypeSubscription}
	actor := &User{ID: user.ID, Email: user.Email, Username: user.Username}
	_, err = svc.createOrderInTx(ctx, request, actor, plan, &PaymentConfig{MaxPendingOrders: 3, OrderTimeoutMin: 30}, plan.Price, plan.Price, 0, plan.Price, nil)
	require.NoError(t, err, "the unchanged OpenAI policy is accepted under the write transaction")

	// This mutation represents a concurrent administrator change after the
	// outer checkout read. The transaction-bound reload must reject it before
	// another PaymentOrder is persisted.
	_, err = client.Group.UpdateOneID(group.ID).SetPlatform(PlatformAnthropic).Save(ctx)
	require.NoError(t, err)
	before, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	_, err = svc.createOrderInTx(ctx, request, actor, plan, &PaymentConfig{MaxPendingOrders: 3, OrderTimeoutMin: 30}, plan.Price, plan.Price, 0, plan.Price, nil)
	require.Error(t, err)
	require.Equal(t, "PLAN_NOT_AVAILABLE", infraerrors.Reason(err))
	after, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestValidatePlanRequired_AllValid(t *testing.T) {
	err := validatePlanRequired("Pro", 1, 9.99, 30, "days", nil)
	require.NoError(t, err)
}

func TestValidatePlanRequired_EmptyName(t *testing.T) {
	err := validatePlanRequired("", 1, 9.99, 30, "days", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "plan name")
}

func TestValidatePlanRequired_WhitespaceName(t *testing.T) {
	err := validatePlanRequired("   ", 1, 9.99, 30, "days", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "plan name")
}

func TestValidatePlanRequired_ZeroGroupID(t *testing.T) {
	err := validatePlanRequired("Pro", 0, 9.99, 30, "days", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "group")
}

func TestValidatePlanRequired_NegativeGroupID(t *testing.T) {
	err := validatePlanRequired("Pro", -1, 9.99, 30, "days", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "group")
}

func TestValidatePlanRequired_ZeroPrice(t *testing.T) {
	err := validatePlanRequired("Pro", 1, 0, 30, "days", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "price")
}

func TestValidatePlanRequired_NegativePrice(t *testing.T) {
	err := validatePlanRequired("Pro", 1, -5, 30, "days", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "price")
}

func TestValidatePlanRequired_ZeroValidityDays(t *testing.T) {
	err := validatePlanRequired("Pro", 1, 9.99, 0, "days", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity days")
}

func TestValidatePlanRequired_NegativeValidityDays(t *testing.T) {
	err := validatePlanRequired("Pro", 1, 9.99, -7, "days", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity days")
}

func TestValidatePlanRequired_EmptyValidityUnit(t *testing.T) {
	err := validatePlanRequired("Pro", 1, 9.99, 30, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity unit")
}

func TestValidatePlanRequired_WhitespaceValidityUnit(t *testing.T) {
	err := validatePlanRequired("Pro", 1, 9.99, 30, "   ", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity unit")
}

func TestValidatePlanRequired_NameValidatedFirst(t *testing.T) {
	err := validatePlanRequired("", 0, 0, 0, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "plan name")
}

func TestValidatePlanRequired_TrimmedValidName(t *testing.T) {
	err := validatePlanRequired("  Pro  ", 1, 9.99, 30, "days", nil)
	require.NoError(t, err)
}

func TestValidatePlanRequired_NegativeOriginalPrice(t *testing.T) {
	neg := -10.0
	err := validatePlanRequired("Pro", 1, 9.99, 30, "days", &neg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "original price")
}

func TestValidatePlanRequired_ZeroOriginalPrice(t *testing.T) {
	zero := 0.0
	err := validatePlanRequired("Pro", 1, 9.99, 30, "days", &zero)
	require.NoError(t, err)
}

func TestValidatePlanRequired_ValidOriginalPrice(t *testing.T) {
	op := 19.99
	err := validatePlanRequired("Pro", 1, 9.99, 30, "days", &op)
	require.NoError(t, err)
}

// --- validatePlanPatch tests ---

func TestValidatePlanPatch_NegativeOriginalPrice(t *testing.T) {
	neg := -5.0
	err := validatePlanPatch(UpdatePlanRequest{OriginalPrice: &neg})
	require.Error(t, err)
	require.Contains(t, err.Error(), "original price")
}

func TestValidatePlanPatch_ZeroOriginalPrice(t *testing.T) {
	zero := 0.0
	err := validatePlanPatch(UpdatePlanRequest{OriginalPrice: &zero})
	require.NoError(t, err)
}

func TestValidatePlanPatch_ValidOriginalPrice(t *testing.T) {
	op := 29.99
	err := validatePlanPatch(UpdatePlanRequest{OriginalPrice: &op})
	require.NoError(t, err)
}

func TestValidatePlanPatch_NilOriginalPrice(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{OriginalPrice: nil})
	require.NoError(t, err)
}

// --- validatePlanPatch: other fields ---

func ptrStr(s string) *string     { return &s }
func ptrInt(i int) *int           { return &i }
func ptrInt64(i int64) *int64     { return &i }
func ptrFloat(f float64) *float64 { return &f }

func TestValidatePlanPatch_EmptyName(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Name: ptrStr("")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "plan name")
}

func TestValidatePlanPatch_ValidName(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Name: ptrStr("Basic")})
	require.NoError(t, err)
}

func TestValidatePlanPatch_ZeroGroupID(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{GroupID: ptrInt64(0)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "group")
}

func TestValidatePlanPatch_NegativePrice(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Price: ptrFloat(-1)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "price")
}

func TestValidatePlanPatch_ZeroPrice(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Price: ptrFloat(0)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "price")
}

func TestValidatePlanPatch_ValidPrice(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Price: ptrFloat(9.99)})
	require.NoError(t, err)
}

func TestValidatePlanPatch_ZeroValidityDays(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{ValidityDays: ptrInt(0)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity days")
}

func TestValidatePlanPatch_EmptyValidityUnit(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{ValidityUnit: ptrStr("")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity unit")
}

func TestValidatePlanPatch_ValidValidityUnit(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{ValidityUnit: ptrStr("days")})
	require.NoError(t, err)
}

func TestValidatePlanPatch_AllNil(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{})
	require.NoError(t, err)
}

// --- normalizePlanCurrency tests ---
// Empty must stay empty (not coerced to the default payment currency),
// so existing plans keep rendering without any currency label.

func TestNormalizePlanCurrency_EmptyKeepsEmpty(t *testing.T) {
	currency, err := normalizePlanCurrency("")
	require.NoError(t, err)
	require.Equal(t, "", currency)
}

func TestNormalizePlanCurrency_WhitespaceKeepsEmpty(t *testing.T) {
	currency, err := normalizePlanCurrency("   ")
	require.NoError(t, err)
	require.Equal(t, "", currency)
}

func TestNormalizePlanCurrency_LowercaseNormalized(t *testing.T) {
	currency, err := normalizePlanCurrency("nzd")
	require.NoError(t, err)
	require.Equal(t, "NZD", currency)
}

func TestNormalizePlanCurrency_ValidUppercase(t *testing.T) {
	currency, err := normalizePlanCurrency("USD")
	require.NoError(t, err)
	require.Equal(t, "USD", currency)
}

func TestNormalizePlanCurrency_TooShort(t *testing.T) {
	_, err := normalizePlanCurrency("NZ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "currency")
}

func TestNormalizePlanCurrency_TooLong(t *testing.T) {
	_, err := normalizePlanCurrency("NZDD")
	require.Error(t, err)
	require.Contains(t, err.Error(), "currency")
}

func TestNormalizePlanCurrency_NonLetter(t *testing.T) {
	_, err := normalizePlanCurrency("N2D")
	require.Error(t, err)
	require.Contains(t, err.Error(), "currency")
}
