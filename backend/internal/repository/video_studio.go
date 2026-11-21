package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.VideoStudioStore = (*apiKeyRepository)(nil)

func (r *apiKeyRepository) VideoStudioGroupIDs(ctx context.Context, user int64) ([]int64, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT g.id FROM groups g JOIN users u ON u.id=$1 AND u.status='active' AND u.deleted_at IS NULL
 WHERE g.deleted_at IS NULL AND g.status='active' AND g.platform='openai' AND g.subscription_type='standard' AND g.allow_image_generation
 AND ((NOT g.is_exclusive AND NOT u.restrict_public_groups) OR EXISTS (SELECT 1 FROM user_allowed_groups a WHERE a.user_id=u.id AND a.group_id=g.id))
 AND g.models_list_config->'models' ?| ARRAY['muse-video','minimax/h3','minimax/h3-fast'] ORDER BY g.sort_order,g.id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *apiKeyRepository) ClaimVideoStudioJob(ctx context.Context, j service.VideoStudioJob) (bool, error) {
	var params any
	if j.Parameters != nil {
		data, err := json.Marshal(j.Parameters)
		if err != nil {
			return false, err
		}
		params = string(data)
	}
	res, err := r.sql.ExecContext(ctx, `INSERT INTO video_studio_jobs(user_id,id,api_key_id,group_id,model,seconds,size,fingerprint,parameters) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb) ON CONFLICT(user_id,id) DO NOTHING`, j.UserID, j.ID, j.KeyID, j.GroupID, j.Model, j.Seconds, j.Size, j.Fingerprint, params)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

const studioColumns = `id::text,user_id,api_key_id,group_id,model,seconds,size,fingerprint,COALESCE(receipt,''),state,billing_review,created_at,parameters,charged_amount,charged_currency,COALESCE(actual_duration_ms,0)`

type studioScanner interface{ Scan(...any) error }

func scanStudioJob(row studioScanner) (service.VideoStudioJob, error) {
	var j service.VideoStudioJob
	var params []byte
	err := row.Scan(&j.ID, &j.UserID, &j.KeyID, &j.GroupID, &j.Model, &j.Seconds, &j.Size, &j.Fingerprint, &j.Receipt, &j.Status, &j.BillingReview, &j.CreatedAt, &params, &j.ChargedAmount, &j.ChargedCurrency, &j.ActualDurationMS)
	if err == nil && len(params) > 0 {
		err = json.Unmarshal(params, &j.Parameters)
	}
	return j, err
}
func (r *apiKeyRepository) FindVideoStudioJob(ctx context.Context, user int64, id string) (service.VideoStudioJob, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+studioColumns+` FROM video_studio_jobs WHERE user_id=$1 AND id=$2`, user, id)
	if err != nil {
		return service.VideoStudioJob{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return service.VideoStudioJob{}, err
		}
		return service.VideoStudioJob{}, service.ErrVideoStudioNotFound
	}
	return scanStudioJob(rows)
}
func (r *apiKeyRepository) ListVideoStudioJobs(ctx context.Context, user int64, cursor string, limit int) ([]service.VideoStudioJob, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+studioColumns+` FROM video_studio_jobs WHERE user_id=$1 AND ($2::text='' OR (created_at,id)<(SELECT created_at,id FROM video_studio_jobs WHERE user_id=$1 AND id=NULLIF($2,'')::uuid)) ORDER BY created_at DESC,id DESC LIMIT $3`, user, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []service.VideoStudioJob{}
	for rows.Next() {
		j, err := scanStudioJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, j)
	}
	return items, rows.Err()
}
func (r *apiKeyRepository) UpdateVideoStudioJob(ctx context.Context, user int64, id, receipt, state string, review bool) error {
	res, err := r.sql.ExecContext(ctx, `UPDATE video_studio_jobs SET
		receipt=COALESCE(receipt,NULLIF($3,'')),
		state=CASE WHEN state IN ('failed','expired') OR (state='completed' AND $4!='expired') OR (state='running' AND $4='queued') THEN state ELSE $4 END,
		billing_review=CASE WHEN $4='completed' AND state NOT IN ('expired','failed') AND (state!='completed' OR billing_review) THEN $5 ELSE billing_review END
		WHERE user_id=$1 AND id=$2 AND (receipt IS NULL OR receipt=$3)`, user, id, receipt, state, review)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("video studio receipt not updated")
	}
	return err
}

// Both interactive completion and the existing reconciliation worker observe the same private receipt.
func recordStudioDuration(ctx context.Context, db sqlExecutor, j service.VideoStudioJob, ms int64) error {
	if ms <= 0 || ms > 1<<53-1 || j.Receipt == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, `UPDATE video_studio_jobs SET actual_duration_ms=$7
 WHERE user_id=$1 AND id=$2 AND api_key_id=$3 AND group_id=$4 AND model=$5 AND receipt=$6
 AND state='completed' AND actual_duration_ms IS NULL`, j.UserID, j.ID, j.KeyID, j.GroupID, j.Model, j.Receipt, ms)
	return err
}
func (r *apiKeyRepository) RecordVideoStudioDuration(ctx context.Context, j service.VideoStudioJob, ms int64) error {
	return recordStudioDuration(ctx, r.sql, j, ms)
}
func (r *usageBillingRepository) RecordVideoStudioDuration(ctx context.Context, j service.VideoStudioJob, ms int64) error {
	return recordStudioDuration(ctx, r.db, j, ms)
}
