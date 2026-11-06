package service

import (
	"context"
	"errors"
	"sort"
)

// BatchImageAdminModels describes local configuration, not upstream health or user entitlement.
type BatchImageAdminModels struct {
	Models            []BatchImagePublicModel `json:"models"`
	Candidates        []BatchImagePublicModel `json:"candidates"`
	UnavailableReason *string                 `json:"unavailable_reason"`
}

func (s *BatchImagePublicService) AdminModels(ctx context.Context, groupID int64) (*BatchImageAdminModels, error) {
	if s.GroupRepo == nil {
		return nil, ErrBatchImageSettlementPricingMissing
	}
	group, err := s.GroupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, ErrGroupNotFound
	}
	out := &BatchImageAdminModels{Models: []BatchImagePublicModel{}, Candidates: []BatchImagePublicModel{}}
	reason := ""
	if group.Platform != PlatformOpenAI && group.Platform != PlatformGemini {
		reason = "platform_unsupported"
	} else {
		out.Candidates, err = s.adminImageModelCandidates(ctx, group)
		if err != nil {
			return nil, err
		}
		switch {
		case group.Status != StatusActive:
			reason = "group_inactive"
		case !group.AllowImageGeneration:
			reason = "image_generation_disabled"
		case !group.AllowBatchImageGeneration:
			reason = "batch_image_disabled"
		default:
			// This is a group-level view. No individual user's pricing override is applied.
			groupView := *s
			groupView.UserGroupRateRepo = nil
			catalog, listErr := groupView.ListModels(ctx, BatchImageOwner{GroupID: &groupID})
			switch {
			case errors.Is(listErr, ErrBatchImageDisabled):
				reason = "service_disabled"
			case errors.Is(listErr, ErrBatchImageSettlementPricingMissing):
				reason = "pricing_unavailable"
			case listErr != nil:
				return nil, listErr
			default:
				for _, model := range catalog.Data {
					if group.ModelAllowlist.Allows(model.ID) {
						out.Models = append(out.Models, model)
					}
				}
				if len(out.Models) == 0 {
					reason = "no_available_models"
				}
			}
		}
	}
	if reason != "" {
		out.UnavailableReason = &reason
	}
	return out, nil
}

func (s *BatchImagePublicService) adminImageModelCandidates(ctx context.Context, group *Group) ([]BatchImagePublicModel, error) {
	accounts, err := s.listCandidateAccounts(ctx, &group.ID, group.Platform)
	if err != nil {
		return nil, err
	}
	modelIDs := append(defaultBatchImageModelCandidates(), group.ModelAllowlist.Models...)
	for i := range accounts {
		modelIDs = append(modelIDs, batchImageModelsFromAccountMapping(&accounts[i])...)
	}
	sort.Strings(modelIDs)
	result := make([]BatchImagePublicModel, 0)
	previous := ""
	for _, model := range modelIDs {
		platform, supported := batchImageModelPlatform(model)
		if !supported || platform != group.Platform || model == previous {
			continue
		}
		previous = model
		for _, provider := range batchImageProviderSelectionOrder("", model) {
			result = append(result, BatchImagePublicModel{
				ID: model, Object: "image.batch.model", Provider: provider,
				Capabilities: s.modelCapabilities(model),
			})
		}
	}
	return result, nil
}
