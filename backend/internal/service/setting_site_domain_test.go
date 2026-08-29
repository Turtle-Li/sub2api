package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type missingSiteDomainRepo struct{}

func (missingSiteDomainRepo) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (missingSiteDomainRepo) GetValue(context.Context, string) (string, error) {
	return "", ErrSettingNotFound
}

func (missingSiteDomainRepo) Set(context.Context, string, string) error { return nil }

func (missingSiteDomainRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (missingSiteDomainRepo) SetMultiple(context.Context, map[string]string) error { return nil }

func (missingSiteDomainRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

func (missingSiteDomainRepo) Delete(context.Context, string) error { return nil }

func TestNormalizeSiteDomain(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "empty", value: "  ", want: ""},
		{name: "domain", value: " ai.example.com ", want: "ai.example.com"},
		{name: "domain is lowercased and trailing slash removed", value: " AI.EXAMPLE.COM/ ", want: "ai.example.com"},
		{name: "domain with port", value: "localhost:8080", want: "localhost:8080"},
		{name: "protocol is rejected", value: "https://ai.example.com", wantErr: true},
		{name: "path is rejected", value: "ai.example.com/app", wantErr: true},
		{name: "query is rejected", value: "ai.example.com?x=1", wantErr: true},
		{name: "userinfo is rejected", value: "user@ai.example.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeSiteDomain(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGetSiteDomainMissingSettingDisablesEnforcement(t *testing.T) {
	service := NewSettingService(missingSiteDomainRepo{}, &config.Config{})

	domain, err := service.GetSiteDomain(context.Background())

	require.NoError(t, err)
	require.Empty(t, domain)
}
