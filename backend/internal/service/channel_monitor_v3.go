package service

import (
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Channel monitor V3 is an admin-curated status page over the same passive
// facts V2 aggregates from real user requests. Components map groups to rows;
// every bar is cut into the same time slots, so columns line up across bars.
// Optional operator-owned representative probes are separately configured and
// stored; they never enter these real-request facts or customer usage records.

const (
	ChannelMonitorV3StatusOperational = "operational"
	ChannelMonitorV3StatusDegraded    = "degraded"
	ChannelMonitorV3StatusDown        = "down"
	// Insufficient marks a slot with fewer requests than the configured minimum.
	ChannelMonitorV3StatusInsufficient = "insufficient"
	ChannelMonitorV3StatusUnknown      = "unknown"

	ChannelMonitorV3VisibilityGroup  = "group"
	ChannelMonitorV3VisibilityPublic = "public"

	// ChannelMonitorV3History is how far back bars can page: V2 keeps its
	// one-minute facts for seven days.
	ChannelMonitorV3History = 7 * 24 * time.Hour
	// ChannelMonitorV3IncidentMinDuration is how long slots must stay down to
	// count as an incident. Shorter blips stay red on the bars but are left out
	// of the incident history.
	ChannelMonitorV3IncidentMinDuration = 10 * time.Minute

	channelMonitorV3MaxCategories = 50
	channelMonitorV3MaxComponents = 100
	channelMonitorV3IncidentPage  = 100
)

// ChannelMonitorV3Intervals are the slot lengths a page may use (minutes).
var ChannelMonitorV3Intervals = []int{1, 2, 3, 5, 10, 15, 30, 60}

// ChannelMonitorV3TTFTThresholds are V2's latency histogram bounds, so a
// threshold compares exactly against the P50 bucket.
var ChannelMonitorV3TTFTThresholds = []int{1000, 2000, 3000, 5000, 8000, 10000, 15000, 30000, 60000, 120000}

var channelMonitorV3Ranges = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}

var (
	ErrChannelMonitorV3Invalid  = infraerrors.BadRequest("CHANNEL_MONITOR_V3_INVALID", "invalid channel monitor v3 settings")
	ErrChannelMonitorV3Conflict = infraerrors.Conflict("CHANNEL_MONITOR_V3_CONFLICT", "channel monitor v3 settings were changed elsewhere; reload and try again")
	ErrChannelMonitorV3NotFound = infraerrors.NotFound("CHANNEL_MONITOR_V3_NOT_FOUND", "channel monitor v3 item not found")
)

func channelMonitorV3Invalid(format string, args ...any) error {
	return infraerrors.Newf(http.StatusBadRequest, "CHANNEL_MONITOR_V3_INVALID", format, args...)
}

type ChannelMonitorV3Config struct {
	GroupProbe        ChannelMonitorV3GroupProbeConfig `json:"group_probe"`
	PublicEnabled     bool                             `json:"public_enabled"`
	Version           int                              `json:"version"`
	IntervalMinutes   int                              `json:"interval_minutes"`
	Cells             int                              `json:"cells"`
	AvailabilityRange string                           `json:"availability_range"`
	// A slot is down at DownErrorRate, degraded at DegradedErrorRate or when
	// its first-token P50 is above DegradedTTFTMs.
	DownErrorRate     float64 `json:"down_error_rate"`
	DegradedErrorRate float64 `json:"degraded_error_rate"`
	DegradedTTFTMs    int     `json:"degraded_ttft_ms"`
	// MinRequests below which a slot is shown as too quiet to judge.
	MinRequests int `json:"min_requests"`
	// IgnoredErrorCategories are errors users cause; they never count against a channel.
	IgnoredErrorCategories []string  `json:"ignored_error_categories"`
	FeaturedComponentID    *int64    `json:"featured_component_id"`
	FooterNote             string    `json:"footer_note"`
	UpdatedAt              time.Time `json:"updated_at"`
	UpdatedBy              *int64    `json:"updated_by,omitempty"`
}

type ChannelMonitorV3Category struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ChannelMonitorV3CategoryInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ChannelMonitorV3Component struct {
	ID          int64  `json:"id"`
	CategoryID  *int64 `json:"category_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	GroupID     int64  `json:"group_id"`
	// Model narrows the row to one requested model; empty means the whole group.
	Model          string `json:"model"`
	DegradedTTFTMs int    `json:"degraded_ttft_ms"`
	ShowMultiplier bool   `json:"show_multiplier"`
	Visibility     string `json:"visibility"`
	Enabled        bool   `json:"enabled"`
	SortOrder      int    `json:"sort_order"`
	// Joined from groups for display.
	GroupName           string    `json:"group_name"`
	GroupPlatform       string    `json:"group_platform"`
	GroupStatus         string    `json:"group_status"`
	GroupRateMultiplier float64   `json:"group_rate_multiplier"`
	GroupDeleted        bool      `json:"group_deleted"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ChannelMonitorV3ComponentInput struct {
	CategoryID     *int64 `json:"category_id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	GroupID        int64  `json:"group_id"`
	Model          string `json:"model"`
	DegradedTTFTMs int    `json:"degraded_ttft_ms"`
	ShowMultiplier bool   `json:"show_multiplier"`
	Visibility     string `json:"visibility"`
	Enabled        bool   `json:"enabled"`
}

// ChannelMonitorV3Settings is everything the admin editor needs in one read.
type ChannelMonitorV3Settings struct {
	Config          ChannelMonitorV3Config      `json:"config"`
	Categories      []ChannelMonitorV3Category  `json:"categories"`
	Components      []ChannelMonitorV3Component `json:"components"`
	ErrorCategories []string                    `json:"error_categories"`
	DataThrough     *time.Time                  `json:"data_through"`
}

// Passive facts read from the V2 aggregates. Slot is zero for range totals.
type ChannelMonitorV3Fact struct {
	GroupID int64
	Model   string
	Slot    time.Time
	Success int64
	Errors  int64
}

type ChannelMonitorV3ErrorFact struct {
	GroupID  int64
	Model    string
	Slot     time.Time
	Category string
	Errors   int64
}

type ChannelMonitorV3LatencyFact struct {
	GroupID    int64
	Model      string
	Slot       time.Time
	UpperBound int64
	Count      int64
}

type ChannelMonitorV3Facts struct {
	Metrics []ChannelMonitorV3Fact
	Errors  []ChannelMonitorV3ErrorFact
	Latency []ChannelMonitorV3LatencyFact
}

type ChannelMonitorV3Cell struct {
	Start       time.Time `json:"start"`
	Status      string    `json:"status"`
	SuccessRate float64   `json:"success_rate"`
	TTFTP50Ms   *int64    `json:"ttft_p50_ms,omitempty"`
	TopError    string    `json:"top_error,omitempty"`
	// Volumes are admin-only so the page cannot be used to size the fleet.
	Requests      int64 `json:"requests,omitempty"`
	Errors        int64 `json:"errors,omitempty"`
	IgnoredErrors int64 `json:"ignored_errors,omitempty"`
}

type ChannelMonitorV3ComponentStatus struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Model       string   `json:"model,omitempty"`
	Multiplier  *float64 `json:"multiplier,omitempty"`
	// Status is the latest slot that had enough traffic to judge.
	Status string `json:"status"`
	// Availability is the 0–100 share of answered requests over the
	// configured range; nil without traffic.
	Availability *float64                `json:"availability"`
	Requests     int64                   `json:"requests,omitempty"`
	LastDataAt   *time.Time              `json:"last_data_at"`
	Cells        []*ChannelMonitorV3Cell `json:"cells"`
	GroupID      int64                   `json:"group_id,omitempty"`
}

type ChannelMonitorV3CategoryStatus struct {
	ID           int64                             `json:"id"`
	Name         string                            `json:"name"`
	Description  string                            `json:"description,omitempty"`
	Availability *float64                          `json:"availability"`
	Components   []ChannelMonitorV3ComponentStatus `json:"components"`
}

type ChannelMonitorV3Window struct {
	// Start is the first slot; End is the exclusive end of the last slot.
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Latest   bool      `json:"latest"`
	HasOlder bool      `json:"has_older"`
}

type ChannelMonitorV3Status struct {
	GeneratedAt       time.Time                        `json:"generated_at"`
	IntervalMinutes   int                              `json:"interval_minutes"`
	Cells             int                              `json:"cells"`
	AvailabilityRange string                           `json:"availability_range"`
	AvailabilitySince time.Time                        `json:"availability_since"`
	DownErrorRate     float64                          `json:"down_error_rate"`
	DegradedErrorRate float64                          `json:"degraded_error_rate"`
	DegradedTTFTMs    int                              `json:"degraded_ttft_ms"`
	MinRequests       int                              `json:"min_requests"`
	DataThrough       *time.Time                       `json:"data_through"`
	FooterNote        string                           `json:"footer_note"`
	Window            ChannelMonitorV3Window           `json:"window"`
	Featured          *ChannelMonitorV3ComponentStatus `json:"featured"`
	Categories        []ChannelMonitorV3CategoryStatus `json:"categories"`
	OpenIncidents     int                              `json:"open_incidents"`
}

// ChannelMonitorV3Incident is a run of consecutive down slots of one component.
type ChannelMonitorV3Incident struct {
	ComponentID   int64      `json:"component_id"`
	ComponentName string     `json:"component_name"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at"`
	DownSlots     int        `json:"down_slots"`
	TopError      string     `json:"top_error,omitempty"`
	Requests      int64      `json:"requests,omitempty"`
	Errors        int64      `json:"errors,omitempty"`
}

type ChannelMonitorV3IncidentPage struct {
	Items    []ChannelMonitorV3Incident `json:"items"`
	Total    int                        `json:"total"`
	Page     int                        `json:"page"`
	PageSize int                        `json:"page_size"`
	Since    time.Time                  `json:"since"`
}

// ChannelMonitorV3Viewer is the server-derived scope of one request.
