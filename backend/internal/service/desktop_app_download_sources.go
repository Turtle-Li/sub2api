package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	defaultDesktopChatGPTMacOSArm64URL  = "https://persistent.oaistatic.com/codex-app-prod/ChatGPT.dmg"
	defaultDesktopChatGPTMacOSX64URL    = "https://persistent.oaistatic.com/codex-app-prod/ChatGPT-latest-x64.dmg"
	defaultDesktopChatGPTWindowsStoreID = "9PLM9XGG6VKS"
	defaultDesktopChatGPTWindowsURL     = "https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi"
	defaultDesktopClaudeMacOSURL        = "https://claude.ai/api/desktop/darwin/universal/dmg/latest/redirect"
	defaultDesktopClaudeWindowsX64URL   = "https://claude.ai/api/desktop/win32/x64/setup/latest/redirect"
	defaultDesktopClaudeWindowsArm64URL = "https://claude.ai/api/desktop/win32/arm64/setup/latest/redirect"
)

var windowsStoreProductIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{8,32}$`)

// DesktopAppDownloadSources is the public, admin-managed installer-source
// contract consumed by Sub2 Desktop. Empty fields intentionally fall back to
// the built-in official sources so older databases remain usable.
type DesktopAppDownloadSources struct {
	ChatGPTMacOSArm64URL         string `json:"chatgpt_macos_arm64_url"`
	ChatGPTMacOSX64URL           string `json:"chatgpt_macos_x64_url"`
	ChatGPTWindowsStoreProductID string `json:"chatgpt_windows_store_product_id"`
	ChatGPTWindowsInstallerURL   string `json:"chatgpt_windows_installer_url"`
	ClaudeMacOSURL               string `json:"claude_macos_url"`
	ClaudeWindowsX64URL          string `json:"claude_windows_x64_url"`
	ClaudeWindowsArm64URL        string `json:"claude_windows_arm64_url"`
}

// DefaultDesktopAppDownloadSources returns the official installer sources
// shipped with this server version.
func DefaultDesktopAppDownloadSources() DesktopAppDownloadSources {
	return DesktopAppDownloadSources{
		ChatGPTMacOSArm64URL:         defaultDesktopChatGPTMacOSArm64URL,
		ChatGPTMacOSX64URL:           defaultDesktopChatGPTMacOSX64URL,
		ChatGPTWindowsStoreProductID: defaultDesktopChatGPTWindowsStoreID,
		ChatGPTWindowsInstallerURL:   defaultDesktopChatGPTWindowsURL,
		ClaudeMacOSURL:               defaultDesktopClaudeMacOSURL,
		ClaudeWindowsX64URL:          defaultDesktopClaudeWindowsX64URL,
		ClaudeWindowsArm64URL:        defaultDesktopClaudeWindowsArm64URL,
	}
}

func normalizeDesktopAppDownloadSources(sources DesktopAppDownloadSources) DesktopAppDownloadSources {
	defaults := DefaultDesktopAppDownloadSources()
	sources.ChatGPTMacOSArm64URL = desktopSourceOrDefault(
		strings.TrimSpace(sources.ChatGPTMacOSArm64URL),
		defaults.ChatGPTMacOSArm64URL,
	)
	sources.ChatGPTMacOSX64URL = desktopSourceOrDefault(
		strings.TrimSpace(sources.ChatGPTMacOSX64URL),
		defaults.ChatGPTMacOSX64URL,
	)
	sources.ChatGPTWindowsStoreProductID = desktopSourceOrDefault(
		strings.TrimSpace(sources.ChatGPTWindowsStoreProductID),
		defaults.ChatGPTWindowsStoreProductID,
	)
	sources.ChatGPTWindowsInstallerURL = desktopSourceOrDefault(
		strings.TrimSpace(sources.ChatGPTWindowsInstallerURL),
		defaults.ChatGPTWindowsInstallerURL,
	)
	sources.ClaudeMacOSURL = desktopSourceOrDefault(
		strings.TrimSpace(sources.ClaudeMacOSURL),
		defaults.ClaudeMacOSURL,
	)
	sources.ClaudeWindowsX64URL = desktopSourceOrDefault(
		strings.TrimSpace(sources.ClaudeWindowsX64URL),
		defaults.ClaudeWindowsX64URL,
	)
	sources.ClaudeWindowsArm64URL = desktopSourceOrDefault(
		strings.TrimSpace(sources.ClaudeWindowsArm64URL),
		defaults.ClaudeWindowsArm64URL,
	)
	return sources
}

func desktopSourceOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// NormalizeAndValidateDesktopAppDownloadSources accepts future official hosts
// without requiring a client release, while enforcing secure absolute URLs and
// a Store-compatible product ID.
func NormalizeAndValidateDesktopAppDownloadSources(
	sources DesktopAppDownloadSources,
) (DesktopAppDownloadSources, error) {
	sources = normalizeDesktopAppDownloadSources(sources)
	urls := map[string]string{
		"ChatGPT macOS arm64 URL":       sources.ChatGPTMacOSArm64URL,
		"ChatGPT macOS x64 URL":         sources.ChatGPTMacOSX64URL,
		"ChatGPT Windows installer URL": sources.ChatGPTWindowsInstallerURL,
		"Claude macOS URL":              sources.ClaudeMacOSURL,
		"Claude Windows x64 URL":        sources.ClaudeWindowsX64URL,
		"Claude Windows arm64 URL":      sources.ClaudeWindowsArm64URL,
	}
	for label, rawURL := range urls {
		if err := validateDesktopInstallerHTTPSURL(rawURL); err != nil {
			return DesktopAppDownloadSources{}, fmt.Errorf("%s: %w", label, err)
		}
	}
	if !windowsStoreProductIDPattern.MatchString(sources.ChatGPTWindowsStoreProductID) {
		return DesktopAppDownloadSources{}, fmt.Errorf(
			"ChatGPT Windows Store product ID must contain 8-32 letters or digits",
		)
	}
	return sources, nil
}

func validateDesktopInstallerHTTPSURL(rawURL string) error {
	if len(rawURL) > 2048 {
		return fmt.Errorf("URL is too long")
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("must be an absolute URL")
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("must use https with a valid host")
	}
	if parsed.User != nil {
		return fmt.Errorf("must not contain embedded credentials")
	}
	return nil
}

func parseDesktopAppDownloadSources(raw string) DesktopAppDownloadSources {
	var sources DesktopAppDownloadSources
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &sources); err != nil {
			return DefaultDesktopAppDownloadSources()
		}
	}
	normalized, err := NormalizeAndValidateDesktopAppDownloadSources(sources)
	if err != nil {
		return DefaultDesktopAppDownloadSources()
	}
	return normalized
}

func marshalDesktopAppDownloadSources(sources DesktopAppDownloadSources) (string, error) {
	normalized, err := NormalizeAndValidateDesktopAppDownloadSources(sources)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal desktop app download sources: %w", err)
	}
	return string(payload), nil
}
