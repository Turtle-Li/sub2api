package service

import (
	"context"
	"math"
)

func paidFallbackInvalidRate(account *Account) bool {
	return account != nil && account.RateMultiplier != nil && (math.IsNaN(*account.RateMultiplier) || math.IsInf(*account.RateMultiplier, 0) || *account.RateMultiplier < 0)
}

func refreshPaidFallbackAdmission(ctx context.Context, selected *Account) (*Account, bool) {
	permit, _ := ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit)
	if permit == nil || permit.gateway == nil || selected == nil || !selected.IsPaidFallback() {
		return selected, true
	}
	s := permit.gateway
	if s.accountRepo == nil {
		return selected, false
	}
	latest := selected
	if s.schedulerSnapshot == nil {
		var err error
		latest, err = s.accountRepo.GetByID(ctx, selected.ID)
		if err != nil || latest == nil {
			return selected, false
		}
	}
	req := permit.request
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, req.GroupID)
	latest = s.recheckSelectedOpenAIAccountFromDBBeforeProfit(ctx, latest, req.GroupID, req.Platform, req.RequestedModel, req.RequireCompact, req.RequiredCapability)
	if latest == nil || !latest.IsPaidFallback() || !s.openAIAccountMatchesSchedulingGroup(latest, req.GroupID) ||
		!accountSupportsOpenAICapabilities(latest, req.RequiredCapability, req.RequiredImageCapability) ||
		!(&defaultOpenAIAccountScheduler{service: s}).isAccountTransportCompatible(latest, req.RequiredTransport) {
		return selected, false
	}
	if req.GroupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, req.GroupID) && s.isUpstreamModelRestrictedByChannel(ctx, *req.GroupID, latest, req.RequestedModel, req.RequireCompact) {
		return selected, false
	}
	return latest, true
}
