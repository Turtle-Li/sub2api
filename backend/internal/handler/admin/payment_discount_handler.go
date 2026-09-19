package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"strconv"
)

func (h *PaymentHandler) ListCoupons(c *gin.Context) {
	page, size := response.ParsePagination(c)
	rows, total, err := h.paymentService.ListPaymentDiscountCodes(c.Request.Context(), page, size, c.Query("search"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, rows, int64(total), page, size)
}
func (h *PaymentHandler) SaveCoupon(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authorization required")
		return
	}
	var req struct {
		service.PaymentDiscountCodeInput
		Version int64 `json:"version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid coupon configuration")
		return
	}
	var id int64
	if c.Param("id") != "" {
		var err error
		id, err = strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid coupon ID")
			return
		}
	}
	result, err := h.paymentService.SavePaymentDiscountCode(c.Request.Context(), subject.UserID, id, req.PaymentDiscountCodeInput, req.Version)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *PaymentHandler) CouponUsages(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid coupon ID")
		return
	}
	page, size := response.ParsePagination(c)
	rows, total, err := h.paymentService.ListPaymentDiscountUses(c.Request.Context(), id, page, size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, rows, int64(total), page, size)
}

func (h *PaymentHandler) CouponAudits(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid coupon ID")
		return
	}
	page, size := response.ParsePagination(c)
	rows, total, err := h.paymentService.ListPaymentDiscountAudits(c.Request.Context(), id, page, size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, rows, int64(total), page, size)
}
