package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.GoodPutVideoJobRepository = (*usageBillingRepository)(nil)

type videoJobTable string

const (
	goodPutVideoTable    videoJobTable = "goodput_video_jobs"
	hiAPIVideoTable      videoJobTable = "hiapi_video_jobs"
	museStudioVideoTable videoJobTable = "muse_studio_video_jobs"
)

type videoJobLedger struct {
	*usageBillingRepository
	table videoJobTable
}

func (r *usageBillingRepository) MuseStudioVideoJobs() service.DurableVideoJobRepository {
	return videoJobLedger{r, museStudioVideoTable}
}

func (r *usageBillingRepository) HiAPIVideoJobs() service.DurableVideoJobRepository {
	return videoJobLedger{r, hiAPIVideoTable}
}

func (r *usageBillingRepository) ReserveVideoJob(ctx context.Context, job service.DurableVideoJob, publicEnabled bool) error {
	job.Resolution = service.VideoBillingResolution720P
	return videoJobLedger{r, goodPutVideoTable}.ReserveVideoJob(ctx, job, publicEnabled)
}

func (r *usageBillingRepository) AttachVideoReceipt(ctx context.Context, id, publicID, nativeID string) error {
	return videoJobLedger{r, goodPutVideoTable}.AttachVideoReceipt(ctx, id, publicID, nativeID)
}

func (r *usageBillingRepository) FindVideoJob(ctx context.Context, publicID string, userID, keyID, groupID int64) (*service.DurableVideoJob, error) {
	return videoJobLedger{r, goodPutVideoTable}.FindVideoJob(ctx, publicID, userID, keyID, groupID)
}

func (r *usageBillingRepository) ClaimVideoJob(ctx context.Context) (*service.DurableVideoJob, error) {
	return videoJobLedger{r, goodPutVideoTable}.ClaimVideoJob(ctx)
}

func (r *usageBillingRepository) FinishVideoJob(ctx context.Context, id, state string) (bool, error) {
	return videoJobLedger{r, goodPutVideoTable}.FinishVideoJob(ctx, id, state)
}

func (r *usageBillingRepository) CompleteGoodPutVideoJob(ctx context.Context, id string, billedSeconds int) (bool, error) {
	return videoJobLedger{r, goodPutVideoTable}.finishVideoJob(ctx, id, "completed", billedSeconds)
}

func (r *usageBillingRepository) ReviewGoodPutVideoJob(ctx context.Context, id, reason string) (bool, error) {
	if reason != service.GoodPutReviewUsageUnverified && reason != service.GoodPutReviewReceiptExpired {
		return false, errors.New("invalid GoodPut video review reason")
	}
	var applied bool
	err := r.db.QueryRowContext(ctx, `WITH revised AS (UPDATE goodput_video_jobs SET state='review',review_reason=$2,next_poll_at=NOW()+INTERVAL '5 minutes'
 WHERE id=$1 AND native_id IS NOT NULL AND (state='pending' OR (state='review' AND review_reason='')) RETURNING *),
 studio AS (UPDATE video_studio_jobs j SET state='completed',billing_review=true FROM revised r
 WHERE $2='completed_usage_unverified' AND r.api_key_id=0 AND j.id=r.studio_request_id AND j.user_id=r.user_id AND j.group_id=r.group_id AND j.api_key_id=0 AND j.model=r.model AND j.charged_amount IS NULL) SELECT EXISTS(SELECT 1 FROM revised)`, id, reason).Scan(&applied)
	return applied, err
}

func (r *usageBillingRepository) RecordGoodPutContentOutcome(ctx context.Context, id string, failed bool) error {
	_, err := r.db.ExecContext(ctx, `UPDATE goodput_video_jobs
		SET content_failure_at=CASE WHEN $2 THEN COALESCE(content_failure_at,NOW()) ELSE NULL END
		WHERE id=$1 AND (content_failure_at IS NOT NULL) <> $2`, id, failed)
	return err
}

func (r videoJobLedger) ReserveVideoJob(ctx context.Context, job service.DurableVideoJob, publicEnabled bool) error {
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
	if !exclusive && (!publicEnabled || r.table == hiAPIVideoTable) {
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
	if job.APIKeyID == service.VideoStudioWalletSlot {
		if err := r.checkStudioReservation(ctx, tx, job); err != nil {
			return err
		}
	} else if err := r.checkBearerReservation(ctx, tx, job); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+string(r.table)+`
		(id,user_id,api_key_id,group_id,account_id,model,seconds,hold_amount,reference_amount,rate_multiplier,currency,
		account_rate,account_quota_cost,account_quota_daily,account_quota_weekly,account_request_count,resolution,next_poll_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,NOW()+INTERVAL '2 minutes')`, job.ID, job.UserID, job.APIKeyID, job.GroupID,
		job.AccountID, job.Model, job.Seconds, job.HoldAmount, job.ReferenceAmount, job.Multiplier, job.Currency, job.AccountRate, job.AccountQuotaCost,
		job.AccountQuotaDaily, job.AccountQuotaWeekly, job.AccountRequestCount, job.Resolution); err != nil {
		return err
	}
	if job.APIKeyID == service.VideoStudioWalletSlot {
		// Bind the hold to an already claimed session job under the same owner; a client UUID alone grants nothing.
		var linked string
		if err := tx.QueryRowContext(ctx, `UPDATE `+string(r.table)+` d SET studio_request_id=j.id FROM video_studio_jobs j WHERE d.id=$1 AND j.user_id=d.user_id AND j.group_id=d.group_id AND j.api_key_id=0 AND j.model=d.model AND j.seconds=d.seconds AND j.state='submitting' AND j.id=$2 RETURNING j.id`, job.ID, job.StudioRequestID).Scan(&linked); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Ordinary bearer-key quota, rate and outstanding-task limits remain unchanged.
func (r videoJobLedger) checkBearerReservation(ctx context.Context, tx *sql.Tx, job service.DurableVideoJob) error {
	var allowance bool
	// A confirmed completion with unverified usage is not an in-flight job.
	// Its still-frozen quote must nevertheless count against this key's limits.
	goodPut := r.table == goodPutVideoTable
	err := tx.QueryRowContext(ctx, `SELECT (quota = 0 OR quota_used + budget.amount <= quota)
		AND (rate_limit_5h = 0 OR (CASE WHEN window_5h_start + INTERVAL '5 hours' > NOW() THEN usage_5h ELSE 0 END) + budget.amount <= rate_limit_5h)
		AND (rate_limit_1d = 0 OR (CASE WHEN window_1d_start + INTERVAL '24 hours' > NOW() THEN usage_1d ELSE 0 END) + budget.amount <= rate_limit_1d)
		AND (rate_limit_7d = 0 OR (CASE WHEN window_7d_start + INTERVAL '7 days' > NOW() THEN usage_7d ELSE 0 END) + budget.amount <= rate_limit_7d)
		FROM api_keys CROSS JOIN (SELECT $4::numeric + COALESCE(SUM(hold_amount),0) AS amount FROM goodput_video_jobs
			WHERE $5 AND api_key_id=$1 AND state='review' AND review_reason='completed_usage_unverified') budget
		WHERE id=$1 AND user_id=$2 AND group_id=$3 AND deleted_at IS NULL AND status='active'
		AND (expires_at IS NULL OR expires_at > NOW()) FOR UPDATE OF api_keys`, job.APIKeyID, job.UserID, job.GroupID, job.HoldAmount, goodPut).Scan(&allowance)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !allowance) {
		return service.ErrGoodPutVideoFunds
	}
	if err != nil {
		return err
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM goodput_video_jobs WHERE api_key_id=$1 AND state IN ('submitting','pending','review')
			AND NOT ($2 AND state='review' AND review_reason='completed_usage_unverified')
		UNION ALL SELECT 1 FROM hiapi_video_jobs WHERE api_key_id=$1 AND state IN ('submitting','pending','review'))`, job.APIKeyID, goodPut).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return service.ErrGoodPutVideoLimit
	}
	return nil
}

func (r videoJobLedger) AttachVideoReceipt(ctx context.Context, id, publicID, nativeID string) error {
	var attached string
	query := `UPDATE ` + string(r.table) + ` SET public_id=$2,native_id=$3,
 state=CASE WHEN native_id IS NULL THEN 'pending' ELSE state END, next_poll_at=CASE WHEN native_id IS NULL THEN NOW() ELSE next_poll_at END
 WHERE id=$1 AND ((native_id IS NULL AND state IN ('submitting','review')) OR (public_id=$2 AND native_id=$3))`
	if r.table == hiAPIVideoTable {
		query += " RETURNING id"
	} else {
		query = `WITH bound AS (` + query + ` RETURNING *), studio AS (
 UPDATE video_studio_jobs j SET receipt=b.public_id,state=CASE WHEN j.state IN ('submitting','uncertain') THEN 'queued' ELSE j.state END FROM bound b
 WHERE j.id=b.studio_request_id AND j.user_id=b.user_id AND j.group_id=b.group_id AND j.api_key_id=0 AND j.model=b.model AND (j.receipt IS NULL OR j.receipt=b.public_id)) SELECT id FROM bound`
	}
	return r.db.QueryRowContext(ctx, query, id, publicID, nativeID).Scan(&attached)
}

const durableVideoJobColumns = `id,COALESCE(public_id,''),COALESCE(native_id,''),user_id,api_key_id,group_id,account_id,
	model,seconds,hold_amount,reference_amount,rate_multiplier,currency,account_rate,account_quota_cost,account_quota_daily,
	account_quota_weekly,account_request_count,created_at,resolution,state`

func (r videoJobLedger) jobColumns() string {
	if r.table == goodPutVideoTable {
		return durableVideoJobColumns + `,COALESCE(billed_seconds,0),content_failure_at IS NOT NULL,COALESCE(studio_request_id::text,'')`
	}
	if r.table == museStudioVideoTable {
		return durableVideoJobColumns + `,0,false,COALESCE(studio_request_id::text,'')`
	}
	return durableVideoJobColumns + `,0,false,''`
}

func scanDurableVideoJob(row *sql.Row) (*service.DurableVideoJob, error) {
	var job service.DurableVideoJob
	err := row.Scan(&job.ID, &job.PublicID, &job.NativeID, &job.UserID, &job.APIKeyID, &job.GroupID, &job.AccountID,
		&job.Model, &job.Seconds, &job.HoldAmount, &job.ReferenceAmount, &job.Multiplier, &job.Currency, &job.AccountRate, &job.AccountQuotaCost,
		&job.AccountQuotaDaily, &job.AccountQuotaWeekly, &job.AccountRequestCount, &job.CreatedAt, &job.Resolution, &job.State, &job.BilledSeconds, &job.ContentFailed, &job.StudioRequestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r videoJobLedger) FindVideoJob(ctx context.Context, publicID string, userID, keyID, groupID int64) (*service.DurableVideoJob, error) {
	return scanDurableVideoJob(r.db.QueryRowContext(ctx, `SELECT `+r.jobColumns()+` FROM `+string(r.table)+`
		WHERE public_id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4 AND created_at > NOW() - INTERVAL '24 hours'`, publicID, userID, keyID, groupID))
}

func (r videoJobLedger) ClaimVideoJob(ctx context.Context) (*service.DurableVideoJob, error) {
	if r.table == goodPutVideoTable {
		// Legacy review rows with a receipt can be checked again, but not
		// submissions with no receipt nor receipts outside the read window.
		return scanDurableVideoJob(r.db.QueryRowContext(ctx, `UPDATE goodput_video_jobs
			SET next_poll_at=NOW()+CASE WHEN state='review' THEN INTERVAL '5 minutes' ELSE INTERVAL '20 seconds' END,
				state=CASE WHEN state='submitting' THEN 'review' ELSE state END
			WHERE id=(SELECT id FROM goodput_video_jobs WHERE next_poll_at <= NOW()
				AND (state IN ('submitting','pending') OR (state='review' AND native_id IS NOT NULL
					AND review_reason IN ('','completed_usage_unverified') AND created_at > NOW()-INTERVAL '24 hours'))
				ORDER BY next_poll_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+r.jobColumns()))
	}
	return scanDurableVideoJob(r.db.QueryRowContext(ctx, `UPDATE `+string(r.table)+` SET next_poll_at=NOW()+INTERVAL '20 seconds',
		state=CASE WHEN state='submitting' THEN 'review' ELSE state END
		WHERE id=(SELECT id FROM `+string(r.table)+` WHERE state IN ('submitting','pending') AND next_poll_at <= NOW()
		ORDER BY next_poll_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+r.jobColumns()))
}

func (r videoJobLedger) FinishVideoJob(ctx context.Context, id, state string) (bool, error) {
	return r.finishVideoJob(ctx, id, state, 0)
}

func (r videoJobLedger) finishVideoJob(ctx context.Context, id, state string, billedSeconds int) (bool, error) {
	if state != "completed" && state != "failed" && state != "review" {
		return false, errors.New("invalid video settlement state")
	}
	if r.table == goodPutVideoTable && state == "completed" && (billedSeconds < 4 || billedSeconds > 15) {
		return false, errors.New("invalid GoodPut billable duration")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	query := `UPDATE ` + string(r.table) + ` SET state=$2 WHERE id=$1 AND (state='pending' OR (state='submitting' AND $2='failed'))`
	args := []any{id, state}
	if r.table == goodPutVideoTable {
		// Preserve a review incident for Ops even if reconciliation wins the race
		// with the next notification cycle (including rows from the old binary).
		const retainReview = `review_reason=CASE WHEN state='review' AND review_reason='' THEN 'legacy_review' ELSE review_reason END`
		if state == "completed" {
			query = `UPDATE goodput_video_jobs SET state=$2,billed_seconds=$3,` + retainReview + `
				WHERE id=$1 AND native_id IS NOT NULL AND created_at > NOW()-INTERVAL '24 hours'
				AND (state='pending' OR (state='review' AND review_reason IN ('','completed_usage_unverified')))
				AND ((model='minimax/h3' AND $3 BETWEEN 4 AND seconds)
				 OR (model='minimax/h3-fast' AND $3 BETWEEN 5 AND LEAST(14,seconds)))`
			args = append(args, billedSeconds)
		} else if state == "failed" {
			// A review with a bound receipt can represent an earlier completion
			// (including legacy rows). A later failed status must not refund it.
			query = `UPDATE goodput_video_jobs SET state=$2 WHERE id=$1 AND state IN ('pending','submitting')`
		}
	}
	job, err := scanDurableVideoJob(tx.QueryRowContext(ctx, query+` RETURNING `+r.jobColumns(), args...))
	if err != nil || job == nil {
		return false, err
	}
	if state != "review" {
		settlement := *job
		if job.BilledSeconds > 0 {
			// Scale only the submitted snapshot, with the ledger's numeric precision.
			err := tx.QueryRowContext(ctx, `SELECT round(hold_amount*billed_seconds/seconds,8),
				round(reference_amount*billed_seconds/seconds,8),round(account_quota_cost*billed_seconds/seconds,8)
				FROM goodput_video_jobs WHERE id=$1`, id).Scan(&settlement.HoldAmount, &settlement.ReferenceAmount, &settlement.AccountQuotaCost)
			if err != nil {
				return false, err
			}
			settlement.Seconds = job.BilledSeconds
		}
		debit := settlement.HoldAmount
		if state == "failed" {
			debit = 0
		}
		var ownerID int64
		if err := tx.QueryRowContext(ctx, `UPDATE users SET frozen_balance=frozen_balance-$2, balance=balance+($2-$3), updated_at=NOW()
			WHERE id=$1 AND frozen_balance >= $2 RETURNING id`, job.UserID, job.HoldAmount, debit).Scan(&ownerID); err != nil {
			return false, err
		}
		if job.APIKeyID == service.VideoStudioWalletSlot {
			// The ledger owns the charge; the history row is a private display snapshot in the same atomic commit.
			if _, err := tx.ExecContext(ctx, `UPDATE video_studio_jobs SET state=$3,billing_review=false,
    charged_amount=CASE WHEN $3='completed' THEN $4::numeric ELSE NULL END, charged_currency=CASE WHEN $3='completed' THEN $5::text ELSE NULL END
    WHERE user_id=$1 AND id=$2 AND group_id=$6 AND api_key_id=0 AND model=$7 AND (receipt IS NULL OR receipt=$8)`, job.UserID, job.StudioRequestID, state, debit, job.Currency, job.GroupID, job.Model, job.PublicID); err != nil {
				return false, err
			}
		}
		if state == "completed" {
			if err := r.recordVideoSettlement(ctx, tx, settlement); err != nil {
				return false, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// The user row is already locked. A wallet slot is unique only within (user,group),
// never a shared per-key quota. Recheck the live entitlement in the hold transaction.
func (r videoJobLedger) checkStudioReservation(ctx context.Context, tx *sql.Tx, job service.DurableVideoJob) error {
	if (r.table != museStudioVideoTable || job.Model != service.MuseVideoModel) && (r.table != goodPutVideoTable || !service.IsGoodPutVideoModel(job.Model)) {
		return service.ErrGoodPutVideoGroup
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groups g JOIN users u ON u.id=$1 WHERE g.id=$2 AND g.deleted_at IS NULL AND g.status='active' AND g.subscription_type='standard' AND g.platform='openai' AND g.allow_image_generation AND COALESCE((g.models_list_config->>'enabled')::boolean,false) AND (g.models_list_config->'models') ? $3
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(g.models_list_config->'models') AS model_id WHERE NOT (CASE WHEN $3='muse-video' THEN model_id IN ('muse-video','muse-image','muse-spark') ELSE model_id IN ('minimax/h3','minimax/h3-fast') END)) AND ((NOT g.is_exclusive OR $4) AND NOT u.restrict_public_groups OR EXISTS(SELECT 1 FROM user_allowed_groups a WHERE a.user_id=u.id AND a.group_id=g.id)))`, job.UserID, job.GroupID, job.Model, job.StudioGroupShared).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return service.ErrGoodPutVideoGroup
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+string(r.table)+` WHERE user_id=$1 AND group_id=$2 AND api_key_id=0 AND state IN ('submitting','pending','review'))`, job.UserID, job.GroupID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return service.ErrGoodPutVideoLimit
	}
	return nil
}

func (r videoJobLedger) recordVideoSettlement(ctx context.Context, tx *sql.Tx, job service.DurableVideoJob) error {
	cmd := &service.UsageBillingCommand{UserID: job.UserID, APIKeyID: job.APIKeyID, AccountID: job.AccountID,
		AccountType: service.AccountTypeAPIKey, AccountQuotaCost: job.AccountQuotaCost,
		AccountQuotaHasDailyLimit: job.AccountQuotaDaily, AccountQuotaHasWeeklyLimit: job.AccountQuotaWeekly,
		AccountRequestCount: job.AccountRequestCount}
	// Like Image Studio, the durable row is the wallet usage record. usage_logs
	// requires a real api_keys FK. Account counters still settle in this transaction.
	if job.APIKeyID == service.VideoStudioWalletSlot {
		return r.applyUsageBillingEffects(ctx, tx, cmd, &service.UsageBillingApplyResult{})
	}
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
	mode, inbound, upstream, media := string(service.BillingModeVideo), "/v1/videos", "/v1/videos", "video"
	if r.table == hiAPIVideoTable {
		upstream = "/v1/tasks"
	}
	log := &service.UsageLog{UserID: job.UserID, APIKeyID: job.APIKeyID, AccountID: job.AccountID, GroupID: &job.GroupID,
		RequestID: service.StableGrokVideoBillingRequestID(job.PublicID), Model: job.Model, RequestedModel: job.Model,
		UpstreamModel: &job.Model, Currency: job.Currency, TotalCost: job.ReferenceAmount, ActualCost: job.HoldAmount,
		RateMultiplier: job.Multiplier, AccountRateMultiplier: &job.AccountRate, BillingType: service.BillingTypeBalance, BillingMode: &mode,
		RequestType: service.RequestTypeSync, VideoCount: 1, VideoResolution: &job.Resolution, VideoDurationSeconds: &job.Seconds,
		InboundEndpoint: &inbound, UpstreamEndpoint: &upstream, MediaType: &media}
	inserted, err := (&usageLogRepository{}).createSingle(ctx, tx, log)
	if err == nil && !inserted {
		return errors.New("video usage row already exists before settlement")
	}
	return err
}
