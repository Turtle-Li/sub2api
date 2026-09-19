package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentDiscountScopeMigrationRetainsLegacyEligibilityAndBoundsNewScopes(t *testing.T) {
	content, err := FS.ReadFile("254_payment_discount_scope.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS order_types JSONB NOT NULL DEFAULT '[\"balance\", \"subscription\"]'::jsonb")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS plan_ids JSONB NOT NULL DEFAULT '[]'::jsonb")
	require.Contains(t, sql, "payment_discount_codes_order_types_valid")
	require.Contains(t, sql, "jsonb_array_length(order_types) > 0")
	require.Contains(t, sql, "order_types <@ '[\"balance\", \"subscription\"]'::jsonb")
	require.Contains(t, sql, "payment_discount_codes_plan_ids_valid")
	require.Contains(t, sql, "jsonb_array_length(plan_ids) <= 100")
	require.Contains(t, sql, "order_types ? 'subscription'")
	require.NotContains(t, sql, "reset_card")
	require.NotContains(t, sql, "DROP COLUMN")
}
