package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShopDecoderRejectsUnsafeBodiesWithoutEchoingSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"content":"synthetic-session"}`, true},
		{"unknown privileged field", `{"content":"synthetic-session","user_id":2}`, false},
		{"trailing document", `{"content":"synthetic-session"}{}`, false},
		{"malformed", `{"content":"synthetic-session`, false},
		{"oversize", `{"content":"synthetic-session` + strings.Repeat("x", 100) + `"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			var input struct {
				Content string `json:"content"`
			}
			require.Equal(t, tc.valid, decodeShopRequest(c, &input, 80))
			if tc.valid {
				require.Equal(t, "synthetic-session", input.Content)
			} else {
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.NotContains(t, rec.Body.String(), "synthetic-session")
				require.NotContains(t, rec.Body.String(), "user_id")
			}
		})
	}
}

func TestShopCheckoutDurationAcceptsOnlySupportedIntegers(t *testing.T) {
	for _, tc := range []struct {
		body    string
		message string
	}{
		{`{}`, "Idempotency-Key"},
		{`{"duration_days":1}`, "Idempotency-Key"},
		{`{"duration_days":7}`, "Idempotency-Key"},
		{`{"duration_days":0}`, "有效期必须是整数1或7天"},
		{`{"duration_days":5}`, "有效期必须是整数1或7天"},
		{`{"duration_days":7.0}`, "有效期必须是整数1或7天"},
		{`{"duration_days":"7"}`, "有效期必须是整数1或7天"},
		{`{"duration_days":null}`, "有效期必须是整数1或7天"},
		{`{"duration_days":7,"price":1}`, "请求内容无效"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			c.Request = httptest.NewRequest(http.MethodPost, "/shop/orders", strings.NewReader(tc.body))
			(&ShopHandler{}).CreateOrder(c)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), tc.message)
		})
	}
}
