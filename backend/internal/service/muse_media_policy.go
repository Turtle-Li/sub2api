package service

import "strings"

const legacyMuseTextModel = "muse-spark"

func IsMuseMediaModel(model string) bool {
	return model == MuseImageModel || model == MuseVideoModel
}

// IsMuseMediaOnly identifies the dedicated OpenAI-compatible Muse group from
// its configured allowlist, including groups that still list the retired text model.
func (g *Group) IsMuseMediaOnly() bool {
	if g == nil || g.Platform != PlatformOpenAI || !g.ModelAllowlistEnabled() || len(g.ModelAllowlist.Models) == 0 {
		return false
	}
	for _, model := range g.ModelAllowlist.Models {
		model = strings.ToLower(strings.TrimSpace(model))
		if model != legacyMuseTextModel && !IsMuseMediaModel(model) {
			return false
		}
	}
	return true
}

// IsMuseMediaOnly uses the same explicit identity mappings as native media
// admission. Request model names and group memberships do not affect it.
func (a *Account) IsMuseMediaOnly() bool {
	models := a.museNativeModelMapping()
	return models[MuseImageModel] == MuseImageModel || models[MuseVideoModel] == MuseVideoModel
}

func (a *Account) museNativeModelMapping() map[string]any {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return nil
	}
	// OpenAI API-key identity mappings come directly from credentials. Check
	// those two entries without populating the mutable model-mapping cache.
	models, _ := a.Credentials["model_mapping"].(map[string]any)
	if len(models) == 0 || strings.TrimSpace(a.GetCredential("base_url")) == "" {
		return nil
	}
	return models
}
