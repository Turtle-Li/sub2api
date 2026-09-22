//go:build unit

package provider

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/smartwalle/alipay/v3"
	"github.com/stretchr/testify/require"
)

func TestIsTradeNotExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error returns false",
			err:  nil,
			want: false,
		},
		{
			name: "error containing ACQ.TRADE_NOT_EXIST returns true",
			err:  errors.New("alipay: sub_code=ACQ.TRADE_NOT_EXIST, sub_msg=交易不存在"),
			want: true,
		},
		{
			name: "error not containing the code returns false",
			err:  errors.New("alipay: sub_code=ACQ.SYSTEM_ERROR, sub_msg=系统错误"),
			want: false,
		},
		{
			name: "error with only partial match returns false",
			err:  errors.New("ACQ.TRADE_NOT"),
			want: false,
		},
		{
			name: "error with exact constant value returns true",
			err:  errors.New(alipayErrTradeNotExist),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isTradeNotExist(tt.err)
			if got != tt.want {
				t.Errorf("isTradeNotExist(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestAlipayRefundReferencePrefersStableProductReference(t *testing.T) {
	t.Parallel()

	request := payment.RefundRequest{
		OrderID:         "sub2-order-123",
		Amount:          "12.34",
		RefundReference: "sub2-cancel-123",
	}
	require.Equal(t, "sub2-cancel-123", alipayRefundReference(request))

	request.RefundReference = ""
	require.Contains(t, alipayRefundReference(request), "sub2-order-123-refund-")
}

func TestNewAlipay(t *testing.T) {
	t.Parallel()

	validConfig := map[string]string{
		"appId":      "2021001234567890",
		"privateKey": "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC...",
	}

	// helper to clone and override config fields
	withOverride := func(overrides map[string]string) map[string]string {
		cfg := make(map[string]string, len(validConfig))
		for k, v := range validConfig {
			cfg[k] = v
		}
		for k, v := range overrides {
			cfg[k] = v
		}
		return cfg
	}

	tests := []struct {
		name      string
		config    map[string]string
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "valid config succeeds",
			config:  validConfig,
			wantErr: false,
		},
		{
			name:      "missing appId",
			config:    withOverride(map[string]string{"appId": ""}),
			wantErr:   true,
			errSubstr: "appId",
		},
		{
			name:      "missing privateKey",
			config:    withOverride(map[string]string{"privateKey": ""}),
			wantErr:   true,
			errSubstr: "privateKey",
		},
		{
			name:      "nil config map returns error for appId",
			config:    map[string]string{},
			wantErr:   true,
			errSubstr: "appId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewAlipay("test-instance", tt.config)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("expected non-nil Alipay instance")
			}
			if got.instanceID != "test-instance" {
				t.Errorf("instanceID = %q, want %q", got.instanceID, "test-instance")
			}
		})
	}
}

func TestAlipayTimeoutExpressUsesSafeWholeMinutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		seconds int
		want    string
	}{
		{seconds: 0, want: ""},
		{seconds: 59, want: ""},
		{seconds: 60, want: "1m"},
		{seconds: 359, want: "5m"},
		{seconds: 15 * 24 * 60 * 60, want: "21600m"},
		{seconds: 15*24*60*60 + 60, want: ""},
	}
	for _, tt := range tests {
		if got := alipayTimeoutExpress(tt.seconds); got != tt.want {
			t.Fatalf("alipayTimeoutExpress(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestValidateAlipayQRCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "production payload", value: "https://qr.alipay.com/precreate-token?scene=bar_code", want: true},
		{name: "sandbox payload", value: "https://qr.alipaydev.com/precreate-token", want: true},
		{name: "wrong host", value: "https://openapi.alipay.com/gateway.do?pay", want: false},
		{name: "lookalike host", value: "https://qr.alipay.com.example.com/precreate-token", want: false},
		{name: "insecure scheme", value: "http://qr.alipay.com/precreate-token", want: false},
		{name: "explicit port", value: "https://qr.alipay.com:443/precreate-token", want: false},
		{name: "userinfo", value: "https://user@qr.alipay.com/precreate-token", want: false},
		{name: "fragment", value: "https://qr.alipay.com/precreate-token#fragment", want: false},
		{name: "root path", value: "https://qr.alipay.com/", want: false},
		{name: "surrounding whitespace", value: " https://qr.alipay.com/precreate-token", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidateAlipayQRCode(tt.value); got != tt.want {
				t.Errorf("ValidateAlipayQRCode(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestValidateAlipayPayURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "production gateway", value: "https://openapi.alipay.com/gateway.do?page-pay", want: true},
		{name: "sandbox gateway", value: "https://openapi-sandbox.dl.alipaydev.com/gateway.do?page-pay", want: true},
		{name: "wrong path", value: "https://openapi.alipay.com/checkout/page-pay", want: false},
		{name: "wrong host", value: "https://pay.totools.cn/checkout/page-pay", want: false},
		{name: "insecure scheme", value: "http://openapi.alipay.com/gateway.do?page-pay", want: false},
		{name: "explicit port", value: "https://openapi.alipay.com:443/gateway.do?page-pay", want: false},
		{name: "fragment", value: "https://openapi.alipay.com/gateway.do?page-pay#fragment", want: false},
		{name: "missing query", value: "https://openapi.alipay.com/gateway.do", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidateAlipayPayURL(tt.value); got != tt.want {
				t.Errorf("ValidateAlipayPayURL(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestCreateTradeUsesPagePayForDesktop(t *testing.T) {
	origPreCreate := alipayTradePreCreate
	origPagePay := alipayTradePagePay
	origWapPay := alipayTradeWapPay
	origTradeQuery := alipayTradeQuery
	t.Cleanup(func() {
		alipayTradePreCreate = origPreCreate
		alipayTradePagePay = origPagePay
		alipayTradeWapPay = origWapPay
		alipayTradeQuery = origTradeQuery
	})

	preCreateCalls := 0
	pagePayCalls := 0
	wapPayCalls := 0
	alipayTradePreCreate = func(ctx context.Context, client *alipay.Client, param alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
		preCreateCalls++
		if param.TimeoutExpress != "10m" {
			t.Fatalf("precreate timeout_express = %q, want 10m", param.TimeoutExpress)
		}
		return nil, errors.New("merchant does not have FACE_TO_FACE_PAYMENT")
	}
	alipayTradeQuery = func(context.Context, *alipay.Client, alipay.TradeQuery) (*alipay.TradeQueryRsp, error) {
		return nil, errors.New(alipayErrTradeNotExist)
	}
	alipayTradePagePay = func(client *alipay.Client, param alipay.TradePagePay) (*url.URL, error) {
		pagePayCalls++
		if param.OutTradeNo != "sub2_100" {
			t.Fatalf("out_trade_no = %q, want %q", param.OutTradeNo, "sub2_100")
		}
		if param.NotifyURL != "https://merchant.example.com/api/v1/payment/webhook/alipay" {
			t.Fatalf("notify_url = %q", param.NotifyURL)
		}
		if param.TimeoutExpress != "10m" {
			t.Fatalf("page pay timeout_express = %q, want 10m", param.TimeoutExpress)
		}
		if param.QRPayMode != "4" {
			t.Fatalf("qr_pay_mode = %q, want 4", param.QRPayMode)
		}
		if param.QRCodeWidth != "220" {
			t.Fatalf("qrcode_width = %q, want 220", param.QRCodeWidth)
		}
		return url.Parse("https://openapi.alipay.com/gateway.do?page-pay")
	}
	alipayTradeWapPay = func(client *alipay.Client, param alipay.TradeWapPay) (*url.URL, error) {
		wapPayCalls++
		return url.Parse("https://openapi.alipay.com/gateway.do?wap-pay")
	}

	provider := &Alipay{}
	resp, err := provider.createDesktopTrade(context.Background(), &alipay.Client{}, payment.CreatePaymentRequest{
		OrderID:          "sub2_100",
		Amount:           "88.00",
		Subject:          "Balance recharge",
		ExpiresInSeconds: 600,
	}, "https://merchant.example.com/api/v1/payment/webhook/alipay", "https://merchant.example.com/payment/result")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preCreateCalls != 1 {
		t.Fatalf("precreate calls = %d, want 1", preCreateCalls)
	}
	if pagePayCalls != 1 {
		t.Fatalf("page pay calls = %d, want 1", pagePayCalls)
	}
	if wapPayCalls != 0 {
		t.Fatalf("wap pay calls = %d, want 0", wapPayCalls)
	}
	if resp.PayURL == "" {
		t.Fatal("expected pay_url for desktop page pay")
	}
	if resp.CheckoutFrameURL != "https://openapi.alipay.com/gateway.do?page-pay" {
		t.Fatalf("checkout_frame_url = %q, want page pay checkout URL", resp.CheckoutFrameURL)
	}
	// page.pay returns a checkout page URL, not a scannable QR payload —
	// it must never be exposed via QRCode (the frontend would render an
	// unscannable image from it).
	if resp.QRCode != "" {
		t.Fatalf("qr_code = %q, want empty for page pay", resp.QRCode)
	}
}

func TestCreateTradeDoesNotFallbackWhenPrecreateStateIsUnconfirmed(t *testing.T) {
	origPreCreate := alipayTradePreCreate
	origPagePay := alipayTradePagePay
	origTradeQuery := alipayTradeQuery
	t.Cleanup(func() {
		alipayTradePreCreate = origPreCreate
		alipayTradePagePay = origPagePay
		alipayTradeQuery = origTradeQuery
	})

	alipayTradePreCreate = func(context.Context, *alipay.Client, alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
		return nil, context.DeadlineExceeded
	}
	alipayTradeQuery = func(context.Context, *alipay.Client, alipay.TradeQuery) (*alipay.TradeQueryRsp, error) {
		return nil, context.DeadlineExceeded
	}
	pagePayCalls := 0
	alipayTradePagePay = func(*alipay.Client, alipay.TradePagePay) (*url.URL, error) {
		pagePayCalls++
		return url.Parse("https://openapi.alipay.com/gateway.do?page-pay")
	}

	provider := &Alipay{}
	_, err := provider.createDesktopTrade(context.Background(), &alipay.Client{}, payment.CreatePaymentRequest{
		OrderID: "sub2_uncertain_precreate",
		Amount:  "40.00",
		Subject: "Subscription reset card",
	}, "https://merchant.example.com/api/v1/payment/webhook/alipay", "https://merchant.example.com/payment/result")
	if err == nil || !strings.Contains(err.Error(), "state unconfirmed") {
		t.Fatalf("error = %v, want state unconfirmed", err)
	}
	if pagePayCalls != 0 {
		t.Fatalf("page pay calls = %d, want 0", pagePayCalls)
	}
}

// When the provider instance is configured with paymentMode == "redirect",
// the desktop flow must skip precreate and go straight to page.pay.
func TestCreateTradeRedirectModeSkipsPrecreate(t *testing.T) {
	origPreCreate := alipayTradePreCreate
	origPagePay := alipayTradePagePay
	t.Cleanup(func() {
		alipayTradePreCreate = origPreCreate
		alipayTradePagePay = origPagePay
	})

	preCreateCalls := 0
	pagePayCalls := 0
	alipayTradePreCreate = func(ctx context.Context, client *alipay.Client, param alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
		preCreateCalls++
		return &alipay.TradePreCreateRsp{
			Error:  alipay.Error{Code: alipay.CodeSuccess},
			QRCode: "https://qr.alipay.com/precreate-token",
		}, nil
	}
	alipayTradePagePay = func(client *alipay.Client, param alipay.TradePagePay) (*url.URL, error) {
		pagePayCalls++
		if param.ProductCode != alipayProductCodePagePay {
			t.Fatalf("product_code = %q, want %q", param.ProductCode, alipayProductCodePagePay)
		}
		if param.QRPayMode != "4" {
			t.Fatalf("qr_pay_mode = %q, want 4", param.QRPayMode)
		}
		if param.QRCodeWidth != "220" {
			t.Fatalf("qrcode_width = %q, want 220", param.QRCodeWidth)
		}
		return url.Parse("https://openapi.alipay.com/gateway.do?page-pay")
	}

	provider := &Alipay{
		config: map[string]string{"paymentMode": "redirect"},
	}
	resp, err := provider.createDesktopTrade(context.Background(), &alipay.Client{}, payment.CreatePaymentRequest{
		OrderID: "sub2_103",
		Amount:  "12.00",
		Subject: "Balance recharge",
	}, "https://merchant.example.com/api/v1/payment/webhook/alipay", "https://merchant.example.com/payment/result")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preCreateCalls != 0 {
		t.Fatalf("precreate calls = %d, want 0 (redirect mode must skip precreate)", preCreateCalls)
	}
	if pagePayCalls != 1 {
		t.Fatalf("page pay calls = %d, want 1", pagePayCalls)
	}
	if resp.PayURL == "" {
		t.Fatal("expected pay_url for redirect mode")
	}
	if resp.CheckoutFrameURL != "https://openapi.alipay.com/gateway.do?page-pay" {
		t.Fatalf("checkout_frame_url = %q, want page pay checkout URL", resp.CheckoutFrameURL)
	}
	if resp.QRCode != "" {
		t.Fatalf("qr_code = %q, want empty for redirect mode", resp.QRCode)
	}
}

func TestCreateTradeUsesWapPayForMobile(t *testing.T) {
	origWapPay := alipayTradeWapPay
	t.Cleanup(func() {
		alipayTradeWapPay = origWapPay
	})

	wapPayCalls := 0
	alipayTradeWapPay = func(client *alipay.Client, param alipay.TradeWapPay) (*url.URL, error) {
		wapPayCalls++
		if param.ReturnURL != "https://merchant.example.com/payment/result" {
			t.Fatalf("return_url = %q", param.ReturnURL)
		}
		if param.TimeoutExpress != "9m" {
			t.Fatalf("timeout_express = %q, want 9m", param.TimeoutExpress)
		}
		return url.Parse("https://openapi.alipay.com/gateway.do?wap-pay")
	}

	provider := &Alipay{}
	resp, err := provider.createWapTrade(&alipay.Client{}, payment.CreatePaymentRequest{
		OrderID:          "sub2_101",
		Amount:           "18.00",
		Subject:          "Balance recharge",
		IsMobile:         true,
		ExpiresInSeconds: 599,
	}, "https://merchant.example.com/api/v1/payment/webhook/alipay", "https://merchant.example.com/payment/result")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wapPayCalls != 1 {
		t.Fatalf("wap pay calls = %d, want 1", wapPayCalls)
	}
	if resp.PayURL == "" {
		t.Fatal("expected pay_url for mobile wap pay")
	}
}

func TestCreatePaymentUsesPrecreateForMobileWhenEnabled(t *testing.T) {
	origPreCreate := alipayTradePreCreate
	origWapPay := alipayTradeWapPay
	t.Cleanup(func() {
		alipayTradePreCreate = origPreCreate
		alipayTradeWapPay = origWapPay
	})

	precreateCalls := 0
	wapPayCalls := 0
	alipayTradePreCreate = func(_ context.Context, _ *alipay.Client, param alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
		precreateCalls++
		if param.OutTradeNo != "sub2_mobile_precreate" {
			t.Fatalf("out_trade_no = %q", param.OutTradeNo)
		}
		if param.ProductCode != alipayProductCodePreCreate {
			t.Fatalf("product_code = %q, want %q", param.ProductCode, alipayProductCodePreCreate)
		}
		return &alipay.TradePreCreateRsp{
			Error:  alipay.Error{Code: alipay.CodeSuccess},
			QRCode: "https://qr.alipay.com/mobile-dynamic-token",
		}, nil
	}
	alipayTradeWapPay = func(_ *alipay.Client, _ alipay.TradeWapPay) (*url.URL, error) {
		wapPayCalls++
		return url.Parse("https://openapi.alipay.com/gateway.do?wap-pay")
	}

	provider := &Alipay{client: &alipay.Client{}, config: map[string]string{}}
	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:               "sub2_mobile_precreate",
		Amount:                "28.00",
		Subject:               "Balance recharge",
		IsMobile:              true,
		AlipayMobilePrecreate: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if precreateCalls != 1 || wapPayCalls != 0 {
		t.Fatalf("precreate calls = %d, wap calls = %d; want 1, 0", precreateCalls, wapPayCalls)
	}
	if resp.QRCode != "https://qr.alipay.com/mobile-dynamic-token" || resp.PayURL != "" {
		t.Fatalf("unexpected response: qr_code=%q pay_url=%q", resp.QRCode, resp.PayURL)
	}
}

func TestCreatePaymentKeepsWapPayForMobileWhenPrecreateDisabled(t *testing.T) {
	origPreCreate := alipayTradePreCreate
	origWapPay := alipayTradeWapPay
	t.Cleanup(func() {
		alipayTradePreCreate = origPreCreate
		alipayTradeWapPay = origWapPay
	})

	precreateCalls := 0
	wapPayCalls := 0
	alipayTradePreCreate = func(_ context.Context, _ *alipay.Client, _ alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
		precreateCalls++
		return nil, errors.New("unexpected precreate call")
	}
	alipayTradeWapPay = func(_ *alipay.Client, _ alipay.TradeWapPay) (*url.URL, error) {
		wapPayCalls++
		return url.Parse("https://openapi.alipay.com/gateway.do?wap-pay")
	}

	provider := &Alipay{client: &alipay.Client{}, config: map[string]string{}}
	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:  "sub2_mobile_wap",
		Amount:   "18.00",
		Subject:  "Balance recharge",
		IsMobile: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if precreateCalls != 0 || wapPayCalls != 1 {
		t.Fatalf("precreate calls = %d, wap calls = %d; want 0, 1", precreateCalls, wapPayCalls)
	}
	if resp.PayURL == "" || resp.QRCode != "" {
		t.Fatalf("unexpected response: qr_code=%q pay_url=%q", resp.QRCode, resp.PayURL)
	}
}

func TestCreateTradeUsesPrecreateForDesktopWhenAvailable(t *testing.T) {
	origPreCreate := alipayTradePreCreate
	origPagePay := alipayTradePagePay
	t.Cleanup(func() {
		alipayTradePreCreate = origPreCreate
		alipayTradePagePay = origPagePay
	})

	preCreateCalls := 0
	pagePayCalls := 0
	alipayTradePreCreate = func(ctx context.Context, client *alipay.Client, param alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
		preCreateCalls++
		if param.ProductCode != alipayProductCodePreCreate {
			t.Fatalf("product_code = %q, want %q", param.ProductCode, alipayProductCodePreCreate)
		}
		return &alipay.TradePreCreateRsp{
			Error:  alipay.Error{Code: alipay.CodeSuccess},
			QRCode: "https://qr.alipay.com/precreate-token",
		}, nil
	}
	alipayTradePagePay = func(client *alipay.Client, param alipay.TradePagePay) (*url.URL, error) {
		pagePayCalls++
		return url.Parse("https://openapi.alipay.com/gateway.do?page-pay")
	}

	provider := &Alipay{}
	resp, err := provider.createDesktopTrade(context.Background(), &alipay.Client{}, payment.CreatePaymentRequest{
		OrderID: "sub2_102",
		Amount:  "66.00",
		Subject: "Balance recharge",
	}, "https://merchant.example.com/api/v1/payment/webhook/alipay", "https://merchant.example.com/payment/result")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preCreateCalls != 1 {
		t.Fatalf("precreate calls = %d, want 1", preCreateCalls)
	}
	if pagePayCalls != 0 {
		t.Fatalf("page pay calls = %d, want 0", pagePayCalls)
	}
	if resp.QRCode != "https://qr.alipay.com/precreate-token" {
		t.Fatalf("qr_code = %q", resp.QRCode)
	}
	if resp.PayURL != "" {
		t.Fatalf("pay_url = %q, want empty for precreate", resp.PayURL)
	}
}

func TestAlipayMerchantIdentityMetadata(t *testing.T) {
	t.Parallel()

	provider := &Alipay{
		config: map[string]string{
			"appId": "2021001234567890",
		},
	}

	metadata := provider.MerchantIdentityMetadata()
	if metadata["app_id"] != "2021001234567890" {
		t.Fatalf("app_id = %q, want %q", metadata["app_id"], "2021001234567890")
	}
}

func TestParseAlipayAmount(t *testing.T) {
	t.Parallel()

	amount, err := parseAlipayAmount("", "88.00", "77.00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amount != 88 {
		t.Fatalf("amount = %v, want 88", amount)
	}

	if _, err := parseAlipayAmount("", "not-a-number"); err == nil {
		t.Fatal("expected error when no valid amount field exists")
	}
}
