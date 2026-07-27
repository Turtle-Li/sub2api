package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// NormalizeDesktopControlPlaneURL validates and canonicalizes the account
// control-plane origin returned to desktop clients.
func NormalizeDesktopControlPlaneURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("must be an absolute URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("must not include credentials, query parameters, or fragments")
	}
	if path := parsed.EscapedPath(); path != "" && path != "/" {
		return "", fmt.Errorf("must be an origin without a path")
	}

	scheme := strings.ToLower(parsed.Scheme)
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("must include a host")
	}
	if scheme != "https" && !(scheme == "http" && isLoopbackDesktopHost(host)) {
		return "", fmt.Errorf("must use HTTPS (HTTP is allowed only for loopback development)")
	}

	parsed.Scheme = scheme
	parsed.Path = ""
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func isLoopbackDesktopHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// GetDesktopControlPlaneURL returns the independently configured desktop
// account control plane. A missing or blank setting intentionally signals that
// the public handler should fall back to the discovery request's own origin.
func (s *SettingService) GetDesktopControlPlaneURL(ctx context.Context) (string, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyDesktopControlPlaneURL)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("get desktop control plane url: %w", err)
	}

	normalized, err := NormalizeDesktopControlPlaneURL(value)
	if err != nil {
		return "", fmt.Errorf("invalid desktop control plane url: %w", err)
	}
	return normalized, nil
}
