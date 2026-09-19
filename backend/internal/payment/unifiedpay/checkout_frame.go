package unifiedpay

import (
	"net/url"
	"strings"
)

const maximumAlipayEmbeddedCheckoutFrameURLLength = 16384

// AlipayEmbeddedCheckoutFrameURL returns raw only when it is an official,
// signed alipay.trade.page.pay QR presentation. The returned value is display
// material only; it is never evidence that an order has been paid.
func AlipayEmbeddedCheckoutFrameURL(raw string) string {
	if raw == "" || len(raw) > maximumAlipayEmbeddedCheckoutFrameURLLength || raw != strings.TrimSpace(raw) ||
		strings.ContainsAny(raw, "\x00\r\n\t ") {
		return ""
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil ||
		parsed.Scheme != "https" || parsed.Host == "" || parsed.Port() != "" ||
		parsed.Path != "/gateway.do" || parsed.RawPath != "" || parsed.RawQuery == "" ||
		parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return ""
	}
	if parsed.Host != "openapi.alipay.com" && parsed.Host != "openapi-sandbox.dl.alipaydev.com" {
		return ""
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return ""
	}
	method, methodOK := singleAlipayCheckoutQueryValue(query, "method")
	sign, signOK := singleAlipayCheckoutQueryValue(query, "sign")
	signType, signTypeOK := singleAlipayCheckoutQueryValue(query, "sign_type")
	bizContent, bizContentOK := singleAlipayCheckoutQueryValue(query, "biz_content")
	if !methodOK || !signOK || !signTypeOK || !bizContentOK || method != "alipay.trade.page.pay" ||
		signType != "RSA2" || strings.TrimSpace(sign) != sign {
		return ""
	}
	var businessContent struct {
		QRPayMode   string `json:"qr_pay_mode"`
		QRCodeWidth string `json:"qrcode_width"`
	}
	if strictUnmarshalObject([]byte(bizContent), &businessContent, false) != nil ||
		businessContent.QRPayMode != "4" || businessContent.QRCodeWidth != "224" {
		return ""
	}
	return raw
}

func singleAlipayCheckoutQueryValue(query url.Values, key string) (string, bool) {
	values, ok := query[key]
	if !ok || len(values) != 1 || values[0] == "" {
		return "", false
	}
	return values[0], true
}
