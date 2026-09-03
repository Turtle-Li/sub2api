package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

const AccountProxyUnavailableReason = "ACCOUNT_PROXY_UNAVAILABLE"

var ErrAccountProxyUnavailable = infraerrors.New(
	http.StatusServiceUnavailable,
	AccountProxyUnavailableReason,
	"the account's configured proxy is unavailable",
)

type accountProxyLookup interface {
	GetByID(ctx context.Context, id int64) (*Proxy, error)
}

// ResolveAccountProxyURL enforces the account proxy invariant at the upstream
// boundary. Only accounts without proxy_id may use a direct connection.
func ResolveAccountProxyURL(account *Account) (string, error) {
	return resolveAccountProxyURLAt(account, nil, time.Now())
}

// ResolveAccountProxyURLWithLookup hydrates a missing proxy relation before
// applying the same fail-closed policy. Callers must not treat lookup failures
// as permission to connect directly.
func ResolveAccountProxyURLWithLookup(
	ctx context.Context,
	account *Account,
	lookup accountProxyLookup,
) (string, error) {
	if account == nil || account.ProxyID == nil {
		return resolveAccountProxyURLAt(account, nil, time.Now())
	}
	proxy := account.Proxy
	if proxy == nil || proxy.ID != *account.ProxyID {
		if lookup == nil {
			return "", accountProxyUnavailable(account, "proxy relation is not loaded")
		}
		loaded, err := lookup.GetByID(ctx, *account.ProxyID)
		if err != nil {
			return "", ErrAccountProxyUnavailable.WithCause(fmt.Errorf("proxy lookup failed: %w", err))
		}
		proxy = loaded
	}
	return resolveAccountProxyURLAt(account, proxy, time.Now())
}

func resolveAccountProxyURLAt(account *Account, hydrated *Proxy, now time.Time) (string, error) {
	if account == nil {
		return "", ErrAccountProxyUnavailable.WithCause(fmt.Errorf("account is nil"))
	}
	if account.ProxyID == nil {
		return "", nil
	}
	if *account.ProxyID <= 0 {
		return "", accountProxyUnavailable(account, "proxy id is invalid")
	}

	proxy := hydrated
	if proxy == nil {
		proxy = account.Proxy
	}
	if proxy == nil {
		return "", accountProxyUnavailable(account, "proxy relation is missing")
	}
	if proxy.ID != *account.ProxyID {
		return "", accountProxyUnavailable(account, "proxy relation does not match proxy id")
	}
	if !proxy.IsActive() {
		return "", accountProxyUnavailable(account, "proxy is not active")
	}
	if proxy.IsExpired(now) {
		return "", accountProxyUnavailable(account, "proxy is expired")
	}
	if account.IsOpenAIOAuthLike() && !FixedEgressCompatibilityModeEnabled() {
		if err := validateFixedEgressProxy(proxy); err != nil {
			return "", accountProxyUnavailable(account, fmt.Sprintf("OpenAI Codex proxy does not satisfy fixed-egress requirements: %v", err))
		}
	}

	normalized, _, err := proxyurl.Parse(proxy.URL())
	if err != nil {
		return "", ErrAccountProxyUnavailable.WithCause(fmt.Errorf("proxy configuration is invalid: %w", err))
	}
	if normalized == "" {
		return "", accountProxyUnavailable(account, "proxy URL is empty")
	}
	return normalized, nil
}

func accountProxyUnavailable(account *Account, detail string) error {
	accountID := int64(0)
	proxyID := int64(0)
	if account != nil {
		accountID = account.ID
		if account.ProxyID != nil {
			proxyID = *account.ProxyID
		}
	}
	return ErrAccountProxyUnavailable.WithCause(fmt.Errorf(
		"account %d proxy %d unavailable: %s",
		accountID,
		proxyID,
		detail,
	))
}
