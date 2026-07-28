package service

import (
	"context"
	"encoding/json"
	"fmt"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const DesktopConsoleSchemaVersion = 2

// DesktopConsoleSettings is the deliberately small control-plane contract for
// TT Switch. It shares administrator identity with the service admin, but not
// the service admin's broad system-settings payload.
type DesktopConsoleSettings struct {
	SchemaVersion   int                 `json:"schema_version"`
	ControlPlaneURL string              `json:"control_plane_url"`
	Promotions      []DesktopPromotion  `json:"promotions"`
	UpdatePolicy    DesktopUpdatePolicy `json:"update_policy"`
}

// GetDesktopConsoleSettings reads only settings owned by TT Switch.
func (s *SettingService) GetDesktopConsoleSettings(ctx context.Context) (*DesktopConsoleSettings, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyDesktopControlPlaneURL,
		SettingKeyDesktopPromotions,
		SettingKeyDesktopUpdatePolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("get desktop console settings: %w", err)
	}

	controlPlaneURL, err := NormalizeDesktopControlPlaneURL(values[SettingKeyDesktopControlPlaneURL])
	if err != nil {
		return nil, fmt.Errorf("invalid stored desktop control plane url: %w", err)
	}
	updatePolicy, err := ParseDesktopUpdatePolicy(values[SettingKeyDesktopUpdatePolicy])
	if err != nil {
		return nil, fmt.Errorf("invalid stored desktop update policy: %w", err)
	}

	return &DesktopConsoleSettings{
		SchemaVersion:   DesktopConsoleSchemaVersion,
		ControlPlaneURL: controlPlaneURL,
		Promotions:      ParseDesktopPromotions(values[SettingKeyDesktopPromotions]),
		UpdatePolicy:    updatePolicy,
	}, nil
}

// UpdateDesktopConsoleSettings validates and persists the complete TT Switch
// settings document without reading or writing unrelated service settings.
func (s *SettingService) UpdateDesktopConsoleSettings(
	ctx context.Context,
	settings DesktopConsoleSettings,
) (*DesktopConsoleSettings, error) {
	controlPlaneURL, err := NormalizeDesktopControlPlaneURL(settings.ControlPlaneURL)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_DESKTOP_CONTROL_PLANE_URL", err.Error())
	}
	promotions, err := NormalizeDesktopPromotions(settings.Promotions)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_DESKTOP_PROMOTIONS", err.Error())
	}
	promotionBytes, err := json.Marshal(promotions)
	if err != nil {
		return nil, fmt.Errorf("encode desktop promotions: %w", err)
	}
	updatePolicy, err := NormalizeDesktopUpdatePolicy(settings.UpdatePolicy)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_DESKTOP_UPDATE_POLICY", err.Error())
	}
	updatePolicyBytes, err := json.Marshal(updatePolicy)
	if err != nil {
		return nil, fmt.Errorf("encode desktop update policy: %w", err)
	}

	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyDesktopControlPlaneURL: controlPlaneURL,
		SettingKeyDesktopPromotions:      string(promotionBytes),
		SettingKeyDesktopUpdatePolicy:    string(updatePolicyBytes),
	}); err != nil {
		return nil, fmt.Errorf("update desktop console settings: %w", err)
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}

	return &DesktopConsoleSettings{
		SchemaVersion:   DesktopConsoleSchemaVersion,
		ControlPlaneURL: controlPlaneURL,
		Promotions:      promotions,
		UpdatePolicy:    updatePolicy,
	}, nil
}
