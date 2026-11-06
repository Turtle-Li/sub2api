package service

const BatchImageProviderWorkBuddy = "workbuddy_images"
const workBuddyImageReadyKey = "workbuddy_image_single_attempt_verified"
const workBuddyImageNativeRetryAcceptedKey = "workbuddy_image_native_retry_accepted"

func isWorkBuddyImageModel(model string) bool {
	switch model {
	case "cn:hunyuan-image-alpha", "cn:hunyuan-image-alpha-edit", "global:hy-image-3.5":
		return true
	default:
		return false
	}
}

func workBuddyImageReferenceCount(model string) int {
	if model == "cn:hunyuan-image-alpha-edit" {
		return 1
	}
	return 0
}

func workBuddyImageAccountReady(a *Account) bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return false
	}
	ready, _ := a.Extra[workBuddyImageReadyKey].(bool)
	nativeRetryAccepted, _ := a.Extra[workBuddyImageNativeRetryAcceptedKey].(bool)
	return (ready || nativeRetryAccepted) && batchImageProviderAPIKey(a) != "" && a.GetCredential("base_url") != ""
}

func validateWorkBuddyImageRequest(r OpenAIImageBatchItemRequest) error {
	if !isWorkBuddyImageModel(r.Model) || len(r.ReferenceImages) != workBuddyImageReferenceCount(r.Model) || r.AspectRatio != "" || (r.ImageSize != "" && r.ImageSize != "1K") || (r.ResponseMimeType != "" && r.ResponseMimeType != "image/png") {
		return ErrBatchImageProviderInvalidInput
	}
	for _, ref := range r.ReferenceImages {
		if len(ref.Data) == 0 || len(ref.Data) > 8<<20 || ref.FileURI != "" {
			return ErrBatchImageProviderInvalidInput
		}
	}
	return nil
}
