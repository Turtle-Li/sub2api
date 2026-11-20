package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.GoodPutVideoJobRepository = (*usageBillingRepository)(nil)

func (r *usageBillingRepository) ReserveGoodPutVideoJob(ctx context.Context, job service.GoodPutVideoJob, publicEnabled bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exclusive bool
	if err := tx.QueryRowContext(ctx, `SELECT is_exclusive FROM groups WHERE id=$1 AND deleted_at IS NULL AND status='active'`, job.GroupID).Scan(&exclusive); errors.Is(err, sql.ErrNoRows) {
		return service.ErrGoodPutVideoGroup
	} else if err != nil {
		return err
	}
	if !exclusive && !publicEnabled {
		return service.ErrGoodPutVideoGroup
	}
	// Wallet first, then API key, matching normal usage billing lock order.
	result, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance-$2, frozen_balance=frozen_balance+$2, updated_at=NOW()
		WHERE id=$1 AND status='active' AND deleted_at IS NULL AND balance >= $2`, job.UserID, job.HoldAmount)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return service.ErrGoodPutVideoFunds
	}
	var allowance bool
	err = tx.QueryRowContext(ctx, `SELECT (quota = 0 OR quota_used + $4 <= quota)
		AND (rate_limit_5h = 0 OR (CASE WHEN window_5h_start + INTERVAL '5 hours' > NOW() THEN usage_5h ELSE 0 END) + $4 <= rate_limit_5h)
		AND (rate_limit_1d = 0 OR (CASE WHEN window_1d_start + INTERVAL '24 hours' > NOW() THEN usage_1d ELSE 0 END) + $4 <= rate_limit_1d)
		AND (rate_limit_7d = 0 OR (CASE WHEN window_7d_start + INTERVAL '7 days' > NOW() THEN usage_7d ELSE 0 END) + $4 <= rate_limit_7d)
		FROM api_keys WHERE id=$1 AND user_id=$2 AND group_id=$3 AND deleted_at IS NULL AND status='active'
		AND (expires_at IS NULL OR expires_at > NOW()) FOR UPDATE`, job.APIKeyID, job.UserID, job.GroupID, job.HoldAmount).Scan(&allowance)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !allowance) {
		return service.ErrGoodPutVideoFunds
	}
	if err != nil {
		return err
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM goodput_video_jobs WHERE api_key_id=$1 AND state IN ('submitting','pending','review'))`, job.APIKeyID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return service.ErrGoodPutVideoLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO goodput_video_jobs
		(id,user_id,api_key_id,group_id,account_id,model,seconds,hold_amount,reference_amount,rate_multiplier,currency,
		account_rate,account_quota_cost,account_quota_daily,account_quota_weekly,account_request_count,next_poll_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NOW()+INTERVAL '2 minutes')`, job.ID, job.UserID, job.APIKeyID, job.GroupID,
		job.AccountID, job.Model, job.Seconds, job.HoldAmount, job.ReferenceAmount, job.Multiplier, job.Currency, job.AccountRate, job.AccountQuotaCost,
		job.AccountQuotaDaily, job.AccountQuotaWeekly, job.AccountRequestCount); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *usageBillingRepository) AttachGoodPutVideoReceipt(ctx context.Context, id, publicID, nativeID string) error {
	var attached string
	return r.db.QueryRowContext(ctx, `UPDATE goodput_video_jobs SET public_id=$2,native_id=$3,
		state=CASE WHEN native_id IS NULL THEN 'pending' ELSE state END,
		next_poll_at=CASE WHEN native_id IS NULL THEN NOW() ELSE next_poll_at END
		WHERE id=$1 AND ((native_id IS NULL AND state IN ('submitting','review')) OR (public_id=$2 AND native_id=$3)) RETURNING id`, id, publicID, nativeID).Scan(&attached)
}

const goodPutVideoJobColumns = `id,COALESCE(public_id,''),COALESCE(native_id,''),user_id,api_key_id,group_id,account_id,
	model,seconds,hold_amount,reference_amount,rate_multiplier,currency,account_rate,account_quota_cost,account_quota_daily,
	account_quota_weekly,account_request_count,created_at`

func scanGoodPutVideoJob(row *sql.Row) (*service.GoodPutVideoJob, error) {
	var job service.GoodPutVideoJob
	err := row.Scan(&job.ID, &job.PublicID, &job.NativeID, &job.UserID, &job.APIKeyID, &job.GroupID, &job.AccountID,
		&job.Model, &job.Seconds, &job.HoldAmount, &job.ReferenceAmount, &job.Multiplier, &job.Currency, &job.AccountRate, &job.AccountQuotaCost,
		&job.AccountQuotaDaily, &job.AccountQuotaWeekly, &job.AccountRequestCount, &job.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *usageBillingRepository) FindGoodPutVideoJob(ctx context.Context, publicID string, userID, keyID, groupID int64) (*service.GoodPutVideoJob, error) {
	return scanGoodPutVideoJob(r.db.QueryRowContext(ctx, `SELECT `+goodPutVideoJobColumns+` FROM goodput_video_jobs
		WHERE public_id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4 AND created_at > NOW() - INTERVAL '24 hours'`, publicID, userID, keyID, groupID))
}

func (r *usageBillingRepository) ClaimGoodPutVideoJob(ctx context.Context) (*service.GoodPutVideoJob, error) {
	return scanGoodPutVideoJob(r.db.QueryRowContext(ctx, `UPDATE goodput_video_jobs SET next_poll_at=NOW()+INTERVAL '20 seconds',
		state=CASE WHEN state='submitting' THEN 'review' ELSE state END
		WHERE id=(SELECT id FROM goodput_video_jobs WHERE state IN ('submitting','pending') AND next_poll_at <= NOW()
		ORDER BY next_poll_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+goodPutVideoJobColumns))
}

func (r *usageBillingRepository) FinishGoodPutVideoJob(ctx context.Context, id, state string) (bool, error) {
	if state != "completed" && state != "failed" && state != "review" {
		return false, errors.New("invalid GoodPut settlement state")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := scanGoodPutVideoJob(tx.QueryRowContext(ctx, `UPDATE goodput_video_jobs SET state=$2
		WHERE id=$1 AND (state='pending' OR (state='submitting' AND $2='failed')) RETURNING `+goodPutVideoJobColumns, id, state))
	if err != nil || job == nil {
		return false, err
	}
	if state != "review" {
		refund := 0.0
		if state == "failed" {
			refund = job.HoldAmount
		}
		var ownerID int64
		if err := tx.QueryRowContext(ctx, `UPDATE users SET frozen_balance=frozen_balance-$2, balance=balance+$3, updated_at=NOW()
			WHERE id=$1 AND frozen_balance >= $2 RETURNING id`, job.UserID, job.HoldAmount, refund).Scan(&ownerID); err != nil {
			return false, err
		}
		if state == "completed" {
			if err := r.recordGoodPutVideoSettlement(ctx, tx, *job); err != nil {
				return false, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *usageBillingRepository) recordGoodPutVideoSettlement(ctx context.Context, tx *sql.Tx, job service.GoodPutVideoJob) error {
	cmd := &service.UsageBillingCommand{UserID: job.UserID, APIKeyID: job.APIKeyID, AccountID: job.AccountID,
		AccountType: service.AccountTypeAPIKey, AccountQuotaCost: job.AccountQuotaCost,
		AccountQuotaHasDailyLimit: job.AccountQuotaDaily, AccountQuotaHasWeeklyLimit: job.AccountQuotaWeekly,
		AccountRequestCount: job.AccountRequestCount}
	var hasQuota, hasRateLimit bool
	err := tx.QueryRowContext(ctx, `SELECT quota > 0, rate_limit_5h > 0 OR rate_limit_1d > 0 OR rate_limit_7d > 0
		FROM api_keys WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, job.APIKeyID).Scan(&hasQuota, &hasRateLimit)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if hasQuota {
		cmd.APIKeyQuotaCost = job.HoldAmount
	}
	if hasRateLimit {
		cmd.APIKeyRateLimitCost = job.HoldAmount
	}
	if err := r.applyUsageBillingEffects(ctx, tx, cmd, &service.UsageBillingApplyResult{}); err != nil {
		return err
	}
	resolution, mode, endpoint, media := service.VideoBillingResolution720P, string(service.BillingModeVideo), "/v1/videos", "video"
	log := &service.UsageLog{UserID: job.UserID, APIKeyID: job.APIKeyID, AccountID: job.AccountID, GroupID: &job.GroupID,
		RequestID: service.StableGrokVideoBillingRequestID(job.PublicID), Model: job.Model, RequestedModel: job.Model,
		UpstreamModel: &job.Model, Currency: job.Currency, TotalCost: job.ReferenceAmount, ActualCost: job.HoldAmount,
		RateMultiplier: job.Multiplier, AccountRateMultiplier: &job.AccountRate, BillingType: service.BillingTypeBalance, BillingMode: &mode,
		RequestType: service.RequestTypeSync, VideoCount: 1, VideoResolution: &resolution, VideoDurationSeconds: &job.Seconds,
		InboundEndpoint: &endpoint, UpstreamEndpoint: &endpoint, MediaType: &media}
	inserted, err := (&usageLogRepository{}).createSingle(ctx, tx, log)
	if err == nil && !inserted {
		return errors.New("GoodPut video usage row already exists before settlement")
	}
	return err
}
