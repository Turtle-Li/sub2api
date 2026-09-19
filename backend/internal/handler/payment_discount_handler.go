package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *PaymentHandler) QuoteCoupon(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid coupon quote request")
		return
	}
	quote, err := h.paymentService.QuotePaymentDiscount(c.Request.Context(), service.CreateOrderRequest{UserID: subject.UserID, Amount: req.Amount, PaymentType: req.PaymentType, OrderType: req.OrderType, PlanID: req.PlanID, SubscriptionID: req.SubscriptionID, ResetCardTierRevision: req.ResetCardTierRevision, CouponCode: req.CouponCode})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, quote)
}
