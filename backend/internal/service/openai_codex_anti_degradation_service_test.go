package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mockAntiDegradationAccountRepo struct {
	AccountRepository
	updatedExtra map[int64]map[string]any
}

func (m *mockAntiDegradationAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	if m.updatedExtra == nil {
		m.updatedExtra = make(map[int64]map[string]any)
	}
	m.updatedExtra[id] = updates
	return nil
}

func TestAntiDegradationHelpers(t *testing.T) {
	t.Run("extractClusterHostFromOailb", func(t *testing.T) {
		// Sample JWT with {"host":"chat.gateway.unified-149.api.openai.com"}
		// Header: eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9 ({"alg":"ES256","typ":"JWT"})
		// Payload: eyJob3N0IjoiY2hhdC5nYXRld2F5LnVuaWZpZWQtMTQ5LmFwaS5vcGVuYWkuY29tIn0
		sampleJWT := "eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJob3N0IjoiY2hhdC5nYXRld2F5LnVuaWZpZWQtMTQ5LmFwaS5vcGVuYWkuY29tIn0.sig"
		host := extractClusterHostFromOailb(sampleJWT)
		require.Equal(t, "chat.gateway.unified-149.api.openai.com", host)

		require.Equal(t, "unknown", extractClusterHostFromOailb("invalid"))
	})

	t.Run("parseCookiesFromHeader", func(t *testing.T) {
		h := http.Header{}
		h.Add("Set-Cookie", "__cflb=0H28vzvP5FJafnkHxisyt7LDgmfBeeo1MBEvvVmWoco; Path=/; HttpOnly")
		h.Add("Set-Cookie", "__oailb=some-jwt-token; Path=/; Domain=chatgpt.com")

		cookies := parseCookiesFromHeader(h)
		require.Equal(t, "0H28vzvP5FJafnkHxisyt7LDgmfBeeo1MBEvvVmWoco", cookies["__cflb"])
		require.Equal(t, "some-jwt-token", cookies["__oailb"])
	})

	t.Run("persistAntiDegradationData", func(t *testing.T) {
		repo := &mockAntiDegradationAccountRepo{}
		svc := NewOpenAICodexAntiDegradationService(repo, nil, 1*time.Hour)

		now := time.Now().UTC()
		err := svc.persistAntiDegradationData(
			context.Background(),
			69,
			"780-byte-test-ticket",
			"__cflb=xyz; __oailb=jwt",
			DefaultWestUSProxyURL,
			now,
			now.Add(2*time.Hour),
			now.Add(1*time.Hour),
		)
		require.NoError(t, err)

		updates := repo.updatedExtra[69]
		require.NotNil(t, updates)

		routingCookie, ok := updates[PinnedCodexRoutingCookieExtraKey].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "__cflb=xyz; __oailb=jwt", routingCookie["cookie"])
		require.Equal(t, DefaultWestUSProxyURL, routingCookie["proxy"])

		turnStates, ok := updates[PinnedCodexTurnStatesExtraKey].(map[string]any)
		require.True(t, ok)
		astraEntry, ok := turnStates["gpt-6-astra"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "780-byte-test-ticket", astraEntry["state"])
		require.Equal(t, 20, astraEntry["state_len"])
		require.Equal(t, "__cflb=xyz; __oailb=jwt", astraEntry["cookie"])
	})
}
