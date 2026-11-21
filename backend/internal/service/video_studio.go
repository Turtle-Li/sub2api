package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// VideoStudioParameters contains submitted options, never prompt or media data.
type VideoStudioParameters struct {
	DurationMode   VideoStudioDurationMode `json:"duration_mode,omitempty"`
	ReferenceKinds []string                `json:"reference_kinds,omitempty"`
	Resolution     string                  `json:"resolution,omitempty"`
	NativeSize     string                  `json:"native_size,omitempty"`
	Timeout        *int                    `json:"timeout,omitempty"`
	HasExtra       bool                    `json:"has_extra,omitempty"`
	MaterialMode   string                  `json:"material_mode"`
	ReferenceCount int                     `json:"reference_count"`
	Seed           *int64                  `json:"seed,omitempty"`
}

// VideoStudioJob is a private history index, not a provider job or a second billing ledger.
type VideoStudioJob struct {
	ChargedAmount    *float64               `json:"charged_amount,omitempty"`
	ChargedCurrency  *string                `json:"charged_currency,omitempty"`
	ID               string                 `json:"id"`
	Model            string                 `json:"model"`
	Seconds          int                    `json:"seconds"` // Submission/quote basis, never playback duration.
	ActualDurationMS int64                  `json:"actual_duration_ms,omitempty"`
	Size             string                 `json:"size"`
	Status           string                 `json:"status"`
	CreatedAt        time.Time              `json:"created_at"`
	ExpiresAt        *time.Time             `json:"expires_at,omitempty"`
	BillingReview    bool                   `json:"billing_review,omitempty"`
	UserID           int64                  `json:"-"`
	KeyID            int64                  `json:"-"`
	GroupID          int64                  `json:"-"`
	Fingerprint      string                 `json:"-"`
	Parameters       *VideoStudioParameters `json:"parameters,omitempty"`
	HasPoster        bool                   `json:"has_poster,omitempty"`
	PosterExpiresAt  *time.Time             `json:"poster_expires_at,omitempty"`
	Receipt          string                 `json:"-"`
}

func (j VideoStudioJob) Visible(now time.Time) VideoStudioJob {
	if j.KeyID == VideoStudioWalletSlot && j.Status == "completed" && j.ChargedAmount == nil {
		j.BillingReview = true
	}
	if IsGoodPutVideoModel(j.Model) || (j.Model == MuseVideoModel && j.KeyID == VideoStudioWalletSlot) {
		expiry := j.CreatedAt.Add(goodPutVideoReceiptWindow)
		j.ExpiresAt = &expiry
		if !now.Before(expiry) && j.Receipt != "" && j.Status != "failed" {
			j.Status = "expired"
		}
	}
	if j.Status == "submitting" && now.Sub(j.CreatedAt) > 5*time.Minute {
		j.Status = "uncertain"
	}
	return j
}

var ErrVideoStudioNotFound = errors.New("video studio job not found")

// Writes only metadata observed in the delivered file; it cannot affect a wallet or a receipt.
type VideoStudioDurationWriter interface {
	RecordVideoStudioDuration(context.Context, VideoStudioJob, int64) error
}

type VideoStudioStore interface {
	VideoStudioDurationWriter
	VideoStudioGroupIDs(context.Context, int64) ([]int64, error)
	ClaimVideoStudioJob(context.Context, VideoStudioJob) (bool, error)
	FindVideoStudioJob(context.Context, int64, string) (VideoStudioJob, error)
	ListVideoStudioJobs(context.Context, int64, string, int) ([]VideoStudioJob, error)
	UpdateVideoStudioJob(context.Context, int64, string, string, string, bool) error
}

func (s *APIKeyService) VideoStudioStore() VideoStudioStore {
	if s == nil {
		return nil
	}
	r, _ := s.apiKeyRepo.(VideoStudioStore)
	return r
}

type VideoStudioMuseOptions struct {
	ResolutionMaxBytes int `json:"resolution_max_bytes"`
	ExtraMaxBytes      int `json:"extra_max_bytes"`
	TimeoutMax         int `json:"timeout_max"`
	NativeSizeMaxBytes int `json:"native_size_max_bytes"`
}

const VideoStudioMaxReferences = 10

type VideoStudioDurationMode string

const (
	VideoStudioDurationAutomatic  VideoStudioDurationMode = "automatic"
	VideoStudioDurationSelectable VideoStudioDurationMode = "selectable"
)

type VideoStudioModel struct {
	DurationMode   VideoStudioDurationMode `json:"duration_mode"`
	ReferenceKinds []string                `json:"reference_kinds"`
	MuseOptions    *VideoStudioMuseOptions `json:"muse_options,omitempty"`
	ID             string                  `json:"id"`
	Label          string                  `json:"label"`
	Provider       string                  `json:"provider"`
	MinSeconds     int                     `json:"min_seconds"`
	MaxSeconds     int                     `json:"max_seconds"`
	DefaultSeconds int                     `json:"default_seconds"`
	Sizes          []string                `json:"sizes"`
	SupportsFrames bool                    `json:"supports_frames"`
	SupportsSeed   bool                    `json:"supports_seed"`
	MaxReferences  int                     `json:"max_references"`
	Quotes         map[int]float64         `json:"quotes"`
	Currency       string                  `json:"currency"`
	QuoteID        string                  `json:"quote_id"`
	KeyID          int64                   `json:"-"`
	GroupID        int64                   `json:"-"`
}

func studioModel(id string) VideoStudioModel {
	m := VideoStudioModel{ID: id, DurationMode: VideoStudioDurationSelectable, MaxReferences: VideoStudioMaxReferences, ReferenceKinds: []string{"image", "video", "audio"}, MaxSeconds: 15, DefaultSeconds: 5}
	switch id {
	case MuseVideoModel:
		m.Provider = "muse"
		m.ReferenceKinds = append(m.ReferenceKinds, "file")
		m.MuseOptions = &VideoStudioMuseOptions{ResolutionMaxBytes: 64, ExtraMaxBytes: 4096, TimeoutMax: 600, NativeSizeMaxBytes: 64}
		m.Label = "Muse"
		m.DurationMode = VideoStudioDurationAutomatic
		m.MinSeconds, m.MaxSeconds, m.DefaultSeconds = MuseVideoDefaultSeconds, MuseVideoDefaultSeconds, MuseVideoDefaultSeconds
		m.Sizes = []string{"16:9", "9:16", "1:1"}
	case GoodPutH3Model:
		m.Provider = "goodput"
		m.Label = "H3"
		m.SupportsFrames = true
		m.MinSeconds = 4
		m.Sizes = []string{"1344x768", "768x1344", "768x768", "1024x768", "768x1024", "1536x672"}
	case GoodPutH3FastModel:
		m.Provider = "goodput"
		m.Label = "H3 Fast"
		m.SupportsFrames = true
		m.SupportsSeed = true
		m.MinSeconds = 5
		m.MaxSeconds = 14
		m.Sizes = []string{"1344x768", "768x1344"}
	}
	return m
}

// Listing is advisory. A fresh catalogue is compared on submit, then shared key admission and the media gateway run.
func (s *OpenAIGatewayService) VideoStudioModels(ctx context.Context, keys []*APIKey) ([]VideoStudioModel, error) {
	out := []VideoStudioModel{}
	seen := map[string]bool{}
	accountsByGroup := map[int64][]Account{}
	for _, k := range keys {
		if k == nil || k.Group == nil || k.GroupID == nil || k.Group.ID != *k.GroupID || k.User == nil || k.User.ID != k.UserID || !k.User.IsActive() || !k.IsActive() || k.IsExpired() || k.IsQuotaExhausted() {
			continue
		}
		g := k.Group
		if !g.IsActive() || g.IsSubscriptionType() || !GroupAllowsImageGeneration(g) || !studioWalletAccess(s.cfg, k.User, g) {
			continue
		}
		muse := s.StudioModelGroup(g, MuseVideoModel)
		h3 := s.StudioModelGroup(g, GoodPutH3Model) || s.StudioModelGroup(g, GoodPutH3FastModel)
		if !muse && !h3 {
			continue
		}
		accounts, ok := accountsByGroup[g.ID]
		if !ok {
			var err error
			accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, g.ID, PlatformOpenAI)
			if err != nil {
				return nil, err
			}
			accountsByGroup[g.ID] = accounts
		}
		for _, id := range []string{MuseVideoModel, GoodPutH3Model, GoodPutH3FastModel} {
			if seen[id] || (id == MuseVideoModel) != muse || !slices.Contains(g.ModelAllowlist.Models, id) {
				continue
			}
			cap := OpenAIEndpointCapabilityGoodPutVideo
			if muse {
				cap = OpenAIEndpointCapabilityMuseVideo
			}
			mapped := slices.ContainsFunc(accounts, func(a Account) bool {
				return a.IsSchedulable() && a.SupportsOpenAIEndpointCapability(cap) && a.GetModelMapping()[id] == id
			})
			if !mapped {
				continue
			}
			if (muse && !s.HasMuseVideoPrice(ctx, k)) || (h3 && !s.HasGoodPutVideoPrice(ctx, k, id)) {
				continue
			}
			m := studioModel(id)
			m.KeyID = k.ID
			m.GroupID = g.ID
			m.Quotes = map[int]float64{}
			factor := resolveVideoRateMultiplier(k, s.ResolveUserGroupRateMultiplier(ctx, k.UserID, g.ID, g.RateMultiplier))
			if !finitePositiveVideoCost(factor) {
				continue
			}
			for n := m.MinSeconds; n <= m.MaxSeconds; n++ {
				cost := s.calculateOpenAIVideoCost(ctx, id, k, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: n, VideoResolution: VideoBillingResolution720P}, factor)
				finalizeUsageCurrency(cost, k, false)
				amount := QuantizeUsageBillingAmount(cost.ActualCost)
				unit := costCurrency(cost)
				if !finitePositiveVideoCost(amount) || (m.Currency != "" && m.Currency != unit) {
					break
				}
				m.Currency = unit
				m.Quotes[n] = amount
			}
			if len(m.Quotes) != m.MaxSeconds-m.MinSeconds+1 {
				continue
			}
			prices, _ := json.Marshal(m.Quotes)
			sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s:%s:%s", k.UserID, k.ID, g.ID, id, m.Currency, prices)))
			m.QuoteID = hex.EncodeToString(sum[:])
			out = append(out, m)
			seen[id] = true
		}
		if len(out) == 3 {
			break
		}
	}
	return out, nil
}

type videoStudioQuoteKey struct{}

type videoStudioQuote struct {
	requestID string
	amount    float64
	currency  string
}

func WithVideoStudioQuote(ctx context.Context, amount float64, currency string) context.Context {
	return context.WithValue(ctx, videoStudioQuoteKey{}, videoStudioQuote{amount: amount, currency: currency})
}
func WithVideoStudioSubmission(ctx context.Context, id string, amount float64, currency string) context.Context {
	return context.WithValue(ctx, videoStudioQuoteKey{}, videoStudioQuote{requestID: id, amount: amount, currency: currency})
}
func acceptsStudioQuote(ctx context.Context, amount float64, currency string) bool {
	expected, studio := ctx.Value(videoStudioQuoteKey{}).(videoStudioQuote)
	return !studio || (finitePositiveVideoCost(expected.amount) && expected.amount == amount && expected.currency == currency)
}
func (s *OpenAIGatewayService) StudioMuseQuoteMatches(ctx context.Context, k *APIKey, seconds int) bool {
	if ctx.Value(videoStudioQuoteKey{}) == nil {
		return true
	}
	k = s.apiKeyWithFreshGroupMediaPricing(ctx, k)
	if k == nil || k.Group == nil || k.GroupID == nil {
		return false
	}
	factor := resolveVideoRateMultiplier(k, s.ResolveUserGroupRateMultiplier(ctx, k.UserID, *k.GroupID, k.Group.RateMultiplier))
	cost := s.calculateOpenAIVideoCost(ctx, MuseVideoModel, k, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: seconds, VideoResolution: VideoBillingResolution720P}, factor)
	finalizeUsageCurrency(cost, k, false)
	return acceptsStudioQuote(ctx, QuantizeUsageBillingAmount(cost.ActualCost), costCurrency(cost))
}
