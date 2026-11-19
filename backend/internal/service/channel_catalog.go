package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const featureKeyGPTDisplayModels = "gpt_display_models_by_group"

type GPTDisplayModelsByGroup map[int64][]string

// ChannelModelCatalog reconciles display lists only. It never writes routing,
// pricing or the manual entries used to configure the channel.
type ChannelModelCatalog struct {
	accounts AccountRepository
	billing  *BillingService
}

func NewChannelModelCatalog(accounts AccountRepository, billing *BillingService) *ChannelModelCatalog {
	return &ChannelModelCatalog{accounts: accounts, billing: billing}
}

func (ch *Channel) gptDisplayModelsByGroup() GPTDisplayModelsByGroup {
	return ch.displayModelsByGroup(featureKeyGPTDisplayModels)
}

func (ch *Channel) displayModelsByGroup(key string) map[int64][]string {
	value := ch.FeaturesConfig[key]
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var models map[int64][]string
	if err := json.Unmarshal(data, &models); err != nil {
		return nil
	}
	return models
}

// PreviewGPTDisplayModels reads the proposed channel's group accounts only when
// an admin requests a preview. It does not save anything or contact providers.
func (s *ChannelService) PreviewGPTDisplayModels(ctx context.Context, ch *Channel) (GPTDisplayModelsByGroup, error) {
	result := make(GPTDisplayModelsByGroup)
	if ch.RestrictModels || len(ch.GroupIDs) == 0 {
		return result, nil
	}
	if s.modelCatalog == nil || s.modelCatalog.accounts == nil {
		return nil, infraerrors.ServiceUnavailable("GPT_DISPLAY_PREVIEW_UNAVAILABLE", "GPT display model preview unavailable")
	}
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups for GPT display preview: %w", err)
	}
	manual := ch.SupportedModels()
	for i := range groups {
		group := &groups[i]
		if !slices.Contains(ch.GroupIDs, group.ID) {
			continue
		}
		names, err := s.modelCatalog.previewGroup(ctx, ch, group, manual)
		if err != nil {
			return nil, err
		}
		if len(names) > 0 {
			result[group.ID] = names
		}
	}
	return result, nil
}

func permitsGPTDisplay(ch *Channel, group *Group) bool {
	return !ch.RestrictModels && group.Platform == PlatformOpenAI && !group.CodexModelsManifestConfig.Enabled
}

func (s *ChannelModelCatalog) previewGroup(ctx context.Context, ch *Channel, group *Group, manual []SupportedModel) ([]string, error) {
	if !permitsGPTDisplay(ch, group) {
		return nil, nil
	}
	accounts, err := s.accounts.ListModelAvailabilityCandidates(ctx, &group.ID, []string{PlatformOpenAI}, false)
	if err != nil {
		return nil, fmt.Errorf("list GPT display candidates for group %d: %w", group.ID, err)
	}
	seen := existingGPTDisplayNames(manual)
	var names []string
	now := time.Now()
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != PlatformOpenAI || !eligibleDisplayAccount(account, now) {
			continue
		}
		for name, target := range account.GetModelMapping() {
			if name != target || !supportsOpenAIRequestModel(account, name, false) {
				continue
			}
			if _, ok := s.displayModel(ch, group, seen, name); ok {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names, nil
}

func eligibleDisplayAccount(account *Account, now time.Time) bool {
	return account.IsActive() && account.Schedulable && !account.IsPaidFallback() &&
		(!account.AutoPauseOnExpired || account.ExpiresAt == nil || now.Before(*account.ExpiresAt))
}

// savedModels reads only the approved names; viewer requests never inspect accounts.
func (s *ChannelModelCatalog) savedModels(ch *Channel, group *Group, manual []SupportedModel, names []string) []SupportedModel {
	if len(names) == 0 || s == nil || !ch.IsActive() || !permitsGPTDisplay(ch, group) {
		return nil
	}
	seen := existingGPTDisplayNames(manual)
	var models []SupportedModel
	for _, name := range names {
		if model, ok := s.displayModel(ch, group, seen, name); ok {
			models = append(models, model)
		}
	}
	return models
}

func existingGPTDisplayNames(manual []SupportedModel) map[string]struct{} {
	seen := make(map[string]struct{}, len(manual))
	for _, model := range manual {
		if model.Platform == PlatformOpenAI {
			seen[strings.ToLower(model.Name)] = struct{}{}
		}
	}
	return seen
}

func (s *ChannelModelCatalog) displayModel(ch *Channel, group *Group, seen map[string]struct{}, name string) (SupportedModel, bool) {
	if _, exists := seen[name]; exists || !isConcreteGPTCatalogID(name) ||
		!group.ModelAllowlist.Allows(name) || ch.IsModelHiddenFromPlaza(name) || channelRewritesCatalogModel(ch, name) {
		return SupportedModel{}, false
	}
	seen[name] = struct{}{}
	model := SupportedModel{Name: name, Platform: PlatformOpenAI}
	if IsGPTImageGenerationModel(name) {
		// Per-image prices are group-specific; only the plaza quotes them.
		return model, group.AllowImageGeneration
	}
	model.Pricing = s.textDisplayPrice(ch, PlatformOpenAI, name)
	return model, model.Pricing != nil
}

func isConcreteGPTCatalogID(name string) bool {
	return isConcreteCatalogID(name, "gpt-") && (IsGPTImageGenerationModel(name) || !obviousNonTextProbeModel(name))
}

func isConcreteCatalogID(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func channelRewritesCatalogModel(ch *Channel, name string) bool {
	for source, target := range ch.ModelMapping[PlatformOpenAI] {
		if target != name && matchWildcard(strings.ToLower(source), name) {
			return true
		}
	}
	return false
}

func (s *ChannelModelCatalog) textDisplayPrice(ch *Channel, platform, name string) *ChannelModelPricing {
	// The channel view shows the configured base card, not a user's effective
	// group rate. Exact cards were already covered by SupportedModels.
	for i := range ch.ModelPricing {
		card := &ch.ModelPricing[i]
		if card.Platform != platform {
			continue
		}
		for _, pattern := range card.Models {
			if matchWildcard(strings.ToLower(pattern), name) && !pricingNeedsFallback(card) {
				cp := card.Clone()
				return &cp
			}
		}
	}
	if s.billing == nil {
		return nil
	}
	price, err := s.billing.GetModelPricingForAuthoring(name, PricingCurrencyUSD)
	if err != nil {
		return nil
	}
	return &ChannelModelPricing{
		BillingMode:        BillingModeToken,
		Currency:           PricingCurrencyUSD,
		InputPrice:         nonZeroPtr(price.InputPricePerToken),
		OutputPrice:        nonZeroPtr(price.OutputPricePerToken),
		CacheReadPrice:     nonZeroPtr(price.CacheReadPricePerToken),
		CacheWritePrice:    nonZeroPtr(price.CacheCreationPricePerToken),
		CacheWrite1hPrice:  nonZeroPtr(price.CacheCreation1hPrice),
		CacheWrite12hPrice: price.CacheCreation12hPricePerToken,
	}
}
