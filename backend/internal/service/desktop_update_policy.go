package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const DesktopUpdatePolicySchemaVersion = 1

const (
	maxDesktopUpdateReasonRunes = 240
	maxDesktopDownloadURLLength = 2048
)

// DesktopUpdatePolicy is the administrator-owned version policy. It is kept
// deliberately separate from the updater manifest: clients still verify the
// signed Tauri update artifact before installing it.
type DesktopUpdatePolicy struct {
	SchemaVersion           int    `json:"schema_version"`
	LatestVersion           string `json:"latest_version"`
	MinimumSupportedVersion string `json:"minimum_supported_version"`
	EnforcementEnabled      bool   `json:"enforcement_enabled"`
	EnforceAfter            string `json:"enforce_after,omitempty"`
	Reason                  string `json:"reason,omitempty"`
	ManualDownloadURL       string `json:"manual_download_url,omitempty"`
}

// DesktopUpdateStatus is the public, client-specific result returned to TT
// Switch. The server computes the two booleans so clients cannot disagree
// about activation time or version ordering.
type DesktopUpdateStatus struct {
	SchemaVersion           int    `json:"schema_version"`
	CurrentVersion          string `json:"current_version"`
	LatestVersion           string `json:"latest_version"`
	MinimumSupportedVersion string `json:"minimum_supported_version"`
	UpdateAvailable         bool   `json:"update_available"`
	UpdateRequired          bool   `json:"update_required"`
	EnforcementEnabled      bool   `json:"enforcement_enabled"`
	EnforceAfter            string `json:"enforce_after,omitempty"`
	Reason                  string `json:"reason,omitempty"`
	ManualDownloadURL       string `json:"manual_download_url,omitempty"`
}

func DefaultDesktopUpdatePolicy() DesktopUpdatePolicy {
	return DesktopUpdatePolicy{
		SchemaVersion: DesktopUpdatePolicySchemaVersion,
	}
}

func NormalizeDesktopUpdatePolicy(policy DesktopUpdatePolicy) (DesktopUpdatePolicy, error) {
	latestVersion, err := normalizeDesktopVersion(policy.LatestVersion, true)
	if err != nil {
		return DesktopUpdatePolicy{}, fmt.Errorf("latest_version %w", err)
	}
	minimumVersion, err := normalizeDesktopVersion(policy.MinimumSupportedVersion, true)
	if err != nil {
		return DesktopUpdatePolicy{}, fmt.Errorf("minimum_supported_version %w", err)
	}
	if latestVersion != "" && minimumVersion != "" &&
		semver.Compare("v"+latestVersion, "v"+minimumVersion) < 0 {
		return DesktopUpdatePolicy{}, fmt.Errorf(
			"latest_version must be greater than or equal to minimum_supported_version",
		)
	}
	if policy.EnforcementEnabled && minimumVersion == "" {
		return DesktopUpdatePolicy{}, fmt.Errorf(
			"minimum_supported_version is required when enforcement is enabled",
		)
	}

	enforceAfter := strings.TrimSpace(policy.EnforceAfter)
	if enforceAfter != "" {
		parsed, parseErr := time.Parse(time.RFC3339, enforceAfter)
		if parseErr != nil {
			return DesktopUpdatePolicy{}, fmt.Errorf("enforce_after must use RFC3339")
		}
		enforceAfter = parsed.UTC().Format(time.RFC3339)
	}

	reason := strings.TrimSpace(policy.Reason)
	if len([]rune(reason)) > maxDesktopUpdateReasonRunes {
		return DesktopUpdatePolicy{}, fmt.Errorf(
			"reason is too long (maximum %d characters)",
			maxDesktopUpdateReasonRunes,
		)
	}
	manualDownloadURL, err := normalizeDesktopDownloadURL(policy.ManualDownloadURL)
	if err != nil {
		return DesktopUpdatePolicy{}, fmt.Errorf("manual_download_url %w", err)
	}

	return DesktopUpdatePolicy{
		SchemaVersion:           DesktopUpdatePolicySchemaVersion,
		LatestVersion:           latestVersion,
		MinimumSupportedVersion: minimumVersion,
		EnforcementEnabled:      policy.EnforcementEnabled,
		EnforceAfter:            enforceAfter,
		Reason:                  reason,
		ManualDownloadURL:       manualDownloadURL,
	}, nil
}

func ParseDesktopUpdatePolicy(raw string) (DesktopUpdatePolicy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultDesktopUpdatePolicy(), nil
	}
	var policy DesktopUpdatePolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return DesktopUpdatePolicy{}, fmt.Errorf("decode desktop update policy: %w", err)
	}
	if policy.SchemaVersion != 0 &&
		policy.SchemaVersion != DesktopUpdatePolicySchemaVersion {
		return DesktopUpdatePolicy{}, fmt.Errorf(
			"unsupported desktop update policy schema %d",
			policy.SchemaVersion,
		)
	}
	return NormalizeDesktopUpdatePolicy(policy)
}

func (s *SettingService) GetDesktopUpdatePolicy(ctx context.Context) (DesktopUpdatePolicy, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDesktopUpdatePolicy)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultDesktopUpdatePolicy(), nil
		}
		return DesktopUpdatePolicy{}, fmt.Errorf("get desktop update policy: %w", err)
	}
	return ParseDesktopUpdatePolicy(raw)
}

func (policy DesktopUpdatePolicy) StatusFor(
	clientVersion string,
	now time.Time,
) DesktopUpdateStatus {
	normalizedClient, clientErr := normalizeDesktopVersion(clientVersion, false)
	enforcementActive := policy.EnforcementEnabled
	if policy.EnforceAfter != "" {
		if enforceAt, err := time.Parse(time.RFC3339, policy.EnforceAfter); err == nil {
			enforcementActive = enforcementActive && !now.Before(enforceAt)
		}
	}

	updateAvailable := clientErr == nil &&
		policy.LatestVersion != "" &&
		semver.Compare("v"+normalizedClient, "v"+policy.LatestVersion) < 0
	updateRequired := enforcementActive &&
		policy.MinimumSupportedVersion != "" &&
		(clientErr != nil ||
			semver.Compare("v"+normalizedClient, "v"+policy.MinimumSupportedVersion) < 0)

	return DesktopUpdateStatus{
		SchemaVersion:           DesktopUpdatePolicySchemaVersion,
		CurrentVersion:          strings.TrimSpace(clientVersion),
		LatestVersion:           policy.LatestVersion,
		MinimumSupportedVersion: policy.MinimumSupportedVersion,
		UpdateAvailable:         updateAvailable,
		UpdateRequired:          updateRequired,
		EnforcementEnabled:      policy.EnforcementEnabled,
		EnforceAfter:            policy.EnforceAfter,
		Reason:                  policy.Reason,
		ManualDownloadURL:       policy.ManualDownloadURL,
	}
}

// DesktopClientVersion extracts the immutable version identifier added by the
// official desktop build. The boolean indicates that the request claims to be
// TT Switch even if the version itself is malformed; enforcement treats that
// malformed version as unsupported.
func DesktopClientVersion(explicitVersion, userAgent string) (string, bool) {
	if version := strings.TrimSpace(explicitVersion); version != "" {
		return version, true
	}
	userAgent = strings.TrimSpace(userAgent)
	const prefix = "TT-Switch/"
	if len(userAgent) < len(prefix) ||
		!strings.EqualFold(userAgent[:len(prefix)], prefix) {
		return "", false
	}
	fields := strings.Fields(userAgent[len(prefix):])
	if len(fields) == 0 {
		return "", true
	}
	return strings.TrimSpace(fields[0]), true
}

func normalizeDesktopVersion(raw string, allowEmpty bool) (string, error) {
	version := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	if version == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("must be a semantic version")
	}
	canonical := "v" + version
	if !semver.IsValid(canonical) || semver.Canonical(canonical) != canonical {
		return "", fmt.Errorf("must be a canonical semantic version such as 1.2.3")
	}
	return version, nil
}

func normalizeDesktopDownloadURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxDesktopDownloadURLLength {
		return "", fmt.Errorf("is too long")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("must be an absolute URL")
	}
	host := parsed.Hostname()
	isLoopback := strings.EqualFold(host, "localhost")
	if address := net.ParseIP(host); address != nil && address.IsLoopback() {
		isLoopback = true
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback) {
		return "", fmt.Errorf("must use HTTPS")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("must not contain credentials or a fragment")
	}
	return parsed.String(), nil
}
