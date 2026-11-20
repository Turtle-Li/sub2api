package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyCCSwitchCodexModel = "ccswitch_codex_model"
const DefaultCCSwitchCodexModel = "gpt-6.2-sol"

func normalizeCCSwitchCodexModel(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultCCSwitchCodexModel, nil
	}
	valid := len(value) <= 100
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._:/", char)) {
			valid = false
			break
		}
	}
	if !valid {
		return "", infraerrors.BadRequest("INVALID_CC_SWITCH_CODEX_MODEL", "CC Switch Codex model must be at most 100 characters using letters, numbers, hyphens, dots, underscores, colons or slashes")
	}
	return value, nil
}
