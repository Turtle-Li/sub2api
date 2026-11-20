package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/gin-gonic/gin"
)

type museStudioLedgerProvider interface {
	MuseStudioVideoJobs() DurableVideoJobRepository
}

func (c *UsageBillingLaneCombiner) MuseStudioVideoJobs() DurableVideoJobRepository {
	if c != nil {
		if p, ok := c.repo.(museStudioLedgerProvider); ok {
			return p.MuseStudioVideoJobs()
		}
	}
	return nil
}
func (s *OpenAIGatewayService) museStudioLedger() DurableVideoJobRepository {
	if p, ok := s.usageBillingRepo.(museStudioLedgerProvider); ok {
		return p.MuseStudioVideoJobs()
	}
	return nil
}

func (s *OpenAIGatewayService) FindMuseStudioJob(ctx context.Context, k *APIKey, id string) (*DurableVideoJob, error) {
	r := s.museStudioLedger()
	if r == nil {
		return nil, errors.New("Muse Studio ledger unavailable")
	}
	if k == nil || k.GroupID == nil || k.ID != VideoStudioWalletSlot || !IsMuseVideoTaskID(id) {
		return nil, nil
	}
	return r.FindVideoJob(ctx, id, k.UserID, k.ID, *k.GroupID)
}

var ErrVideoStudioWalletUnavailable = errors.New("video wallet ledger unavailable")
var ErrVideoStudioQuoteChanged = errors.New("video wallet quote changed")

type museRejected struct{ status int }

func (e *museRejected) Error() string {
	return fmt.Sprintf("Muse rejected submission: HTTP %d", e.status)
}

func (s *OpenAIGatewayService) SubmitMuseStudioJob(ctx context.Context, c *gin.Context, k *APIKey, a *Account, info GrokMediaRequestInfo, body []byte) (gin.H, error) {
	r := s.museStudioLedger()
	if r == nil {
		return nil, ErrVideoStudioWalletUnavailable
	}
	if k == nil || k.ID != VideoStudioWalletSlot || k.GroupID == nil || !s.StudioModelGroup(k.Group, MuseVideoModel) || info.Model != MuseVideoModel || !runtimegate.SharedWorkAllowed() {
		return nil, ErrGoodPutVideoGroup
	}
	k = s.apiKeyWithFreshGroupMediaPricing(ctx, k)
	if ctx.Value(videoStudioQuoteKey{}) == nil || !s.HasMuseVideoPrice(ctx, k) {
		return nil, ErrVideoStudioQuoteChanged
	}
	j, err := s.quoteDurableVideoJob(ctx, k, a, info)
	if err != nil || !acceptsStudioQuote(ctx, j.HoldAmount, j.Currency) {
		return nil, ErrVideoStudioQuoteChanged
	}
	route, err := s.museVideoRoute(ctx, a, MuseVideoCreate)
	if err != nil {
		return nil, fmt.Errorf("%w: Muse channel unavailable", ErrGoodPutVideoGroup)
	}
	if err := r.ReserveVideoJob(ctx, j, s.cfg != nil && s.cfg.Gateway.VideoStudioMuseGroupID == j.GroupID); err != nil {
		return nil, err
	}
	s.invalidateGoodPutWallet(ctx, j.UserID)
	result, receipt, err := s.forwardMuseVideo(ctx, c, a, MuseVideoCreate, "", body, route)
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		var rejected *museRejected
		if errors.As(err, &rejected) {
			s.finishDurableVideo(save, r, j, "failed")
		}
		return nil, err
	}
	if err := r.AttachVideoReceipt(save, j.ID, result.ResponseID, strings.TrimPrefix(result.ResponseID, "muse_")); err != nil {
		slog.Error("Muse Studio wallet receipt needs reconciliation", "reservation", j.ID, "receipt", result.ResponseID)
		return nil, err
	}
	return receipt, nil
}

func (s *OpenAIGatewayService) ObserveMuseStudioJob(ctx context.Context, j *DurableVideoJob, status string) {
	if j == nil || j.State != "pending" || (status != "completed" && status != "failed") {
		return
	}
	r := s.museStudioLedger()
	if r == nil {
		return
	}
	s.finishDurableVideo(ctx, r, *j, status)
}

// This worker never generates. A verified receipt may populate the bounded private cover cache.
func (s *OpenAIGatewayService) reconcileMuseStudioWallet(ctx context.Context) {
	r := s.museStudioLedger()
	if r == nil || !runtimegate.SharedWorkAllowed() {
		return
	}
	j, err := r.ClaimVideoJob(ctx)
	if err != nil {
		slog.Warn("Muse Studio wallet scan failed", "error", err)
		return
	}
	if j == nil {
		return
	}
	if j.NativeID == "" {
		slog.Warn("Muse Studio submission missing receipt", "reservation", j.ID)
		return
	}
	if time.Since(j.CreatedAt) > goodPutVideoReceiptWindow {
		s.finishDurableVideo(ctx, r, *j, "review")
		return
	}
	if !IsMuseVideoTaskID(j.PublicID) || j.PublicID != "muse_"+j.NativeID || j.Model != MuseVideoModel || j.APIKeyID != VideoStudioWalletSlot {
		return
	}
	a, err := s.accountRepo.GetByID(ctx, j.AccountID)
	if err != nil || a == nil || !a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseVideo) {
		return
	}
	slot, err := s.concurrencyService.AcquireAccountSlot(ctx, a.ID, a.Concurrency)
	if err != nil || !slot.Acquired {
		return
	}
	defer slot.ReleaseFunc()
	base, proxy, token, err := s.museUpstream(ctx, a)
	if err != nil {
		return
	}
	resp, err := s.doMuseRequest(WithHTTPUpstreamRedirectsDisabled(ctx), a, http.MethodGet, base+"/videos/"+j.NativeID, proxy, token, "", nil)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	const limit = 1 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(b) > limit {
		return
	}
	var task museNativeVideoTask
	if json.Unmarshal(b, &task) != nil || task.ID != j.NativeID || (task.Kind != "" && task.Kind != "video") || (task.Status == "completed" && !museVideoFilename.MatchString(task.Result.Filename)) || !runtimegate.SharedWorkAllowed() {
		return
	}
	s.ObserveMuseStudioJob(ctx, j, task.Status)
	if task.Status == "completed" && j.StudioRequestID != "" {
		posterCtx, source := WatchStudioPosterSource(ctx)
		s.bindMuseStudioPoster(posterCtx, j.PublicID, a, base, proxy, task.Result.Filename)
		s.CaptureVideoStudioPoster(ctx, VideoStudioJob{ID: j.StudioRequestID, UserID: j.UserID, GroupID: j.GroupID, KeyID: j.APIKeyID, Model: j.Model, Receipt: j.PublicID, CreatedAt: j.CreatedAt, Status: "completed"}, source, s.studioMediaWriter())
	}
}
