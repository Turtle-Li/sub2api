//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRateLimitModelAliasesPreservesMappedAndOriginalNames(t *testing.T) {
	require.Equal(
		t,
		[]string{"claude-fable-5", "cheap-alias"},
		rateLimitModelAliases("claude-fable-5", "cheap-alias"),
	)
	require.Equal(t, []string{"claude-fable-5"}, rateLimitModelAliases("claude-fable-5", "CLAUDE-FABLE-5"))
}
