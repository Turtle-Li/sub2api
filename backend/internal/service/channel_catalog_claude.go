package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const featureKeyClaudeDisplayModels = "claude_display_models_by_group"

type ClaudeDisplayPreview struct {
	ModelsByGroup         map[int64][]string `json:"models_by_group"`
	PreviousModelsByGroup map[int64][]string `json:"previous_models_by_group"`
}

// PreviewClaudeDisplayModels reconciles the proposed display against the account
// configuration of each selected Anthropic group, without changing the channel.
func (s *ChannelService) PreviewClaudeDisplayModels(ctx context.Context, draft *Channel, channelID int64) (ClaudeDisplayPreview, error) {
	if err := ctx.Err(); err != nil {
		return ClaudeDisplayPreview{}, err
	}
	preview := ClaudeDisplayPreview{ModelsByGroup: make(map[int64][]string), PreviousModelsByGroup: make(map[int64][]string)}
	if draft.RestrictModels || len(draft.GroupIDs) == 0 {
		return preview, nil
	}
	if s.modelCatalog == nil || s.modelCatalog.accounts == nil {
		return ClaudeDisplayPreview{}, infraerrors.ServiceUnavailable("CLAUDE_DISPLAY_PREVIEW_UNAVAILABLE", "Claude display model preview unavailable")
	}
	if err := validateNoConflictingMappings(draft.ModelMapping); err != nil {
		return ClaudeDisplayPreview{}, err
	}
	var previous map[int64][]string
	if channelID > 0 {
		channel, err := s.GetByID(ctx, channelID)
		if err != nil {
			return ClaudeDisplayPreview{}, err
		}
		previous = channel.displayModelsByGroup(featureKeyClaudeDisplayModels)
	}
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return ClaudeDisplayPreview{}, fmt.Errorf("list active groups for Claude display preview: %w", err)
	}
	manual := draft.SupportedModels()
	for i := range groups {
		group := &groups[i]
		if group.Platform != PlatformAnthropic || !slices.Contains(draft.GroupIDs, group.ID) {
			continue
		}
		saved := previous[group.ID]
		after, err := s.modelCatalog.previewClaudeGroup(ctx, draft, group, manual, saved)
		if err != nil {
			return ClaudeDisplayPreview{}, err
		}
		if saved == nil {
			saved = claudeDisplayNames(draft, manual)
		}
		preview.ModelsByGroup[group.ID] = after
		preview.PreviousModelsByGroup[group.ID] = saved
	}
	if err := ctx.Err(); err != nil {
		return ClaudeDisplayPreview{}, err
	}
	return preview, nil
}

func claudeDisplayNames(ch *Channel, models []SupportedModel) []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		if model.Platform == PlatformAnthropic && !ch.IsModelHiddenFromPlaza(model.Name) {
			names = append(names, model.Name)
		}
	}
	return names
}

func (s *ChannelModelCatalog) previewClaudeGroup(ctx context.Context, ch *Channel, group *Group, manual []SupportedModel, saved []string) ([]string, error) {
	platforms := append(domain.GroupAccountPlatforms(group.Platform), PlatformAntigravity)
	accounts, err := s.accounts.ListModelAvailabilityCandidates(ctx, &group.ID, platforms, false)
	if err != nil {
		return nil, fmt.Errorf("list Claude display candidates for group %d: %w", group.ID, err)
	}

	// Known rows include manual aliases and pricing-only entries. A wildcard or
	// unrestricted account can admit these names, but does not invent new ones.
	candidates := make(map[string]string)
	for _, name := range saved {
		if isConcreteCatalogID(name, "claude-") {
			candidates[strings.ToLower(name)] = name
		}
	}
	for _, name := range claudeDisplayNames(ch, manual) {
		candidates[strings.ToLower(name)] = name
	}
	pool := make([]*Account, 0, len(accounts))
	now := time.Now()
	for i := range accounts {
		account := &accounts[i]
		if !eligibleDisplayAccount(account, now) || (group.RequirePrivacySet && !account.IsPrivacySet()) ||
			!claudeDisplayAccountAllowed(account) {
			continue
		}
		pool = append(pool, account)
		for name := range account.GetModelMapping() {
			if _, shown := candidates[strings.ToLower(name)]; !shown && isConcreteCatalogID(name, "claude-") {
				candidates[name] = name
			}
		}
	}

	mapping := newEmptyChannelCache()
	expandMappingToCache(mapping, ch, group.ID, group.Platform)
	current := make([]string, 0, len(candidates))
	for lower, name := range candidates {
		if name == "" || ch.IsModelHiddenFromPlaza(name) || !group.ModelAllowlist.Allows(name) {
			continue
		}
		mapped := lookupMappingAcrossPlatforms(mapping, group.ID, group.Platform, lower)
		if mapped == "" {
			mapped = name
		}
		for _, account := range pool {
			if domain.IsMultiModelPlatform(account.Platform) && !account.SupportsGroupPlatformModel(group.Platform, mapped) {
				continue
			}
			if gatewayAccountSupportsModel(account, mapped) {
				current = append(current, name)
				break
			}
		}
	}
	slices.Sort(current)
	return current, nil
}

func claudeDisplayAccountAllowed(account *Account) bool {
	switch account.Platform {
	case PlatformAnthropic:
		return true
	case PlatformAntigravity:
		return account.IsMixedSchedulingEnabled()
	default:
		return domain.IsMultiModelPlatform(account.Platform) && account.Type == AccountTypeAPIKey
	}
}

// projectClaudeDisplay replaces this group's display only. A nil result leaves
// manual rows alone; a non-nil empty list intentionally displays no Claude rows.
func (s *ChannelModelCatalog) projectClaudeDisplay(ch *Channel, group *Group, manual []SupportedModel, names []string) []SupportedModel {
	if s == nil || names == nil || group.Platform != PlatformAnthropic || ch.RestrictModels {
		return nil
	}
	byName := make(map[string]SupportedModel, len(manual))
	for _, model := range manual {
		if model.Platform == PlatformAnthropic {
			byName[strings.ToLower(model.Name)] = model
		}
	}
	models := make([]SupportedModel, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		lower := strings.ToLower(name)
		if seen[lower] || ch.IsModelHiddenFromPlaza(name) || !group.ModelAllowlist.Allows(name) {
			continue
		}
		seen[lower] = true
		if model, ok := byName[lower]; ok {
			models = append(models, model)
		} else if isConcreteCatalogID(name, "claude-") {
			models = append(models, SupportedModel{
				Name: name, Platform: PlatformAnthropic, Pricing: s.textDisplayPrice(ch, PlatformAnthropic, name),
				CreditsGated: isAnthropicFableModel(name),
			})
		}
	}
	slices.SortFunc(models, func(a, b SupportedModel) int { return strings.Compare(a.Name, b.Name) })
	return models
}
