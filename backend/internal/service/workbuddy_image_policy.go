package service

// WorkBuddyImagePolicyEligible identifies an explicitly mapped OpenAI-compatible image account.
func WorkBuddyImagePolicyEligible(account *Account) bool {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return false
	}
	for model, target := range account.GetModelMapping() {
		if isWorkBuddyImageModel(model) && model == target {
			return true
		}
	}
	return false
}

// WorkBuddyImageNativeRetryPolicy changes only the operator's official-runtime acceptance.
func WorkBuddyImageNativeRetryPolicy(accepted bool) map[string]any {
	return map[string]any{workBuddyImageNativeRetryAcceptedKey: accepted}
}
