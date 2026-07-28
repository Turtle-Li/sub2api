//go:build unit

package dto

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemSettingsContractExcludesDesktopConsoleFields(t *testing.T) {
	systemSettingsType := reflect.TypeOf(SystemSettings{})
	jsonFields := make(map[string]struct{}, systemSettingsType.NumField())
	for index := 0; index < systemSettingsType.NumField(); index++ {
		jsonFields[systemSettingsType.Field(index).Tag.Get("json")] = struct{}{}
	}

	_, hasControlPlane := jsonFields["desktop_control_plane_url"]
	_, hasPromotions := jsonFields["desktop_promotions"]
	_, hasUpdatePolicy := jsonFields["desktop_update_policy"]
	require.False(t, hasControlPlane)
	require.False(t, hasPromotions)
	require.False(t, hasUpdatePolicy)
}
