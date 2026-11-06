package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	shopDaypassInstance  = "sub2api-production"
	shopDaypassSocket    = "/run/sub2api-daypass-vault/public.sock"
	shopDaypassVaultPath = "/v1/secret/data/ops/newapi/daypass"
	shopDaypassVaultRef  = "vault://secret/data/ops/newapi/daypass#bearer"
)

var (
	errShopDaypassUnavailable = errors.New("daypass gateway unavailable")
	errShopDaypassExpired     = errors.New("daypass order expired")
	errShopDaypassRevoked     = errors.New("daypass order revoked")
)

type shopDaypassBinding struct {
	Instance  string `json:"instance"`
	OrderID   int64  `json:"order_id"`
	ProductID int64  `json:"product_id"`
	PriceFen  int64  `json:"price_fen"`
	Currency  string `json:"currency"`
	PaidAt    int64  `json:"paid_at"`
}

type shopDaypassGrant struct {
	Instance  string `json:"instance"`
	OrderID   int64  `json:"order_id"`
	ProductID int64  `json:"product_id"`
	TokenID   int    `json:"token_id"`
	PaidAt    int64  `json:"paid_at"`
	ExpiresAt int64  `json:"expires_at"`
	Status    string `json:"status"`
	Key       string `json:"key"`
}

type shopDaypassGateway interface {
	Ready(context.Context, int) error
	Provision(context.Context, shopDaypassBinding) (*shopDaypassGrant, error)
	Revoke(context.Context, shopDaypassBinding) error
}

type shopDaypassClient struct {
	client     *http.Client
	endpoint   string
	loadBearer func(context.Context) (string, error)
	productID  int64
}

func newShopDaypassGateway(productID int64, endpoint string) shopDaypassGateway {
	return &shopDaypassClient{endpoint: endpoint, loadBearer: loadShopDaypassBearer, productID: productID,
		client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{Proxy: nil, DisableCompression: true, MaxConnsPerHost: 2, MaxIdleConnsPerHost: 2,
				ResponseHeaderTimeout: 10 * time.Second, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}}}
}

func loadShopDaypassBearer(ctx context.Context) (string, error) {
	client := hardenedFeishuPaymentVaultClient(shopDaypassSocket, nil)
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://vault"+shopDaypassVaultPath, nil)
	if err != nil {
		return "", errShopDaypassUnavailable
	}
	response, err := client.Do(req)
	if err != nil {
		return "", errShopDaypassUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", errShopDaypassUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	defer clear(body)
	if err != nil || len(body) > 4096 {
		return "", errShopDaypassUnavailable
	}
	bearer, err := parseFeishuPaymentVaultEnvelope(body, "bearer")
	if err != nil || !validShopDaypassBearer(bearer) {
		return "", errShopDaypassUnavailable
	}
	return bearer, nil
}

func validShopDaypassBearer(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func (c *shopDaypassClient) Ready(ctx context.Context, durationDays int) error {
	bearer, err := c.loadBearer(ctx)
	if err != nil {
		return errShopDaypassUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/api/daypass/automation/ready", nil)
	if err != nil {
		return errShopDaypassUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	res, err := c.client.Do(req)
	if err != nil {
		return errShopDaypassUnavailable
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	defer clear(raw)
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Concurrencies []int  `json:"concurrencies"`
			DurationDays  []int  `json:"duration_days"`
			Instance      string `json:"instance"`
			ProductID     int64  `json:"product_id"`
			Status        string `json:"status"`
		} `json:"data"`
	}
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &result) != nil || res.StatusCode != http.StatusOK || !result.Success ||
		result.Data.Instance != shopDaypassInstance || result.Data.ProductID != c.productID || result.Data.Status != "ready" || len(result.Data.Concurrencies) != 2 || result.Data.Concurrencies[0] != 2 || result.Data.Concurrencies[1] != 3 {
		return errShopDaypassUnavailable
	}
	// Older receivers omit duration_days. They still serve existing day passes.
	if durationDays != 1 && !slices.Contains(result.Data.DurationDays, durationDays) {
		return errShopDaypassUnavailable
	}
	return nil
}

func (c *shopDaypassClient) Provision(ctx context.Context, binding shopDaypassBinding) (*shopDaypassGrant, error) {
	return c.call(ctx, "provision", binding)
}

func (c *shopDaypassClient) Revoke(ctx context.Context, binding shopDaypassBinding) error {
	_, err := c.call(ctx, "revoke", binding)
	return err
}

func (c *shopDaypassClient) call(ctx context.Context, action string, binding shopDaypassBinding) (*shopDaypassGrant, error) {
	duration := shopDaypassDurationForPrice(binding.PriceFen)
	if duration == 0 {
		return nil, errShopDaypassUnavailable
	}
	bearer, err := c.loadBearer(ctx)
	if err != nil {
		return nil, errShopDaypassUnavailable
	}
	body, err := json.Marshal(binding)
	if err != nil {
		return nil, errShopDaypassUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/daypass/automation/"+action, bytes.NewReader(body))
	if err != nil {
		return nil, errShopDaypassUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return nil, errShopDaypassUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	defer clear(raw)
	if err != nil || len(raw) > 16384 {
		return nil, errShopDaypassUnavailable
	}
	var result struct {
		Success bool             `json:"success"`
		Code    string           `json:"code"`
		Data    shopDaypassGrant `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, errShopDaypassUnavailable
	}
	if response.StatusCode == http.StatusGone {
		if result.Code == "daypass_expired" {
			return nil, errShopDaypassExpired
		}
		if result.Code == "daypass_revoked" {
			return nil, errShopDaypassRevoked
		}
	}
	grant := &result.Data
	if response.StatusCode != http.StatusOK || !result.Success || grant.Instance != binding.Instance || grant.OrderID != binding.OrderID ||
		grant.ProductID != binding.ProductID || grant.PaidAt != binding.PaidAt || grant.ExpiresAt != binding.PaidAt+duration {
		return nil, errShopDaypassUnavailable
	}
	if action == "revoke" {
		if grant.Status != "revoked" || grant.Key != "" {
			return nil, errShopDaypassUnavailable
		}
		return grant, nil
	}
	if grant.Status != "active" || grant.TokenID <= 0 || !strings.HasPrefix(grant.Key, "sk-") || len(grant.Key) < 35 || len(grant.Key) > 131 || strings.ContainsAny(grant.Key, " \t\r\n\x00") {
		return nil, errShopDaypassUnavailable
	}
	return grant, nil
}
