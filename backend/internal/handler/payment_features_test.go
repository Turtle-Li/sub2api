//go:build unit

package handler

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestParseFeaturesStoredFormats(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"catalog JSON", `[" 有效订阅可另购重置卡 ", "重置卡不延长订阅有效期", ""]`, []string{"有效订阅可另购重置卡", "重置卡不延长订阅有效期"}},
		{"legacy lines", " first\n\nsecond ", []string{"first", "second"}},
		{"empty", "", []string{}},
		{"JSON null", "null", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, parseFeatures(tc.raw)) })
	}
}
