package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	feishuPaymentVaultSocket       = "/run/sub2api-feishu-vault/public.sock"
	feishuPaymentVaultReference    = "vault://secret/data/ops/feishu/payment#webhook_url"
	feishuPaymentVaultRequestPath  = "/v1/secret/data/ops/feishu/payment"
	feishuPaymentVaultMaxBodyBytes = 4 * 1024
	feishuPaymentHTTPMaxBodyBytes  = 8 * 1024
	feishuPaymentHTTPTimeout       = 5 * time.Second
)

var (
	errFeishuPaymentWebhookDelivery = errors.New("feishu payment webhook delivery failed")
	feishuPaymentWebhookToken       = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	feishuPaymentVaultToken         = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

type feishuPaymentIncidentSender struct {
	client  *http.Client
	loadURL func(context.Context) (string, error)
	now     func() time.Time
}

// NewFeishuPaymentIncidentSender intentionally has no credential parameter.
// The URL is fetched only when a durable delivery is actually sent, through
// the dedicated public Vault-agent socket configured by deployment.
func NewFeishuPaymentIncidentSender() FeishuPaymentIncidentSender {
	return newFeishuPaymentIncidentSender(nil, loadFeishuPaymentWebhookURL, nil)
}

func newFeishuPaymentIncidentSender(client *http.Client, loadURL func(context.Context) (string, error), now func() time.Time) *feishuPaymentIncidentSender {
	if loadURL == nil {
		loadURL = loadFeishuPaymentWebhookURL
	}
	if now == nil {
		now = time.Now
	}
	return &feishuPaymentIncidentSender{
		client:  hardenedFeishuPaymentHTTPClient(client),
		loadURL: loadURL,
		now:     now,
	}
}

func (s *feishuPaymentIncidentSender) Send(ctx context.Context, delivery FeishuPaymentDelivery) error {
	if s == nil || s.client == nil || s.loadURL == nil {
		return errFeishuPaymentWebhookDelivery
	}
	if ctx == nil {
		ctx = context.Background()
	}
	webhookURL, err := s.loadURL(ctx)
	if err != nil {
		return errFeishuPaymentWebhookDelivery
	}
	if _, err := validateFeishuPaymentWebhookURL(webhookURL); err != nil {
		return errFeishuPaymentWebhookDelivery
	}
	payload, err := json.Marshal(struct {
		MessageType string `json:"msg_type"`
		Content     struct {
			Text string `json:"text"`
		} `json:"content"`
	}{
		MessageType: "text",
		Content: struct {
			Text string `json:"text"`
		}{Text: feishuPaymentIncidentMessage(delivery, s.now().UTC())},
	})
	if err != nil {
		return errFeishuPaymentWebhookDelivery
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return errFeishuPaymentWebhookDelivery
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return errFeishuPaymentWebhookDelivery
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errFeishuPaymentWebhookDelivery
	}
	body, err := readBoundedFeishuPaymentResponse(resp.Body, feishuPaymentHTTPMaxBodyBytes)
	if err != nil || !isFeishuPaymentSuccessResponse(body) {
		return errFeishuPaymentWebhookDelivery
	}
	return nil
}

func hardenedFeishuPaymentHTTPClient(in *http.Client) *http.Client {
	if in == nil {
		transport := &http.Transport{
			Proxy:               nil,
			DisableCompression:  true,
			DialContext:         (&net.Dialer{Timeout: feishuPaymentHTTPTimeout}).DialContext,
			TLSHandshakeTimeout: feishuPaymentHTTPTimeout,
		}
		return &http.Client{
			Transport: transport,
			Timeout:   feishuPaymentHTTPTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	client := *in
	client.Jar = nil
	if client.Timeout <= 0 || client.Timeout > feishuPaymentHTTPTimeout {
		client.Timeout = feishuPaymentHTTPTimeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func validateFeishuPaymentWebhookURL(raw string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u == nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil ||
		u.Host != "open.feishu.cn" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return nil, errFeishuPaymentWebhookDelivery
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "open-apis" || parts[2] != "bot" ||
		parts[3] != "v2" || parts[4] != "hook" || !feishuPaymentWebhookToken.MatchString(parts[5]) {
		return nil, errFeishuPaymentWebhookDelivery
	}
	return u, nil
}

func feishuPaymentIncidentMessage(delivery FeishuPaymentDelivery, now time.Time) string {
	reason := "支付事件需要人工处理"
	switch delivery.IncidentKind {
	case FeishuPaymentIncidentRefundReview:
		reason = "退款需要人工复核"
	case FeishuPaymentIncidentPaidIncomplete:
		reason = "已支付订单尚未完成"
	case FeishuPaymentIncidentTest:
		reason = "合成通知投递检查"
	}
	duration := "0m"
	if !delivery.OpenedAt.IsZero() && now.After(delivery.OpenedAt) {
		duration = now.Sub(delivery.OpenedAt).Truncate(time.Minute).String()
	}
	state := "待处理"
	switch delivery.Kind {
	case FeishuPaymentDeliveryReminder:
		state = "仍待处理"
	case FeishuPaymentDeliveryResolved:
		state = "已解决"
	case FeishuPaymentDeliveryTest:
		state = "测试投递"
	}
	lines := []string{
		"【Sub2】支付事件告警",
		"状态：" + state,
		"原因：" + reason,
		"持续时间：" + duration,
		"事件：" + delivery.IncidentID,
		"类型：" + string(delivery.IncidentKind),
	}
	if delivery.SubjectOrderID > 0 {
		// The route is deliberately the verified list page. Do not invent a
		// query or detail route; the numeric id lets operators locate the order.
		lines = append(lines, "订单："+formatFeishuPaymentOrderID(delivery.SubjectOrderID), "处理入口：https://www.turtleligpt.com/admin/orders")
	}
	return strings.Join(lines, "\n")
}

func formatFeishuPaymentOrderID(orderID int64) string {
	// strconv.FormatInt cannot return a customer field; the ID is the sole
	// order locator allowed in this outbound operational payload.
	return strconv.FormatInt(orderID, 10)
}

func readBoundedFeishuPaymentResponse(body io.Reader, max int64) ([]byte, error) {
	if body == nil || max <= 0 {
		return nil, errFeishuPaymentWebhookDelivery
	}
	data, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, errFeishuPaymentWebhookDelivery
	}
	return data, nil
}

func isFeishuPaymentSuccessResponse(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	var (
		code, legacyCode       json.RawMessage
		hasCode, hasLegacyCode bool
	)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false
		}
		switch key {
		case "code":
			if hasCode {
				return false
			}
			hasCode, code = true, value
		case "StatusCode":
			if hasLegacyCode {
				return false
			}
			hasLegacyCode, legacyCode = true, value
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return false
	}
	if err := ensureFeishuPaymentJSONEOF(decoder); err != nil {
		return false
	}
	if !hasCode && !hasLegacyCode {
		return false
	}
	if hasCode && !feishuPaymentJSONZero(code) {
		return false
	}
	return !hasLegacyCode || feishuPaymentJSONZero(legacyCode)
}

func ensureFeishuPaymentJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected extra json")
	}
	return err
}

func feishuPaymentJSONZero(raw json.RawMessage) bool {
	// A null field is not an explicit numeric success acknowledgement.
	var code *int
	return json.Unmarshal(raw, &code) == nil && code != nil && *code == 0
}

func loadFeishuPaymentWebhookURL(ctx context.Context) (string, error) {
	return loadFeishuPaymentVaultText(ctx, feishuPaymentVaultSocket, feishuPaymentVaultReference, "webhook_url")
}

// loadFeishuPaymentVaultText is a deliberately small, generic secret reader
// for this public socket. It is independent of unifiedpay's private-key loader
// and only permits a validated Vault KV-v2 reference and named scalar field.
func loadFeishuPaymentVaultText(ctx context.Context, socketPath, reference, field string) (string, error) {
	return loadFeishuPaymentVaultTextWithClient(ctx, socketPath, reference, field, nil)
}

func loadFeishuPaymentVaultTextWithClient(ctx context.Context, socketPath, reference, field string, injected *http.Client) (string, error) {
	if socketPath != feishuPaymentVaultSocket || reference != feishuPaymentVaultReference || field != "webhook_url" {
		return "", errFeishuPaymentWebhookDelivery
	}
	path, requestedField, err := parseFeishuPaymentVaultReference(reference)
	if err != nil || requestedField != field {
		return "", errFeishuPaymentWebhookDelivery
	}
	if "/v1/"+path != feishuPaymentVaultRequestPath {
		return "", errFeishuPaymentWebhookDelivery
	}
	if ctx == nil {
		ctx = context.Background()
	}
	client := hardenedFeishuPaymentVaultClient(socketPath, injected)
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://vault/v1/"+path, nil)
	if err != nil {
		return "", errFeishuPaymentWebhookDelivery
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", errFeishuPaymentWebhookDelivery
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errFeishuPaymentWebhookDelivery
	}
	body, err := readBoundedFeishuPaymentResponse(resp.Body, feishuPaymentVaultMaxBodyBytes)
	if err != nil {
		return "", errFeishuPaymentWebhookDelivery
	}
	defer clear(body)
	return parseFeishuPaymentVaultEnvelope(body, field)
}

func hardenedFeishuPaymentVaultClient(socketPath string, injected *http.Client) *http.Client {
	if injected != nil {
		client := *injected
		client.Jar = nil
		if client.Timeout <= 0 || client.Timeout > 3*time.Second {
			client.Timeout = 3 * time.Second
		}
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return &client
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:              nil,
			DisableCompression: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func parseFeishuPaymentVaultReference(reference string) (string, string, error) {
	u, err := url.Parse(reference)
	if err != nil || u.Scheme != "vault" || u.Host != "secret" || u.User != nil || u.RawQuery != "" || u.Fragment == "" || u.RawPath != "" {
		return "", "", errFeishuPaymentWebhookDelivery
	}
	path := u.Host + u.Path
	if path == "" || strings.Contains(path, "..") || strings.Contains(path, "//") || strings.Contains(u.Fragment, "/") || strings.Contains(u.Fragment, "#") {
		return "", "", errFeishuPaymentWebhookDelivery
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || !feishuPaymentVaultToken.MatchString(segment) {
			return "", "", errFeishuPaymentWebhookDelivery
		}
	}
	if !feishuPaymentVaultToken.MatchString(u.Fragment) {
		return "", "", errFeishuPaymentWebhookDelivery
	}
	return path, u.Fragment, nil
}

func parseFeishuPaymentVaultEnvelope(body []byte, field string) (string, error) {
	var envelope struct {
		Data struct {
			Data map[string]json.RawMessage `json:"data"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || ensureFeishuPaymentJSONEOF(decoder) != nil || len(envelope.Data.Data) != 1 {
		return "", errFeishuPaymentWebhookDelivery
	}
	raw, ok := envelope.Data.Data[field]
	if !ok {
		return "", errFeishuPaymentWebhookDelivery
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", errFeishuPaymentWebhookDelivery
	}
	return value, nil
}
