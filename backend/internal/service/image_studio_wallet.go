package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const imageStudioWalletSetting = "image_studio_wallet"

// ImageStudioWalletOwnerSlot is only an ownership/deduplication namespace in
// the batch and billing tables. It does not refer to a row in api_keys.
const ImageStudioWalletOwnerSlot int64 = 0

var (
	ErrImageStudioWalletUnavailable = infraerrors.Forbidden("IMAGE_STUDIO_WALLET_UNAVAILABLE", "Image Studio wallet generation is unavailable")
	ErrImageStudioWalletGroup       = infraerrors.BadRequest("IMAGE_STUDIO_WALLET_INVALID_GROUP", "Choose an active standard OpenAI group with image generation enabled")
	ErrImageStudioWalletAccounts    = infraerrors.BadRequest("IMAGE_STUDIO_WALLET_INVALID_ACCOUNTS", "Choose between 1 and 32 existing non-reserve image upstream accounts")
	ErrImageStudioWalletGroupChange = infraerrors.Conflict("IMAGE_STUDIO_WALLET_GROUP_CHANGED", "Image Studio's configuration has changed; reload before saving")
)

type ImageStudioWalletConfig struct {
	GroupID    int64   `json:"group_id"`
	AccountIDs []int64 `json:"account_ids"`
}

func (config ImageStudioWalletConfig) normalized() (ImageStudioWalletConfig, error) {
	if config.GroupID < 0 {
		return ImageStudioWalletConfig{}, ErrImageStudioWalletGroup
	}
	if config.GroupID == 0 {
		if len(config.AccountIDs) != 0 {
			return ImageStudioWalletConfig{}, ErrImageStudioWalletAccounts
		}
		return ImageStudioWalletConfig{AccountIDs: []int64{}}, nil
	}
	if len(config.AccountIDs) == 0 || len(config.AccountIDs) > 32 {
		return ImageStudioWalletConfig{}, ErrImageStudioWalletAccounts
	}
	config.AccountIDs = slices.Clone(config.AccountIDs)
	slices.Sort(config.AccountIDs)
	if config.AccountIDs[0] <= 0 || len(slices.Compact(config.AccountIDs)) != len(config.AccountIDs) {
		return ImageStudioWalletConfig{}, ErrImageStudioWalletAccounts
	}
	return config, nil
}

func (s *SettingService) imageStudioWalletValue(ctx context.Context) (ImageStudioWalletConfig, string, error) {
	raw, err := s.settingRepo.GetValue(ctx, imageStudioWalletSetting)
	if errors.Is(err, ErrSettingNotFound) {
		return ImageStudioWalletConfig{AccountIDs: []int64{}}, "", nil
	}
	if err != nil {
		return ImageStudioWalletConfig{}, "", err
	}
	var config ImageStudioWalletConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return ImageStudioWalletConfig{}, "", errors.New("invalid stored Image Studio wallet config")
	}
	config, err = config.normalized()
	if err != nil {
		return ImageStudioWalletConfig{}, "", errors.New("invalid stored Image Studio wallet config")
	}
	return config, raw, nil
}

func (s *SettingService) ImageStudioWalletConfig(ctx context.Context) (ImageStudioWalletConfig, error) {
	config, _, err := s.imageStudioWalletValue(ctx)
	return config, err
}

func (s *SettingService) SetImageStudioWalletConfig(ctx context.Context, expected, config ImageStudioWalletConfig) (ImageStudioWalletConfig, error) {
	expected, err := expected.normalized()
	if err != nil {
		return ImageStudioWalletConfig{}, err
	}
	config, err = config.normalized()
	if err != nil {
		return ImageStudioWalletConfig{}, err
	}
	current, raw, err := s.imageStudioWalletValue(ctx)
	if err != nil {
		return ImageStudioWalletConfig{}, err
	}
	if current.GroupID != expected.GroupID || !slices.Equal(current.AccountIDs, expected.AccountIDs) {
		return ImageStudioWalletConfig{}, ErrImageStudioWalletGroupChange
	}
	if current.GroupID == config.GroupID && slices.Equal(current.AccountIDs, config.AccountIDs) {
		return current, nil
	}
	repo, ok := s.settingRepo.(interface {
		CompareAndSet(context.Context, string, string, string) (bool, error)
	})
	if !ok {
		return ImageStudioWalletConfig{}, errors.New("Image Studio wallet configuration updates are unavailable")
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return ImageStudioWalletConfig{}, err
	}
	saved, err := repo.CompareAndSet(ctx, imageStudioWalletSetting, raw, string(encoded))
	if err != nil {
		return ImageStudioWalletConfig{}, err
	}
	if !saved {
		return ImageStudioWalletConfig{}, ErrImageStudioWalletGroupChange
	}
	return config, nil
}

func (s *BatchImagePublicService) ImageStudioWalletGroup(ctx context.Context, groupID int64) (*Group, error) {
	if groupID <= 0 || s.GroupRepo == nil {
		return nil, ErrImageStudioWalletGroup
	}
	group, err := s.GroupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if !isImageStudioWalletGroup(group) {
		return nil, ErrImageStudioWalletGroup
	}
	return group, nil
}

func isImageStudioWalletGroup(group *Group) bool {
	return group != nil && group.ID > 0 && group.Status == StatusActive && group.SubscriptionType == SubscriptionTypeStandard &&
		NormalizeGroupPlatform(group.Platform) == PlatformOpenAI && group.AllowImageGeneration
}

func (s *BatchImagePublicService) ValidateImageStudioWalletConfig(ctx context.Context, config ImageStudioWalletConfig) error {
	config, err := config.normalized()
	if err != nil || config.GroupID == 0 {
		return err
	}
	if _, err = s.ImageStudioWalletGroup(ctx, config.GroupID); err != nil {
		return err
	}
	if s.AccountRepo == nil {
		return ErrImageStudioWalletAccounts
	}
	accounts, err := s.AccountRepo.GetByIDs(ctx, config.AccountIDs)
	if err != nil {
		return err
	}
	if len(accounts) != len(config.AccountIDs) {
		return ErrImageStudioWalletAccounts
	}
	for _, account := range accounts {
		if !isImageStudioWalletAccount(account) {
			return ErrImageStudioWalletAccounts
		}
	}
	return nil
}

func isImageStudioWalletAccount(account *Account) bool {
	if account == nil || account.IsPaidFallback() {
		return false
	}
	for _, model := range batchImageModelsFromAccountMapping(account) {
		if platform, ok := batchImageModelPlatform(model); ok && platform == account.Platform {
			return true
		}
	}
	return false
}

func (s *BatchImagePublicService) batchImageAccountsForOwner(ctx context.Context, owner BatchImageOwner) (func(string) ([]Account, error), error) {
	if !owner.StudioWallet {
		return func(platform string) ([]Account, error) { return s.listCandidateAccounts(ctx, owner.GroupID, platform) }, nil
	}
	accounts, err := s.AccountRepo.GetByIDs(ctx, owner.StudioAccountIDs)
	if err != nil {
		return nil, err
	}
	byPlatform := make(map[string][]Account)
	for _, account := range accounts {
		if isImageStudioWalletAccount(account) && account.IsSchedulable() {
			byPlatform[account.Platform] = append(byPlatform[account.Platform], *account)
		}
	}
	return func(platform string) ([]Account, error) { return slices.Clone(byPlatform[platform]), nil }, nil
}

func (s *BatchImagePublicService) ensureBatchImageOwnerGroup(ctx context.Context, owner BatchImageOwner, model string) error {
	if !owner.StudioWallet {
		return s.ensureGroupAllowsBatchImage(ctx, owner.GroupID, model)
	}
	if owner.APIKeyID != ImageStudioWalletOwnerSlot || owner.GroupID == nil || len(owner.StudioAccountIDs) == 0 {
		return ErrImageStudioWalletUnavailable
	}
	group, err := s.ImageStudioWalletGroup(ctx, *owner.GroupID)
	if err != nil {
		return ErrImageStudioWalletUnavailable.WithCause(err)
	}
	if model != "" && !group.ModelAllowlist.Allows(model) {
		return ErrBatchImageInvalidModel
	}
	return nil
}

// A workbench route shares the group/account multipliers but never the shared
// resolution defaults: every exact model must have its own wallet-unit tariff.
func imageStudioWalletUnitPrice(group *Group, model, size string) (float64, error) {
	model = normalizeChannelPricingModelName(model)
	for i := range group.ModelPricing {
		card := &group.ModelPricing[i]
		if !slices.ContainsFunc(card.Models, func(name string) bool { return normalizeChannelPricingModelName(name) == model }) {
			continue
		}
		if card.Currency != PricingCurrencyCNY || (card.BillingMode != BillingModeImage && card.BillingMode != BillingModePerRequest) {
			break
		}
		price := nativeWalletImageTierPrice(card, size)
		if positiveNativeImagePrice(price) {
			return *price, nil
		}
		break
	}
	return 0, ErrBatchImageSettlementPricingMissing
}

func (s *BatchImagePublicService) imageStudioWalletCapabilities(model string, prices map[string]BatchImageModelPriceRange) *BatchImageModelCapabilities {
	caps := s.modelCapabilities(model)
	if caps == nil {
		caps = &BatchImageModelCapabilities{
			ImageSizes: []string{"1K"}, AspectRatios: []string{}, DefaultImageSize: s.defaultImageSize(),
			MaxReferenceImages: maxBatchImageReferenceImagesForModel(model), ResponseMimeTypes: []string{s.defaultResponseMimeType()},
		}
		if s.Config.ImageUpscale.Active() {
			caps.ImageSizes = append(caps.ImageSizes, "2K", "4K")
		}
	}
	// The native capability slice belongs to the registry. Never mutate it.
	allowed := make([]string, 0, len(caps.ImageSizes))
	for _, size := range caps.ImageSizes {
		if _, ok := prices[size]; ok {
			allowed = append(allowed, size)
		}
	}
	caps.ImageSizes = allowed
	if len(allowed) > 0 && !slices.Contains(allowed, strings.ToUpper(caps.DefaultImageSize)) {
		caps.DefaultImageSize = allowed[0]
	}
	return caps
}
