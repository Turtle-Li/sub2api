package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxDesktopPromotions = 24
)

var desktopPromotionIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var desktopPromotionIcons = map[string]struct{}{
	"agent":    {},
	"chat":     {},
	"globe":    {},
	"link":     {},
	"sparkles": {},
	"tool":     {},
}

var desktopPromotionSurfaces = map[string]struct{}{
	"discover": {},
	"overview": {},
}

// DesktopPromotion is the versioned, presentation-safe catalog item shared by
// the account control plane and TT Switch. It deliberately does not accept
// arbitrary HTML, SVG, or remote image URLs.
type DesktopPromotion struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	TargetURL string   `json:"target_url"`
	CTALabel  string   `json:"cta_label"`
	Badge     string   `json:"badge,omitempty"`
	Icon      string   `json:"icon"`
	Surfaces  []string `json:"surfaces"`
	Enabled   bool     `json:"enabled"`
	SortOrder int      `json:"sort_order"`
	StartsAt  string   `json:"starts_at,omitempty"`
	EndsAt    string   `json:"ends_at,omitempty"`
}

// NormalizeDesktopPromotions validates and canonicalizes an admin-provided
// catalog. The normalized value is safe to persist and return to clients.
func NormalizeDesktopPromotions(items []DesktopPromotion) ([]DesktopPromotion, error) {
	if items == nil {
		return []DesktopPromotion{}, nil
	}
	if len(items) > maxDesktopPromotions {
		return nil, fmt.Errorf("too many items (maximum %d)", maxDesktopPromotions)
	}

	normalized := make([]DesktopPromotion, 0, len(items))
	seenIDs := make(map[string]struct{}, len(items))
	for index, item := range items {
		item.ID = strings.ToLower(strings.TrimSpace(item.ID))
		item.Title = strings.TrimSpace(item.Title)
		item.Summary = strings.TrimSpace(item.Summary)
		item.TargetURL = strings.TrimSpace(item.TargetURL)
		item.CTALabel = strings.TrimSpace(item.CTALabel)
		item.Badge = strings.TrimSpace(item.Badge)
		item.Icon = strings.ToLower(strings.TrimSpace(item.Icon))
		item.StartsAt = strings.TrimSpace(item.StartsAt)
		item.EndsAt = strings.TrimSpace(item.EndsAt)

		if !desktopPromotionIDPattern.MatchString(item.ID) {
			return nil, fmt.Errorf("item %d has an invalid id", index+1)
		}
		if _, exists := seenIDs[item.ID]; exists {
			return nil, fmt.Errorf("duplicate item id %q", item.ID)
		}
		seenIDs[item.ID] = struct{}{}

		if err := validatePromotionText(item.Title, "title", 80, true); err != nil {
			return nil, fmt.Errorf("item %q: %w", item.ID, err)
		}
		if err := validatePromotionText(item.Summary, "summary", 240, false); err != nil {
			return nil, fmt.Errorf("item %q: %w", item.ID, err)
		}
		if err := validatePromotionText(item.CTALabel, "cta_label", 24, false); err != nil {
			return nil, fmt.Errorf("item %q: %w", item.ID, err)
		}
		if item.CTALabel == "" {
			item.CTALabel = "打开"
		}
		if err := validatePromotionText(item.Badge, "badge", 24, false); err != nil {
			return nil, fmt.Errorf("item %q: %w", item.ID, err)
		}

		targetURL, err := normalizeDesktopPromotionURL(item.TargetURL)
		if err != nil {
			return nil, fmt.Errorf("item %q target_url: %w", item.ID, err)
		}
		item.TargetURL = targetURL

		if item.Icon == "" {
			item.Icon = "link"
		}
		if _, ok := desktopPromotionIcons[item.Icon]; !ok {
			return nil, fmt.Errorf("item %q uses unsupported icon %q", item.ID, item.Icon)
		}

		surfaces, err := normalizePromotionSurfaces(item.Surfaces)
		if err != nil {
			return nil, fmt.Errorf("item %q: %w", item.ID, err)
		}
		item.Surfaces = surfaces

		start, err := normalizePromotionTime(item.StartsAt)
		if err != nil {
			return nil, fmt.Errorf("item %q starts_at: %w", item.ID, err)
		}
		end, err := normalizePromotionTime(item.EndsAt)
		if err != nil {
			return nil, fmt.Errorf("item %q ends_at: %w", item.ID, err)
		}
		if start != nil {
			item.StartsAt = start.Format(time.RFC3339)
		}
		if end != nil {
			item.EndsAt = end.Format(time.RFC3339)
		}
		if start != nil && end != nil && !end.After(*start) {
			return nil, fmt.Errorf("item %q ends_at must be after starts_at", item.ID)
		}
		if item.SortOrder < -10000 || item.SortOrder > 10000 {
			return nil, fmt.Errorf("item %q sort_order must be between -10000 and 10000", item.ID)
		}

		normalized = append(normalized, item)
	}

	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].SortOrder == normalized[j].SortOrder {
			return normalized[i].ID < normalized[j].ID
		}
		return normalized[i].SortOrder < normalized[j].SortOrder
	})
	return normalized, nil
}

func validatePromotionText(value, field string, maxRunes int, required bool) error {
	if required && value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s is too long (maximum %d characters)", field, maxRunes)
	}
	return nil
}

func normalizeDesktopPromotionURL(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 {
		return "", fmt.Errorf("must be a non-empty URL of at most 2048 characters")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("must be an absolute URL")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("must not include credentials")
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("must include a host")
	}
	if scheme != "https" && !(scheme == "http" && isDesktopPromotionLoopback(host)) {
		return "", fmt.Errorf("must use HTTPS (HTTP is allowed only for loopback development)")
	}
	parsed.Scheme = scheme
	return parsed.String(), nil
}

func isDesktopPromotionLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func normalizePromotionSurfaces(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{"discover"}, nil
	}
	result := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, surface := range raw {
		surface = strings.ToLower(strings.TrimSpace(surface))
		if _, ok := desktopPromotionSurfaces[surface]; !ok {
			return nil, fmt.Errorf("unsupported surface %q", surface)
		}
		if _, duplicate := seen[surface]; duplicate {
			continue
		}
		seen[surface] = struct{}{}
		result = append(result, surface)
	}
	sort.Strings(result)
	return result, nil
}

func normalizePromotionTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("must use RFC3339")
	}
	return &value, nil
}

func NormalizeDesktopPromotionsJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "[]"
	}
	var items []DesktopPromotion
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return "", fmt.Errorf("must be a JSON array: %w", err)
	}
	normalized, err := NormalizeDesktopPromotions(items)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode promotions: %w", err)
	}
	return string(encoded), nil
}

func ParseDesktopPromotions(raw string) []DesktopPromotion {
	normalized, err := NormalizeDesktopPromotionsJSON(raw)
	if err != nil {
		return []DesktopPromotion{}
	}
	var items []DesktopPromotion
	if err := json.Unmarshal([]byte(normalized), &items); err != nil {
		return []DesktopPromotion{}
	}
	return items
}

func ActiveDesktopPromotions(raw string, now time.Time) []DesktopPromotion {
	items := ParseDesktopPromotions(raw)
	active := make([]DesktopPromotion, 0, len(items))
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		if item.StartsAt != "" {
			start, _ := time.Parse(time.RFC3339, item.StartsAt)
			if now.Before(start) {
				continue
			}
		}
		if item.EndsAt != "" {
			end, _ := time.Parse(time.RFC3339, item.EndsAt)
			if !now.Before(end) {
				continue
			}
		}
		active = append(active, item)
	}
	return active
}

func (s *SettingService) GetDesktopPromotions(ctx context.Context, now time.Time) ([]DesktopPromotion, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyDesktopPromotions)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return []DesktopPromotion{}, nil
		}
		return nil, fmt.Errorf("get desktop promotions: %w", err)
	}
	return ActiveDesktopPromotions(value, now), nil
}
