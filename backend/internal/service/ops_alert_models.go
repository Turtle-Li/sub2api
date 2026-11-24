package service

import "time"

// Ops alert rule/event models.
//
// NOTE: These are admin-facing DTOs and intentionally keep JSON naming aligned
// with the existing ops dashboard frontend (backup style).

const (
	OpsAlertStatusFiring         = "firing"
	OpsAlertStatusResolved       = "resolved"
	OpsAlertStatusManualResolved = "manual_resolved"
)

type OpsAlertRule struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	Enabled  bool   `json:"enabled"`
	Severity string `json:"severity"`

	MetricType string  `json:"metric_type"`
	Operator   string  `json:"operator"`
	Threshold  float64 `json:"threshold"`

	WindowMinutes    int `json:"window_minutes"`
	SustainedMinutes int `json:"sustained_minutes"`
	CooldownMinutes  int `json:"cooldown_minutes"`

	NotifyEmail   bool     `json:"notify_email"`
	NotifyFeishu  bool     `json:"notify_feishu"`
	NotifyEmails  []string `json:"notify_emails"`
	CustomMessage string   `json:"custom_message"`
	// Nil on a full rule write. PUT clients that predate these fields may omit them.
	NotificationUpdate *OpsAlertNotificationUpdate `json:"-"`

	Filters map[string]any `json:"filters,omitempty"`

	LastTriggeredAt *time.Time `json:"last_triggered_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type OpsAlertNotificationUpdate struct {
	Feishu, Emails, CustomMessage bool
}

type OpsAlertEvent struct {
	ID       int64  `json:"id"`
	RuleID   int64  `json:"rule_id"`
	Severity string `json:"severity"`
	Status   string `json:"status"`

	Title       string `json:"title"`
	Description string `json:"description"`

	MetricValue    *float64 `json:"metric_value,omitempty"`
	ThresholdValue *float64 `json:"threshold_value,omitempty"`

	Dimensions map[string]any `json:"dimensions,omitempty"`

	FiredAt    time.Time  `json:"fired_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`

	EmailSent            bool      `json:"email_sent"`
	FeishuSent           bool      `json:"feishu_sent"`
	EmailDeliveryStatus  string    `json:"email_delivery_status"`
	FeishuDeliveryStatus string    `json:"feishu_delivery_status"`
	CreatedAt            time.Time `json:"created_at"`

	// A non-nil plan (including an empty one) marks a tracked, new event.
	DeliveryPlan []OpsAlertDeliveryTarget `json:"-"`
}

const (
	OpsAlertDeliveryEmail       = "email"
	OpsAlertDeliveryFeishu      = "feishu"
	OpsAlertFeishuTarget        = "payment_feishu"
	OpsAlertDeliveryPending     = "pending"
	OpsAlertDeliverySent        = "sent"
	OpsAlertDeliveryFailed      = "failed"
	OpsAlertDeliverySkipped     = "skipped"
	OpsAlertDeliveryMaxAttempts = 5
	OpsAlertDeliveryLifetime    = 30 * time.Minute
)

type OpsAlertDeliveryTarget struct {
	Channel string
	Target  string
}

type OpsAlertDelivery struct {
	OpsAlertDeliveryTarget
	ID           int64
	EventID      int64
	AttemptCount int
	ClaimToken   string
	CreatedAt    time.Time
}

type OpsAlertDeliveryOutcome struct {
	Status      string
	NextAttempt time.Time
	ErrorCode   string // Bounded internal code, never the transport error.
	Now         time.Time
}

type OpsAlertSilence struct {
	ID int64 `json:"id"`

	RuleID   int64   `json:"rule_id"`
	Platform string  `json:"platform"`
	GroupID  *int64  `json:"group_id,omitempty"`
	Region   *string `json:"region,omitempty"`

	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`

	CreatedBy *int64    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type OpsAlertEventFilter struct {
	Limit int

	// Cursor pagination (descending by fired_at, then id).
	BeforeFiredAt *time.Time
	BeforeID      *int64

	// Optional filters.
	Status    string
	Severity  string
	EmailSent *bool

	StartTime *time.Time
	EndTime   *time.Time

	// Dimensions filters (best-effort).
	Platform string
	GroupID  *int64
}
