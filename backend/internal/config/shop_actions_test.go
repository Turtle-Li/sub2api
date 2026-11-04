package config

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"github.com/stretchr/testify/require"
)

func TestShopActionDeploymentConfiguration(t *testing.T) {
	public := shopaction.Integration{ID: "license", Version: 1, Adapter: shopaction.HTTP, Endpoint: "https://hooks.example.com/issue", CredentialAlias: "license", Enabled: true}
	native := shopaction.Integration{ID: "newapi-domestic", Version: 1, Adapter: shopaction.Native, Endpoint: "http://100.64.0.1:17867", Enabled: true}
	for _, tc := range []struct {
		name  string
		cfg   shopaction.Config
		valid bool
	}{
		{"disabled by default", shopaction.Config{}, true},
		{"cannot enable empty registry", shopaction.Config{Enabled: true}, false},
		{"public HTTP and explicit native tailnet", shopaction.Config{Enabled: true, Integrations: []shopaction.Integration{public, native}}, true},
		{"duplicate identity version", shopaction.Config{Integrations: []shopaction.Integration{public, public}}, false},
		{"unrecognized adapter", shopaction.Config{Integrations: []shopaction.Integration{{ID: "license", Version: 1, Adapter: "arbitrary.script.v1", Endpoint: public.Endpoint}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Config{ShopActions: tc.cfg}).validateShopActions()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
