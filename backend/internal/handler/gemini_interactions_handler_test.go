package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGeminiTTSAndAudioSpeechEndpoints(t *testing.T) {
	require.Equal(t, EndpointGeminiInteractions, NormalizeInboundEndpoint("/v1beta/interactions"))
	require.Equal(t, EndpointAudioSpeech, NormalizeInboundEndpoint("/v1/audio/speech"))
	require.Equal(t, EndpointAudioSpeech, NormalizeInboundEndpoint("/audio/speech"))

	require.Equal(t, EndpointGeminiInteractions, DeriveUpstreamEndpoint(EndpointGeminiInteractions, "/v1beta/interactions", service.PlatformGemini))
	require.Equal(t, EndpointGeminiInteractions, DeriveUpstreamEndpoint(EndpointAudioSpeech, "/v1/audio/speech", service.PlatformGemini))
	require.Equal(t, EndpointGeminiInteractions, DeriveUpstreamEndpoint(EndpointAudioSpeech, "/audio/speech", service.PlatformGemini))
}
