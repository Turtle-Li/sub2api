//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRefundAmountRejectedBeforeFinancialAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, amount := range []string{"0", "-1", "0.001", "99999999999999999999", "null", "true"} {
		t.Run(amount, func(t *testing.T) {
			handler, _ := newRefundStepUpHandler(&service.PaymentService{}, true)
			router := newRefundStepUpRouter(handler, true, false)
			post := httptest.NewRecorder()
			router.ServeHTTP(post, refundStepUpRequest("/api/v1/admin/payment/orders/1/refund", `{"quote_revision":"test","refund_amount":`+amount+`,"reason":"test"}`))
			require.Equal(t, http.StatusBadRequest, post.Code)
			get := httptest.NewRecorder()
			router.GET("/api/v1/admin/payment/orders/:id/refund-review", handler.GetRefundReview)
			router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/admin/payment/orders/1/refund-review?refund_amount="+amount, nil))
			require.Equal(t, http.StatusBadRequest, get.Code)
		})
	}
}
