package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPurchaseResetCardRejectsInvalidPurchaseKeyBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/7/purchase-reset-card", strings.NewReader(`{
		"expected_plan_id": 4,
		"expected_price": 40,
		"purchase_key": "not-a-uuid"
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 11})

	(&SubscriptionHandler{}).PurchaseResetCard(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var body response.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "RESET_CARD_PURCHASE_KEY_INVALID", body.Reason)
}

func TestResetCardHandlersRejectMissingAuthAndInvalidSubscriptionID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &SubscriptionHandler{}

	t.Run("missing auth", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "id", Value: "7"}}
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/7/reset-card-quote", nil)
		h.GetResetCardQuote(c)
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	t.Run("invalid id", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "id", Value: "not-an-id"}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/not-an-id/purchase-reset-card", nil)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 11})
		h.PurchaseResetCard(c)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})
}
