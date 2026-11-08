package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ShopHandler struct {
	shop     *service.ShopService
	payment  *service.PaymentService
	totp     *service.TotpService
	users    *service.UserService
	settings *service.SettingService
}

func NewShopHandler(shop *service.ShopService, payment *service.PaymentService, totp *service.TotpService, users *service.UserService, settings *service.SettingService) *ShopHandler {
	return &ShopHandler{shop: shop, payment: payment, totp: totp, users: users, settings: settings}
}

func decodeShopRequest(c *gin.Context, value any, limit int64) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		response.BadRequest(c, "请求内容无效或超出大小限制")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		response.BadRequest(c, "请求内容无效")
		return false
	}
	return true
}

func shopID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效的编号")
		return 0, false
	}
	return id, true
}

func (h *ShopHandler) Products(c *gin.Context)      { h.products(c, false) }
func (h *ShopHandler) AdminProducts(c *gin.Context) { h.products(c, true) }

func (h *ShopHandler) ActionIntegrations(c *gin.Context) {
	response.Success(c, h.shop.ActionIntegrations())
}

func (h *ShopHandler) ManageAutomation(c *gin.Context) {
	if !middleware.EnforceStepUp(c, h.totp, h.users, h.settings) {
		return
	}
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	id, ok := shopID(c)
	if !ok {
		return
	}
	var input service.ShopAutomationInput
	if !decodeShopRequest(c, &input, 4096) {
		return
	}
	out, err := h.shop.ManageAutomation(c.Request.Context(), id, subject.UserID, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
func (h *ShopHandler) products(c *gin.Context, admin bool) {
	items, err := h.shop.Products(c.Request.Context(), admin)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *ShopHandler) Product(c *gin.Context) {
	id, ok := shopID(c)
	if !ok {
		return
	}
	p, err := h.shop.Product(c.Request.Context(), id, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, p)
}

func (h *ShopHandler) SaveProduct(c *gin.Context) {
	var id int64
	if c.Param("id") != "" {
		value, ok := shopID(c)
		if !ok {
			return
		}
		id = value
	}
	var p service.ShopProduct
	if !decodeShopRequest(c, &p, 256*1024) {
		return
	}
	result, err := h.shop.SaveProduct(c.Request.Context(), id, p)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *ShopHandler) CreateOrder(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	var in struct {
		ProductID       int64           `json:"product_id"`
		ProductRevision int             `json:"product_revision"`
		PaymentType     string          `json:"payment_type"`
		Concurrency     json.RawMessage `json:"concurrency"`
		DurationDays    json.RawMessage `json:"duration_days"`
		SKUID           string          `json:"sku_id"`
	}
	if !decodeShopRequest(c, &in, 4096) {
		return
	}
	sku := service.ShopSKUSelection{ID: in.SKUID}
	if len(in.SKUID) > 80 {
		response.BadRequest(c, "SKU 编码无效")
		return
	}
	if len(in.Concurrency) != 0 && (json.Unmarshal(in.Concurrency, &sku.Concurrency) != nil || sku.Concurrency != 2 && sku.Concurrency != 3) {
		response.BadRequest(c, "并发必须是整数2或3")
		return
	}
	if len(in.DurationDays) != 0 && (json.Unmarshal(in.DurationDays, &sku.DurationDays) != nil || sku.DurationDays != 1 && sku.DurationDays != 7) {
		response.BadRequest(c, "有效期必须是整数1或7天")
		return
	}
	if len(c.Request.Header.Values("Idempotency-Key")) != 1 {
		response.BadRequest(c, "请提供唯一 Idempotency-Key")
		return
	}
	out, err := h.payment.CreateShopOrder(c.Request.Context(), in.ProductID, in.ProductRevision, service.CreateOrderRequest{UserID: subject.UserID, ShopSKU: sku, PaymentType: in.PaymentType, ClientIP: c.ClientIP(), SrcHost: c.Request.Host, SrcURL: c.Request.Referer(), IdempotencyKey: c.GetHeader("Idempotency-Key"), Locale: c.GetHeader("Accept-Language")})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *ShopHandler) Order(c *gin.Context)      { h.order(c, false) }
func (h *ShopHandler) AdminOrder(c *gin.Context) { h.order(c, true) }
func (h *ShopHandler) order(c *gin.Context, admin bool) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	id, ok := shopID(c)
	if !ok {
		return
	}
	out, err := h.shop.Order(c.Request.Context(), id, subject.UserID, admin)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *ShopHandler) Orders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	out, err := h.shop.Orders(c.Request.Context(), page, size, c.Query("status"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *ShopHandler) SubmitInput(c *gin.Context) { h.input(c, false) }
func (h *ShopHandler) CheckInput(c *gin.Context)  { h.input(c, true) }
func (h *ShopHandler) input(c *gin.Context, checkOnly bool) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	id, ok := shopID(c)
	if !ok {
		return
	}
	var in struct {
		Content  string `json:"content"`
		Revision int    `json:"revision"`
	}
	if !decodeShopRequest(c, &in, 800*1024) {
		return
	}
	if checkOnly {
		out, err := h.shop.CheckInput(c.Request.Context(), id, subject.UserID, in.Revision, in.Content)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, out)
		return
	}
	out, err := h.shop.SubmitInput(c.Request.Context(), id, subject.UserID, in.Revision, in.Content)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *ShopHandler) RevealInput(c *gin.Context) {
	if !middleware.EnforceStepUp(c, h.totp, h.users, h.settings) {
		return
	}
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	id, ok := shopID(c)
	if !ok {
		return
	}
	var in struct {
		Revision int `json:"revision"`
	}
	if !decodeShopRequest(c, &in, 4096) {
		return
	}
	content, revision, err := h.shop.RevealInput(c.Request.Context(), id, subject.UserID, in.Revision)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"content": content, "input_revision": revision})
}

func (h *ShopHandler) Action(c *gin.Context) {
	if !middleware.EnforceStepUp(c, h.totp, h.users, h.settings) {
		return
	}
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	id, ok := shopID(c)
	if !ok {
		return
	}
	var in service.ShopActionInput
	if !decodeShopRequest(c, &in, 256*1024) {
		return
	}
	out, err := h.shop.Action(c.Request.Context(), id, subject.UserID, in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *ShopHandler) RetryNotifications(c *gin.Context) {
	id, ok := shopID(c)
	if !ok {
		return
	}
	var in struct {
		Channel string `json:"channel"`
	}
	if !decodeShopRequest(c, &in, 4096) {
		return
	}
	out, err := h.shop.RetryNotification(c.Request.Context(), id, in.Channel)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
