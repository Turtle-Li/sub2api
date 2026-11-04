package service

import (
	"context"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"time"
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
	ShopDaypassGuide    = "deepseek-daypass"
)

type ShopProduct struct {
	ID                   int64                `json:"id"`
	Name                 string               `json:"name"`
	Category             string               `json:"category"`
	Summary              string               `json:"summary"`
	Description          string               `json:"description"`
	Price                float64              `json:"price"`
	Currency             string               `json:"currency"`
	DurationValue        int                  `json:"duration_value"`
	DurationUnit         string               `json:"duration_unit"`
	FulfillmentMode      string               `json:"fulfillment_mode"`
	InputFormat          string               `json:"input_format"`
	InputURL             string               `json:"input_url"`
	InputInstructions    string               `json:"input_instructions"`
	GuideKey             string               `json:"guide_key"`
	Enabled              bool                 `json:"enabled"`
	SortOrder            int                  `json:"sort_order"`
	Revision             int                  `json:"revision"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
	PaymentActions       []shopaction.Binding `json:"payment_actions,omitempty"`
	AutomaticFulfillment bool                 `json:"automatic_fulfillment"`
}

type ShopOrderDetail struct {
	OrderID                 int64                 `json:"order_id"`
	OutTradeNo              string                `json:"out_trade_no"`
	Product                 ShopProduct           `json:"product"`
	UserID                  int64                 `json:"user_id"`
	UserEmail               string                `json:"user_email,omitempty"`
	PayAmount               float64               `json:"pay_amount"`
	Currency                string                `json:"currency"`
	PaymentStatus           string                `json:"payment_status"`
	FulfillmentStatus       string                `json:"fulfillment_status"`
	Revision                int                   `json:"revision"`
	InputRevision           int                   `json:"input_revision"`
	InputAccount            string                `json:"input_account"`
	InputCheck              *ShopInputCheck       `json:"input_check,omitempty"`
	InputSubmittedAt        *time.Time            `json:"input_submitted_at,omitempty"`
	CreatedAt               time.Time             `json:"created_at"`
	PaidAt                  *time.Time            `json:"paid_at,omitempty"`
	CompletedAt             *time.Time            `json:"completed_at,omitempty"`
	StatusNote              string                `json:"status_note"`
	DeliveryContent         string                `json:"delivery_content,omitempty"`
	EmailStatus             string                `json:"email_status"`
	AdminNotificationStatus string                `json:"admin_notification_status"`
	CanSubmit               bool                  `json:"can_submit"`
	CanStart                bool                  `json:"can_start"`
	CanComplete             bool                  `json:"can_complete"`
	CanRevealInput          bool                  `json:"can_reveal_input"`
	Automation              *ShopAutomationStatus `json:"automation,omitempty"`
	DeliveryKind            string                `json:"delivery_kind"`
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
	client     *dbent.Client
	encryptor  SecretEncryptor
	stableKey  bool
	settings   *SettingService
	email      shopEmailSender
	feishu     FeishuPaymentTextSender
	daypassCfg config.ShopDaypassConfig
	daypass    shopDaypassGateway
	actionsCfg shopaction.Config
	webhook    func(shopaction.Integration) shopaction.Adapter
}

type shopEmailSender interface {
	SendEmail(context.Context, string, string, string) error
}

func NewShopService(client *dbent.Client, encryptor SecretEncryptor, cfg *config.Config, settings *SettingService, email *EmailService) *ShopService {
	s := &ShopService{client: client, encryptor: encryptor, stableKey: cfg != nil && cfg.Totp.EncryptionKeyConfigured, settings: settings, email: email, feishu: NewFeishuPaymentTextSender()}
	s.webhook = func(i shopaction.Integration) shopaction.Adapter {
		return shopaction.NewWebhook(i, loadShopActionSigningSecret)
	}
	if cfg != nil {
		s.daypassCfg = cfg.ShopDaypass
		s.actionsCfg = cfg.ShopActions
	}
	if s.daypassCfg.ProductID > 0 {
		for _, integration := range s.actionsCfg.Integrations {
			if integration.Adapter == shopaction.Native {
				s.daypass = newShopDaypassGateway(s.daypassCfg.ProductID, integration.Endpoint)
				break
			}
		}
	}
	return s
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
