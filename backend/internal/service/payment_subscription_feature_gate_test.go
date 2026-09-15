package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPaymentConfigSubscriptionEnabledDefaultsOnAndHonorsExplicitFalse(t *testing.T) {
	t.Parallel()

	svc := &PaymentConfigService{}
	require.True(t, svc.parsePaymentConfig(map[string]string{}).SubscriptionEnabled)
	require.True(t, svc.parsePaymentConfig(map[string]string{SettingKeySubscriptionEnabled: "true"}).SubscriptionEnabled)
	require.False(t, svc.parsePaymentConfig(map[string]string{SettingKeySubscriptionEnabled: "false"}).SubscriptionEnabled)
}

func TestCreateOrderRejectsSubscriptionWhenSiteModeDisablesSubscriptions(t *testing.T) {
	t.Parallel()

	repo := &paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentEnabled:         "true",
		SettingKeySubscriptionEnabled: "false",
	}}
	svc := &PaymentService{configService: NewPaymentConfigService(nil, repo, nil)}

	_, err := svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID:      7,
		Amount:      0.01,
		PaymentType: payment.TypeWxpay,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      3,
	})
	require.Equal(t, "PLAN_NOT_AVAILABLE", infraerrors.Reason(err))
}
