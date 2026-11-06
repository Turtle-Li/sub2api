package service

func (s *BatchImagePublicService) bridgeModel(model string) *bridgeModel {
	provider := imageBridgeProviderForModel(model)
	if provider == "" || s == nil || s.ProviderRegistry == nil {
		return nil
	}
	raw, ok := s.ProviderRegistry.Get(provider)
	if !ok {
		return nil
	}
	p, ok := raw.(*OpenAIImagesBatchProvider)
	if !ok || !p.SubmissionAvailable() {
		return nil
	}
	e, ok := p.executor.(*bridgeImageExecutor)
	if !ok {
		return nil
	}
	m, ok := e.models[model]
	if !ok {
		return nil
	}
	return &m
}
func (s *BatchImagePublicService) modelCapabilities(model string) *BatchImageModelCapabilities {
	if m := s.bridgeModel(model); m != nil {
		caps := m.BatchImageModelCapabilities
		return &caps
	}
	if isWorkBuddyImageModel(model) {
		return &BatchImageModelCapabilities{ImageSizes: []string{"1K"}, AspectRatios: []string{}, DefaultImageSize: "1K", MaxReferenceImages: workBuddyImageReferenceCount(model), ResponseMimeTypes: []string{"image/png"}}
	}
	if model == "muse-image" {
		return &BatchImageModelCapabilities{ImageSizes: []string{"1K"}, AspectRatios: []string{}, DefaultImageSize: "1K", DefaultAspectRatio: "", MaxReferenceImages: 1, ResponseMimeTypes: []string{"image/png"}}
	}
	return nil
}
