package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
