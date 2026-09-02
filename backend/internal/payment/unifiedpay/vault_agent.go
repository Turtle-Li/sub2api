package unifiedpay

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultVaultAgentTimeout = 3 * time.Second
	maximumVaultAgentBody    = 4 * 1024
	vaultAgentSocketPath     = "/run/sub2api-payment-vault/public.sock"
)

type vaultAgentKVResponse struct {
	Data struct {
		Data map[string]string `json:"data"`
	} `json:"data"`
}

// loadVaultEd25519PrivateKey loads one exact field from the colocated,
// memory-only agent over a fixed Unix socket. It never receives a Vault token,
// cookie, proxy setting, redirect, or caller-controlled header.
func loadVaultEd25519PrivateKey(ctx context.Context, socketPath, rawReference string, injected *http.Client) (ed25519.PrivateKey, error) {
	if ctx == nil || ctx.Err() != nil || !validVaultAgentSocket(socketPath) {
		return nil, ErrInvalidConfiguration
	}
	path, field, ok := parseVaultReference(rawReference)
	if !ok {
		return nil, ErrInvalidConfiguration
	}
	client := hardenedVaultAgentClient(socketPath, injected)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://vault/v1/"+path, nil)
	if err != nil {
		return nil, ErrInvalidConfiguration
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil || response == nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, ErrInvalidConfiguration
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maximumVaultAgentBody+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || len(body) > maximumVaultAgentBody {
		clear(body)
		return nil, ErrInvalidConfiguration
	}
	defer clear(body)
	var envelope vaultAgentKVResponse
	if err := strictUnmarshalObject(body, &envelope, true); err != nil || len(envelope.Data.Data) != 1 {
		return nil, ErrInvalidConfiguration
	}
	encoded, exists := envelope.Data.Data[field]
	if !exists || encoded == "" || strings.TrimSpace(encoded) != encoded {
		return nil, ErrInvalidConfiguration
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		clear(decoded)
		return nil, ErrInvalidConfiguration
	}
	derived := ed25519.NewKeyFromSeed(decoded[:ed25519.SeedSize])
	if subtle.ConstantTimeCompare(decoded, derived) != 1 {
		clear(decoded)
		clear(derived)
		return nil, ErrInvalidConfiguration
	}
	clear(derived)
	return ed25519.PrivateKey(decoded), nil
}

func hardenedVaultAgentClient(socketPath string, injected *http.Client) *http.Client {
	client := http.Client{Timeout: defaultVaultAgentTimeout}
	if injected != nil {
		client = *injected
		if client.Timeout <= 0 || client.Timeout > defaultVaultAgentTimeout {
			client.Timeout = defaultVaultAgentTimeout
		}
	} else {
		transport := &http.Transport{
			Proxy: nil,
			DialContext: func(dialContext context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(dialContext, "unix", socketPath)
			},
			DisableCompression: true,
		}
		client.Transport = transport
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func validVaultAgentSocket(value string) bool {
	return value == vaultAgentSocketPath && filepath.IsAbs(value) && filepath.Clean(value) == value
}

func parseVaultReference(raw string) (string, string, bool) {
	const prefix = "vault://"
	if raw == "" || strings.TrimSpace(raw) != raw || !strings.HasPrefix(raw, prefix) || strings.Count(raw, "#") != 1 {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(raw, prefix), "#", 2)
	if !validVaultPath(parts[0]) || !validVaultField(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func validVaultPath(value string) bool {
	if len(value) < 3 || len(value) > 512 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." || !validVaultToken(segment, 1, 128) {
			return false
		}
	}
	return true
}

func validVaultField(value string) bool {
	return validVaultToken(value, 1, 128)
}

func validVaultToken(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for i := 0; i < len(value); i++ {
		character := value[i]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}
