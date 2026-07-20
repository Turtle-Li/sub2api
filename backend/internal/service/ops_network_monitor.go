package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	opsNetworkLimitSourceUnknown = "unknown"
	opsNetworkLimitSourceManual  = "manual"
	opsNetworkLimitSourceCloud   = "cloud_api"

	opsNetworkSamplerInterval = 5 * time.Second

	defaultAbnormalRetryProtectionTriggerPercent = 90
	defaultAbnormalRetryProtectionMinBodyBytes   = 5 * 1024 * 1024
	defaultAbnormalRetryProtectionWindowSeconds  = 60
	defaultAbnormalRetryProtectionMaxRepeats     = 1
)

type opsNetworkCounters struct {
	RXBytes   uint64
	TXBytes   uint64
	RXDropped uint64
	TXDropped uint64
	RXErrors  uint64
	TXErrors  uint64
}

type opsNetworkSample struct {
	At             time.Time
	RXMbps         float64
	TXMbps         float64
	RXBytesDelta   int64
	TXBytesDelta   int64
	RXDroppedDelta int64
	TXDroppedDelta int64
	RXErrorsDelta  int64
	TXErrorsDelta  int64
	ElapsedSeconds float64
}

type opsNetworkSampler struct {
	mu        sync.Mutex
	basePath  string
	procPath  string
	lastIface string
	last      opsNetworkCounters
	lastAt    time.Time
	samples   []opsNetworkSample
	latest    *OpsNetworkBandwidthSummary
}

func newOpsNetworkSampler() *opsNetworkSampler {
	return &opsNetworkSampler{
		basePath: "/sys/class/net",
		procPath: "/proc/net/route",
		samples:  make([]opsNetworkSample, 0, 720),
	}
}

func defaultOpsNetworkBandwidthSettings() *OpsNetworkBandwidthSettings {
	return &OpsNetworkBandwidthSettings{
		Enabled:                               true,
		Iface:                                 "",
		LimitSource:                           opsNetworkLimitSourceUnknown,
		AbnormalRetryProtectionEnabled:        false,
		AbnormalRetryProtectionTriggerPercent: defaultAbnormalRetryProtectionTriggerPercent,
		AbnormalRetryProtectionMinBodyBytes:   defaultAbnormalRetryProtectionMinBodyBytes,
		AbnormalRetryProtectionWindowSeconds:  defaultAbnormalRetryProtectionWindowSeconds,
		AbnormalRetryProtectionMaxRepeats:     defaultAbnormalRetryProtectionMaxRepeats,
		WarnPercent:                           80,
		CriticalPercent:                       95,
	}
}

func normalizeOpsNetworkBandwidthSettings(cfg *OpsNetworkBandwidthSettings) {
	if cfg == nil {
		return
	}
	cfg.Iface = strings.TrimSpace(cfg.Iface)
	switch strings.ToLower(strings.TrimSpace(cfg.LimitSource)) {
	case opsNetworkLimitSourceManual:
		cfg.LimitSource = opsNetworkLimitSourceManual
	case opsNetworkLimitSourceCloud:
		cfg.LimitSource = opsNetworkLimitSourceCloud
	default:
		cfg.LimitSource = opsNetworkLimitSourceUnknown
	}
	if cfg.WarnPercent <= 0 || cfg.WarnPercent > 100 {
		cfg.WarnPercent = 80
	}
	if cfg.CriticalPercent <= 0 || cfg.CriticalPercent > 100 {
		cfg.CriticalPercent = 95
	}
	if cfg.CriticalPercent < cfg.WarnPercent {
		cfg.CriticalPercent = cfg.WarnPercent
	}
	if cfg.RXLimitMbps != nil && *cfg.RXLimitMbps <= 0 {
		cfg.RXLimitMbps = nil
	}
	if cfg.TXLimitMbps != nil && *cfg.TXLimitMbps <= 0 {
		cfg.TXLimitMbps = nil
	}
	if cfg.LimitSource == opsNetworkLimitSourceUnknown && (cfg.RXLimitMbps != nil || cfg.TXLimitMbps != nil) {
		cfg.LimitSource = opsNetworkLimitSourceManual
	}
	if cfg.AbnormalRetryProtectionTriggerPercent <= 0 || cfg.AbnormalRetryProtectionTriggerPercent > 100 {
		cfg.AbnormalRetryProtectionTriggerPercent = defaultAbnormalRetryProtectionTriggerPercent
	}
	if cfg.AbnormalRetryProtectionMinBodyBytes <= 0 {
		cfg.AbnormalRetryProtectionMinBodyBytes = defaultAbnormalRetryProtectionMinBodyBytes
	}
	if cfg.AbnormalRetryProtectionWindowSeconds <= 0 {
		cfg.AbnormalRetryProtectionWindowSeconds = defaultAbnormalRetryProtectionWindowSeconds
	}
	if cfg.AbnormalRetryProtectionMaxRepeats <= 0 {
		cfg.AbnormalRetryProtectionMaxRepeats = defaultAbnormalRetryProtectionMaxRepeats
	}
}

func validateOpsNetworkBandwidthSettings(cfg *OpsNetworkBandwidthSettings) error {
	if cfg == nil {
		return errors.New("invalid config")
	}
	if cfg.RXLimitMbps != nil && (*cfg.RXLimitMbps <= 0 || *cfg.RXLimitMbps > 1_000_000) {
		return errors.New("rx_limit_mbps must be between 0 and 1000000")
	}
	if cfg.TXLimitMbps != nil && (*cfg.TXLimitMbps <= 0 || *cfg.TXLimitMbps > 1_000_000) {
		return errors.New("tx_limit_mbps must be between 0 and 1000000")
	}
	if cfg.WarnPercent <= 0 || cfg.WarnPercent > 100 {
		return errors.New("warn_percent must be between 0 and 100")
	}
	if cfg.CriticalPercent <= 0 || cfg.CriticalPercent > 100 {
		return errors.New("critical_percent must be between 0 and 100")
	}
	if cfg.CriticalPercent < cfg.WarnPercent {
		return errors.New("critical_percent must be greater than or equal to warn_percent")
	}
	if cfg.AbnormalRetryProtectionTriggerPercent <= 0 || cfg.AbnormalRetryProtectionTriggerPercent > 100 {
		return errors.New("abnormal_retry_protection_trigger_percent must be between 0 and 100")
	}
	if cfg.AbnormalRetryProtectionMinBodyBytes <= 0 || cfg.AbnormalRetryProtectionMinBodyBytes > 256*1024*1024 {
		return errors.New("abnormal_retry_protection_min_body_bytes must be between 1 and 268435456")
	}
	if cfg.AbnormalRetryProtectionWindowSeconds <= 0 || cfg.AbnormalRetryProtectionWindowSeconds > 3600 {
		return errors.New("abnormal_retry_protection_window_seconds must be between 1 and 3600")
	}
	if cfg.AbnormalRetryProtectionMaxRepeats <= 0 || cfg.AbnormalRetryProtectionMaxRepeats > 10 {
		return errors.New("abnormal_retry_protection_max_repeats must be between 1 and 10")
	}
	return nil
}

func (s *OpsService) GetNetworkBandwidthSettings(ctx context.Context) (*OpsNetworkBandwidthSettings, error) {
	cfg := defaultOpsNetworkBandwidthSettings()
	if s == nil || s.settingRepo == nil {
		return cfg, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpsNetworkBandwidthSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			if b, mErr := json.Marshal(cfg); mErr == nil {
				_ = s.settingRepo.Set(ctx, SettingKeyOpsNetworkBandwidthSettings, string(b))
			}
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		cfg = defaultOpsNetworkBandwidthSettings()
	}
	normalizeOpsNetworkBandwidthSettings(cfg)
	return cfg, nil
}

func (s *OpsService) UpdateNetworkBandwidthSettings(ctx context.Context, req *OpsNetworkBandwidthSettingsUpdateRequest) (*OpsNetworkBandwidthSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req == nil {
		return nil, errors.New("invalid request")
	}
	cfg, err := s.GetNetworkBandwidthSettings(ctx)
	if err != nil {
		return nil, err
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.Iface != nil {
		cfg.Iface = strings.TrimSpace(*req.Iface)
	}
	if req.ClearRXLimit {
		cfg.RXLimitMbps = nil
	}
	if req.ClearTXLimit {
		cfg.TXLimitMbps = nil
	}
	if req.RXLimitMbps != nil {
		cfg.RXLimitMbps = req.RXLimitMbps
	}
	if req.TXLimitMbps != nil {
		cfg.TXLimitMbps = req.TXLimitMbps
	}
	if req.LimitSource != nil {
		cfg.LimitSource = *req.LimitSource
	}
	if req.AbnormalRetryProtectionEnabled != nil {
		cfg.AbnormalRetryProtectionEnabled = *req.AbnormalRetryProtectionEnabled
	}
	if req.AbnormalRetryProtectionTriggerPercent != nil {
		cfg.AbnormalRetryProtectionTriggerPercent = *req.AbnormalRetryProtectionTriggerPercent
	}
	if req.AbnormalRetryProtectionMinBodyBytes != nil {
		cfg.AbnormalRetryProtectionMinBodyBytes = *req.AbnormalRetryProtectionMinBodyBytes
	}
	if req.AbnormalRetryProtectionWindowSeconds != nil {
		cfg.AbnormalRetryProtectionWindowSeconds = *req.AbnormalRetryProtectionWindowSeconds
	}
	if req.AbnormalRetryProtectionMaxRepeats != nil {
		cfg.AbnormalRetryProtectionMaxRepeats = *req.AbnormalRetryProtectionMaxRepeats
	}
	normalizeOpsNetworkBandwidthSettings(cfg)
	if err := validateOpsNetworkBandwidthSettings(cfg); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOpsNetworkBandwidthSettings, string(raw)); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *OpsService) GetNetworkBandwidthSummary(ctx context.Context) (*OpsNetworkBandwidthSummary, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	cfg, err := s.GetNetworkBandwidthSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return &OpsNetworkBandwidthSummary{
			Enabled:     false,
			LimitSource: cfg.LimitSource,
			Status:      "disabled",
			CollectedAt: time.Now().UTC(),
		}, nil
	}
	if s.networkSampler == nil {
		s.networkSampler = newOpsNetworkSampler()
	}
	sampler := s.networkSampler
	if summary, ok := sampler.latestSummary(); ok {
		if cfg.Iface == "" || summary.Iface == cfg.Iface {
			return applyOpsNetworkBandwidthSettingsToSummary(summary, cfg), nil
		}
	}

	// Cold-start fallback: the background sampler starts immediately in the real
	// app, but this keeps tests and just-booted nodes from returning an empty page.
	if err := s.collectNetworkBandwidthSample(ctx); err != nil {
		return nil, err
	}
	if summary, ok := sampler.latestSummary(); ok {
		return applyOpsNetworkBandwidthSettingsToSummary(summary, cfg), nil
	}
	now := time.Now().UTC()
	return &OpsNetworkBandwidthSummary{
		Enabled:     true,
		LimitSource: cfg.LimitSource,
		Status:      "warming_up",
		CollectedAt: now,
		Notice:      "Collecting first sample; rates will appear after the next refresh.",
	}, nil
}

func (s *OpsService) StartNetworkBandwidthSampler() {
	if s == nil {
		return
	}
	s.networkSamplerStartOnce.Do(func() {
		if s.networkSampler == nil {
			s.networkSampler = newOpsNetworkSampler()
		}
		s.networkSamplerStopCh = make(chan struct{})
		s.networkSamplerWG.Add(1)
		go s.runNetworkBandwidthSampler()
	})
}

func (s *OpsService) StopNetworkBandwidthSampler() {
	if s == nil {
		return
	}
	s.networkSamplerStopOnce.Do(func() {
		if s.networkSamplerStopCh != nil {
			close(s.networkSamplerStopCh)
		}
	})
	s.networkSamplerWG.Wait()
}

func (s *OpsService) runNetworkBandwidthSampler() {
	defer s.networkSamplerWG.Done()

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.collectNetworkBandwidthSample(ctx); err != nil {
				logger.LegacyPrintf("service.ops_network", "[OpsNetwork] collect sample failed: %v", err)
			}
			cancel()
			timer.Reset(opsNetworkSamplerInterval)
		case <-s.networkSamplerStopCh:
			return
		}
	}
}

func (s *OpsService) collectNetworkBandwidthSample(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.IsMonitoringEnabled(ctx) {
		return nil
	}
	cfg, err := s.GetNetworkBandwidthSettings(ctx)
	if err != nil {
		return err
	}
	if s.networkSampler == nil {
		s.networkSampler = newOpsNetworkSampler()
	}
	if !cfg.Enabled {
		s.networkSampler.setLatestSummary(&OpsNetworkBandwidthSummary{
			Enabled:     false,
			LimitSource: cfg.LimitSource,
			Status:      "disabled",
			CollectedAt: time.Now().UTC(),
		})
		return nil
	}
	summary, err := s.networkSampler.collect(cfg)
	if err != nil {
		return err
	}
	s.networkSampler.setLatestSummary(summary)
	return nil
}

func (s *OpsService) GetNetworkInterfaces(ctx context.Context) ([]OpsNetworkInterfaceInfo, string, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, "", err
	}
	sampler := s.networkSampler
	if sampler == nil {
		sampler = newOpsNetworkSampler()
	}
	return sampler.interfaces()
}

func (s *opsNetworkSampler) interfaces() ([]OpsNetworkInterfaceInfo, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	defaultIface, _ := detectDefaultNetworkInterface(s.procPath, s.basePath)
	items, err := listOpsNetworkInterfaces(s.basePath, defaultIface, true)
	if err != nil {
		return nil, defaultIface, err
	}
	if len(items) == 0 {
		items, err = listOpsNetworkInterfaces(s.basePath, defaultIface, false)
		if err != nil {
			return nil, defaultIface, err
		}
	}
	return items, defaultIface, nil
}

func (s *opsNetworkSampler) latestSummary() (*OpsNetworkBandwidthSummary, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latest == nil {
		return nil, false
	}
	return cloneOpsNetworkBandwidthSummary(s.latest), true
}

func (s *opsNetworkSampler) setLatestSummary(summary *OpsNetworkBandwidthSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = cloneOpsNetworkBandwidthSummary(summary)
}

func (s *opsNetworkSampler) collect(cfg *OpsNetworkBandwidthSettings) (*OpsNetworkBandwidthSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	iface := strings.TrimSpace(cfg.Iface)
	if iface == "" {
		detected, err := detectDefaultNetworkInterface(s.procPath, s.basePath)
		if err != nil {
			return nil, err
		}
		iface = detected
	}

	now := time.Now().UTC()
	current, err := readOpsNetworkCounters(s.basePath, iface)
	if err != nil {
		return nil, err
	}

	if s.lastAt.IsZero() || s.lastIface != iface {
		s.last = current
		s.lastAt = now
		s.lastIface = iface
		return s.buildSummary(cfg, iface, opsNetworkSample{At: now}, now, "warming_up"), nil
	}

	elapsed := now.Sub(s.lastAt).Seconds()
	if elapsed <= 0 {
		return nil, fmt.Errorf("invalid network sample interval")
	}

	sample := opsNetworkSample{
		At:             now,
		RXBytesDelta:   counterDelta(current.RXBytes, s.last.RXBytes),
		TXBytesDelta:   counterDelta(current.TXBytes, s.last.TXBytes),
		RXDroppedDelta: counterDelta(current.RXDropped, s.last.RXDropped),
		TXDroppedDelta: counterDelta(current.TXDropped, s.last.TXDropped),
		RXErrorsDelta:  counterDelta(current.RXErrors, s.last.RXErrors),
		TXErrorsDelta:  counterDelta(current.TXErrors, s.last.TXErrors),
		ElapsedSeconds: elapsed,
	}
	sample.RXMbps = float64(sample.RXBytesDelta) * 8 / 1_000_000 / elapsed
	sample.TXMbps = float64(sample.TXBytesDelta) * 8 / 1_000_000 / elapsed

	s.last = current
	s.lastAt = now
	s.lastIface = iface
	s.samples = append(s.samples, sample)
	cutoff := now.Add(-1 * time.Hour)
	i := 0
	for i < len(s.samples) && s.samples[i].At.Before(cutoff) {
		i++
	}
	if i > 0 {
		copy(s.samples, s.samples[i:])
		s.samples = s.samples[:len(s.samples)-i]
	}

	summary := s.buildSummary(cfg, iface, sample, now, "")
	return summary, nil
}

func (s *opsNetworkSampler) buildSummary(cfg *OpsNetworkBandwidthSettings, iface string, current opsNetworkSample, now time.Time, statusOverride string) *OpsNetworkBandwidthSummary {
	rx1, tx1 := avgOpsNetworkMbps(s.samples, now.Add(-1*time.Minute))
	rx5, tx5 := avgOpsNetworkMbps(s.samples, now.Add(-5*time.Minute))
	rx15, tx15 := avgOpsNetworkMbps(s.samples, now.Add(-15*time.Minute))
	rxPeak, txPeak := peakOpsNetworkMbps(s.samples)

	summary := &OpsNetworkBandwidthSummary{
		Enabled:        cfg.Enabled,
		Iface:          iface,
		LimitSource:    cfg.LimitSource,
		RXLimitMbps:    cfg.RXLimitMbps,
		TXLimitMbps:    cfg.TXLimitMbps,
		RXMbps:         roundNetworkFloat(current.RXMbps),
		TXMbps:         roundNetworkFloat(current.TXMbps),
		MaxMbps:        roundNetworkFloat(math.Max(current.RXMbps, current.TXMbps)),
		RXBytesDelta:   current.RXBytesDelta,
		TXBytesDelta:   current.TXBytesDelta,
		RXDroppedDelta: current.RXDroppedDelta,
		TXDroppedDelta: current.TXDroppedDelta,
		RXErrorsDelta:  current.RXErrorsDelta,
		TXErrorsDelta:  current.TXErrorsDelta,
		ElapsedSeconds: roundNetworkFloat(current.ElapsedSeconds),
		SampleCount:    len(s.samples),
		RXAvg1mMbps:    roundNetworkFloat(rx1),
		TXAvg1mMbps:    roundNetworkFloat(tx1),
		RXAvg5mMbps:    roundNetworkFloat(rx5),
		TXAvg5mMbps:    roundNetworkFloat(tx5),
		RXAvg15mMbps:   roundNetworkFloat(rx15),
		TXAvg15mMbps:   roundNetworkFloat(tx15),
		RXPeak1hMbps:   roundNetworkFloat(rxPeak),
		TXPeak1hMbps:   roundNetworkFloat(txPeak),
		Status:         "unknown",
		CollectedAt:    now,
	}
	if statusOverride != "" {
		summary.Status = statusOverride
		summary.Notice = "Collecting first sample; rates will appear after the next refresh."
		return summary
	}

	var maxUtil *float64
	if cfg.RXLimitMbps != nil {
		v := current.RXMbps / *cfg.RXLimitMbps * 100
		v = roundNetworkFloat(v)
		summary.RXUtilizationPercent = &v
		maxUtil = maxFloatPtr(maxUtil, v)
	}
	if cfg.TXLimitMbps != nil {
		v := current.TXMbps / *cfg.TXLimitMbps * 100
		v = roundNetworkFloat(v)
		summary.TXUtilizationPercent = &v
		maxUtil = maxFloatPtr(maxUtil, v)
	}
	summary.MaxUtilizationPercent = maxUtil

	if maxUtil != nil {
		switch {
		case *maxUtil >= cfg.CriticalPercent:
			summary.Status = "critical"
		case *maxUtil >= cfg.WarnPercent:
			summary.Status = "warning"
		default:
			summary.Status = "ok"
		}
		return summary
	}

	summary.Status = "unknown"
	return summary
}

func applyOpsNetworkBandwidthSettingsToSummary(summary *OpsNetworkBandwidthSummary, cfg *OpsNetworkBandwidthSettings) *OpsNetworkBandwidthSummary {
	if summary == nil || cfg == nil {
		return summary
	}
	summary.Enabled = cfg.Enabled
	summary.LimitSource = cfg.LimitSource
	summary.RXLimitMbps = cloneFloatPtr(cfg.RXLimitMbps)
	summary.TXLimitMbps = cloneFloatPtr(cfg.TXLimitMbps)
	summary.RXUtilizationPercent = nil
	summary.TXUtilizationPercent = nil
	summary.MaxUtilizationPercent = nil

	if summary.Status == "warming_up" {
		return summary
	}

	var maxUtil *float64
	if cfg.RXLimitMbps != nil {
		v := summary.RXMbps / *cfg.RXLimitMbps * 100
		v = roundNetworkFloat(v)
		summary.RXUtilizationPercent = &v
		maxUtil = maxFloatPtr(maxUtil, v)
	}
	if cfg.TXLimitMbps != nil {
		v := summary.TXMbps / *cfg.TXLimitMbps * 100
		v = roundNetworkFloat(v)
		summary.TXUtilizationPercent = &v
		maxUtil = maxFloatPtr(maxUtil, v)
	}
	summary.MaxUtilizationPercent = maxUtil

	if maxUtil == nil {
		summary.Status = "unknown"
		return summary
	}
	switch {
	case *maxUtil >= cfg.CriticalPercent:
		summary.Status = "critical"
	case *maxUtil >= cfg.WarnPercent:
		summary.Status = "warning"
	default:
		summary.Status = "ok"
	}
	return summary
}

func counterDelta(current, last uint64) int64 {
	if current >= last {
		return int64(current - last)
	}
	return int64(current)
}

func maxFloatPtr(current *float64, next float64) *float64 {
	if current == nil || next > *current {
		v := next
		return &v
	}
	return current
}

func avgOpsNetworkMbps(samples []opsNetworkSample, cutoff time.Time) (float64, float64) {
	var rx, tx float64
	var n int
	for _, sample := range samples {
		if sample.At.Before(cutoff) {
			continue
		}
		rx += sample.RXMbps
		tx += sample.TXMbps
		n++
	}
	if n == 0 {
		return 0, 0
	}
	return rx / float64(n), tx / float64(n)
}

func peakOpsNetworkMbps(samples []opsNetworkSample) (float64, float64) {
	var rx, tx float64
	for _, sample := range samples {
		if sample.RXMbps > rx {
			rx = sample.RXMbps
		}
		if sample.TXMbps > tx {
			tx = sample.TXMbps
		}
	}
	return rx, tx
}

func cloneOpsNetworkBandwidthSummary(summary *OpsNetworkBandwidthSummary) *OpsNetworkBandwidthSummary {
	if summary == nil {
		return nil
	}
	clone := *summary
	if summary.RXLimitMbps != nil {
		v := *summary.RXLimitMbps
		clone.RXLimitMbps = &v
	}
	if summary.TXLimitMbps != nil {
		v := *summary.TXLimitMbps
		clone.TXLimitMbps = &v
	}
	if summary.RXUtilizationPercent != nil {
		v := *summary.RXUtilizationPercent
		clone.RXUtilizationPercent = &v
	}
	if summary.TXUtilizationPercent != nil {
		v := *summary.TXUtilizationPercent
		clone.TXUtilizationPercent = &v
	}
	if summary.MaxUtilizationPercent != nil {
		v := *summary.MaxUtilizationPercent
		clone.MaxUtilizationPercent = &v
	}
	return &clone
}

func cloneFloatPtr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func roundNetworkFloat(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func readOpsNetworkCounters(basePath, iface string) (opsNetworkCounters, error) {
	read := func(name string) (uint64, error) {
		raw, err := os.ReadFile(filepath.Join(basePath, iface, "statistics", name))
		if err != nil {
			return 0, err
		}
		v, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			return 0, err
		}
		return v, nil
	}
	var c opsNetworkCounters
	var err error
	if c.RXBytes, err = read("rx_bytes"); err != nil {
		return c, err
	}
	if c.TXBytes, err = read("tx_bytes"); err != nil {
		return c, err
	}
	c.RXDropped, _ = read("rx_dropped")
	c.TXDropped, _ = read("tx_dropped")
	c.RXErrors, _ = read("rx_errors")
	c.TXErrors, _ = read("tx_errors")
	return c, nil
}

func detectDefaultNetworkInterface(routePath, basePath string) (string, error) {
	if iface, err := detectDefaultInterfaceFromRoute(routePath); err == nil && iface != "" {
		return iface, nil
	}
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if isIgnoredOpsNetworkInterface(name) {
			continue
		}
		stateRaw, err := os.ReadFile(filepath.Join(basePath, name, "operstate"))
		if err == nil && strings.TrimSpace(string(stateRaw)) != "up" {
			continue
		}
		return name, nil
	}
	return "", fmt.Errorf("no usable network interface found")
}

func detectDefaultInterfaceFromRoute(routePath string) (string, error) {
	f, err := os.Open(routePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return "", scanner.Err()
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		if fields[1] == "00000000" {
			return fields[0], nil
		}
	}
	return "", scanner.Err()
}

func listOpsNetworkInterfaces(basePath, defaultIface string, skipVirtual bool) ([]OpsNetworkInterfaceInfo, error) {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return nil, err
	}
	items := make([]OpsNetworkInterfaceInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "lo" {
			continue
		}
		if skipVirtual && isIgnoredOpsNetworkInterface(name) {
			continue
		}
		if _, err := os.Stat(filepath.Join(basePath, name, "statistics", "rx_bytes")); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(basePath, name, "statistics", "tx_bytes")); err != nil {
			continue
		}
		state := "unknown"
		if stateRaw, err := os.ReadFile(filepath.Join(basePath, name, "operstate")); err == nil {
			if trimmed := strings.TrimSpace(string(stateRaw)); trimmed != "" {
				state = trimmed
			}
		}
		items = append(items, OpsNetworkInterfaceInfo{
			Name:      name,
			State:     state,
			IsDefault: name == defaultIface,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDefault != items[j].IsDefault {
			return items[i].IsDefault
		}
		if items[i].State == "up" && items[j].State != "up" {
			return true
		}
		if items[i].State != "up" && items[j].State == "up" {
			return false
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func isIgnoredOpsNetworkInterface(name string) bool {
	return name == "lo" ||
		strings.HasPrefix(name, "docker") ||
		strings.HasPrefix(name, "br-") ||
		strings.HasPrefix(name, "veth")
}
