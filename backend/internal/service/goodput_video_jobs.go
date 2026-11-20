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

var (
	ErrGoodPutVideoFunds = errors.New("Insufficient balance or API key allowance for this video")
	ErrGoodPutVideoLimit = errors.New("This API key already has an unsettled GoodPut H3 task")
	ErrGoodPutVideoGroup = errors.New("GoodPut H3 submissions are not enabled for this group")
)

type GoodPutVideoJob struct {
	ID, PublicID, NativeID, Model, Currency string
	UserID, APIKeyID, GroupID, AccountID    int64
	Seconds                                 int
	HoldAmount, ReferenceAmount, Multiplier float64
	AccountRate, AccountQuotaCost           float64
	AccountQuotaDaily, AccountQuotaWeekly   bool
	AccountRequestCount                     int
	CreatedAt                               time.Time
}

type GoodPutVideoJobRepository interface {
	ReserveGoodPutVideoJob(context.Context, GoodPutVideoJob, bool) error
	AttachGoodPutVideoReceipt(context.Context, string, string, string) error
	FindGoodPutVideoJob(context.Context, string, int64, int64, int64) (*GoodPutVideoJob, error)
	ClaimGoodPutVideoJob(context.Context) (*GoodPutVideoJob, error)
	FinishGoodPutVideoJob(context.Context, string, string) (bool, error)
}

func (s *OpenAIGatewayService) goodPutVideoJobs() GoodPutVideoJobRepository {
	repo, _ := s.usageBillingRepo.(GoodPutVideoJobRepository)
	return repo
}

func (s *OpenAIGatewayService) goodPutPublicGroup(group *Group) bool {
	return s != nil && s.cfg != nil && group != nil && group.ID > 0 && s.cfg.Gateway.GoodPutVideoPublicGroupID == group.ID
}

func (s *OpenAIGatewayService) CanSubmitGoodPutVideo(group *Group) bool {
	return IsGoodPutVideoGroup(group) && (group.IsExclusive || s.goodPutPublicGroup(group))
}

func (s *OpenAIGatewayService) FindGoodPutVideoJob(ctx context.Context, key *APIKey, id string) (*GoodPutVideoJob, error) {
	repo := s.goodPutVideoJobs()
	if repo == nil || key.GroupID == nil {
		return nil, nil
	}
	return repo.FindGoodPutVideoJob(ctx, id, key.UserID, key.ID, *key.GroupID)
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
	factor := resolveVideoRateMultiplier(key, s.ResolveUserGroupRateMultiplier(ctx, key.UserID, *key.GroupID, key.Group.RateMultiplier))
	cost := s.calculateOpenAIVideoCost(ctx, info.Model, key, &OpenAIForwardResult{
		VideoCount: 1, VideoDurationSeconds: info.DurationSeconds, VideoResolution: info.Resolution,
	}, factor)
	finalizeUsageCurrency(cost, key, false)
	amount := QuantizeUsageBillingAmount(cost.ActualCost)
	reference := QuantizeUsageBillingAmount(cost.TotalCost)
	if !finitePositiveGoodPutCost(amount) || !finitePositiveGoodPutCost(reference) || !finitePositiveGoodPutCost(factor) {
		return GoodPutVideoTask{}, &GoodPutVideoError{Status: 503, Code: "goodput_video_price_unavailable", Message: "GoodPut H3 requires a positive wallet quote"}
	}
	job := GoodPutVideoJob{ID: uuid.NewString(), UserID: key.UserID, APIKeyID: key.ID, GroupID: *key.GroupID,
		AccountID: account.ID, Model: info.Model, Seconds: info.DurationSeconds, HoldAmount: amount,
		ReferenceAmount: reference, Multiplier: factor, Currency: costCurrency(cost), AccountRate: account.BillingRateMultiplier()}
	if account.SupportsAccountQuota() && account.HasAnyQuotaLimit() {
		job.AccountQuotaCost = QuantizeUsageBillingAmount(accountQuotaBasis(cost) * job.AccountRate)
		job.AccountQuotaDaily = account.GetQuotaDailyLimit() > 0
		job.AccountQuotaWeekly = account.GetQuotaWeeklyLimit() > 0
	}
	if account.SupportsAccountQuota() && account.GetQuotaDailyRequestLimit() > 0 {
		job.AccountRequestCount = 1
	}
	if err := repo.ReserveGoodPutVideoJob(ctx, job, s.goodPutPublicGroup(key.Group)); err != nil {
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
			if _, finishErr := repo.FinishGoodPutVideoJob(save, job.ID, "failed"); finishErr != nil {
				return GoodPutVideoTask{}, finishErr
			}
			s.invalidateGoodPutWallet(save, job.UserID)
		} else {
			slog.Warn("goodput video submission uncertain", "reservation_id", job.ID)
		}
		return GoodPutVideoTask{}, err
	}
	// Attach is idempotent. Retrying receipt persistence is safe; POST is not.
	if err := repo.AttachGoodPutVideoReceipt(save, job.ID, task.ID(), task.NativeID()); err != nil {
		if err := repo.AttachGoodPutVideoReceipt(save, job.ID, task.ID(), task.NativeID()); err == nil {
			return task, nil
		}
		slog.Error("goodput video receipt persistence failed", "reservation_id", job.ID, "task_id", task.ID())
		return GoodPutVideoTask{}, &GoodPutVideoError{Status: 502, Code: "goodput_video_receipt_unavailable", Message: "GoodPut accepted the task but its receipt could not be saved; do not resubmit"}
	}
	return task, nil
}

func finitePositiveGoodPutCost(v float64) bool { return v > 0 && !math.IsInf(v, 0) }

func (s *OpenAIGatewayService) invalidateGoodPutWallet(ctx context.Context, userID int64) {
	if s.billingCacheService != nil {
		if err := s.billingCacheService.InvalidateUserBalance(ctx, userID); err != nil {
			slog.Warn("goodput video wallet cache invalidation failed", "user_id", userID)
		}
	}
}

func (s *OpenAIGatewayService) reconcileGoodPutVideo(ctx context.Context) {
	repo := s.goodPutVideoJobs()
	if !runtimegate.SharedWorkAllowed() {
		return
	}
	job, err := repo.ClaimGoodPutVideoJob(ctx)
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
		s.finishGoodPutVideo(ctx, *job, "review")
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
	case task.Completed() && task.Seconds() == job.Seconds:
		s.finishGoodPutVideo(ctx, *job, "completed")
	case task.Completed():
		s.finishGoodPutVideo(ctx, *job, "review")
	case task.state == "failed":
		s.finishGoodPutVideo(ctx, *job, "failed")
	}
}

func (s *OpenAIGatewayService) finishGoodPutVideo(ctx context.Context, job GoodPutVideoJob, state string) {
	applied, err := s.goodPutVideoJobs().FinishGoodPutVideoJob(ctx, job.ID, state)
	if err != nil || (applied && state == "review") {
		slog.Warn("goodput video settlement requires attention", "task_id", job.PublicID, "state", state)
	}
	if applied && s.billingCacheService != nil {
		s.invalidateGoodPutWallet(ctx, job.UserID)
		if err := s.billingCacheService.InvalidateAPIKeyRateLimit(ctx, job.APIKeyID); err != nil {
			slog.Warn("goodput video key rate cache invalidation failed", "key_id", job.APIKeyID)
		}
	}
}

func (s *OpenAIGatewayService) startGoodPutVideoReconciliation() {
	if s.goodPutVideoJobs() == nil || s.concurrencyService == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.stopGoodPutVideo = func() { cancel(); <-done }
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
				s.reconcileGoodPutVideo(pass)
				stop()
			}
		}
	}()
}
