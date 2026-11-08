package service

import "context"

func (s *GatewayService) claudeOAuthSystemPromptInjectionSettings(ctx context.Context) (bool, string, string) {
	enabled, prompt, blocks := true, "", ""
	if s != nil && s.settingService != nil {
		enabled, prompt, blocks = s.settingService.GetClaudeOAuthSystemPromptInjectionSettings(ctx)
	}
	// Only the operator-owned runner can select this profile. Reuse the existing
	// two-block billing/identity shape, also used by Fable, without changing
	// normal OAuth fingerprinting, authentication, headers or beta selection.
	if groupID, ok := ctx.Value(channelMonitorV3ProbeGroupContextKey{}).(int64); ok && groupID > 0 {
		return enabled, "", claudeFableOAuthSystemPromptBlocks
	}
	return enabled, prompt, blocks
}
