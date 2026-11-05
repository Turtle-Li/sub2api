package service

import (
	"context"
	"io"
	"net/http"
	"regexp"

	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

const shopActionVaultSocket = "/run/sub2api-shop-actions-vault/public.sock"

var shopActionCredentialAlias = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func loadShopActionSigningSecret(ctx context.Context, alias string) ([]byte, error) {
	unavailable := &shopaction.Failure{Kind: "CREDENTIAL_UNAVAILABLE", Retryable: true}
	if !shopActionCredentialAlias.MatchString(alias) {
		return nil, unavailable
	}
	client := hardenedFeishuPaymentVaultClient(shopActionVaultSocket, nil)
	defer client.CloseIdleConnections()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://vault/v1/secret/data/ops/shop-actions/"+alias, nil)
	if err != nil {
		return nil, unavailable
	}
	res, err := client.Do(r)
	if err != nil {
		return nil, unavailable
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return nil, unavailable
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	defer clear(raw)
	if err != nil || len(raw) > 4096 {
		return nil, unavailable
	}
	value, err := parseFeishuPaymentVaultEnvelope(raw, "signing_secret")
	if err != nil || len(value) > 256 {
		return nil, unavailable
	}
	return []byte(value), nil
}
