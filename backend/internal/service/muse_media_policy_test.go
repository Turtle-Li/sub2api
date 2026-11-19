//go:build unit

package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMuseAccountOnlySupportsNativeMediaScheduling(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://muse.example/v1",
		"model_mapping": map[string]any{
			MuseImageModel: MuseImageModel, MuseVideoModel: MuseVideoModel, "gpt-alias": legacyMuseTextModel,
		},
	}}
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseImage))
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseVideo))
	for _, capability := range []OpenAIEndpointCapability{
		"", OpenAIEndpointCapabilityChatCompletions, OpenAIEndpointCapabilityResponses,
		OpenAIEndpointCapabilityEmbeddings, OpenAIEndpointCapabilityAlphaSearch, OpenAIEndpointCapabilityLive,
	} {
		require.False(t, account.SupportsOpenAIEndpointCapability(capability), "capability %q", capability)
	}
	ordinary := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url":      "https://ordinary.example/v1",
		"model_mapping": map[string]any{MuseImageModel: "gpt-image-2", "muse-spark-1.3-contributor": "muse-spark-1.3-contributor"},
	}}
	require.True(t, ordinary.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.False(t, ordinary.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseImage))
}

func TestMuseAccountRejectsGenericForwardingBeforeNetwork(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	muse := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url":      "https://muse.example/v1",
		"model_mapping": map[string]any{MuseImageModel: MuseImageModel, "gpt-alias": legacyMuseTextModel},
	}}
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/images/generations"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://muse.example"+path,
			strings.NewReader(`{"model":"muse-image","input":"sample"}`))
		require.NoError(t, err)
		_, err = svc.doOpenAIModelUpstream(req, "", muse)
		require.ErrorIs(t, err, errMuseMediaOnlyUpstream)
	}
	require.Empty(t, upstream.requests)

	ordinary := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	req := httptest.NewRequest(http.MethodPost, "https://ordinary.example/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	resp, err := svc.doOpenAIModelUpstream(req, "", ordinary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/chat/completions", upstream.requests[0].URL.Path)
}
