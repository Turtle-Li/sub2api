package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const PaidFallbackExtraKey = "paid_fallback"
const settingKeyPaidFallback = "paid_fallback_settings"

var errPaidFallbackPoolMismatch = errors.New("account belongs to another fallback pool")
var ErrPaidFallbackRestartRequired = errors.New("paid fallback is no longer eligible; start a new request with full conversation history and without previous_response_id")

func validatePaidFallbackExtra(platform string, incoming, existing map[string]any) error {
	value, exists := incoming[PaidFallbackExtraKey]
	if !exists {
		return nil
	}
	if old, present := existing[PaidFallbackExtraKey]; present && reflect.DeepEqual(old, value) {
		return nil
	}
	enabled, ok := value.(bool)
	if !ok {
		return infraerrors.BadRequest("PAID_FALLBACK_INVALID", "paid_fallback must be a boolean")
	}
	if enabled && !domain.UsesOpenAIGateway(platform) {
		return infraerrors.BadRequest("PAID_FALLBACK_PLATFORM_INVALID", "paid fallback requires an OpenAI-compatible account")
	}
	return nil
}

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
	Enabled  bool    `json:"enabled"`
	GroupIDs []int64 `json:"group_ids"`
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
	if len(settings.GroupIDs) > 1000 {
		return errors.New("paid fallback group scope exceeds 1000 entries")
	}
	for _, id := range settings.GroupIDs {
		if id <= 0 {
			return errors.New("paid fallback groups require positive IDs")
		}
	}
	if settings.Enabled && len(settings.GroupIDs) == 0 {
		return errors.New("enabled paid fallback requires at least one group")
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

func (s *SettingService) DisablePaidFallback(ctx context.Context) (*PaidFallbackSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("paid fallback settings unavailable")
	}
	raw, err := s.settingRepo.GetValue(ctx, settingKeyPaidFallback)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, fmt.Errorf("read paid fallback settings: %w", err)
	}
	settings := &PaidFallbackSettings{}
	if raw != "" && json.Unmarshal([]byte(raw), settings) != nil {
		settings = &PaidFallbackSettings{}
	}
	settings.Enabled = false
	if validatePaidFallbackSettings(settings) != nil {
		settings = &PaidFallbackSettings{}
	}
	if err := s.UpdatePaidFallbackSettings(ctx, settings); err != nil {
		return nil, err
	}
	return settings, nil
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
// header. It identifies the caller for auditing; policy applies to every key in an enabled group.
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
	return err == nil && keyID > 0 && settings.Enabled && slices.Contains(settings.GroupIDs, *groupID)
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
