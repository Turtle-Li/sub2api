package handler

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIImagesShouldRecordFailedResult(t *testing.T) {
	serverErr := &service.OpenAIImagesUpstreamError{Code: "server_error"}

	require.True(t, openAIImagesShouldRecordFailedResult(&service.OpenAIForwardResult{Stream: true, ImageCount: 1}, errors.New("stream failed")))
	require.True(t, openAIImagesShouldRecordFailedResult(&service.OpenAIForwardResult{PartialOutput: true}, errors.New("partial stream failed")))
	require.False(t, openAIImagesShouldRecordFailedResult(&service.OpenAIForwardResult{ImageCount: 1}, serverErr))
	require.False(t, openAIImagesShouldRecordFailedResult(&service.OpenAIForwardResult{ImageCount: 0}, serverErr))
	require.False(t, openAIImagesShouldRecordFailedResult(&service.OpenAIForwardResult{ImageCount: 0}, nil))
	require.False(t, openAIImagesShouldRecordFailedResult(nil, serverErr))
}

func TestOpenAIImagesStreamKeepaliveIsNotSemanticOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	before := service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c)
	_, err := c.Writer.WriteString(":\n\n")
	require.NoError(t, err)

	require.False(t, openAIImagesResponseWritten(c, true, before))
	require.False(t, openAIImagesForwardErrorAlreadyCommunicated(c, true, before, errors.New("upstream response failed: empty image output")))
}

func TestOpenAIGatewayHandlerImages_DisabledGroupRejectsBeforeScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-image-2","prompt":"draw","size":"1024x1024"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	groupID := int64(111)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      222,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                   groupID,
			AllowImageGeneration: false,
		},
		User: &service.User{ID: 333},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 333, Concurrency: 1})

	h := &OpenAIGatewayHandler{
		gatewayService:      &service.OpenAIGatewayService{},
		billingCacheService: &service.BillingCacheService{},
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   &ConcurrencyHelper{concurrencyService: &service.ConcurrencyService{}},
	}

	h.Images(c)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Contains(t, rec.Body.String(), service.ImageGenerationPermissionMessage())
}
