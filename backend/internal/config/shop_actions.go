package config

import (
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

func (c *Config) validateShopActions() error {
	seen := make(map[string]bool, len(c.ShopActions.Integrations))
	for _, i := range c.ShopActions.Integrations {
		if shopaction.ValidateIntegration(i) != nil {
			return fmt.Errorf("shop_actions: invalid integration %q version %d", i.ID, i.Version)
		}
		key := fmt.Sprintf("%s@%d", i.ID, i.Version)
		if seen[key] {
			return fmt.Errorf("shop_actions: duplicate integration version %q", key)
		}
		seen[key] = true
	}
	if c.ShopActions.Enabled && len(c.ShopActions.Integrations) == 0 {
		return fmt.Errorf("shop_actions: register an integration before enabling")
	}
	return nil
}
