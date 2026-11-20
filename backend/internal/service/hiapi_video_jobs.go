package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/gin-gonic/gin"
)

type hiAPIVideoLedgerProvider interface {
	HiAPIVideoJobs() DurableVideoJobRepository
}

func (s *OpenAIGatewayService) hiAPIVideoJobs() DurableVideoJobRepository {
	provider, ok := s.usageBillingRepo.(hiAPIVideoLedgerProvider)
	if !ok {
		return nil
	}
	return provider.HiAPIVideoJobs()
}

func (s *OpenAIGatewayService) FindHiAPIVideoJob(ctx context.Context, key *APIKey, id string) (*DurableVideoJob, error) {
	repo := s.hiAPIVideoJobs()
	if repo == nil || key.GroupID == nil || !IsHiAPIVideoTaskID(id) {
		return nil, nil
	}
	return repo.FindVideoJob(ctx, id, key.UserID, key.ID, *key.GroupID)
}

func HiAPIVideoPublic(job *DurableVideoJob) gin.H {
	status := job.State
	switch status {
	case "pending", "submitting":
		status = "in_progress"
	}
	out := gin.H{"id": job.PublicID, "object": "video", "model": job.Model, "status": status,
		"created_at": job.CreatedAt.Unix(), "seconds": job.Seconds, "resolution": job.Resolution}
	switch job.State {
	case "completed":
		out["usage"] = gin.H{"duration_seconds": job.Seconds}
		out["video_url"] = HiAPIVideoTaskPath + job.PublicID + "/content"
	case "failed":
		out["error"] = gin.H{"code": "generation_failed", "message": "HiAPI did not generate this video"}
	case "review":
		out["error"] = gin.H{"code": "outcome_unconfirmed", "message": "Video outcome is being reviewed; do not resubmit"}
	}
	return out
}

func (s *OpenAIGatewayService) SubmitHiAPIVideoJob(ctx context.Context, key *APIKey, account *Account, request *HiAPIVideoRequest) (*DurableVideoJob, error) {
	repo := s.hiAPIVideoJobs()
	key = s.apiKeyWithFreshGroupMediaPricing(ctx, key)
	if repo == nil || key.GroupID == nil || !runtimegate.SharedWorkAllowed() ||
		!IsHiAPIVideoGroup(key.Group) || !key.Group.IsExclusive || !key.Group.AllowImageGeneration ||
		!account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityHiAPIVideo) {
		return nil, &HiAPIVideoError{Status: 503, Code: "hiapi_video_unavailable", Message: "HiAPI video is not enabled for this wallet group"}
	}
	info := request.Info()
	// Require an explicit positive per-second price for the requested tier.
	price := key.Group.GetVideoPriceForModel(info.Model, info.Resolution)
	if price == nil || !finitePositiveVideoCost(*price) {
		return nil, &HiAPIVideoError{Status: 503, Code: "hiapi_video_price_unavailable", Message: "HiAPI video pricing is not configured for this resolution"}
	}
	job, err := s.quoteDurableVideoJob(ctx, key, account, info)
	if err != nil {
		return nil, &HiAPIVideoError{Status: 503, Code: "hiapi_video_price_unavailable", Message: "HiAPI video requires a positive wallet quote"}
	}
	body, err := request.marshal()
	if err != nil {
		return nil, err
	}
	if err := repo.ReserveVideoJob(ctx, job, false); err != nil {
		switch {
		case errors.Is(err, ErrGoodPutVideoFunds):
			return nil, &HiAPIVideoError{Status: 429, Code: "hiapi_video_limit", Message: err.Error()}
		case errors.Is(err, ErrGoodPutVideoLimit):
			return nil, &HiAPIVideoError{Status: 429, Code: "hiapi_video_limit", Message: "This API key already has an unsettled video task"}
		case errors.Is(err, ErrGoodPutVideoGroup):
			return nil, &HiAPIVideoError{Status: 403, Code: "hiapi_video_group_not_enabled", Message: "HiAPI video requires an exclusive wallet group"}
		default:
			return nil, err
		}
	}
	cacheCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	s.invalidateGoodPutWallet(cacheCtx, job.UserID)
	stop()
	var receipt hiAPIVideoReceipt
	if ctx.Err() != nil || !runtimegate.SharedWorkAllowed() {
		err = errHiAPIVideoNotDispatched
	} else {
		submit, cancel := context.WithTimeout(ctx, 45*time.Second)
		receipt, err = s.sendHiAPIVideo(submit, account, "", job.ID, body)
		cancel()
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		var rejected *HiAPIVideoError
		if errors.Is(err, errHiAPIVideoNotDispatched) || (errors.As(err, &rejected) && rejected.rejected) {
			if _, finishErr := repo.FinishVideoJob(save, job.ID, "failed"); finishErr != nil {
				return nil, finishErr
			}
			s.invalidateGoodPutWallet(save, job.UserID)
		} else {
			slog.Warn("hiapi video submission uncertain", "reservation_id", job.ID)
		}
		return nil, err
	}
	job.PublicID, job.NativeID, job.State, job.CreatedAt = receipt.publicID(), receipt.ID, "pending", time.Now()
	if err := repo.AttachVideoReceipt(save, job.ID, job.PublicID, job.NativeID); err != nil {
		if err := repo.AttachVideoReceipt(save, job.ID, job.PublicID, job.NativeID); err != nil {
			slog.Error("hiapi video receipt persistence failed", "reservation_id", job.ID, "task_id", job.PublicID)
			return nil, &HiAPIVideoError{Status: http.StatusBadGateway, Code: "hiapi_video_receipt_unavailable", Message: "HiAPI accepted the task but its receipt could not be saved; do not resubmit"}
		}
	}
	return &job, nil
}

func (s *OpenAIGatewayService) reconcileHiAPIVideo(ctx context.Context) {
	if !runtimegate.SharedWorkAllowed() {
		return
	}
	repo := s.hiAPIVideoJobs()
	job, err := repo.ClaimVideoJob(ctx)
	if err != nil {
		slog.Warn("hiapi video reconciliation scan failed")
		return
	}
	if job == nil {
		return
	}
	if job.NativeID == "" {
		slog.Warn("hiapi video receipt missing; review required", "reservation_id", job.ID)
		return
	}
	if time.Since(job.CreatedAt) > goodPutVideoReceiptWindow {
		s.finishDurableVideo(ctx, repo, *job, "review")
		return
	}
	account, err := s.accountRepo.GetByID(ctx, job.AccountID)
	if err != nil || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityHiAPIVideo) {
		return
	}
	slot, err := s.concurrencyService.AcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil || !slot.Acquired {
		return
	}
	defer slot.ReleaseFunc()
	if !runtimegate.SharedWorkAllowed() {
		return
	}
	receipt, err := s.sendHiAPIVideo(ctx, account, job.NativeID, "", nil)
	if err != nil || !runtimegate.SharedWorkAllowed() {
		return
	}
	switch receipt.Status {
	case "success":
		if receipt.videoURL() == "" {
			s.finishDurableVideo(ctx, repo, *job, "review")
			return
		}
		s.finishDurableVideo(ctx, repo, *job, "completed")
	case "fail":
		s.finishDurableVideo(ctx, repo, *job, "failed")
	}
}
