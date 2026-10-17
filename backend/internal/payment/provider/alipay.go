package provider

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/smartwalle/alipay/v3"
)

// Alipay product codes.
const (
	alipayProductCodePreCreate  = "FACE_TO_FACE_PAYMENT"
	alipayProductCodeWapPay     = "QUICK_WAP_WAY"
	alipayProductCodePagePay    = "FAST_INSTANT_TRADE_PAY"
	alipayMaximumTimeoutMinutes = 15 * 24 * 60
)

// Alipay response constants.
const (
	alipayFundChangeYes    = "Y"
	alipayErrTradeNotExist = "ACQ.TRADE_NOT_EXIST"
	alipayRefundStatusSucc = "REFUND_SUCCESS"
	alipayRefundRequestSep = "-refund-"
	// 支付宝提示退款发起后过早查询可能查不到、之后又处理成功；超过该时长仍无记录才判定失败。
	alipayRefundQueryNotFoundGrace = 10 * time.Minute
)

var alipayQRCodeHosts = map[string]struct{}{
	"qr.alipay.com":    {},
	"qr.alipaydev.com": {},
}

var alipayGatewayHosts = map[string]struct{}{
	"openapi.alipay.com":               {},
	"openapi-sandbox.dl.alipaydev.com": {},
}

var (
	alipayTradeWapPay = func(client *alipay.Client, param alipay.TradeWapPay) (*url.URL, error) {
		return client.TradeWapPay(param)
	}
	alipayTradePreCreate = func(ctx context.Context, client *alipay.Client, param alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
		return client.TradePreCreate(ctx, param)
	}
	alipayTradeQuery = func(ctx context.Context, client *alipay.Client, param alipay.TradeQuery) (*alipay.TradeQueryRsp, error) {
		return client.TradeQuery(ctx, param)
	}
	alipayTradePagePay = func(client *alipay.Client, param alipay.TradePagePay) (*url.URL, error) {
		return client.TradePagePay(param)
	}
	alipayTradeRefund = func(ctx context.Context, client *alipay.Client, param alipay.TradeRefund) (*alipay.TradeRefundRsp, error) {
		return client.TradeRefund(ctx, param)
	}
	alipayTradeFastPayRefundQuery = func(ctx context.Context, client *alipay.Client, param alipay.TradeFastPayRefundQuery) (*alipay.TradeFastPayRefundQueryRsp, error) {
		return client.TradeFastPayRefundQuery(ctx, param)
	}
)

// Alipay implements payment.Provider and payment.CancelableProvider using the smartwalle/alipay SDK.
type Alipay struct {
	instanceID string
	config     map[string]string // appId, privateKey, publicKey (or alipayPublicKey), notifyUrl, returnUrl

	mu     sync.Mutex
	client *alipay.Client
}

// NewAlipay creates a new Alipay provider instance.
func NewAlipay(instanceID string, config map[string]string) (*Alipay, error) {
	required := []string{"appId", "privateKey"}
	for _, k := range required {
		if config[k] == "" {
			return nil, fmt.Errorf("alipay config missing required key: %s", k)
		}
	}
	return &Alipay{
		instanceID: instanceID,
		config:     config,
	}, nil
}

func (a *Alipay) getClient() (*alipay.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	client, err := alipay.New(a.config["appId"], a.config["privateKey"], true)
	if err != nil {
		return nil, fmt.Errorf("alipay init client: %w", err)
	}
	pubKey := a.config["publicKey"]
	if pubKey == "" {
		pubKey = a.config["alipayPublicKey"]
	}
	if pubKey == "" {
		return nil, fmt.Errorf("alipay config missing required key: publicKey (or alipayPublicKey)")
	}
	if err := client.LoadAliPayPublicKey(pubKey); err != nil {
		return nil, fmt.Errorf("alipay load public key: %w", err)
	}
	a.client = client
	return a.client, nil
}

func (a *Alipay) Name() string        { return "Alipay" }
func (a *Alipay) ProviderKey() string { return payment.TypeAlipay }
func (a *Alipay) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeAlipay}
}

func (a *Alipay) MerchantIdentityMetadata() map[string]string {
	if a == nil {
		return nil
	}
	appID := strings.TrimSpace(a.config["appId"])
	if appID == "" {
		return nil
	}
	return map[string]string{"app_id": appID}
}

// CreatePayment creates an Alipay payment using the following routing:
//   - Mobile (H5), default: alipay.trade.wap.pay — browser redirect into Alipay.
//   - Mobile with AlipayMobilePrecreate: alipay.trade.precreate — return the
//     dynamic QR payload so the frontend can open it through the Alipay app.
//   - Desktop, default: prefer alipay.trade.precreate (FACE_TO_FACE_PAYMENT) to
//     get a scannable QR payload. If precreate is unavailable for the merchant,
//     fall back to alipay.trade.page.pay and expose its same-order checkout URL;
//     the frontend can encode that URL in the shared QR dialog.
//   - Desktop, paymentMode == "redirect": skip precreate and go straight to
//     alipay.trade.page.pay so the frontend always opens the Alipay checkout
//     in a new tab. Use this when the merchant has not enabled FACE_TO_FACE_PAYMENT.
//
// Note: alipay.trade.page.pay returns a checkout page URL. It remains in
// PayURL and is never mislabeled as a native provider QRCode payload.
func (a *Alipay) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	notifyURL := a.config["notifyUrl"]
	if req.NotifyURL != "" {
		notifyURL = req.NotifyURL
	}
	returnURL := a.config["returnUrl"]
	if req.ReturnURL != "" {
		returnURL = req.ReturnURL
	}

	if req.IsMobile {
		if req.AlipayMobilePrecreate {
			return a.createPrecreateTrade(ctx, client, req, notifyURL)
		}
		return a.createWapTrade(client, req, notifyURL, returnURL)
	}
	return a.createDesktopTrade(ctx, client, req, notifyURL, returnURL)
}

func (a *Alipay) createWapTrade(client *alipay.Client, req payment.CreatePaymentRequest, notifyURL, returnURL string) (*payment.CreatePaymentResponse, error) {
	param := alipay.TradeWapPay{}
	param.OutTradeNo = req.OrderID
	param.TotalAmount = req.Amount
	param.Subject = req.Subject
	param.ProductCode = alipayProductCodeWapPay
	param.NotifyURL = notifyURL
	param.ReturnURL = returnURL
	param.TimeoutExpress = alipayTimeoutExpress(req.ExpiresInSeconds)

	payURL, err := alipayTradeWapPay(client, param)
	if err != nil {
		return nil, fmt.Errorf("alipay TradeWapPay: %w", err)
	}
	if payURL == nil || !ValidateAlipayPayURL(payURL.String()) {
		return nil, fmt.Errorf("alipay TradeWapPay: invalid checkout URL")
	}
	return &payment.CreatePaymentResponse{
		TradeNo: req.OrderID,
		PayURL:  payURL.String(),
	}, nil
}

func (a *Alipay) createDesktopTrade(ctx context.Context, client *alipay.Client, req payment.CreatePaymentRequest, notifyURL, returnURL string) (*payment.CreatePaymentResponse, error) {
	// Explicit redirect mode: merchant opted into "always open the Alipay
	// checkout page in a new tab" via the provider instance's payment_mode.
	// Skip precreate to avoid a wasted API call.
	if strings.EqualFold(strings.TrimSpace(a.config["paymentMode"]), "redirect") {
		return a.createPagePayTrade(client, req, notifyURL, returnURL)
	}

	resp, precreateErr := a.createPrecreateTrade(ctx, client, req, notifyURL)
	if precreateErr == nil {
		return resp, nil
	}
	// A transport or response error may occur after precreate committed. Query
	// the same merchant order before falling back to page.pay; only a verified
	// TRADE_NOT_EXIST result permits a second product flow for this order ID.
	queried, queryErr := a.queryOrderWithClient(ctx, client, req.OrderID)
	if queryErr != nil || queried == nil || queried.Metadata[payment.QueryMetadataOrderNotFound] != "true" {
		return nil, fmt.Errorf("alipay desktop precreate state unconfirmed: %w", precreateErr)
	}

	resp, pagePayErr := a.createPagePayTrade(client, req, notifyURL, returnURL)
	if pagePayErr == nil {
		return resp, nil
	}

	return nil, fmt.Errorf("alipay desktop payment failed: precreate=%v; pagepay=%w", precreateErr, pagePayErr)
}

func (a *Alipay) createPrecreateTrade(ctx context.Context, client *alipay.Client, req payment.CreatePaymentRequest, notifyURL string) (*payment.CreatePaymentResponse, error) {
	param := alipay.TradePreCreate{}
	param.OutTradeNo = req.OrderID
	param.TotalAmount = req.Amount
	param.Subject = req.Subject
	param.ProductCode = alipayProductCodePreCreate
	param.NotifyURL = notifyURL
	param.TimeoutExpress = alipayTimeoutExpress(req.ExpiresInSeconds)

	rsp, err := alipayTradePreCreate(ctx, client, param)
	if err != nil {
		return nil, fmt.Errorf("alipay TradePreCreate: %w", err)
	}
	if rsp == nil {
		return nil, fmt.Errorf("alipay TradePreCreate: empty response")
	}
	if rsp.IsFailure() {
		return nil, fmt.Errorf("alipay TradePreCreate failed: %s", rsp.Error.Error())
	}
	if !ValidateAlipayQRCode(rsp.QRCode) {
		return nil, fmt.Errorf("alipay TradePreCreate: empty qr_code")
	}

	return &payment.CreatePaymentResponse{
		TradeNo: req.OrderID,
		QRCode:  rsp.QRCode,
	}, nil
}

// ValidateAlipayQRCode validates the provider payload before it is persisted or
// exposed to a browser QR renderer.
func ValidateAlipayQRCode(raw string) bool {
	if raw == "" || raw != strings.TrimSpace(raw) || len(raw) > 16384 || strings.ContainsAny(raw, "\x00\r\n\t ") || strings.ContainsRune(raw, '#') {
		return false
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed == nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" || parsed.RawFragment != "" || parsed.EscapedPath() == "" || parsed.EscapedPath() == "/" {
		return false
	}
	rawAuthority := ""
	if schemeEnd := strings.Index(raw, "://"); schemeEnd >= 0 {
		rest := raw[schemeEnd+3:]
		if end := strings.IndexAny(rest, "/?#"); end >= 0 {
			rest = rest[:end]
		}
		rawAuthority = rest
	}
	if rawAuthority == "" || !strings.EqualFold(rawAuthority, parsed.Host) {
		return false
	}
	_, ok := alipayQRCodeHosts[strings.ToLower(parsed.Hostname())]
	return ok
}

// ValidateAlipayPayURL validates a direct Alipay page/WAP checkout URL before
// it is persisted or exposed to a browser. Unified payment checkout URLs use
// their own gateway allowlist and are validated elsewhere.
func ValidateAlipayPayURL(raw string) bool {
	if raw == "" || raw != strings.TrimSpace(raw) || len(raw) > 16384 || strings.ContainsAny(raw, "\x00\r\n\t ") || strings.ContainsRune(raw, '#') {
		return false
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed == nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" || parsed.RawFragment != "" || parsed.Path != "/gateway.do" || parsed.RawPath != "" || parsed.RawQuery == "" {
		return false
	}
	rawAuthority := ""
	if schemeEnd := strings.Index(raw, "://"); schemeEnd >= 0 {
		rest := raw[schemeEnd+3:]
		if end := strings.IndexAny(rest, "/?#"); end >= 0 {
			rest = rest[:end]
		}
		rawAuthority = rest
	}
	if rawAuthority == "" || !strings.EqualFold(rawAuthority, parsed.Host) {
		return false
	}
	_, ok := alipayGatewayHosts[strings.ToLower(parsed.Hostname())]
	return ok
}

func (a *Alipay) createPagePayTrade(client *alipay.Client, req payment.CreatePaymentRequest, notifyURL, returnURL string) (*payment.CreatePaymentResponse, error) {
	param := alipay.TradePagePay{}
	param.OutTradeNo = req.OrderID
	param.TotalAmount = req.Amount
	param.Subject = req.Subject
	param.ProductCode = alipayProductCodePagePay
	param.NotifyURL = notifyURL
	param.ReturnURL = returnURL
	param.TimeoutExpress = alipayTimeoutExpress(req.ExpiresInSeconds)
	param.QRPayMode = "4"
	param.QRCodeWidth = "220"

	payURL, err := alipayTradePagePay(client, param)
	if err != nil {
		return nil, fmt.Errorf("alipay TradePagePay: %w", err)
	}
	if payURL == nil || !ValidateAlipayPayURL(payURL.String()) {
		return nil, fmt.Errorf("alipay TradePagePay: invalid checkout URL")
	}
	return &payment.CreatePaymentResponse{
		TradeNo:          req.OrderID,
		PayURL:           payURL.String(),
		CheckoutFrameURL: payURL.String(),
	}, nil
}

func alipayTimeoutExpress(expiresInSeconds int) string {
	// Alipay accepts whole minutes from 1m through 15d. Rounding down keeps the
	// provider checkout at or inside the already-persisted local deadline.
	minutes := expiresInSeconds / int(time.Minute/time.Second)
	if minutes < 1 || minutes > alipayMaximumTimeoutMinutes {
		return ""
	}
	return strconv.Itoa(minutes) + "m"
}

// QueryOrder queries the trade status via Alipay.
func (a *Alipay) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}
	return a.queryOrderWithClient(ctx, client, tradeNo)
}

func (a *Alipay) queryOrderWithClient(ctx context.Context, client *alipay.Client, tradeNo string) (*payment.QueryOrderResponse, error) {
	result, err := alipayTradeQuery(ctx, client, alipay.TradeQuery{OutTradeNo: tradeNo})
	if err != nil {
		if isTradeNotExist(err) {
			return &payment.QueryOrderResponse{
				TradeNo: tradeNo,
				Status:  payment.ProviderStatusPending,
				Metadata: map[string]string{
					payment.QueryMetadataOrderNotFound: "true",
				},
			}, nil
		}
		return nil, fmt.Errorf("alipay TradeQuery: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("alipay TradeQuery: empty response")
	}

	status := payment.ProviderStatusPending
	switch result.TradeStatus {
	case alipay.TradeStatusSuccess, alipay.TradeStatusFinished:
		status = payment.ProviderStatusPaid
	case alipay.TradeStatusClosed:
		status = payment.ProviderStatusFailed
	}

	amount, err := strconv.ParseFloat(result.TotalAmount, 64)
	if err != nil {
		amount, err = parseAlipayAmount(
			result.TotalAmount,
			result.ReceiptAmount,
			result.BuyerPayAmount,
			result.InvoiceAmount,
		)
		if err != nil {
			return nil, fmt.Errorf("alipay parse amount: %w", err)
		}
	}

	return &payment.QueryOrderResponse{
		TradeNo:  result.TradeNo,
		Status:   status,
		Amount:   amount,
		PaidAt:   result.SendPayDate,
		Metadata: a.MerchantIdentityMetadata(),
	}, nil
}

// VerifyNotification decodes and verifies an Alipay async notification.
func (a *Alipay) VerifyNotification(ctx context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	values, err := url.ParseQuery(rawBody)
	if err != nil {
		return nil, fmt.Errorf("alipay parse notification: %w", err)
	}

	notification, err := client.DecodeNotification(ctx, values)
	if err != nil {
		return nil, fmt.Errorf("alipay verify notification: %w", err)
	}

	status := payment.ProviderStatusFailed
	if notification.TradeStatus == alipay.TradeStatusSuccess || notification.TradeStatus == alipay.TradeStatusFinished {
		status = payment.ProviderStatusSuccess
	}

	amount, err := strconv.ParseFloat(notification.TotalAmount, 64)
	if err != nil {
		amount, err = parseAlipayAmount(
			notification.TotalAmount,
			notification.ReceiptAmount,
			notification.BuyerPayAmount,
		)
		if err != nil {
			return nil, fmt.Errorf("alipay parse notification amount: %w", err)
		}
	}

	metadata := a.MerchantIdentityMetadata()
	if appID := strings.TrimSpace(notification.AppId); appID != "" {
		if metadata == nil {
			metadata = map[string]string{}
		}
		metadata["app_id"] = appID
	}

	return &payment.PaymentNotification{
		TradeNo:  notification.TradeNo,
		OrderID:  notification.OutTradeNo,
		Amount:   amount,
		Status:   status,
		RawData:  rawBody,
		Metadata: metadata,
	}, nil
}

// Refund requests a refund through Alipay.
func (a *Alipay) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	outRequestNo := alipayRefundReference(req)
	result, err := alipayTradeRefund(ctx, client, alipay.TradeRefund{
		OutTradeNo:   req.OrderID,
		RefundAmount: req.Amount,
		RefundReason: req.Reason,
		OutRequestNo: outRequestNo,
	})
	if err != nil {
		return nil, fmt.Errorf("alipay TradeRefund: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("alipay TradeRefund: empty response")
	}

	refundStatus := payment.ProviderStatusPending
	if result.FundChange == alipayFundChangeYes {
		refundStatus = payment.ProviderStatusSuccess
	}

	// fund_change != Y 时需凭退款请求号查询终态，因此 RefundID 记录 out_request_no。
	return &payment.RefundResponse{
		RefundID: outRequestNo,
		Status:   refundStatus,
	}, nil
}

func alipayRefundReference(req payment.RefundRequest) string {
	if reference := strings.TrimSpace(req.RefundReference); reference != "" {
		return reference
	}
	return fmt.Sprintf("%s-refund-%d", req.OrderID, time.Now().UnixNano())
}

// QueryRefund 通过 alipay.trade.fastpay.refund.query 查询退款终态。refund_status 为 REFUND_SUCCESS
// 表示成功；退款发起超过宽限期仍查不到记录表示支付宝未受理或退款失败；其余情况视为仍在处理中。
func (a *Alipay) QueryRefund(ctx context.Context, req payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	orderID := strings.TrimSpace(req.OrderID)
	outRequestNo := strings.TrimSpace(req.RefundID)
	// 旧版本把支付宝交易号记为 RefundID，无法据此查询退款请求，只能人工核实。
	if orderID == "" || !isQueryableAlipayRefundReference(orderID, outRequestNo) {
		return nil, infraerrors.BadRequest("REFUND_QUERY_UNSUPPORTED", "this refund was issued without a queryable refund request number; please verify manually")
	}
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}
	result, err := alipayTradeFastPayRefundQuery(ctx, client, alipay.TradeFastPayRefundQuery{
		OutTradeNo:   orderID,
		OutRequestNo: outRequestNo,
	})
	if err != nil {
		return nil, fmt.Errorf("alipay query refund: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("alipay query refund: empty response")
	}
	// SDK 对带签名的业务失败响应（如 ACQ.SYSTEM_ERROR）不返回 error，字段为空；
	// 不检查错误码会被误判为"无退款记录"而失败收尾。
	if result.IsFailure() {
		return nil, fmt.Errorf("alipay query refund: %w", result.Error)
	}

	status := payment.ProviderStatusPending
	switch {
	case strings.TrimSpace(result.RefundStatus) == alipayRefundStatusSucc:
		status = payment.ProviderStatusSuccess
	case strings.TrimSpace(result.OutRequestNo) == "" && strings.TrimSpace(result.RefundAmount) == "" &&
		alipayRefundAbsenceIsFinal(req.RequestedAt, time.Now()):
		status = payment.ProviderStatusFailed
	}
	return &payment.RefundResponse{RefundID: outRequestNo, Status: status}, nil
}

func alipayRefundAbsenceIsFinal(requestedAt, now time.Time) bool {
	return !requestedAt.IsZero() && now.Sub(requestedAt) >= alipayRefundQueryNotFoundGrace
}

func isQueryableAlipayRefundReference(orderID, reference string) bool {
	if strings.HasPrefix(reference, orderID+alipayRefundRequestSep) {
		return true
	}
	// Local cancellation persists this reference before calling the provider.
	suffix, ok := strings.CutPrefix(reference, "sub2-cancel-")
	if !ok || suffix == "" || suffix[0] == '0' {
		return false
	}
	for _, digit := range suffix {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// CancelPayment closes a pending trade on Alipay.
func (a *Alipay) CancelPayment(ctx context.Context, tradeNo string) error {
	client, err := a.getClient()
	if err != nil {
		return err
	}

	_, err = client.TradeClose(ctx, alipay.TradeClose{OutTradeNo: tradeNo})
	if err != nil {
		if isTradeNotExist(err) {
			return nil
		}
		return fmt.Errorf("alipay TradeClose: %w", err)
	}
	return nil
}

func isTradeNotExist(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), alipayErrTradeNotExist)
}

func parseAlipayAmount(values ...string) (float64, error) {
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		amount, err := strconv.ParseFloat(raw, 64)
		if err == nil {
			return amount, nil
		}
	}
	return 0, fmt.Errorf("no valid amount field")
}

// Ensure interface compliance.
var (
	_ payment.Provider                 = (*Alipay)(nil)
	_ payment.CancelableProvider       = (*Alipay)(nil)
	_ payment.RefundQueryProvider      = (*Alipay)(nil)
	_ payment.MerchantIdentityProvider = (*Alipay)(nil)
)
