package service

import "strings"

// Output-image token counts from Google's Gemini/Vertex image specifications.
// Keep token rates in the pricing resolver; convert units only at this flat
// per-image billing boundary. Batch/group discounts are applied by the caller.
// https://ai.google.dev/gemini-api/docs/pricing?hl=en
// https://cloud.google.com/model-garden/pricing
func batchImageOutputTokenCount(model, size string) (int, bool) {
	model = strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "models/"), "-preview")
	size = strings.ToUpper(strings.TrimSpace(size))
	if size == "" {
		size = "1K"
	}
	switch model {
	case "gemini-2.5-flash-image":
		return 1290, size == "1K"
	case "gemini-3-pro-image":
		switch size {
		case "1K", "2K":
			return 1120, true
		case "4K":
			return 2000, true
		}
	case "gemini-3.1-flash-image":
		switch size {
		case "1K":
			return 1120, true
		case "2K":
			return 1680, true
		case "4K":
			return 2520, true
		}
	case "gemini-3.1-flash-lite-image":
		return 1120, size == "1K"
	}
	return 0, false
}
