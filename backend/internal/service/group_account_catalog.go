package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// Administrative discovery is scoped to the persisted group membership. A
// temporary cooldown is not a removal, and a lookup failure is not an empty pool.
func groupCatalogAccounts(ctx context.Context, repo AccountRepository, group *Group, platform string) ([]Account, error) {
	if repo == nil {
		return nil, errors.New("group account catalog unavailable")
	}
	platforms := domain.GroupAccountPlatforms(platform)
	if platform == PlatformComposite {
		platforms = domain.ConcretePlatformIDs()
	} else if catalogAllowsMixedAntigravity(platform) {
		platforms = append(platforms, PlatformAntigravity)
	}
	accounts, err := repo.ListModelAvailabilityCandidates(ctx, &group.ID, platforms, false)
	if err != nil {
		return nil, fmt.Errorf("list account catalog for group %d: %w", group.ID, err)
	}
	now := time.Now()
	pool := make([]Account, 0, len(accounts))
	requireOAuth := group.RequireOAuthOnly && groupSupportsOAuthOnlyFilter(group.Platform)
	for i := range accounts {
		a := accounts[i]
		if eligibleDisplayAccount(&a, now) && slices.Contains(platforms, a.Platform) &&
			(!group.RequirePrivacySet || a.IsPrivacySet()) && (!requireOAuth || a.Type != AccountTypeAPIKey) {
			a.PrepareSharedSnapshot() // Reuse the resolved mapping across all names in this read.
			pool = append(pool, a)
		}
	}
	return pool, nil
}

func catalogAllowsMixedAntigravity(platform string) bool {
	return platform == PlatformAnthropic || platform == PlatformGemini
}

func concreteAccountModel(name string) bool {
	return name != "" && !strings.Contains(name, "*") && strings.IndexFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) == -1
}

func catalogAccountSupports(a *Account, platform, name string) bool {
	if IsMuseMediaModel(name) {
		capability := OpenAIEndpointCapabilityMuseImage
		if name == MuseVideoModel {
			capability = OpenAIEndpointCapabilityMuseVideo
		}
		return (platform == PlatformOpenAI || platform == PlatformComposite) && a.SupportsOpenAIEndpointCapability(capability)
	}
	if a.IsMuseMediaOnly() {
		return false
	}
	if a.Platform == PlatformAntigravity && catalogAllowsMixedAntigravity(platform) {
		family, known := DetectModelPlatform(a.GetMappedModel(name))
		if !a.IsMixedSchedulingEnabled() || !known || family != platform {
			return false
		}
	} else if !a.SupportsGroupPlatformModel(platform, name) {
		return false
	}
	if domain.IsModelProviderPlatform(a.Platform) && len(a.GetModelMapping()) == 0 {
		// An unfiltered native relay is not evidence for another vendor's
		// family, even when an old allowlist or price card still names it.
		if family, known := DetectModelPlatform(name); known && family != a.Platform {
			return false
		}
	}
	if a.Platform == PlatformOpenAI {
		return a.IsModelSupported(name)
	}
	return gatewayAccountSupportsModel(a, name)
}

func catalogAccountNames(a *Account) []string {
	// Passthrough may retain mappings from its former allowlist mode. They
	// aren't a live model catalog. Native Muse media admission is independent.
	if !a.IsOpenAIPassthroughEnabled() || a.IsOpenAIPassthroughModelFilterEnabled() || a.IsMuseMediaOnly() {
		if mapping := a.GetModelMapping(); len(mapping) > 0 {
			return slices.Collect(maps.Keys(mapping))
		}
	}
	if a.IsBedrock() {
		return slices.Collect(maps.Keys(domain.DefaultBedrockModelMapping))
	}
	// Only native account types with a known finite catalogue may supply
	// implicit names. An unrestricted relay has no enumerable model list.
	switch a.Platform {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini:
		if a.IsOAuth() {
			return defaultModelsListCandidateIDs(a.Platform)
		}
	case PlatformTypeSafe, PlatformOpenCodeGo:
		return defaultModelsListCandidateIDs(a.Platform)
	case PlatformDeepseek:
		return deepseekServableModels
	}
	return nil
}

func groupAccountModelNames(accounts []Account, platform string) []string {
	var names []string
	for i := range accounts {
		a := &accounts[i]
		for _, name := range catalogAccountNames(a) {
			if concreteAccountModel(name) && catalogAccountSupports(a, platform, name) {
				names = append(names, name)
			}
		}
	}
	return dedupeAndSortModelIDs(names)
}
