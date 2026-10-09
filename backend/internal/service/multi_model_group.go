package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type accountGroupModelConstraintKey struct{}
type multiModelGroupProtocolKey struct{}

type multiModelGroupProtocol struct {
	service *OpenAIGatewayService
	inbound string
}

type accountGroupModelConstraint struct {
	platform       string
	model          string
	requireCompact bool
	nativeGemini   bool
	protocol       *multiModelGroupProtocol
}

// WithMultiModelGroupProtocol makes admission use the same model transformations
// as the selected account's existing forwarding profile.
func (s *OpenAIGatewayService) WithMultiModelGroupProtocol(ctx context.Context, inbound string) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, multiModelGroupProtocolKey{}, &multiModelGroupProtocol{service: s, inbound: inbound})
}

func multiModelGroupInputModel(ctx context.Context, model string) string {
	if mapped := channelMappedModelFromRateLimitContext(ctx); mapped != "" {
		return mapped
	}
	if publicModel, ok := RequestedPublicModelFromContext(ctx); ok && strings.TrimSpace(model) == publicModel {
		if resolvedModel, ok := ResolvedUpstreamModelFromContext(ctx); ok {
			return resolvedModel
		}
	}
	return model
}

func multiModelGroupRequestedModel(ctx context.Context, account *Account, model string) string {
	if account == nil || !domain.IsMultiModelPlatform(account.Platform) {
		return model
	}
	model = multiModelGroupInputModel(ctx, model)
	protocol, _ := ctx.Value(multiModelGroupProtocolKey{}).(*multiModelGroupProtocol)
	if protocol != nil && protocol.inbound == APIProtocolAnthropic {
		routingModel := resolveOpenAIForwardModel(account, model, "")
		if protocol.service.resolveUpstreamProtocolFor(ctx, account, APIProtocolAnthropic, routingModel) != APIProtocolAnthropic {
			model = NormalizeOpenAICompatRequestedModel(model)
		}
	}
	return model
}

func supportsGroupPlatformModelWithContext(ctx context.Context, account *Account, platform, model string, requireCompact bool) bool {
	if account == nil || !domain.IsGroupAccountPlatformCompatible(platform, account.Platform) {
		return false
	}
	if account.Platform == platform || platform == PlatformComposite {
		return true
	}
	model = multiModelGroupRequestedModel(ctx, account, model)
	protocol, _ := ctx.Value(multiModelGroupProtocolKey{}).(*multiModelGroupProtocol)
	upstreamModel := resolveOpenAIAccountUpstreamModelForRequest(account, model, requireCompact)
	if protocol != nil && protocol.inbound != APIProtocolResponses {
		upstreamModel = normalizeOpenAIModelForUpstream(account, resolveOpenAIForwardModel(account, model, ""))
	}
	return account.SupportsGroupPlatformUpstreamModel(platform, upstreamModel)
}

func withAccountGroupModelConstraint(ctx context.Context, platform, model string, requireCompact ...bool) context.Context {
	protocol, _ := ctx.Value(multiModelGroupProtocolKey{}).(*multiModelGroupProtocol)
	constraint := accountGroupModelConstraint{platform: platform, model: multiModelGroupInputModel(ctx, model), nativeGemini: nativeGeminiAccountSelection(ctx), protocol: protocol}
	constraint.requireCompact = len(requireCompact) > 0 && requireCompact[0]
	return context.WithValue(ctx, accountGroupModelConstraintKey{}, constraint)
}

func supportsHydratedAccountGroupModel(ctx context.Context, selected, hydrated *Account) bool {
	if hydrated == nil {
		return false
	}
	constraint, ok := ctx.Value(accountGroupModelConstraintKey{}).(accountGroupModelConstraint)
	if !ok {
		return true
	}
	if constraint.nativeGemini && domain.IsMultiModelPlatform(hydrated.Platform) {
		return false
	}
	if !domain.IsMultiModelPlatform(hydrated.Platform) && (selected == nil || !domain.IsMultiModelPlatform(selected.Platform)) {
		return true
	}
	if constraint.protocol != nil {
		ctx = context.WithValue(ctx, multiModelGroupProtocolKey{}, constraint.protocol)
	}
	return supportsGroupPlatformModelWithContext(ctx, hydrated, constraint.platform, constraint.model, constraint.requireCompact)
}

// Check the actual serialized model at the shared upstream send boundaries.
// This also protects direct forward callers and protocol-catalog changes after selection.
func validateMultiModelGroupUpstream(ctx context.Context, c *gin.Context, account *Account, body []byte) error {
	if account == nil || !domain.IsMultiModelPlatform(account.Platform) {
		return nil
	}
	platform := ""
	model := gjson.GetBytes(body, "model").String()
	if constraint, ok := ctx.Value(accountGroupModelConstraintKey{}).(accountGroupModelConstraint); ok {
		platform = constraint.platform
		if constraint.nativeGemini {
			return fmt.Errorf("%w: native Gemini protocol requires a native account", ErrNoAvailableAccounts)
		}
	} else if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil {
		platform = group.Platform
	} else if c != nil {
		if value, ok := c.Get("api_key"); ok {
			if key, ok := value.(*APIKey); ok && key.Group != nil {
				platform = key.Group.Platform
			}
		}
	}
	if platform == "" || platform == PlatformComposite {
		if resolvedPlatform, ok := ResolvedTargetPlatformFromContext(ctx); ok {
			platform = resolvedPlatform
		}
	}
	if platform == "" || account.SupportsGroupPlatformUpstreamModel(platform, model) {
		return nil
	}
	return fmt.Errorf("%w: upstream model does not belong to group platform %s", ErrNoAvailableAccounts, platform)
}
