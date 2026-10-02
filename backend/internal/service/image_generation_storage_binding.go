package service

func (s *OpenAIGatewayService) SetImageStorageResolver(resolve ImageStorageResolver) {
	if s != nil {
		s.imageStorageResolver = resolve
		if s.imageUpscaler != nil {
			s.imageUpscaler.SetImageStorageResolver(resolve)
		}
	}
}

func (s *GeminiMessagesCompatService) SetImageStorageResolver(resolve ImageStorageResolver) {
	if s != nil {
		s.imageStorageResolver = resolve
		if s.imageUpscaler != nil {
			s.imageUpscaler.SetImageStorageResolver(resolve)
		}
	}
}

func (s *AntigravityGatewayService) SetImageStorageResolver(resolve ImageStorageResolver) {
	if s != nil {
		s.imageStorageResolver = resolve
		if s.imageUpscaler != nil {
			s.imageUpscaler.SetImageStorageResolver(resolve)
		}
	}
}
