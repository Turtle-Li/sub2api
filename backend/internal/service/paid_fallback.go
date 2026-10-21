package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const PaidFallbackExtraKey = "paid_fallback"
const settingKeyPaidFallback = "paid_fallback_settings"

var errPaidFallbackPoolMismatch = errors.New("account belongs to another fallback pool")

// IsPaidFallback deliberately quarantines malformed markers. An imported
// reserve must never become ordinary capacity because its marker is invalid.
func (a *Account) IsPaidFallback() bool {
	if a == nil {
		return false
	}
	value, exists := a.Extra[PaidFallbackExtraKey]
	return exists && value != false
}

type PaidFallbackSettings struct {
	Enabled   bool    `json:"enabled"`
	GroupIDs  []int64 `json:"group_ids"`
	APIKeyIDs []int64 `json:"api_key_ids"`
}

func (s *SettingService) GetPaidFallbackSettings(ctx context.Context) (*PaidFallbackSettings, error) {
	settings := &PaidFallbackSettings{}
	if s == nil || s.settingRepo == nil {
		return settings, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, settingKeyPaidFallback)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return settings, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read paid fallback settings: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), settings); err != nil {
		return nil, fmt.Errorf("decode paid fallback settings: %w", err)
	}
	if err := validatePaidFallbackSettings(settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func validatePaidFallbackSettings(settings *PaidFallbackSettings) error {
	if settings == nil {
		return errors.New("paid fallback settings required")
	}
	for _, ids := range [][]int64{settings.GroupIDs, settings.APIKeyIDs} {
		if len(ids) > 1000 {
			return errors.New("paid fallback allowlist exceeds 1000 entries")
		}
		for _, id := range ids {
			if id <= 0 {
				return errors.New("paid fallback allowlists require positive IDs")
			}
		}
	}
	if settings.Enabled && (len(settings.GroupIDs) == 0 || len(settings.APIKeyIDs) == 0) {
		return errors.New("enabled paid fallback requires group and API-key allowlists")
	}
	return nil
}

func (s *SettingService) UpdatePaidFallbackSettings(ctx context.Context, settings *PaidFallbackSettings) error {
	if err := validatePaidFallbackSettings(settings); err != nil {
		return err
	}
	if s == nil || s.settingRepo == nil {
		return errors.New("paid fallback settings unavailable")
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, settingKeyPaidFallback, string(data))
}

type paidFallbackAPIKeyKey struct{}
type paidFallbackFailuresKey struct{}
type paidFallbackPermitKey struct{}
type paidFallbackOnlyKey struct{}

type paidFallbackPermit struct {
	settings  *SettingService
	gateway   *OpenAIGatewayService
	request   OpenAIAccountScheduleRequest
	groupID   int64
	apiKeyID  int64
	accountID int64
}

// WithPaidFallbackAPIKey accepts the authenticated key identity, never a client
// header. The group AND key must both be explicitly enabled by an administrator.
func WithPaidFallbackAPIKey(ctx context.Context, apiKeyID int64) context.Context {
	return context.WithValue(ctx, paidFallbackAPIKeyKey{}, apiKeyID)
}

// WithPaidFallbackFailure records upstream failures, separately from the mixed
// scheduler exclusions (which also contain profit and admission vetoes).
func WithPaidFallbackFailure(ctx context.Context, account *Account, status int) context.Context {
	failureStatus := status == 401 || status == 403 || status == 429 || status >= 500 && status <= 599
	if account == nil || account.IsPaidFallback() || !failureStatus {
		return ctx
	}
	previous, _ := ctx.Value(paidFallbackFailuresKey{}).(map[int64]bool)
	failed := make(map[int64]bool, len(previous)+1)
	for id, value := range previous {
		failed[id] = value
	}
	failed[account.ID] = true
	return context.WithValue(ctx, paidFallbackFailuresKey{}, failed)
}

func paidFallbackFailedAccount(ctx context.Context, id int64) bool {
	failed, _ := ctx.Value(paidFallbackFailuresKey{}).(map[int64]bool)
	return failed[id]
}

func WithPaidFallbackOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, paidFallbackOnlyKey{}, true)
}

func (s *SettingService) paidFallbackAllowed(ctx context.Context, groupID *int64) bool {
	if groupID == nil {
		return false
	}
	keyID, _ := ctx.Value(paidFallbackAPIKeyKey{}).(int64)
	settings, err := s.GetPaidFallbackSettings(ctx)
	return err == nil && settings.Enabled && slices.Contains(settings.GroupIDs, *groupID) && slices.Contains(settings.APIKeyIDs, keyID)
}

func paidFallbackAccountInPool(ctx context.Context, account *Account) bool {
	permit, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
	return account != nil && account.IsPaidFallback() == (permit != nil)
}

func filterPaidFallbackPool(ctx context.Context, accounts []Account) []Account {
	first := 0
	for first < len(accounts) && paidFallbackAccountInPool(ctx, &accounts[first]) {
		first++
	}
	if first == len(accounts) {
		return accounts
	}
	filtered := make([]Account, first, len(accounts))
	copy(filtered, accounts[:first])
	for i := first + 1; i < len(accounts); i++ {
		if paidFallbackAccountInPool(ctx, &accounts[i]) {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered
}

func ContextWithPaidFallbackSelection(ctx context.Context, selection *AccountSelectionResult) context.Context {
	if selection != nil {
		current, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
		if current == selection.paidFallbackPermit {
			return ctx
		}
		return context.WithValue(ctx, paidFallbackPermitKey{}, selection.paidFallbackPermit)
	}
	return ctx
}

func paidFallbackAdmissionFailure(ctx context.Context, account *Account) string {
	permit, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
	if account == nil {
		return ""
	}
	if !account.IsPaidFallback() {
		if permit != nil {
			return "paid_fallback_account_changed"
		}
		return ""
	}
	if paidFallbackInvalidRate(account) {
		return "paid_fallback_invalid_account_rate"
	}
	if permit == nil || permit.accountID != account.ID {
		return "paid_fallback_unauthorized"
	}
	checkCtx := WithPaidFallbackAPIKey(ctx, permit.apiKeyID)
	if !permit.settings.paidFallbackAllowed(checkCtx, &permit.groupID) {
		return "paid_fallback_disabled"
	}
	return ""
}
