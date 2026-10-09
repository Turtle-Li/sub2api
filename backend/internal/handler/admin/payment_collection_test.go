//go:build unit

package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectionHandlerKeepsHumanStepUpAndStrictBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, body    string
		apiKey, grant bool
		status        int
	}{
		{"api key", `{"amount_fen":43200,"payment_type":"alipay"}`, true, true, 403},
		{"no step up", `{"amount_fen":43200,"payment_type":"alipay"}`, false, false, 403},
		{"valid", `{"amount_fen":43200,"payment_type":"alipay"}`, false, true, 500},
		{"duplicate", `{"amount_fen":1,"amount_fen":43200,"payment_type":"alipay"}`, false, true, 400},
		{"beneficiary", `{"amount_fen":43200,"payment_type":"alipay","user_id":1}`, false, true, 400},
		{"fractional fen", `{"amount_fen":432.1,"payment_type":"alipay"}`, false, true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newRefundStepUpHandler(nil, tc.grant)
			r := newOwnerTestOrderRouter(h, true, tc.apiKey)
			r.POST("/api/v1/admin/payment/collection/orders", h.CreateCollectionOrder)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/payment/collection/orders", strings.NewReader(tc.body))
			req.Header.Set("Idempotency-Key", "collection-handler-key")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code)
		})
	}
}
