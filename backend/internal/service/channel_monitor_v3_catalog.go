package service

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"
)

func (s *ChannelMonitorV3Service) CreateCategory(ctx context.Context, input ChannelMonitorV3CategoryInput) (*ChannelMonitorV3Category, error) {
	if err := normalizeChannelMonitorV3Category(&input); err != nil {
		return nil, err
	}
	existing, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	if len(existing) >= channelMonitorV3MaxCategories {
		return nil, channelMonitorV3Invalid("at most %d categories", channelMonitorV3MaxCategories)
	}
	return s.repo.CreateCategory(ctx, input)
}

func (s *ChannelMonitorV3Service) UpdateCategory(ctx context.Context, id int64, input ChannelMonitorV3CategoryInput) (*ChannelMonitorV3Category, error) {
	if err := normalizeChannelMonitorV3Category(&input); err != nil {
		return nil, err
	}
	updated, err := s.repo.UpdateCategory(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, ErrChannelMonitorV3NotFound
	}
	return updated, nil
}

// DeleteCategory keeps its components; they leave the list until moved to
// another category (or stay as the featured card).
func (s *ChannelMonitorV3Service) DeleteCategory(ctx context.Context, id int64) error {
	ok, err := s.repo.DeleteCategory(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrChannelMonitorV3NotFound
	}
	return nil
}

func normalizeChannelMonitorV3Category(input *ChannelMonitorV3CategoryInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 64 {
		return channelMonitorV3Invalid("category name is required (at most 64 characters)")
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		return channelMonitorV3Invalid("category description must be at most 500 characters")
	}
	return nil
}

func (s *ChannelMonitorV3Service) CreateComponent(ctx context.Context, input ChannelMonitorV3ComponentInput) (*ChannelMonitorV3Component, error) {
	if err := s.validateComponent(ctx, &input); err != nil {
		return nil, err
	}
	existing, err := s.repo.ListComponents(ctx)
	if err != nil {
		return nil, err
	}
	if len(existing) >= channelMonitorV3MaxComponents {
		return nil, channelMonitorV3Invalid("at most %d components", channelMonitorV3MaxComponents)
	}
	return s.repo.CreateComponent(ctx, input)
}

func (s *ChannelMonitorV3Service) UpdateComponent(ctx context.Context, id int64, input ChannelMonitorV3ComponentInput) (*ChannelMonitorV3Component, error) {
	if err := s.validateComponent(ctx, &input); err != nil {
		return nil, err
	}
	updated, err := s.repo.UpdateComponent(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, ErrChannelMonitorV3NotFound
	}
	return updated, nil
}

func (s *ChannelMonitorV3Service) DeleteComponent(ctx context.Context, id int64) error {
	ok, err := s.repo.DeleteComponent(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrChannelMonitorV3NotFound
	}
	return nil
}

func (s *ChannelMonitorV3Service) Reorder(ctx context.Context, categoryIDs, componentIDs []int64) error {
	if len(categoryIDs) > channelMonitorV3MaxCategories || len(componentIDs) > channelMonitorV3MaxComponents {
		return channelMonitorV3Invalid("too many items to reorder")
	}
	return s.repo.Reorder(ctx, categoryIDs, componentIDs)
}

func (s *ChannelMonitorV3Service) validateComponent(ctx context.Context, input *ChannelMonitorV3ComponentInput) error {
	if err := normalizeChannelMonitorV3Component(input); err != nil {
		return err
	}
	if input.CategoryID != nil {
		categories, err := s.repo.ListCategories(ctx)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(categories, func(c ChannelMonitorV3Category) bool { return c.ID == *input.CategoryID }) {
			return channelMonitorV3Invalid("category %d does not exist", *input.CategoryID)
		}
	}
	if s.groups != nil {
		if group, err := s.groups.GetByID(ctx, input.GroupID); err != nil || group == nil {
			return channelMonitorV3Invalid("group %d does not exist", input.GroupID)
		}
	}
	return nil
}

func normalizeChannelMonitorV3Component(input *ChannelMonitorV3ComponentInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Model = strings.TrimSpace(input.Model)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 100 {
		return channelMonitorV3Invalid("component name is required (at most 100 characters)")
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		return channelMonitorV3Invalid("component description must be at most 500 characters")
	}
	if input.GroupID <= 0 {
		return channelMonitorV3Invalid("component group is required")
	}
	if utf8.RuneCountInString(input.Model) > 100 {
		return channelMonitorV3Invalid("model must be at most 100 characters")
	}
	if input.CategoryID != nil && *input.CategoryID <= 0 {
		input.CategoryID = nil
	}
	if input.DegradedTTFTMs != 0 && !slices.Contains(ChannelMonitorV3TTFTThresholds, input.DegradedTTFTMs) {
		return channelMonitorV3Invalid("first-token threshold must be 0 (inherit) or one of the latency buckets")
	}
	switch input.Visibility {
	case "":
		input.Visibility = ChannelMonitorV3VisibilityGroup
	case ChannelMonitorV3VisibilityGroup, ChannelMonitorV3VisibilityPublic:
	default:
		return channelMonitorV3Invalid("visibility must be group or public")
	}
	return nil
}
