package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type paidFallbackSettingRepo struct {
	SettingRepository
	value string
	err   error
}

func (r *paidFallbackSettingRepo) GetValue(context.Context, string) (string, error) {
	return r.value, r.err
}
func (r *paidFallbackSettingRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return r.err
}

func paidFallbackTestSettings(t *testing.T) (*SettingService, *paidFallbackSettingRepo) {
	t.Helper()
	value, err := json.Marshal(PaidFallbackSettings{Enabled: true, GroupIDs: []int64{7}})
	require.NoError(t, err)
	repo := &paidFallbackSettingRepo{value: string(value)}
	return NewSettingService(repo, nil), repo
}

func TestPaidFallbackMarkerFailClosed(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{
		{nil, true}, {false, false}, {true, true}, {"false", true}, {0, true},
	} {
		account := &Account{Extra: map[string]any{PaidFallbackExtraKey: tc.value}}
		require.Equal(t, tc.want, account.IsPaidFallback())
	}
	require.False(t, (&Account{}).IsPaidFallback())
}

func TestPaidFallbackScopeAndKillSwitch(t *testing.T) {
	settings, repo := paidFallbackTestSettings(t)
	ctx := WithPaidFallbackAPIKey(context.Background(), 11)
	group := int64(7)
	require.True(t, settings.paidFallbackAllowed(ctx, &group))
	require.True(t, settings.paidFallbackAllowed(WithPaidFallbackAPIKey(ctx, 999), &group))
	require.False(t, settings.paidFallbackAllowed(ctx, nil))
	require.False(t, settings.paidFallbackAllowed(context.Background(), &group))
	otherGroup := int64(8)
	require.False(t, settings.paidFallbackAllowed(ctx, &otherGroup))
	permit := &paidFallbackPermit{settings: settings, groupID: 7, apiKeyID: 11, accountID: 23}
	paid := &Account{ID: 23, Extra: map[string]any{PaidFallbackExtraKey: true}}
	ctx = context.WithValue(ctx, paidFallbackPermitKey{}, permit)
	require.Empty(t, paidFallbackAdmissionFailure(ctx, paid))
	repo.value = `{"enabled":false}`
	require.Equal(t, "paid_fallback_disabled", paidFallbackAdmissionFailure(ctx, paid))
	for _, value := range []string{"", "{", `{"enabled":true}`, `{"enabled":true,"group_ids":[-7]}`} {
		repo.value = value
		require.False(t, settings.paidFallbackAllowed(ctx, &group))
	}
}

func TestPaidFallbackPoolIsExclusive(t *testing.T) {
	normal := Account{ID: 1}
	paid := Account{ID: 2, Extra: map[string]any{PaidFallbackExtraKey: true}}
	ctx := context.Background()
	require.Equal(t, []Account{normal}, filterPaidFallbackPool(ctx, []Account{normal, paid}))
	ctx = context.WithValue(ctx, paidFallbackPermitKey{}, &paidFallbackPermit{})
	require.Equal(t, []Account{paid}, filterPaidFallbackPool(ctx, []Account{normal, paid}))
}

func TestPaidFallbackFailureEvidence(t *testing.T) {
	for _, code := range []int{400, 404, 408, 422, 499} {
		ctx := WithPaidFallbackFailure(context.Background(), &Account{ID: 2}, code)
		require.False(t, paidFallbackFailedAccount(ctx, 2))
	}
	ctx := WithPaidFallbackFailure(context.Background(), &Account{ID: 2}, 503)
	require.True(t, paidFallbackFailedAccount(ctx, 2))
	ctx = WithPaidFallbackFailure(ctx, &Account{ID: 3}, 429)
	require.True(t, paidFallbackFailedAccount(ctx, 2))
	require.True(t, paidFallbackFailedAccount(ctx, 3))
	ctx = WithPaidFallbackFailure(ctx, &Account{ID: 4, Extra: map[string]any{PaidFallbackExtraKey: true}}, 503)
	require.False(t, paidFallbackFailedAccount(ctx, 4))
}

type paidFallbackDisableRepo struct {
	SettingRepository
	value   string
	readErr error
	writes  int
}

func (r *paidFallbackDisableRepo) GetValue(context.Context, string) (string, error) {
	return r.value, r.readErr
}

func (r *paidFallbackDisableRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	r.writes++
	return nil
}

func TestPaidFallbackImmediateDisablePreservesValidScope(t *testing.T) {
	repo := &paidFallbackDisableRepo{value: `{"enabled":true,"group_ids":[7,8],"api_key_ids":[11,12]}`}
	s := NewSettingService(repo, nil)
	saved, err := s.DisablePaidFallback(context.Background())
	require.NoError(t, err)
	require.False(t, saved.Enabled)
	require.Equal(t, []int64{7, 8}, saved.GroupIDs)
	require.NotContains(t, repo.value, "api_key_ids")
	group := int64(7)
	require.False(t, s.paidFallbackAllowed(WithPaidFallbackAPIKey(context.Background(), 11), &group))
}

func TestPaidFallbackLegacyKeyScopeDoesNotRestrictGroupUsers(t *testing.T) {
	repo := &paidFallbackSettingRepo{value: `{"enabled":true,"group_ids":[7],"api_key_ids":[11]}`}
	s := NewSettingService(repo, nil)
	group := int64(7)
	require.True(t, s.paidFallbackAllowed(WithPaidFallbackAPIKey(context.Background(), 12), &group))
}

func TestPaidFallbackImmediateDisableRepairsInvalidConfig(t *testing.T) {
	for _, raw := range []string{`{`, `{"enabled":true,"group_ids":[-7],"api_key_ids":[11]}`, `{"enabled":true}`} {
		t.Run(raw, func(t *testing.T) {
			repo := &paidFallbackDisableRepo{value: raw}
			s := NewSettingService(repo, nil)
			saved, err := s.DisablePaidFallback(context.Background())
			require.NoError(t, err)
			require.False(t, saved.Enabled)
			loaded, err := s.GetPaidFallbackSettings(context.Background())
			require.NoError(t, err)
			require.False(t, loaded.Enabled)
		})
	}
}

func TestPaidFallbackImmediateDisableDoesNotOverwriteOnReadFailure(t *testing.T) {
	raw := `{"enabled":true,"group_ids":[7],"api_key_ids":[11]}`
	repo := &paidFallbackDisableRepo{value: raw, readErr: errors.New("dependency unavailable")}
	_, err := NewSettingService(repo, nil).DisablePaidFallback(context.Background())
	require.Error(t, err)
	require.Equal(t, raw, repo.value)
	require.Zero(t, repo.writes)
}
