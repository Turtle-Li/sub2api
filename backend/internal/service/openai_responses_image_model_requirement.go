package service

import (
	"context"
	"strings"
)

type openAIResponsesImageModelRequirementsContextKey struct{}

type openAIResponsesImageModelRequirements struct {
	models []string
}

// WithOpenAIResponsesImageModelRequirements records the native image_generation
// tool models declared by one Responses request. The requirement is deliberately
// request-scoped: account configuration remains the authority for model support.
func WithOpenAIResponsesImageModelRequirements(ctx context.Context, models []string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(models) == 0 {
		// An explicit empty value is meaningful for a later WebSocket text turn:
		// it clears any native-image requirement inherited from the first turn.
		return context.WithValue(ctx, openAIResponsesImageModelRequirementsContextKey{}, openAIResponsesImageModelRequirements{})
	}
	requirements := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		requirements = append(requirements, model)
	}
	return context.WithValue(ctx, openAIResponsesImageModelRequirementsContextKey{}, openAIResponsesImageModelRequirements{
		models: requirements,
	})
}

func openAIResponsesImageModelRequirementsFromContext(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	requirements, ok := ctx.Value(openAIResponsesImageModelRequirementsContextKey{}).(openAIResponsesImageModelRequirements)
	if !ok || len(requirements.models) == 0 {
		return nil
	}
	return requirements.models
}

// OpenAIResponsesImageModelRequirementFailureReason returns the scheduler
// diagnostic for a native Responses image tool the account cannot currently
// serve. The model-specific lookup also honors the existing OpenAI image family
// cooldown, without applying it to the request's top-level text model.
func OpenAIResponsesImageModelRequirementFailureReason(ctx context.Context, account *Account, platform string) string {
	if NormalizeOpenAICompatiblePlatform(platform) != PlatformOpenAI {
		return ""
	}
	requirements := openAIResponsesImageModelRequirementsFromContext(ctx)
	if len(requirements) == 0 {
		return ""
	}
	if account == nil {
		return "image_model_not_supported"
	}
	for _, model := range requirements {
		if !account.IsModelSupported(model) {
			return "image_model_not_supported"
		}
		if account.GetModelRateLimitRemainingTimeWithContext(ctx, model) > 0 {
			return "image_model_rate_limited"
		}
	}
	return ""
}

// RevalidateOpenAIResponsesImageModelRequirements refreshes the bound account
// only for a native Responses image tool on OpenAI. A WebSocket connection is
// intentionally account-bound, but an administrator can remove an image model
// mapping after it is established; that later image turn must fail closed
// instead of forwarding it through the stale account snapshot.
func (s *OpenAIGatewayService) RevalidateOpenAIResponsesImageModelRequirements(ctx context.Context, account *Account, platform string) string {
	if NormalizeOpenAICompatiblePlatform(platform) != PlatformOpenAI || len(openAIResponsesImageModelRequirementsFromContext(ctx)) == 0 {
		return ""
	}
	if account == nil || s == nil || s.accountRepo == nil {
		return "image_model_not_supported"
	}

	latest, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil || latest == nil {
		return "image_model_not_supported"
	}
	if reason := OpenAIResponsesImageModelRequirementFailureReason(ctx, latest, platform); reason != "" {
		return reason
	}
	return ""
}
