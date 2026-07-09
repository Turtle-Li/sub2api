package service

import "time"

type OpsNetworkBandwidthSettings struct {
	Enabled                               bool     `json:"enabled"`
	Iface                                 string   `json:"iface"`
	RXLimitMbps                           *float64 `json:"rx_limit_mbps,omitempty"`
	TXLimitMbps                           *float64 `json:"tx_limit_mbps,omitempty"`
	LimitSource                           string   `json:"limit_source"`
	AbnormalRetryProtectionEnabled        bool     `json:"abnormal_retry_protection_enabled"`
	AbnormalRetryProtectionTriggerPercent float64  `json:"abnormal_retry_protection_trigger_percent"`
	AbnormalRetryProtectionMinBodyBytes   int64    `json:"abnormal_retry_protection_min_body_bytes"`
	AbnormalRetryProtectionWindowSeconds  int      `json:"abnormal_retry_protection_window_seconds"`
	AbnormalRetryProtectionMaxRepeats     int      `json:"abnormal_retry_protection_max_repeats"`
	WarnPercent                           float64  `json:"-"`
	CriticalPercent                       float64  `json:"-"`
}

type OpsNetworkBandwidthSettingsUpdateRequest struct {
	Enabled                               *bool    `json:"enabled,omitempty"`
	Iface                                 *string  `json:"iface,omitempty"`
	RXLimitMbps                           *float64 `json:"rx_limit_mbps,omitempty"`
	TXLimitMbps                           *float64 `json:"tx_limit_mbps,omitempty"`
	ClearRXLimit                          bool     `json:"clear_rx_limit,omitempty"`
	ClearTXLimit                          bool     `json:"clear_tx_limit,omitempty"`
	LimitSource                           *string  `json:"limit_source,omitempty"`
	AbnormalRetryProtectionEnabled        *bool    `json:"abnormal_retry_protection_enabled,omitempty"`
	AbnormalRetryProtectionTriggerPercent *float64 `json:"abnormal_retry_protection_trigger_percent,omitempty"`
	AbnormalRetryProtectionMinBodyBytes   *int64   `json:"abnormal_retry_protection_min_body_bytes,omitempty"`
	AbnormalRetryProtectionWindowSeconds  *int     `json:"abnormal_retry_protection_window_seconds,omitempty"`
	AbnormalRetryProtectionMaxRepeats     *int     `json:"abnormal_retry_protection_max_repeats,omitempty"`
}

type OpsNetworkInterfaceInfo struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	IsDefault bool   `json:"is_default"`
}

type OpsNetworkBandwidthSummary struct {
	Enabled bool `json:"enabled"`

	Iface       string `json:"iface"`
	LimitSource string `json:"limit_source"`

	RXLimitMbps *float64 `json:"rx_limit_mbps,omitempty"`
	TXLimitMbps *float64 `json:"tx_limit_mbps,omitempty"`

	RXMbps  float64 `json:"rx_mbps"`
	TXMbps  float64 `json:"tx_mbps"`
	MaxMbps float64 `json:"max_mbps"`

	RXUtilizationPercent  *float64 `json:"rx_utilization_percent,omitempty"`
	TXUtilizationPercent  *float64 `json:"tx_utilization_percent,omitempty"`
	MaxUtilizationPercent *float64 `json:"max_utilization_percent,omitempty"`

	RXBytesDelta int64 `json:"rx_bytes_delta"`
	TXBytesDelta int64 `json:"tx_bytes_delta"`

	RXDroppedDelta int64 `json:"rx_dropped_delta"`
	TXDroppedDelta int64 `json:"tx_dropped_delta"`
	RXErrorsDelta  int64 `json:"rx_errors_delta"`
	TXErrorsDelta  int64 `json:"tx_errors_delta"`

	ElapsedSeconds float64 `json:"elapsed_seconds"`
	SampleCount    int     `json:"sample_count"`

	RXAvg1mMbps  float64 `json:"rx_avg_1m_mbps"`
	TXAvg1mMbps  float64 `json:"tx_avg_1m_mbps"`
	RXAvg5mMbps  float64 `json:"rx_avg_5m_mbps"`
	TXAvg5mMbps  float64 `json:"tx_avg_5m_mbps"`
	RXAvg15mMbps float64 `json:"rx_avg_15m_mbps"`
	TXAvg15mMbps float64 `json:"tx_avg_15m_mbps"`
	RXPeak1hMbps float64 `json:"rx_peak_1h_mbps"`
	TXPeak1hMbps float64 `json:"tx_peak_1h_mbps"`

	Status      string    `json:"status"`
	CollectedAt time.Time `json:"collected_at"`
	Notice      string    `json:"notice,omitempty"`
}
