package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

const goodPutVideoReceiptWindow = 24 * time.Hour

const (
	GoodPutReviewUsageUnverified = "completed_usage_unverified"
	GoodPutReviewReceiptExpired  = "receipt_expired"
)

var (
	ErrGoodPutVideoFunds = errors.New("Insufficient balance or API key allowance for this video")
	ErrGoodPutVideoLimit = errors.New("A previous video submission is still running or unconfirmed; check the original task instead of resubmitting")
	ErrGoodPutVideoGroup = errors.New("GoodPut H3 submissions are not enabled for this group")
)

type DurableVideoJob struct {
	ID, PublicID, NativeID, Model, Currency string
	Resolution, State                       string
	UserID, APIKeyID, GroupID, AccountID    int64
	Seconds, BilledSeconds                  int
	HoldAmount, ReferenceAmount, Multiplier float64
	AccountRate, AccountQuotaCost           float64
	AccountQuotaDaily, AccountQuotaWeekly   bool
	AccountRequestCount                     int
	ContentFailed                           bool
	CreatedAt                               time.Time
}

type DurableVideoJobRepository interface {
	ReserveVideoJob(context.Context, DurableVideoJob, bool) error
	AttachVideoReceipt(context.Context, string, string, string) error
	FindVideoJob(context.Context, string, int64, int64, int64) (*DurableVideoJob, error)
	ClaimVideoJob(context.Context) (*DurableVideoJob, error)
	FinishVideoJob(context.Context, string, string) (bool, error)
}

type GoodPutVideoJobRepository interface {
	DurableVideoJobRepository
	CompleteGoodPutVideoJob(ctx context.Context, id string, billedSeconds int) (bool, error)
	ReviewGoodPutVideoJob(ctx context.Context, id, reason string) (bool, error)
	RecordGoodPutContentOutcome(ctx context.Context, id string, failed bool) error
}

func (s *OpenAIGatewayService) goodPutVideoJobs() GoodPutVideoJobRepository {
	if lanes, ok := s.usageBillingRepo.(*UsageBillingLaneCombiner); ok {
		return lanes.goodPutVideoJobs()
	}
	repo, _ := s.usageBillingRepo.(GoodPutVideoJobRepository)
	return repo
}

func (s *OpenAIGatewayService) goodPutPublicGroup(group *Group) bool {
	return s != nil && s.cfg != nil && group != nil && group.ID > 0 && s.cfg.Gateway.GoodPutVideoPublicGroupID == group.ID
}

func (s *OpenAIGatewayService) CanSubmitGoodPutVideo(group *Group) bool {
	return IsGoodPutVideoGroup(group) && (group.IsExclusive || s.goodPutPublicGroup(group))
}

func (s *OpenAIGatewayService) FindGoodPutVideoJob(ctx context.Context, key *APIKey, id string) (*DurableVideoJob, error) {
	repo := s.goodPutVideoJobs()
	if repo == nil || key.GroupID == nil {
		return nil, nil
	}
	return repo.FindVideoJob(ctx, id, key.UserID, key.ID, *key.GroupID)
}

func (s *OpenAIGatewayService) RecordGoodPutContentOutcome(ctx context.Context, job *DurableVideoJob, failed bool) {
	if job == nil || (!job.ContentFailed && !failed) || ctx.Err() != nil || !runtimegate.SharedWorkAllowed() {
		return
	}
	if repo := s.goodPutVideoJobs(); repo != nil {
		record, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		if err := repo.RecordGoodPutContentOutcome(record, job.ID, failed); err != nil {
			slog.Warn("goodput video content outcome could not be recorded", "task_id", job.PublicID)
		}
	}
}

func (s *OpenAIGatewayService) SubmitGoodPutVideoJob(ctx context.Context, key *APIKey, account *Account, req *GoodPutVideoRequest) (GoodPutVideoTask, error) {
	repo := s.goodPutVideoJobs()
	if repo == nil || key.GroupID == nil || !s.CanSubmitGoodPutVideo(key.Group) || !runtimegate.SharedWorkAllowed() {
		return GoodPutVideoTask{}, &GoodPutVideoError{Status: 503, Code: "goodput_video_unavailable", Message: "GoodPut durable billing is unavailable"}
	}
	body, err := s.prepareGoodPutVideoSubmission(ctx, account, req)
	if err != nil {
		return GoodPutVideoTask{}, err
	}
	key = s.apiKeyWithFreshGroupMediaPricing(ctx, key)
	info := req.Info()
	if !s.CanSubmitGoodPutVideo(key.Group) || !s.hasGoodPutVideoPrice(ctx, key, info.Model) {
		return GoodPutVideoTask{}, &GoodPutVideoError{Status: 503, Code: "goodput_video_price_unavailable", Message: "GoodPut H3 video pricing is not configured for this group"}
	}
	job, err := s.quoteDurableVideoJob(ctx, key, account, info)
	if err != nil {
		return GoodPutVideoTask{}, &GoodPutVideoError{Status: 503, Code: "goodput_video_price_unavailable", Message: "GoodPut H3 requires a positive wallet quote"}
	}
	if err := repo.ReserveVideoJob(ctx, job, s.goodPutPublicGroup(key.Group)); err != nil {
		if errors.Is(err, ErrGoodPutVideoGroup) {
			return GoodPutVideoTask{}, &GoodPutVideoError{Status: 403, Code: "goodput_video_group_not_enabled", Message: err.Error()}
		}
		if errors.Is(err, ErrGoodPutVideoFunds) || errors.Is(err, ErrGoodPutVideoLimit) {
			return GoodPutVideoTask{}, &GoodPutVideoError{Status: 429, Code: "goodput_video_limit", Message: err.Error()}
		}
		return GoodPutVideoTask{}, err
	}
	// Keep the account slot and submission cancellation bound to the original
	// request. Receipt writes use a separate bounded context after dispatch.
	cacheCtx, stopCache := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	s.invalidateGoodPutWallet(cacheCtx, job.UserID)
	stopCache()
	var task GoodPutVideoTask
	if ctx.Err() != nil || !runtimegate.SharedWorkAllowed() {
		err = errGoodPutVideoNotDispatched
	} else {
		dispatch, cancel := context.WithTimeout(ctx, 45*time.Second)
		task, err = s.sendGoodPutVideo(dispatch, account, http.MethodPost, goodPutVideoOrigin+"/v1/videos", "", body)
		cancel()
	}
	save, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if err != nil {
		var rejected *GoodPutVideoError
		if errors.Is(err, errGoodPutVideoNotDispatched) || (errors.As(err, &rejected) && rejected.submissionRejected) {
			if _, finishErr := repo.FinishVideoJob(save, job.ID, "failed"); finishErr != nil {
				return GoodPutVideoTask{}, finishErr
			}
			s.invalidateGoodPutWallet(save, job.UserID)
		} else {
			slog.Warn("goodput video submission uncertain", "reservation_id", job.ID)
			return GoodPutVideoTask{}, &GoodPutVideoError{Status: http.StatusBadGateway, Code: "goodput_video_outcome_unconfirmed", Message: "GoodPut submission could not be confirmed; do not resubmit"}
		}
		return GoodPutVideoTask{}, err
	}
	// Attach is idempotent. Retrying receipt persistence is safe; POST is not.
	if err := repo.AttachVideoReceipt(save, job.ID, task.ID(), task.NativeID()); err != nil {
		if err := repo.AttachVideoReceipt(save, job.ID, task.ID(), task.NativeID()); err == nil {
			return task, nil
		}
		slog.Error("goodput video receipt persistence failed", "reservation_id", job.ID, "task_id", task.ID())
		return GoodPutVideoTask{}, &GoodPutVideoError{Status: 502, Code: "goodput_video_receipt_unavailable", Message: "GoodPut accepted the task but its receipt could not be saved; do not resubmit"}
	}
	return task, nil
}

func (s *OpenAIGatewayService) quoteDurableVideoJob(ctx context.Context, key *APIKey, account *Account, info GrokMediaRequestInfo) (DurableVideoJob, error) {
	factor := resolveVideoRateMultiplier(key, s.ResolveUserGroupRateMultiplier(ctx, key.UserID, *key.GroupID, key.Group.RateMultiplier))
	cost := s.calculateOpenAIVideoCost(ctx, info.Model, key, &OpenAIForwardResult{
		VideoCount: 1, VideoDurationSeconds: info.DurationSeconds, VideoResolution: info.Resolution,
	}, factor)
	finalizeUsageCurrency(cost, key, false)
	amount := QuantizeUsageBillingAmount(cost.ActualCost)
	reference := QuantizeUsageBillingAmount(cost.TotalCost)
	if !finitePositiveVideoCost(amount) || !finitePositiveVideoCost(reference) || !finitePositiveVideoCost(factor) {
		return DurableVideoJob{}, errors.New("video requires a positive wallet quote")
	}
	job := DurableVideoJob{ID: uuid.NewString(), UserID: key.UserID, APIKeyID: key.ID, GroupID: *key.GroupID,
		AccountID: account.ID, Model: info.Model, Seconds: info.DurationSeconds, Resolution: info.Resolution, HoldAmount: amount,
		ReferenceAmount: reference, Multiplier: factor, Currency: costCurrency(cost), AccountRate: account.BillingRateMultiplier()}
	if account.SupportsAccountQuota() && account.HasAnyQuotaLimit() {
		job.AccountQuotaCost = QuantizeUsageBillingAmount(accountQuotaBasis(cost) * job.AccountRate)
		job.AccountQuotaDaily = account.GetQuotaDailyLimit() > 0
		job.AccountQuotaWeekly = account.GetQuotaWeeklyLimit() > 0
	}
	if account.SupportsAccountQuota() && account.GetQuotaDailyRequestLimit() > 0 {
		job.AccountRequestCount = 1
	}
	return job, nil
}

func finitePositiveVideoCost(v float64) bool { return v > 0 && !math.IsInf(v, 0) }

func (s *OpenAIGatewayService) invalidateGoodPutWallet(ctx context.Context, userID int64) {
	if s.billingCacheService != nil {
		if err := s.billingCacheService.InvalidateUserBalance(ctx, userID); err != nil {
			slog.Warn("goodput video wallet cache invalidation failed", "user_id", userID)
		}
	}
}

func (s *OpenAIGatewayService) reconcileGoodPutVideo(ctx context.Context) {
	repo := s.goodPutVideoJobs()
	if repo == nil || !runtimegate.SharedWorkAllowed() {
		return
	}
	job, err := repo.ClaimVideoJob(ctx)
	if err != nil {
		slog.Warn("goodput video reconciliation scan failed")
		return
	}
	if job == nil {
		return
	}
	if job.NativeID == "" {
		// The repository moved a stale submission to review. Never replay it.
		slog.Warn("goodput video receipt missing; review required", "reservation_id", job.ID)
		return
	}
	if time.Since(job.CreatedAt) > goodPutVideoReceiptWindow {
		applied, err := repo.ReviewGoodPutVideoJob(ctx, job.ID, GoodPutReviewReceiptExpired)
		s.afterDurableVideoFinish(ctx, *job, "review", applied, err)
		return
	}
	account, err := s.accountRepo.GetByID(ctx, job.AccountID)
	if err != nil || account == nil || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGoodPutVideo) {
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
	task, err := s.GetGoodPutVideoTask(ctx, account, job.NativeID)
	if err != nil || !runtimegate.SharedWorkAllowed() {
		return
	}
	switch {
	case task.Completed():
		if seconds := task.BillableSeconds(job.Model, job.Seconds); seconds > 0 {
			applied, err := repo.CompleteGoodPutVideoJob(ctx, job.ID, seconds)
			s.afterDurableVideoFinish(ctx, *job, "completed", applied, err)
		} else {
			applied, err := repo.ReviewGoodPutVideoJob(ctx, job.ID, GoodPutReviewUsageUnverified)
			s.afterDurableVideoFinish(ctx, *job, "review", applied, err)
		}
	case task.state == "failed":
		s.finishDurableVideo(ctx, repo, *job, "failed")
	}
}

func (s *OpenAIGatewayService) finishDurableVideo(ctx context.Context, repo DurableVideoJobRepository, job DurableVideoJob, state string) {
	applied, err := repo.FinishVideoJob(ctx, job.ID, state)
	s.afterDurableVideoFinish(ctx, job, state, applied, err)
}

func (s *OpenAIGatewayService) afterDurableVideoFinish(ctx context.Context, job DurableVideoJob, state string, applied bool, err error) {
	if err != nil || (applied && state == "review") {
		slog.Warn("video settlement requires attention", "task_id", job.PublicID, "state", state)
	}
	if applied && s.billingCacheService != nil {
		s.invalidateGoodPutWallet(ctx, job.UserID)
		if err := s.billingCacheService.InvalidateAPIKeyRateLimit(ctx, job.APIKeyID); err != nil {
			slog.Warn("goodput video key rate cache invalidation failed", "key_id", job.APIKeyID)
		}
	}
}

func (s *OpenAIGatewayService) startDurableVideoReconciliation() {
	if s.concurrencyService == nil {
		return
	}
	var stops []func()
	if s.goodPutVideoJobs() != nil {
		stops = append(stops, startVideoReconciliation(s.reconcileGoodPutVideo))
	}
	if s.hiAPIVideoJobs() != nil {
		stops = append(stops, startVideoReconciliation(s.reconcileHiAPIVideo))
	}
	s.stopDurableVideo = func() {
		for _, stop := range stops {
			stop()
		}
	}
}

func startVideoReconciliation(reconcile func(context.Context)) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pass, stop := context.WithTimeout(ctx, 12*time.Second)
				reconcile(pass)
				stop()
			}
		}
	}()
	return func() { cancel(); <-done }
}
