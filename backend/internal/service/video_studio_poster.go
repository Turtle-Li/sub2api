package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

// This is a short-lived media cache, independent of the durable receipt and billing stores.
type VideoStudioPosterCache interface {
	ClaimStudioPoster(context.Context, string, string, time.Duration) (bool, error)
	SaveStudioPoster(context.Context, string, string, []byte, time.Duration) error
	StudioPosterSizes(context.Context, []string) ([]int64, error)
	StudioPosterBytes(context.Context, string) ([]byte, error)
}

const (
	studioPosterLifetime = 24 * time.Hour
	StudioPosterMaxBytes = 128 << 10
)

var studioPosterSlots = make(chan struct{}, 2)

func (j VideoStudioJob) posterTTL(now time.Time) time.Duration {
	if j.Status != "completed" || j.Receipt == "" || j.CreatedAt.IsZero() || j.UserID <= 0 || j.GroupID <= 0 || j.KeyID < 0 {
		return 0
	}
	ttl := j.CreatedAt.Add(studioPosterLifetime).Sub(now)
	if ttl <= 0 {
		return 0
	}
	return min(studioPosterLifetime, ttl)
}

func (j VideoStudioJob) posterKey() string {
	return fmt.Sprintf("%d:%d:%d:%s:%s", j.UserID, j.GroupID, j.KeyID, j.ID, j.Receipt)
}

type studioPosterSourceKey struct{}

// Populated only inside a session operation by the adapter that verified the completed receipt.
// Never persist a URL, copy a key into the decoder, or perform another status observation.
type StudioPosterSource struct {
	receipt string
	open    func(context.Context) (*http.Response, error)
}

func WatchStudioPosterSource(ctx context.Context) (context.Context, *StudioPosterSource) {
	p := &StudioPosterSource{}
	return context.WithValue(ctx, studioPosterSourceKey{}, p), p
}

func (s *OpenAIGatewayService) bindMuseStudioPoster(ctx context.Context, id string, a *Account, base, proxy, filename string) {
	p, _ := ctx.Value(studioPosterSourceKey{}).(*StudioPosterSource)
	if p == nil || !museVideoFilename.MatchString(filename) {
		return
	}
	p.receipt = id
	p.open = func(ctx context.Context) (*http.Response, error) {
		return s.doMuseRequest(WithHTTPUpstreamRedirectsDisabled(ctx), a, http.MethodGet, base+"/media/"+filename, proxy, "", "", nil)
	}
}

func (s *OpenAIGatewayService) BindGoodPutStudioPoster(ctx context.Context, a *Account, t GoodPutVideoTask) {
	p, _ := ctx.Value(studioPosterSourceKey{}).(*StudioPosterSource)
	if p == nil || !t.Completed() || !goodPutNativeVideoID.MatchString(t.nativeID) {
		return
	}
	p.receipt = t.ID()
	p.open = func(ctx context.Context) (*http.Response, error) {
		return s.doGoodPutVideoRequest(ctx, a, http.MethodGet, goodPutVideoOrigin+"/v1/videos/"+t.nativeID+"/content", nil)
	}
}

// Optional work follows the durable state/settlement transition, never playback or a history read.
// There is no waiting queue. Failure to get a frame must not change the receipt or hide the video.
func (s *OpenAIGatewayService) CaptureVideoStudioPoster(ctx context.Context, j VideoStudioJob, src *StudioPosterSource, writer VideoStudioDurationWriter) int64 {
	if !runtimegate.SharedWorkAllowed() || src == nil || src.open == nil || src.receipt != j.Receipt || j.posterTTL(time.Now()) <= 0 {
		return 0
	}
	cache, ok := s.cache.(VideoStudioPosterCache)
	if !ok {
		return 0
	}
	select {
	case studioPosterSlots <- struct{}{}:
		defer func() { <-studioPosterSlots }()
	default:
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	key, lease := j.posterKey(), uuid.NewString()
	got, err := cache.ClaimStudioPoster(ctx, key, lease, min(2*time.Minute, j.posterTTL(time.Now())))
	if err != nil {
		slog.Warn("video studio poster cache unavailable", "job_id", j.ID)
		return 0
	}
	if !got {
		return 0
	}
	preview, err := decodeStudioVideoFrame(ctx, src.open)
	if err != nil {
		slog.Info("video studio frame unavailable", "job_id", j.ID, "model", j.Model)
		return 0
	}
	if ttl := j.posterTTL(time.Now()); ttl > 0 {
		if err := cache.SaveStudioPoster(ctx, key, lease, preview.jpeg, ttl); err != nil {
			slog.Warn("video studio poster write unavailable", "job_id", j.ID)
		}
	}
	if preview.durationMS > 0 && writer != nil {
		if err := writer.RecordVideoStudioDuration(ctx, j, preview.durationMS); err != nil {
			slog.Warn("video studio duration write unavailable", "job_id", j.ID)
		}
	}
	return preview.durationMS
}

// Reconciliation runs after the existing settlement. Its optional metadata write is not a ledger operation.
func (s *OpenAIGatewayService) studioMediaWriter() VideoStudioDurationWriter {
	repo := s.usageBillingRepo
	if c, ok := repo.(*UsageBillingLaneCombiner); ok && c != nil {
		repo = c.repo
	}
	w, _ := repo.(VideoStudioDurationWriter)
	return w
}

// The collection reads byte lengths in one cache round trip, never the images or upstream tasks.
func (s *OpenAIGatewayService) MarkVideoStudioPosters(ctx context.Context, jobs []VideoStudioJob) {
	cache, ok := s.cache.(VideoStudioPosterCache)
	if !ok {
		return
	}
	now := time.Now()
	var keys []string
	var ix []int
	for i, j := range jobs {
		jobs[i].HasPoster = false
		jobs[i].PosterExpiresAt = nil
		if j.posterTTL(now) > 0 {
			keys = append(keys, j.posterKey())
			ix = append(ix, i)
		}
	}
	if len(keys) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	sizes, err := cache.StudioPosterSizes(ctx, keys)
	if err != nil || len(sizes) != len(ix) {
		return
	}
	for i, n := range sizes {
		if n > 64 && n <= StudioPosterMaxBytes {
			j := &jobs[ix[i]]
			expiry := j.CreatedAt.Add(studioPosterLifetime)
			j.HasPoster = true
			j.PosterExpiresAt = &expiry
		}
	}
}

// A cached frame must not outlive the authorization required to open its original video.
func (s *OpenAIGatewayService) ReadVideoStudioPoster(ctx context.Context, j VideoStudioJob, k *APIKey) ([]byte, error) {
	if k == nil || k.GroupID == nil || j.UserID != k.UserID || j.KeyID != k.ID || j.GroupID != *k.GroupID || j.posterTTL(time.Now()) <= 0 {
		return nil, ErrVideoStudioNotFound
	}
	cache, ok := s.cache.(VideoStudioPosterCache)
	if !ok {
		return nil, ErrVideoStudioNotFound
	}
	var accountID int64
	var err error
	cap := OpenAIEndpointCapabilityMuseVideo
	if IsGoodPutVideoModel(j.Model) {
		cap = OpenAIEndpointCapabilityGoodPutVideo
		if !IsGoodPutVideoTaskID(j.Receipt) {
			return nil, ErrVideoStudioNotFound
		}
		job, findErr := s.FindGoodPutVideoJob(ctx, k, j.Receipt)
		if findErr != nil {
			return nil, findErr
		}
		if job != nil {
			if job.AccountID <= 0 || job.PublicID != j.Receipt || job.UserID != j.UserID || job.APIKeyID != j.KeyID || job.GroupID != j.GroupID || job.Model != j.Model || job.State == "failed" {
				return nil, ErrVideoStudioNotFound
			}
			accountID = job.AccountID
		} else {
			pending, loadErr := s.LoadGrokVideoPendingBilling(ctx, j.Receipt, k.UserID, k.ID)
			if loadErr != nil || pending == nil || pending.Model != j.Model {
				return nil, ErrVideoStudioNotFound
			}
		}
	} else if j.Model != MuseVideoModel || !IsMuseVideoTaskID(j.Receipt) {
		return nil, ErrVideoStudioNotFound
	} else if j.KeyID == VideoStudioWalletSlot {
		job, err := s.FindMuseStudioJob(ctx, k, j.Receipt)
		if err != nil {
			return nil, err
		}
		if job == nil || job.State == "failed" || job.AccountID <= 0 {
			return nil, ErrVideoStudioNotFound
		}
		accountID = job.AccountID
	}
	if accountID == 0 {
		accountID, err = s.ResolveGrokMediaVideoRequestAccount(ctx, k.GroupID, j.Receipt, j.UserID, j.KeyID)
		if err != nil || accountID <= 0 {
			return nil, ErrVideoStudioNotFound
		}
	}
	hash := StudioBoundVideoHash(j.Receipt, k)
	selection, _, err := s.SelectMediaVideoRequestAccount(ctx, k.GroupID, hash, accountID, j.Model, PlatformOpenAI, cap)
	if selection != nil && selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	if err != nil || selection == nil || selection.Account == nil || selection.Account.ID != accountID || selection.Account.GetModelMapping()[j.Model] != j.Model {
		return nil, ErrVideoStudioNotFound
	}
	body, err := cache.StudioPosterBytes(ctx, j.posterKey())
	if err != nil {
		return nil, fmt.Errorf("read cached studio poster: %w", err)
	}
	if !validStudioJPEG(body) {
		return nil, ErrVideoStudioNotFound
	}
	return body, nil
}

var errStudioFrameUnavailable = errors.New("no usable video frame")
