package service

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/stretchr/testify/require"
)

func newUnifiedServiceTestGateway(t *testing.T, baseURL string) *unifiedpay.Gateway {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	gateway, err := unifiedpay.New(unifiedpay.Config{
		Enabled: true, BaseURL: baseURL, Environment: unifiedpay.EnvironmentSandbox,
		OrganizationID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ProductID:      "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", AppID: "app.sub2.sandbox",
		RequestKeyID: "sub2.request.sandbox.v1", RequestPrivateKey: key,
		WebhookPublicKeys: map[string]ed25519.PublicKey{"sub2.webhook.sandbox.v1": key.Public().(ed25519.PublicKey)},
		ReturnURL:         "http://127.0.0.1:3000/payment/result",
	})
	require.NoError(t, err)
	return gateway
}

type unifiedRoutingErrorRepo struct{ paymentConfigSettingRepoStub }

func (*unifiedRoutingErrorRepo) GetValue(context.Context, string) (string, error) {
	return "", errors.New("settings unavailable")
}

func TestUnifiedRoutingSettingsFailureStopsCreate(t *testing.T) {
	svc := &PaymentService{configService: &PaymentConfigService{settingRepo: &unifiedRoutingErrorRepo{}}}
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, "https://pay.example.test"), nil)
	selection, err := svc.selectCreateOrderInstance(context.Background(), CreateOrderRequest{PaymentType: payment.TypeAlipay}, &PaymentConfig{}, 1)
	require.Error(t, err)
	require.Nil(t, selection)
}

type unifiedRoutingRecordingBalancer struct {
	payment.LoadBalancer
	providerKey string
}

func (b *unifiedRoutingRecordingBalancer) SelectInstance(_ context.Context, key string, _ payment.PaymentType, _ payment.Strategy, _ float64) (*payment.InstanceSelection, error) {
	b.providerKey = key
	return &payment.InstanceSelection{ProviderKey: key}, nil
}

func TestUnifiedRoutingPinsExplicitLegacyProvider(t *testing.T) {
	for _, test := range []struct{ method, source, want string }{
		{payment.TypeAlipay, VisibleMethodSourceOfficialAlipay, payment.TypeAlipay},
		{payment.TypeAlipay, VisibleMethodSourceEasyPayAlipay, payment.TypeEasyPay},
		{payment.TypeWxpay, VisibleMethodSourceOfficialWechat, payment.TypeWxpay},
	} {
		t.Run(test.source, func(t *testing.T) {
			repo := &paymentConfigSettingRepoStub{values: map[string]string{visibleMethodSourceSettingKey(test.method): test.source}}
			balancer := &unifiedRoutingRecordingBalancer{}
			svc := &PaymentService{configService: &PaymentConfigService{settingRepo: repo}, loadBalancer: balancer}
			svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, "https://pay.example.test"), nil)
			_, err := svc.selectCreateOrderInstance(context.Background(), CreateOrderRequest{PaymentType: test.method}, &PaymentConfig{}, 1)
			require.NoError(t, err)
			require.Equal(t, test.want, balancer.providerKey)
		})
	}
}

func TestUnifiedRoutingDoesNotReplaceMissingExplicitProvider(t *testing.T) {
	svc := &PaymentConfigService{settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentVisibleMethodAlipaySource: VisibleMethodSourceOfficialAlipay,
	}}}
	_, err := svc.resolveVisibleMethodProviderKey(context.Background(), payment.TypeAlipay, []*dbent.PaymentProviderInstance{
		{ProviderKey: payment.TypeEasyPay, SupportedTypes: payment.TypeAlipay, Enabled: true},
	})
	require.Error(t, err)
}
