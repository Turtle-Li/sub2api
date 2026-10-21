package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

func (s *OpenAIGatewayService) selectAccountWithScheduler(
	ctx context.Context, groupID *int64, previousResponseID, sessionHash, requestedModel string,
	excludedIDs map[int64]struct{}, requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability, requiredImageCapability OpenAIImagesCapability,
	requireCompact bool, platform string, previousResponseCanMove, useUpstreamTokenCost bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	if permit, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit); permit != nil {
		ctx = context.WithValue(ctx, paidFallbackPermitKey{}, (*paidFallbackPermit)(nil))
	}
	selectPool := func(poolCtx context.Context) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		return s.selectAccountWithSchedulerInPool(poolCtx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove, useUpstreamTokenCost)
	}
	var selection *AccountSelectionResult
	var decision OpenAIAccountScheduleDecision
	err := ErrNoAvailableAccounts
	paidTextCapability := requiredCapability == "" || requiredCapability == OpenAIEndpointCapabilityChatCompletions || requiredCapability == OpenAIEndpointCapabilityResponses
	if previousResponseID != "" && paidTextCapability {
		bound, lookupErr := s.paidFallbackContinuationAccount(ctx, groupID, previousResponseID)
		if lookupErr != nil {
			return nil, decision, lookupErr
		}
		if bound != nil {
			if !s.settingService.paidFallbackAllowed(ctx, groupID) {
				return nil, decision, err
			}
			keyID, _ := ctx.Value(paidFallbackAPIKeyKey{}).(int64)
			permit := &paidFallbackPermit{settings: s.settingService, gateway: s, request: OpenAIAccountScheduleRequest{GroupID: groupID, Platform: NormalizeOpenAICompatiblePlatform(platform), RequestedModel: requestedModel, RequiredTransport: requiredTransport, RequiredCapability: requiredCapability}, groupID: *groupID, apiKeyID: keyID, accountID: bound.ID}
			previousResponseCanMove = false
			selection, decision, err = selectPool(context.WithValue(ctx, paidFallbackPermitKey{}, permit))
			if selection != nil && selection.Account != nil && selection.Account.ID != bound.ID {
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				return nil, decision, ErrNoAvailableAccounts
			}
			if selection != nil {
				selection.paidFallbackPermit = permit
			}
			return selection, decision, err
		}
	}
	if ctx.Value(paidFallbackOnlyKey{}) != true {
		selection, decision, err = selectPool(ctx)
		if err == nil || !errors.Is(err, ErrNoAvailableAccounts) {
			return selection, decision, err
		}
	}
	platform = NormalizeOpenAICompatiblePlatform(platform)
	if (platform != PlatformOpenAI && platform != PlatformDeepseek) || !paidTextCapability || requiredImageCapability != "" || requireCompact || ctx.Err() != nil || !s.settingService.paidFallbackAllowed(ctx, groupID) {
		return selection, decision, err
	}
	// Provider-owned continuation state cannot be moved between credentials.
	if previousResponseID != "" {
		return selection, decision, err
	}
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, groupID)
	ctx = s.withOpenAIProfitControlGate(ctx, groupID)
	req := OpenAIAccountScheduleRequest{GroupID: groupID, Platform: platform, RequestedModel: requestedModel, ExcludedIDs: excludedIDs, RequiredTransport: requiredTransport, RequiredCapability: requiredCapability, RequiredImageCapability: requiredImageCapability}
	if !s.ordinaryPoolUnavailableForPaidFallback(ctx, req) {
		return selection, decision, err
	}
	keyID, _ := ctx.Value(paidFallbackAPIKeyKey{}).(int64)
	permit := &paidFallbackPermit{settings: s.settingService, gateway: s, request: req, groupID: *groupID, apiKeyID: keyID}
	ctx = context.WithValue(ctx, paidFallbackPermitKey{}, permit)
	selection, decision, err = selectPool(ctx)
	if err == nil && selection != nil && selection.Account != nil {
		permit.accountID = selection.Account.ID
		selection.paidFallbackPermit = permit
		slog.WarnContext(ctx, "paid_fallback_selected", "group_id", *groupID, "api_key_id", keyID, "account_id", permit.accountID, "model", requestedModel, "acquired", selection.Acquired)
	}
	return selection, decision, err
}

func (s *OpenAIGatewayService) ordinaryPoolUnavailableForPaidFallback(ctx context.Context, req OpenAIAccountScheduleRequest) bool {
	if s.accountRepo == nil || req.GroupID == nil || s.checkChannelPricingRestriction(ctx, req.GroupID, req.RequestedModel) {
		return false
	}
	accounts, err := s.accountRepo.ListByGroup(ctx, *req.GroupID)
	if err != nil {
		return false
	}
	scheduler := &defaultOpenAIAccountScheduler{service: s}
	hasFailure := false
	for i := range accounts {
		account := &accounts[i]
		if account.Status != StatusActive && account.Status != StatusError {
			continue
		}
		if !account.Schedulable && account.Status != StatusError {
			continue
		}
		if account.IsPaidFallback() || !s.openAIAccountMatchesSchedulingGroup(account, req.GroupID) ||
			!supportsGroupPlatformModelWithContext(ctx, account, req.Platform, req.RequestedModel, false) ||
			!supportsOpenAIRequestModel(account, multiModelGroupRequestedModel(ctx, account, req.RequestedModel), false) ||
			!accountSupportsOpenAICapabilities(account, req.RequiredCapability, req.RequiredImageCapability) ||
			!scheduler.isAccountTransportCompatible(account, req.RequiredTransport) {
			continue
		}
		if s.openAIGroupRequiresPrivacySet(ctx, req.GroupID) && !account.IsPrivacySet() {
			continue
		}
		if s.needsUpstreamChannelRestrictionCheck(ctx, req.GroupID) && s.isUpstreamModelRestrictedByChannel(ctx, *req.GroupID, account, req.RequestedModel, false) {
			continue
		}
		if vetoed, _ := openAIProfitControlVetoReason(ctx, account); vetoed {
			return false
		}
		if _, excluded := req.ExcludedIDs[account.ID]; excluded {
			if !paidFallbackFailedAccount(ctx, account.ID) {
				return false
			}
			hasFailure = true
			continue
		}
		reason := openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx, account, req.Platform, req.RequestedModel, false, req.RequiredCapability)
		runtimeUnavailable := account.Status == StatusError || reason == "model_rate_limited" || strings.HasPrefix(reason, "quota_auto_pause") ||
			paidFallbackFuture(account.RateLimitResetAt) || paidFallbackFuture(account.OverloadUntil) || paidFallbackFuture(account.TempUnschedulableUntil) ||
			s.isOpenAIAccountRequestRuntimeBlocked(account, multiModelGroupRequestedModel(ctx, account, req.RequestedModel))
		if runtimeUnavailable {
			hasFailure = true
			continue
		}
		if reason == "" {
			return false
		}
	}
	return hasFailure
}

func paidFallbackFuture(value *time.Time) bool { return value != nil && value.After(time.Now()) }

func (s *OpenAIGatewayService) paidFallbackContinuationAccount(ctx context.Context, groupID *int64, responseID string) (*Account, error) {
	store := s.getOpenAIWSStateStore()
	if store == nil {
		return nil, nil
	}
	id, err := store.GetResponseAccount(ctx, derefGroupID(groupID), responseID)
	if err != nil || id <= 0 {
		return nil, nil
	}
	var account *Account
	if s.schedulerSnapshot != nil {
		account, err = s.schedulerSnapshot.GetAccount(ctx, id)
	} else if s.accountRepo != nil {
		account, err = s.accountRepo.GetByID(ctx, id)
	}
	if err != nil {
		return nil, nil
	}
	if account != nil && account.IsPaidFallback() {
		return account, nil
	}
	return nil, nil
}

func (s *OpenAIGatewayService) PaidFallbackRetryAllowed(ctx context.Context, groupID *int64, account *Account) bool {
	return account != nil && !account.IsPaidFallback() && ctx.Err() == nil && s.settingService.paidFallbackAllowed(ctx, groupID)
}
