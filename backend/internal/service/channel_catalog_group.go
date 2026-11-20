package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const featureKeyClaudeDisplayModels = "claude_display_models_by_group"

func platformDisplayFeature(platform string) string {
	if platform == PlatformAnthropic {
		return featureKeyClaudeDisplayModels
	}
	return platform + "_account_display_models_by_group"
}

type ChannelDisplayPreview struct {
	ModelsByGroup         map[int64][]string `json:"models_by_group"`
	PreviousModelsByGroup map[int64][]string `json:"previous_models_by_group"`
}

// A preview only owns the groups it actually read. An absent group is not an
// instruction to erase a pinned, inactive or detached group's saved display.
func (ch *Channel) applyDisplayModelUpdates(updates map[string]map[int64][]string) error {
	if len(updates) == 0 {
		return nil
	}
	features := maps.Clone(ch.FeaturesConfig)
	if features == nil {
		features = make(map[string]any)
	}
	for platform, patch := range updates {
		if !domain.IsConcretePlatform(platform) || ch.RestrictModels {
			return infraerrors.BadRequest("INVALID_DISPLAY_UPDATE", "display sync requires a registered platform and an unrestricted channel")
		}
		if len(patch) == 0 {
			continue
		}
		key := platformDisplayFeature(platform)
		groups := ch.displayModelsByGroup(key)
		if groups == nil {
			groups = make(map[int64][]string)
		}
		for id, names := range patch {
			if names == nil || !slices.Contains(ch.GroupIDs, id) {
				return infraerrors.BadRequest("INVALID_DISPLAY_UPDATE", "each updated group must be linked and have an explicit model list")
			}
			groups[id] = names // [] is authoritative: hide even manually listed rows.
		}
		features[key] = groups
	}
	ch.FeaturesConfig = features
	return nil
}

// The older per-provider admin endpoint is still supported.
func (s *ChannelService) PreviewClaudeDisplayModels(ctx context.Context, draft *Channel, channelID int64) (ChannelDisplayPreview, error) {
	return s.PreviewPlatformDisplayModels(ctx, draft, channelID, PlatformAnthropic)
}

func (s *ChannelService) PreviewPlatformDisplayModels(ctx context.Context, draft *Channel, channelID int64, platform string) (ChannelDisplayPreview, error) {
	if !domain.IsConcretePlatform(platform) {
		return ChannelDisplayPreview{}, infraerrors.BadRequest("UNSUPPORTED_PLATFORM", "select a registered platform")
	}
	if err := ctx.Err(); err != nil {
		return ChannelDisplayPreview{}, err
	}
	preview := ChannelDisplayPreview{ModelsByGroup: make(map[int64][]string), PreviousModelsByGroup: make(map[int64][]string)}
	if draft.RestrictModels || len(draft.GroupIDs) == 0 {
		return preview, nil
	}
	if s.modelCatalog == nil || s.modelCatalog.accounts == nil {
		return ChannelDisplayPreview{}, infraerrors.ServiceUnavailable("DISPLAY_PREVIEW_UNAVAILABLE", "group account catalog unavailable")
	}
	if err := validateNoConflictingMappings(draft.ModelMapping); err != nil {
		return ChannelDisplayPreview{}, err
	}
	var previous, legacyGPT map[int64][]string
	var persisted *Channel
	if channelID > 0 {
		channel, err := s.GetByID(ctx, channelID)
		if err != nil {
			return ChannelDisplayPreview{}, err
		}
		previous = channel.displayModelsByGroup(platformDisplayFeature(platform))
		if platform == PlatformOpenAI {
			persisted = channel
			legacyGPT = channel.gptDisplayModelsByGroup()
		}
	}
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return ChannelDisplayPreview{}, fmt.Errorf("list groups for display preview: %w", err)
	}
	manual := draft.SupportedModels()
	for i := range groups {
		g := &groups[i]
		if !slices.Contains(draft.GroupIDs, g.ID) || !canSyncPlatform(g, platform) {
			continue
		}
		before := previous[g.ID]
		if before == nil {
			before = platformDisplayNames(draft, platform, manual)
			if len(legacyGPT[g.ID]) > 0 {
				extra := s.modelCatalog.savedModels(persisted, g, manual, legacyGPT[g.ID])
				before = dedupeAndSortModelIDs(slices.Concat(before, platformDisplayNames(draft, platform, extra)))
			}
		}
		accounts, err := groupCatalogAccounts(ctx, s.modelCatalog.accounts, g, platform)
		if err != nil {
			return ChannelDisplayPreview{}, err // Never apply a partially read snapshot.
		}
		preview.PreviousModelsByGroup[g.ID] = before
		preview.ModelsByGroup[g.ID] = s.modelCatalog.previewPlatformGroup(draft, g, platform, accounts, manual)
	}
	if err := ctx.Err(); err != nil {
		return ChannelDisplayPreview{}, err
	}
	return preview, nil
}

func canSyncPlatform(g *Group, platform string) bool {
	return (g.Platform == PlatformComposite || g.Platform == platform) &&
		!(g.Platform == PlatformOpenAI && g.CodexModelsManifestConfig.Enabled)
}

func platformDisplayNames(ch *Channel, platform string, models []SupportedModel) []string {
	names := make([]string, 0, len(models))
	for _, m := range models {
		if m.Platform == platform && !ch.IsModelHiddenFromPlaza(m.Name) {
			names = append(names, m.Name)
		}
	}
	return names
}

func groupAllowsDisplayName(ch *Channel, g *Group, platform, name string) bool {
	return concreteAccountModel(name) && !ch.IsModelHiddenFromPlaza(name) && g.ModelAllowlist.Allows(name) &&
		(!g.IsMuseMediaOnly() || IsMuseMediaModel(name)) &&
		(platform != PlatformOpenAI || !IsGPTImageGenerationModel(name) || g.AllowImageGeneration)
}

func (s *ChannelModelCatalog) previewPlatformGroup(ch *Channel, g *Group, platform string, accounts []Account, manual []SupportedModel) []string {
	// A known public alias may be admitted by a wildcard, but a wildcard is
	// not a model to publish. Keep request-side names, not upstream targets.
	manualNames := platformDisplayNames(ch, platform, manual)
	configured := make(map[string]bool, len(manualNames))
	for _, name := range manualNames {
		configured[strings.ToLower(name)] = true
	}
	names := slices.Concat(manualNames, groupAccountModelNames(accounts, platform))
	if g.ModelAllowlist.Enabled {
		names = append(names, g.ModelAllowlist.Models...)
	}
	mapping := newEmptyChannelCache()
	expandMappingToCache(mapping, ch, g.ID, g.Platform)
	current := make([]string, 0, len(names))
	seen := make(map[string]bool)
	for _, name := range names {
		lower := strings.ToLower(name)
		if seen[lower] || !groupAllowsDisplayName(ch, g, platform, name) {
			continue
		}
		seen[lower] = true
		inferredOpenAI := platform == PlatformOpenAI && !configured[lower]
		if inferredOpenAI && strings.HasPrefix(lower, "gpt-") {
			if _, ok := s.inferredGPTModel(ch, g, name); !ok {
				continue
			}
		}
		mapped := lookupMappingAcrossPlatforms(mapping, g.ID, g.Platform, lower)
		if mapped == "" {
			mapped = name
		}
		if name != mapped && (IsMuseMediaModel(name) || IsMuseMediaModel(mapped)) {
			continue // Muse endpoints admit exact public identities, not chat aliases.
		}
		if platform == PlatformOpenAI && !g.AllowImageGeneration && IsGPTImageGenerationModel(mapped) {
			continue
		}
		for i := range accounts {
			a := &accounts[i]
			if inferredOpenAI && a.Platform == PlatformOpenAI &&
				(strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(a.GetMappedModel(mapped), "gpt-")) && !nativeGPTDisplayIdentity(a, name) {
				continue // Native GPT discovery publishes declared identities, not inferred aliases.
			}
			if catalogAccountSupports(a, platform, mapped) {
				current = append(current, name)
				break
			}
		}
	}
	slices.Sort(current)
	return current
}

func nativeGPTDisplayIdentity(a *Account, name string) bool {
	target, present := a.GetModelMapping()[name]
	if a.IsOpenAIPassthroughEnabled() {
		// Filtering uses source names only; passthrough never forwards the mapped target.
		return a.IsOpenAIPassthroughModelFilterEnabled() && present
	}
	return present && name == target
}

type platformDisplaySnapshots map[string]map[int64][]string

func (ch *Channel) platformDisplaySnapshots() platformDisplaySnapshots {
	var snapshots platformDisplaySnapshots
	for _, platform := range domain.ConcretePlatformIDs() {
		if entries := ch.displayModelsByGroup(platformDisplayFeature(platform)); len(entries) > 0 {
			if snapshots == nil {
				snapshots = make(platformDisplaySnapshots)
			}
			snapshots[platform] = entries
		}
	}
	return snapshots
}

// nil means no applicable snapshot; an empty slice deliberately hides stale
// manual rows. Composite groups retain manual rows on their unsynced tabs.
func (s *ChannelModelCatalog) projectGroupDisplay(ch *Channel, g *Group, manual []SupportedModel, snapshots platformDisplaySnapshots) []SupportedModel {
	if s == nil || ch.RestrictModels || len(snapshots) == 0 {
		return nil
	}
	var models []SupportedModel
	for platform, groups := range snapshots {
		names := groups[g.ID]
		if names == nil || !canSyncPlatform(g, platform) {
			continue
		}
		if models == nil {
			models = []SupportedModel{}
		}
		byName := make(map[string]SupportedModel)
		for _, m := range manual {
			if m.Platform == platform {
				byName[strings.ToLower(m.Name)] = m
			}
		}
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			lower := strings.ToLower(name)
			if seen[lower] || !groupAllowsDisplayName(ch, g, platform, name) {
				continue
			}
			seen[lower] = true
			m, ok := byName[lower]
			if !ok && platform == PlatformOpenAI && strings.HasPrefix(lower, "gpt-") {
				m, ok = s.inferredGPTModel(ch, g, name)
				if !ok {
					continue
				}
			} else if !ok {
				m = SupportedModel{Name: name, Platform: platform, CreditsGated: isAnthropicFableModel(name)}
				if !obviousNonTextProbeModel(name) && !IsMuseMediaModel(name) {
					m.Pricing = s.textDisplayPrice(ch, platform, name)
				}
			}
			if platform == PlatformOpenAI && !g.AllowImageGeneration && IsGPTImageGenerationModel(m.ProbeModelName) {
				continue
			}
			models = append(models, m)
		}
	}
	if models != nil && g.Platform == PlatformComposite {
		for _, m := range manual {
			if snapshots[m.Platform][g.ID] == nil {
				models = append(models, m)
			}
		}
	}
	slices.SortFunc(models, func(a, b SupportedModel) int {
		if a.Name == b.Name {
			return strings.Compare(a.Platform, b.Platform)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return models
}
