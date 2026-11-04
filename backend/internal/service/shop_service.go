package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shopnotification"
	"github.com/Wei-Shaw/sub2api/ent/shopproduct"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ShopWaitingPayment  = "WAITING_PAYMENT"
	ShopWaitingInput    = "WAITING_INPUT"
	ShopQueued          = "QUEUED"
	ShopProcessing      = "PROCESSING"
	ShopNeedsInput      = "NEEDS_INPUT"
	ShopCompleted       = "COMPLETED"
	ShopClosed          = "CLOSED"
	ShopSessionTopup    = "session_topup"
	ShopAccountDelivery = "account_delivery"
)

type ShopProduct struct {
	ID                int64     `json:"id"`
	Name              string    `json:"name"`
	Category          string    `json:"category"`
	Summary           string    `json:"summary"`
	Description       string    `json:"description"`
	Price             float64   `json:"price"`
	Currency          string    `json:"currency"`
	DurationValue     int       `json:"duration_value"`
	DurationUnit      string    `json:"duration_unit"`
	FulfillmentMode   string    `json:"fulfillment_mode"`
	InputFormat       string    `json:"input_format"`
	InputURL          string    `json:"input_url"`
	InputInstructions string    `json:"input_instructions"`
	GuideKey          string    `json:"guide_key"`
	Enabled           bool      `json:"enabled"`
	SortOrder         int       `json:"sort_order"`
	Revision          int       `json:"revision"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ShopOrderDetail struct {
	OrderID                 int64           `json:"order_id"`
	OutTradeNo              string          `json:"out_trade_no"`
	Product                 ShopProduct     `json:"product"`
	UserID                  int64           `json:"user_id"`
	UserEmail               string          `json:"user_email,omitempty"`
	PayAmount               float64         `json:"pay_amount"`
	Currency                string          `json:"currency"`
	PaymentStatus           string          `json:"payment_status"`
	FulfillmentStatus       string          `json:"fulfillment_status"`
	Revision                int             `json:"revision"`
	InputRevision           int             `json:"input_revision"`
	InputAccount            string          `json:"input_account"`
	InputCheck              *ShopInputCheck `json:"input_check,omitempty"`
	InputSubmittedAt        *time.Time      `json:"input_submitted_at,omitempty"`
	CreatedAt               time.Time       `json:"created_at"`
	PaidAt                  *time.Time      `json:"paid_at,omitempty"`
	CompletedAt             *time.Time      `json:"completed_at,omitempty"`
	StatusNote              string          `json:"status_note"`
	DeliveryContent         string          `json:"delivery_content,omitempty"`
	EmailStatus             string          `json:"email_status"`
	AdminNotificationStatus string          `json:"admin_notification_status"`
	CanSubmit               bool            `json:"can_submit"`
	CanStart                bool            `json:"can_start"`
	CanComplete             bool            `json:"can_complete"`
	CanRevealInput          bool            `json:"can_reveal_input"`
}

type ShopOrderList struct {
	Items    []ShopOrderDetail `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

type ShopActionInput struct {
	Action          string `json:"action"`
	Revision        int    `json:"revision"`
	InputRevision   int    `json:"input_revision"`
	Note            string `json:"note"`
	DeliveryContent string `json:"delivery_content"`
}

type ShopService struct {
	client    *dbent.Client
	encryptor SecretEncryptor
	stableKey bool
	settings  *SettingService
	email     shopEmailSender
	feishu    FeishuPaymentTextSender
}

type shopEmailSender interface {
	SendEmail(context.Context, string, string, string) error
}

func NewShopService(client *dbent.Client, encryptor SecretEncryptor, cfg *config.Config, settings *SettingService, email *EmailService) *ShopService {
	return &ShopService{client: client, encryptor: encryptor, stableKey: cfg != nil && cfg.Totp.EncryptionKeyConfigured, settings: settings, email: email, feishu: NewFeishuPaymentTextSender()}
}

func (s *ShopService) ready() error {
	if s == nil || s.client == nil || s.encryptor == nil || !s.stableKey {
		return infraerrors.ServiceUnavailable("SHOP_ENCRYPTION_UNAVAILABLE", "商城需要配置持久加密密钥，请联系客服")
	}
	return nil
}

func shopConflict() error {
	return infraerrors.Conflict("SHOP_REVISION_CHANGED", "内容已更新，请刷新后重试")
}

func shopProductDTO(p *dbent.ShopProduct) ShopProduct {
	return ShopProduct{ID: p.ID, Name: p.Name, Category: p.Category, Summary: p.Summary, Description: p.Description, Price: float64(p.PriceFen) / 100, Currency: "CNY", DurationValue: p.DurationValue, DurationUnit: p.DurationUnit, FulfillmentMode: p.FulfillmentMode, InputFormat: p.InputFormat, InputURL: p.InputURL, InputInstructions: p.InputInstructions, GuideKey: p.GuideKey, Enabled: p.Enabled, SortOrder: p.SortOrder, Revision: p.Revision, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func normalizeShopProduct(p *ShopProduct) (int64, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Category = strings.TrimSpace(p.Category)
	if p.DurationValue == 0 {
		p.DurationValue = 1
	}
	if p.DurationUnit == "" {
		p.DurationUnit = "month"
	}
	if p.InputFormat == "" {
		p.InputFormat = "session_json"
	}
	if p.InputURL == "" && p.InputFormat == "session_json" {
		p.InputURL = "https://chatgpt.com/api/auth/session"
	}
	p.Currency = "CNY"
	valid := utf8.ValidString(p.Name+p.Category+p.Summary+p.Description+p.InputInstructions) && !strings.ContainsRune(p.Name+p.Category+p.Summary+p.Description+p.InputInstructions+p.InputURL, 0)
	valid = valid && p.Name != "" && utf8.RuneCountInString(p.Name) <= 160 && utf8.RuneCountInString(p.Category) <= 80 && utf8.RuneCountInString(p.Summary) <= 600 && len(p.Description) <= 128*1024 && len(p.InputInstructions) <= 16*1024
	valid = valid && p.DurationValue > 0 && p.DurationValue <= 3650 && (p.DurationUnit == "month" || p.DurationUnit == "day" || p.DurationUnit == "year")
	valid = valid && (p.FulfillmentMode == ShopSessionTopup || p.FulfillmentMode == ShopAccountDelivery) && (p.InputFormat == "session_json" || p.InputFormat == "text") && (p.GuideKey == "" || p.GuideKey == "chatgpt-plus-account")
	valid = valid && !math.IsNaN(p.Price) && !math.IsInf(p.Price, 0) && p.Price >= 0 && p.Price <= 1000000 && math.Abs(p.Price*100-math.Round(p.Price*100)) < 0.000001 && (!p.Enabled || p.Price > 0)
	if p.InputURL != "" {
		u, err := url.Parse(p.InputURL)
		valid = valid && err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && len(p.InputURL) <= 2048
	}
	if !valid {
		return 0, infraerrors.BadRequest("SHOP_PRODUCT_INVALID", "请检查商品名称、价格、期限和交付配置")
	}
	return int64(math.Round(p.Price * 100)), nil
}

func (s *ShopService) Products(ctx context.Context, admin bool) ([]ShopProduct, error) {
	q := s.client.ShopProduct.Query()
	if !admin {
		q.Where(shopproduct.EnabledEQ(true))
	}
	rows, err := q.Order(dbent.Asc(shopproduct.FieldSortOrder), dbent.Asc(shopproduct.FieldID)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list shop products: %w", err)
	}
	out := make([]ShopProduct, 0, len(rows))
	for _, p := range rows {
		out = append(out, shopProductDTO(p))
	}
	return out, nil
}

func (s *ShopService) Product(ctx context.Context, id int64, admin bool) (*ShopProduct, error) {
	q := s.client.ShopProduct.Query().Where(shopproduct.IDEQ(id))
	if !admin {
		q.Where(shopproduct.EnabledEQ(true))
	}
	p, err := q.Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, infraerrors.NotFound("SHOP_PRODUCT_NOT_FOUND", "商品不存在或已下架")
	}
	if err != nil {
		return nil, fmt.Errorf("get shop product: %w", err)
	}
	dto := shopProductDTO(p)
	return &dto, nil
}

func (s *ShopService) SaveProduct(ctx context.Context, id int64, p ShopProduct) (*ShopProduct, error) {
	fen, err := normalizeShopProduct(&p)
	if err != nil {
		return nil, err
	}
	if id == 0 {
		row, err := s.client.ShopProduct.Create().SetName(p.Name).SetCategory(p.Category).SetSummary(p.Summary).SetDescription(p.Description).SetPriceFen(fen).SetDurationValue(p.DurationValue).SetDurationUnit(p.DurationUnit).SetFulfillmentMode(p.FulfillmentMode).SetInputFormat(p.InputFormat).SetInputURL(p.InputURL).SetInputInstructions(p.InputInstructions).SetGuideKey(p.GuideKey).SetEnabled(p.Enabled).SetSortOrder(p.SortOrder).Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("create shop product: %w", err)
		}
		dto := shopProductDTO(row)
		return &dto, nil
	}
	row, err := s.client.ShopProduct.UpdateOneID(id).Where(shopproduct.RevisionEQ(p.Revision)).SetName(p.Name).SetCategory(p.Category).SetSummary(p.Summary).SetDescription(p.Description).SetPriceFen(fen).SetDurationValue(p.DurationValue).SetDurationUnit(p.DurationUnit).SetFulfillmentMode(p.FulfillmentMode).SetInputFormat(p.InputFormat).SetInputURL(p.InputURL).SetInputInstructions(p.InputInstructions).SetGuideKey(p.GuideKey).SetEnabled(p.Enabled).SetSortOrder(p.SortOrder).AddRevision(1).Save(ctx)
	if dbent.IsNotFound(err) {
		return nil, shopConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("update shop product: %w", err)
	}
	dto := shopProductDTO(row)
	return &dto, nil
}

func shopProductMap(p ShopProduct) (map[string]any, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(b, &out)
	return out, err
}

func shopSnapshot(f *dbent.ShopFulfillment) (ShopProduct, error) {
	b, err := json.Marshal(f.ProductSnapshot)
	if err != nil {
		return ShopProduct{}, fmt.Errorf("encode shop snapshot: %w", err)
	}
	var p ShopProduct
	if err = json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("decode shop snapshot: %w", err)
	}
	if p.ID != f.ProductID || p.Currency != "CNY" || (p.FulfillmentMode != ShopSessionTopup && p.FulfillmentMode != ShopAccountDelivery) {
		return p, infraerrors.Conflict("SHOP_SNAPSHOT_INVALID", "订单商品快照异常，请联系客服")
	}
	return p, nil
}

func createShopFulfillmentTx(ctx context.Context, client *dbent.Client, o *dbent.PaymentOrder, p ShopProduct) error {
	snapshot, err := shopProductMap(p)
	if err != nil {
		return err
	}
	_, err = client.ShopFulfillment.Create().SetOrderID(o.ID).SetUserID(o.UserID).SetProductID(p.ID).SetProductSnapshot(snapshot).Save(ctx)
	return err
}

func lockShopFulfillment(ctx context.Context, client *dbent.Client, orderID int64) (*dbent.ShopFulfillment, error) {
	q := client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(orderID))
	if client.Driver().Dialect() == dialect.Postgres {
		q.ForUpdate()
	}
	f, err := q.Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, infraerrors.NotFound("SHOP_ORDER_NOT_FOUND", "商城订单不存在")
	}
	if err != nil {
		return nil, fmt.Errorf("lock shop fulfillment: %w", err)
	}
	return f, nil
}

func queueShopNotification(ctx context.Context, client *dbent.Client, f *dbent.ShopFulfillment, event, channel string) error {
	return client.ShopNotification.Create().SetOrderID(f.OrderID).SetEvent(event).SetRevision(f.Revision).SetChannel(channel).OnConflict().DoNothing().Exec(ctx)
}

// markShopPaidTx runs under the payment row lock in the payment-confirmation transaction.
func markShopPaidTx(ctx context.Context, client *dbent.Client, o *dbent.PaymentOrder) error {
	f, err := lockShopFulfillment(ctx, client, o.ID)
	if err != nil {
		return err
	}
	if f.Status != ShopWaitingPayment {
		return nil
	}
	p, err := shopSnapshot(f)
	if err != nil {
		return err
	}
	state := ShopQueued
	if p.FulfillmentMode == ShopSessionTopup {
		state = ShopWaitingInput
	}
	f, err = client.ShopFulfillment.UpdateOne(f).SetStatus(state).AddRevision(1).Save(ctx)
	if err != nil {
		return fmt.Errorf("queue paid shop order: %w", err)
	}
	return queueShopNotification(ctx, client, f, "paid", "feishu")
}

func shopPaidActionAllowed(o *dbent.PaymentOrder) bool {
	return o.OrderType == payment.OrderTypeManualShop && o.PaidAt != nil && o.Status == OrderStatusPaid
}

func shopEffectiveStatus(o *dbent.PaymentOrder, f *dbent.ShopFulfillment) string {
	if !shopPaidActionAllowed(o) && o.Status != OrderStatusCompleted && (o.PaidAt != nil || o.Status != OrderStatusPending) {
		return ShopClosed
	}
	return f.Status
}

// Apply the same payment fence as shopEffectiveStatus before counting or paging.
func shopStatusFilter(status string) predicate.ShopFulfillment {
	return func(s *sql.Selector) {
		p := sql.Table(paymentorder.Table)
		preserved := sql.Select(p.C(paymentorder.FieldID)).From(p).
			Where(sql.ColumnsEQ(p.C(paymentorder.FieldID), s.C(shopfulfillment.FieldOrderID))).
			Where(sql.Or(
				sql.EQ(p.C(paymentorder.FieldStatus), OrderStatusCompleted),
				sql.And(sql.EQ(p.C(paymentorder.FieldOrderType), payment.OrderTypeManualShop), sql.EQ(p.C(paymentorder.FieldStatus), OrderStatusPaid), sql.NotNull(p.C(paymentorder.FieldPaidAt))),
				sql.And(sql.EQ(p.C(paymentorder.FieldStatus), OrderStatusPending), sql.IsNull(p.C(paymentorder.FieldPaidAt))),
			))
		if status == ShopClosed {
			s.Where(sql.Or(sql.EQ(s.C(shopfulfillment.FieldStatus), ShopClosed), sql.Not(sql.Exists(preserved))))
		} else {
			s.Where(sql.And(sql.EQ(s.C(shopfulfillment.FieldStatus), status), sql.Exists(preserved)))
		}
	}
}

func ensureShopFinancialFence(ctx context.Context, client *dbent.Client, orderID int64) error {
	held, err := unifiedRefundOrderNeedsReview(ctx, client, orderID)
	if err != nil {
		return fmt.Errorf("read shop financial review fence: %w", err)
	}
	if held {
		return infraerrors.Conflict("SHOP_FINANCIAL_REVIEW", "订单存在支付或退款复核，请先核对资金状态")
	}
	return nil
}

func (s *ShopService) Order(ctx context.Context, orderID, userID int64, admin bool) (*ShopOrderDetail, error) {
	q := s.client.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID), paymentorder.OrderTypeEQ(payment.OrderTypeManualShop))
	if !admin {
		q.Where(paymentorder.UserIDEQ(userID))
	}
	o, err := q.Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, infraerrors.NotFound("SHOP_ORDER_NOT_FOUND", "商城订单不存在")
	}
	if err != nil {
		return nil, fmt.Errorf("get shop order: %w", err)
	}
	f, err := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("get shop fulfillment: %w", err)
	}
	detail, err := s.orderDTO(ctx, o, f, admin, !admin)
	if err != nil {
		return nil, err
	}
	if p := detail.Product; p.InputFormat == "session_json" && f.InputCiphertext != "" && f.InputSubmittedAt != nil {
		content, err := s.decrypt(o.ID, "input", f.InputRevision, f.InputCiphertext)
		if err != nil {
			return nil, err
		}
		// Reconstruct the submission-time hints; expiry remains visible to the operator.
		detail.InputCheck, _ = inspectShopSession(content, *f.InputSubmittedAt)
	}
	return detail, nil
}

func (s *ShopService) orderDTO(ctx context.Context, o *dbent.PaymentOrder, f *dbent.ShopFulfillment, admin, includeDelivery bool) (*ShopOrderDetail, error) {
	p, err := shopSnapshot(f)
	if err != nil {
		return nil, err
	}
	state := shopEffectiveStatus(o, f)
	active := shopPaidActionAllowed(o)
	if active {
		if err := ensureShopFinancialFence(ctx, s.client, o.ID); err != nil {
			active = false
		}
	}
	canSubmit := active && p.FulfillmentMode == ShopSessionTopup && (state == ShopWaitingInput || state == ShopNeedsInput || state == ShopQueued)
	canWork := active && (state == ShopQueued || state == ShopProcessing) && (p.FulfillmentMode == ShopAccountDelivery || f.InputCiphertext != "")
	d := &ShopOrderDetail{OrderID: o.ID, OutTradeNo: o.OutTradeNo, Product: p, UserID: o.UserID, PayAmount: o.PayAmount, Currency: "CNY", PaymentStatus: o.Status, FulfillmentStatus: state, Revision: f.Revision, InputRevision: f.InputRevision, InputAccount: f.InputAccount, InputSubmittedAt: f.InputSubmittedAt, CreatedAt: o.CreatedAt, PaidAt: o.PaidAt, CompletedAt: f.CompletedAt, StatusNote: f.StatusNote, EmailStatus: "NOT_SENT", AdminNotificationStatus: "NOT_SENT", CanSubmit: canSubmit, CanStart: admin && canWork && state == ShopQueued, CanComplete: admin && canWork, CanRevealInput: admin && canWork && p.FulfillmentMode == ShopSessionTopup}
	if admin {
		d.UserEmail = o.UserEmail
	}
	if includeDelivery && f.Status == ShopCompleted && o.Status == OrderStatusCompleted && f.DeliveryCiphertext != "" {
		d.DeliveryContent, err = s.decrypt(o.ID, "delivery", 1, f.DeliveryCiphertext)
		if err != nil {
			return nil, err
		}
	}
	jobs, err := s.client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID)).Order(dbent.Desc(shopnotification.FieldID)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("load shop notification status: %w", err)
	}
	for _, j := range jobs {
		if j.Channel == "email" && d.EmailStatus == "NOT_SENT" {
			d.EmailStatus = j.Status
		}
		if j.Channel == "feishu" && d.AdminNotificationStatus == "NOT_SENT" {
			d.AdminNotificationStatus = j.Status
		}
	}
	return d, nil
}

func (s *ShopService) Orders(ctx context.Context, page, pageSize int, status string) (*ShopOrderList, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	q := s.client.ShopFulfillment.Query()
	if status != "" {
		q.Where(shopStatusFilter(status))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count shop orders: %w", err)
	}
	rows, err := q.Order(dbent.Desc(shopfulfillment.FieldCreatedAt), dbent.Desc(shopfulfillment.FieldID)).Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list shop orders: %w", err)
	}
	out := &ShopOrderList{Items: make([]ShopOrderDetail, 0, len(rows)), Total: total, Page: page, PageSize: pageSize}
	for _, f := range rows {
		o, err := s.client.PaymentOrder.Get(ctx, f.OrderID)
		if err != nil {
			return nil, fmt.Errorf("load shop payment: %w", err)
		}
		d, err := s.orderDTO(ctx, o, f, true, false)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, *d)
	}
	return out, nil
}

type shopSecretEnvelope struct {
	OrderID int64  `json:"order_id"`
	Purpose string `json:"purpose"`
	Version int    `json:"version"`
	Content string `json:"content"`
}

func (s *ShopService) encrypt(orderID int64, purpose string, version int, content string) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	b, err := json.Marshal(shopSecretEnvelope{orderID, purpose, version, content})
	if err != nil {
		return "", fmt.Errorf("encode shop secret: %w", err)
	}
	value, err := s.encryptor.Encrypt(string(b))
	if err != nil {
		return "", infraerrors.ServiceUnavailable("SHOP_ENCRYPTION_FAILED", "资料加密失败，请稍后重试")
	}
	return value, nil
}

func (s *ShopService) decrypt(orderID int64, purpose string, version int, ciphertext string) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	raw, err := s.encryptor.Decrypt(ciphertext)
	if err != nil {
		return "", infraerrors.ServiceUnavailable("SHOP_DECRYPTION_FAILED", "资料解密失败，请联系客服")
	}
	var env shopSecretEnvelope
	if json.Unmarshal([]byte(raw), &env) != nil || env.OrderID != orderID || env.Purpose != purpose || env.Version != version {
		return "", infraerrors.Conflict("SHOP_SECRET_INVALID", "订单资料校验失败，请联系客服")
	}
	return env.Content, nil
}

func validateShopInput(content, format string) (string, error) {
	if len(content) > 128*1024 || strings.TrimSpace(content) == "" || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return "", infraerrors.BadRequest("SHOP_INPUT_INVALID", "请粘贴完整资料，大小不能超过128KB")
	}
	if format == "text" {
		return "", nil
	}
	check, err := inspectShopSession(content, time.Now())
	if err != nil {
		return "", err
	}
	return check.Account, nil
}

func (s *ShopService) SubmitInput(ctx context.Context, orderID, userID int64, revision int, content string) (*ShopOrderDetail, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin shop input: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	if o.UserID != userID || o.OrderType != payment.OrderTypeManualShop {
		return nil, infraerrors.NotFound("SHOP_ORDER_NOT_FOUND", "商城订单不存在")
	}
	f, err := lockShopFulfillment(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	if f.Revision != revision {
		return nil, shopConflict()
	}
	p, err := shopSnapshot(f)
	if err != nil {
		return nil, err
	}
	if !shopPaidActionAllowed(o) || p.FulfillmentMode != ShopSessionTopup || (f.Status != ShopWaitingInput && f.Status != ShopNeedsInput && f.Status != ShopQueued) {
		return nil, infraerrors.Conflict("SHOP_INPUT_NOT_ALLOWED", "当前订单不可修改资料")
	}
	if err := ensureShopFinancialFence(ctx, client, o.ID); err != nil {
		return nil, err
	}
	account, err := validateShopInput(content, p.InputFormat)
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.encrypt(o.ID, "input", f.InputRevision+1, content)
	if err != nil {
		return nil, err
	}
	f, err = client.ShopFulfillment.UpdateOne(f).Where(shopfulfillment.RevisionEQ(revision)).SetInputCiphertext(ciphertext).SetInputAccount(account).SetInputSubmittedAt(time.Now()).SetStatus(ShopQueued).SetStatusNote("").AddRevision(1).AddInputRevision(1).Save(ctx)
	if dbent.IsNotFound(err) {
		return nil, shopConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("save shop input: %w", err)
	}
	if err = queueShopNotification(ctx, client, f, "input_received", "feishu"); err != nil {
		return nil, fmt.Errorf("queue shop input notice: %w", err)
	}
	if err = writePaymentAuditTx(ctx, client, o.ID, fmt.Sprintf("SHOP_INPUT_SUBMITTED_%d", f.InputRevision), fmt.Sprintf("user:%d", userID), map[string]any{"input_revision": f.InputRevision}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit shop input: %w", err)
	}
	return s.Order(ctx, orderID, userID, false)
}

func (s *ShopService) RevealInput(ctx context.Context, orderID, adminID int64, revision int) (string, int, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(ctx, client, orderID)
	if err != nil {
		return "", 0, err
	}
	f, err := lockShopFulfillment(ctx, client, orderID)
	if err != nil {
		return "", 0, err
	}
	if f.Revision != revision {
		return "", 0, shopConflict()
	}
	if !shopPaidActionAllowed(o) || (f.Status != ShopQueued && f.Status != ShopProcessing) || f.InputCiphertext == "" {
		return "", 0, infraerrors.Conflict("SHOP_INPUT_UNAVAILABLE", "当前订单没有可读取的资料")
	}
	if err := ensureShopFinancialFence(ctx, client, o.ID); err != nil {
		return "", 0, err
	}
	content, err := s.decrypt(o.ID, "input", f.InputRevision, f.InputCiphertext)
	if err != nil {
		return "", 0, err
	}
	if err = writePaymentAuditTx(ctx, client, o.ID, fmt.Sprintf("SHOP_INPUT_REVEALED_%d", f.InputRevision), fmt.Sprintf("admin:%d", adminID), map[string]any{"input_revision": f.InputRevision}); err != nil {
		return "", 0, err
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	return content, f.InputRevision, nil
}

func (s *ShopService) Action(ctx context.Context, orderID, adminID int64, in ShopActionInput) (*ShopOrderDetail, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if len(in.Note) > 2000 || len(in.DeliveryContent) > 32*1024 || !utf8.ValidString(in.Note+in.DeliveryContent) || strings.ContainsRune(in.Note+in.DeliveryContent, 0) {
		return nil, infraerrors.BadRequest("SHOP_ACTION_INVALID", "备注或交付内容过长或格式无效")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	f, err := lockShopFulfillment(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	if f.Revision != in.Revision {
		return nil, shopConflict()
	}
	if !shopPaidActionAllowed(o) {
		return nil, infraerrors.Conflict("SHOP_PAYMENT_NOT_ACTIONABLE", "当前支付状态不允许交付")
	}
	if err := ensureShopFinancialFence(ctx, client, o.ID); err != nil {
		return nil, err
	}
	p, err := shopSnapshot(f)
	if err != nil {
		return nil, err
	}
	if f.Status != ShopQueued && f.Status != ShopProcessing {
		return nil, infraerrors.Conflict("SHOP_STATE_INVALID", "当前订单不允许此操作")
	}
	b := client.ShopFulfillment.UpdateOne(f).Where(shopfulfillment.RevisionEQ(in.Revision)).SetProcessedBy(adminID).AddRevision(1)
	switch in.Action {
	case "start":
		if f.Status != ShopQueued {
			return nil, shopConflict()
		}
		if p.FulfillmentMode == ShopSessionTopup && f.InputCiphertext == "" {
			return nil, infraerrors.Conflict("SHOP_INPUT_REQUIRED", "用户尚未提交资料")
		}
		b.SetStatus(ShopProcessing)
	case "request_input":
		if p.FulfillmentMode != ShopSessionTopup || strings.TrimSpace(in.Note) == "" {
			return nil, infraerrors.BadRequest("SHOP_NOTE_REQUIRED", "请填写需要用户补充的内容")
		}
		b.SetStatus(ShopNeedsInput).SetStatusNote(strings.TrimSpace(in.Note)).ClearInputCiphertext().SetInputAccount("").ClearInputSubmittedAt().AddInputRevision(1)
	case "complete":
		if p.FulfillmentMode == ShopSessionTopup {
			if f.InputCiphertext == "" || f.InputRevision != in.InputRevision {
				return nil, shopConflict()
			}
			content, err := s.decrypt(o.ID, "input", f.InputRevision, f.InputCiphertext)
			if err != nil {
				return nil, err
			}
			// Completion records work already performed. A token expiring after
			// submission must not prevent the operator from recording that result.
			if p.InputFormat == "session_json" && f.InputSubmittedAt != nil {
				if _, err = inspectShopSession(content, *f.InputSubmittedAt); err != nil {
					return nil, err
				}
			}
		} else {
			if strings.TrimSpace(in.DeliveryContent) == "" {
				return nil, infraerrors.BadRequest("SHOP_DELIVERY_REQUIRED", "请填写账号交付信息")
			}
			ciphertext, err := s.encrypt(o.ID, "delivery", 1, in.DeliveryContent)
			if err != nil {
				return nil, err
			}
			b.SetDeliveryCiphertext(ciphertext)
		}
		now := time.Now()
		b.SetStatus(ShopCompleted).SetCompletedAt(now).SetStatusNote(strings.TrimSpace(in.Note)).ClearInputCiphertext()
		if _, err = client.PaymentOrder.UpdateOne(o).Where(paymentorder.StatusEQ(OrderStatusPaid)).SetStatus(OrderStatusCompleted).SetCompletedAt(now).Save(ctx); err != nil {
			return nil, fmt.Errorf("complete shop payment: %w", err)
		}
	default:
		return nil, infraerrors.BadRequest("SHOP_ACTION_INVALID", "不支持的订单操作")
	}
	f, err = b.Save(ctx)
	if dbent.IsNotFound(err) {
		return nil, shopConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("update shop fulfillment: %w", err)
	}
	if in.Action == "complete" || in.Action == "request_input" {
		if err = queueShopNotification(ctx, client, f, in.Action, "email"); err != nil {
			return nil, err
		}
	}
	if err = writePaymentAuditTx(ctx, client, o.ID, fmt.Sprintf("SHOP_%s_%d", strings.ToUpper(in.Action), f.Revision), fmt.Sprintf("admin:%d", adminID), map[string]any{"revision": f.Revision, "input_revision": f.InputRevision}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit shop action: %w", err)
	}
	return s.Order(ctx, orderID, 0, true)
}
