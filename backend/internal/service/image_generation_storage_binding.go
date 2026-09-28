package service

func (s *OpenAIGatewayService) SetImageStorageResolver(resolve ImageStorageResolver) {
	if s != nil {
		s.imageStorageResolver = resolve
	}
}

func (s *GeminiMessagesCompatService) SetImageStorageResolver(resolve ImageStorageResolver) {
	if s != nil {
		s.imageStorageResolver = resolve
	}
}

func (s *AntigravityGatewayService) SetImageStorageResolver(resolve ImageStorageResolver) {
	if s != nil {
		s.imageStorageResolver = resolve
	}
}
