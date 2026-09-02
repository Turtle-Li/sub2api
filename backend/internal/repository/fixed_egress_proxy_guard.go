package repository

import (
	"context"
	"database/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// lockOpenAIOAuthProxy takes the same row lock used by proxy identity updates.
// Keeping the lock until the caller commits serializes OAuth parent binding
// against a concurrent proxy rewrite. The caller chooses when to validate the
// fixed-egress shape so account and proxy locks can always be acquired in the
// same order: proxy first, then account.
func lockOpenAIOAuthProxy(ctx context.Context, client *dbent.Client, proxyID int64) (*service.Proxy, error) {
	if client == nil || proxyID <= 0 {
		return nil, service.ErrFixedEgressProxyInvalid
	}

	rows, err := client.QueryContext(ctx, `
		SELECT id, status, protocol, host, port,
			COALESCE(username, ''), COALESCE(password, ''),
			expires_at, COALESCE(fallback_mode, ''), backup_proxy_id
		FROM proxies
		WHERE id = $1 AND deleted_at IS NULL
		FOR NO KEY UPDATE
	`, proxyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrFixedEgressProxyInvalid
	}

	var (
		proxy     service.Proxy
		expiresAt sql.NullTime
		backupID  sql.NullInt64
	)
	if err := rows.Scan(
		&proxy.ID,
		&proxy.Status,
		&proxy.Protocol,
		&proxy.Host,
		&proxy.Port,
		&proxy.Username,
		&proxy.Password,
		&expiresAt,
		&proxy.FallbackMode,
		&backupID,
	); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if expiresAt.Valid {
		value := expiresAt.Time
		proxy.ExpiresAt = &value
	}
	if backupID.Valid {
		value := backupID.Int64
		proxy.BackupProxyID = &value
	}
	return &proxy, nil
}

func lockFixedEgressProxyForOpenAIOAuth(ctx context.Context, client *dbent.Client, proxyID int64) (*service.Proxy, error) {
	proxy, err := lockOpenAIOAuthProxy(ctx, client, proxyID)
	if err != nil {
		return nil, err
	}
	if err := service.ValidateFixedEgressProxy(proxy); err != nil {
		return nil, err
	}
	return proxy, nil
}

func hasOpenAIOAuthParentProxyBinding(ctx context.Context, client *dbent.Client, proxyID int64) (bool, error) {
	if client == nil {
		return false, service.ErrFixedEgressGuardUnavailable
	}
	rows, err := client.QueryContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM accounts
			WHERE proxy_id = $1
			  AND platform = $2
			  AND type IN ($3, $4)
			  AND parent_account_id IS NULL
			  AND deleted_at IS NULL
		)
	`, proxyID, service.PlatformOpenAI, service.AccountTypeOAuth, service.AccountTypeSetupToken)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	var bound bool
	if err := rows.Scan(&bound); err != nil {
		return false, err
	}
	return bound, rows.Err()
}

func isOpenAIFixedEgressParent(account *service.Account) bool {
	return account != nil && account.IsOpenAIOAuthLike() && account.ParentAccountID == nil
}

func isActiveOpenAIFixedEgressParent(account *service.Account) bool {
	return isOpenAIFixedEgressParent(account) && account.Status == service.StatusActive
}

func isOpenAIFixedEgressShadow(account *service.Account) bool {
	return account != nil && account.IsOpenAIOAuthLike() && account.ParentAccountID != nil
}

type lockedOpenAIOAuthParentState struct {
	openAIFixedEgressParent bool
	openAIOAuthParent       bool
	status                  string
	proxyID                 *int64
}

func lockOpenAIOAuthParentState(ctx context.Context, client *dbent.Client, accountID int64) (lockedOpenAIOAuthParentState, error) {
	rows, err := client.QueryContext(ctx, `
		SELECT platform, type, status, parent_account_id, proxy_id
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
		FOR NO KEY UPDATE
	`, accountID)
	if err != nil {
		return lockedOpenAIOAuthParentState{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return lockedOpenAIOAuthParentState{}, err
		}
		return lockedOpenAIOAuthParentState{}, service.ErrAccountNotFound
	}
	var platform, accountType, status string
	var parentID, proxyID sql.NullInt64
	if err := rows.Scan(&platform, &accountType, &status, &parentID, &proxyID); err != nil {
		return lockedOpenAIOAuthParentState{}, err
	}
	if err := rows.Err(); err != nil {
		return lockedOpenAIOAuthParentState{}, err
	}
	state := lockedOpenAIOAuthParentState{
		openAIFixedEgressParent: platform == service.PlatformOpenAI &&
			(accountType == service.AccountTypeOAuth || accountType == service.AccountTypeSetupToken) && !parentID.Valid,
		openAIOAuthParent: platform == service.PlatformOpenAI && accountType == service.AccountTypeOAuth && !parentID.Valid,
		status:            status,
	}
	if proxyID.Valid {
		value := proxyID.Int64
		state.proxyID = &value
	}
	return state, nil
}

func sameOptionalProxyID(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

// enforcePersistedOpenAIFixedEgressRelation protects the identity of an
// already-persisted OAuth/setup-token relation, even when one generic Update
// attempts to reclassify it into a different candidate shape. Candidate-shape
// guards run first so their proxy/parent locks preserve the global lock order;
// this final account-row lock then rejects parent/shadow, quota-dimension, or
// proxy changes that did not use the dedicated parent+shadow CAS operation.
func enforcePersistedOpenAIFixedEgressRelation(ctx context.Context, client *dbent.Client, account *service.Account) error {
	if client == nil || account == nil {
		return service.ErrFixedEgressGuardUnavailable
	}
	rows, err := client.QueryContext(ctx, `
		SELECT platform, type, parent_account_id, quota_dimension, proxy_id
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
		FOR NO KEY UPDATE
	`, account.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return service.ErrAccountNotFound
	}

	var (
		platform, accountType, quotaDimension string
		parentID, proxyID                     sql.NullInt64
	)
	if err := rows.Scan(&platform, &accountType, &parentID, &quotaDimension, &proxyID); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	currentProtected := platform == service.PlatformOpenAI &&
		(accountType == service.AccountTypeOAuth || accountType == service.AccountTypeSetupToken)
	if !currentProtected {
		return nil
	}
	// A protected account must not leave (or switch within) the protected
	// identity class through generic Update. Otherwise a caller could pivot it
	// to API-key, then restore OAuth/setup-token with a different proxy after
	// the persisted-source guard no longer applies. Identity conversion needs a
	// dedicated operation; fixed-egress proxy changes need the CAS operation.
	if platform != account.Platform || accountType != account.Type {
		return service.ErrFixedEgressCASRequired
	}

	var currentParentID, currentProxyID *int64
	if parentID.Valid {
		value := parentID.Int64
		currentParentID = &value
	}
	if proxyID.Valid {
		value := proxyID.Int64
		currentProxyID = &value
	}
	if !sameOptionalProxyID(currentParentID, account.ParentAccountID) ||
		quotaDimension != account.QuotaDimensionOrDefault() ||
		!sameOptionalProxyID(currentProxyID, account.ProxyID) {
		return service.ErrFixedEgressCASRequired
	}
	return nil
}

type lockedAccountProxyFallbackState struct {
	openAIFixedEgress bool
	hasFallback       bool
}

func lockAccountProxyFallbackState(ctx context.Context, client *dbent.Client, accountID int64) (lockedAccountProxyFallbackState, error) {
	rows, err := client.QueryContext(ctx, `
		SELECT platform, type, proxy_fallback_origin_id
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
		FOR NO KEY UPDATE
	`, accountID)
	if err != nil {
		return lockedAccountProxyFallbackState{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return lockedAccountProxyFallbackState{}, err
		}
		return lockedAccountProxyFallbackState{}, service.ErrAccountNotFound
	}
	var (
		platform, accountType string
		originID              sql.NullInt64
	)
	if err := rows.Scan(&platform, &accountType, &originID); err != nil {
		return lockedAccountProxyFallbackState{}, err
	}
	if err := rows.Err(); err != nil {
		return lockedAccountProxyFallbackState{}, err
	}
	return lockedAccountProxyFallbackState{
		openAIFixedEgress: platform == service.PlatformOpenAI &&
			(accountType == service.AccountTypeOAuth || accountType == service.AccountTypeSetupToken),
		hasFallback: originID.Valid,
	}, nil
}

// enforceOpenAIOAuthParentUpdate protects direct repository callers (notably
// imports) from bypassing the CAS rule. A parent relation may never be rebound
// through this path, including while disabled or in error. A transition into an
// active parent relation locks and validates its proxy; proxy replacement is
// only available through CompareAndSwapOpenAIOAuthProxy.
func enforceOpenAIOAuthParentUpdate(ctx context.Context, client *dbent.Client, account *service.Account) error {
	if !isOpenAIFixedEgressParent(account) {
		return nil
	}
	if client == nil {
		return service.ErrFixedEgressGuardUnavailable
	}

	// Acquire the proxy lock before the account lock. CAS and create paths use
	// the same order, avoiding a proxy/account lock inversion under concurrency.
	var candidateProxy *service.Proxy
	if account.ProxyID != nil {
		var err error
		candidateProxy, err = lockOpenAIOAuthProxy(ctx, client, *account.ProxyID)
		if err != nil {
			return err
		}
	}
	current, err := lockOpenAIOAuthParentState(ctx, client, account.ID)
	if err != nil {
		return err
	}
	if current.openAIFixedEgressParent && !sameOptionalProxyID(current.proxyID, account.ProxyID) {
		return service.ErrFixedEgressCASRequired
	}
	if isActiveOpenAIFixedEgressParent(account) && account.ProxyID != nil &&
		(!current.openAIFixedEgressParent || current.status != service.StatusActive) {
		return service.ValidateFixedEgressProxy(candidateProxy)
	}
	return nil
}

// enforceOpenAIOAuthShadowProxyRelation closes generic repository create and
// update seams for credential shadows. Credentials resolve through the parent,
// while the selected shadow supplies the runtime proxy; those relationships
// must therefore remain identical on every write. Acquire the candidate proxy
// before the parent account to preserve the CAS lock order.
func enforceOpenAIOAuthShadowProxyRelation(ctx context.Context, client *dbent.Client, account *service.Account) error {
	if !isOpenAIFixedEgressShadow(account) {
		return nil
	}
	if client == nil {
		return service.ErrFixedEgressGuardUnavailable
	}
	if account.ProxyID != nil {
		if _, err := lockOpenAIOAuthProxy(ctx, client, *account.ProxyID); err != nil {
			return err
		}
	}
	parent, err := lockOpenAIOAuthParentState(ctx, client, *account.ParentAccountID)
	if err != nil {
		return err
	}
	if !parent.openAIFixedEgressParent || !sameOptionalProxyID(parent.proxyID, account.ProxyID) {
		return service.ErrCredentialShadowProxyMismatch
	}
	return nil
}
