package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type settingRepository struct {
	client *ent.Client
}

type settingCASExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func NewSettingRepository(client *ent.Client) service.SettingRepository {
	return &settingRepository{client: client}
}

func (r *settingRepository) Get(ctx context.Context, key string) (*service.Setting, error) {
	m, err := r.client.Setting.Query().Where(setting.KeyEQ(key)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, service.ErrSettingNotFound
		}
		return nil, err
	}
	return &service.Setting{
		ID:        m.ID,
		Key:       m.Key,
		Value:     m.Value,
		UpdatedAt: m.UpdatedAt,
	}, nil
}

func (r *settingRepository) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r *settingRepository) Set(ctx context.Context, key, value string) error {
	now := time.Now()
	return r.client.Setting.
		Create().
		SetKey(key).
		SetValue(value).
		SetUpdatedAt(now).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
}

// CompareAndSet changes a setting only when its exact stored value still
// matches expectedValue. An empty expected value means the key must be absent;
// that convention is already used by the desktop-tool catalog's first-write
// CAS. The SQL is shared by the binding/idempotency transaction below.
func (r *settingRepository) CompareAndSet(ctx context.Context, key, expectedValue, value string) (bool, error) {
	return compareAndSetSettingValue(ctx, clientFromContext(ctx, r.client), key, expectedValue, value)
}

func compareAndSetSettingValue(ctx context.Context, executor settingCASExecutor, key, expectedValue, value string) (bool, error) {
	if executor == nil {
		return false, fmt.Errorf("setting compare-and-set executor is nil")
	}
	var (
		result sql.Result
		err    error
	)
	if expectedValue == "" {
		result, err = executor.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (key) DO NOTHING
		`, key, value)
	} else {
		result, err = executor.ExecContext(ctx, `
			UPDATE settings
			SET value = $3, updated_at = NOW()
			WHERE key = $1 AND value = $2
		`, key, expectedValue, value)
	}
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// compareAndDeleteSettingValue verifies and deletes a setting in the same
// transaction. For an expected absence, it briefly inserts then deletes an
// uncommitted sentinel. That locks the unique key through commit, making an
// absent manual binding an actual CAS condition instead of a no-op that could
// miss a concurrent rebind.
func compareAndDeleteSettingValue(ctx context.Context, executor settingCASExecutor, key, expectedValue string) (bool, error) {
	if executor == nil {
		return false, fmt.Errorf("setting compare-and-delete executor is nil")
	}
	if expectedValue != "" {
		result, err := executor.ExecContext(ctx, `
			DELETE FROM settings
			WHERE key = $1 AND value = $2
		`, key, expectedValue)
		if err != nil {
			return false, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		return affected == 1, nil
	}

	const absentCASSentinel = "__sub2api_unified_payment_absent_cas__"
	insert, err := executor.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO NOTHING
	`, key, absentCASSentinel)
	if err != nil {
		return false, err
	}
	inserted, err := insert.RowsAffected()
	if err != nil {
		return false, err
	}
	if inserted != 1 {
		return false, nil
	}
	deleted, err := executor.ExecContext(ctx, `
		DELETE FROM settings
		WHERE key = $1 AND value = $2
	`, key, absentCASSentinel)
	if err != nil {
		return false, err
	}
	affected, err := deleted.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// CommitUnifiedPaymentBindingAndIdempotencySuccess atomically publishes the
// backward-readable raw binding, its revision/fence metadata, and the exact
// idempotency processing lease. If any conditional write has no row, the
// whole transaction rolls back and leaves both setting keys untouched.
func (r *settingRepository) CommitUnifiedPaymentBindingAndIdempotencySuccess(
	ctx context.Context,
	commit service.UnifiedPaymentBindingIdempotencyCommit,
) (service.UnifiedPaymentBindingIdempotencyCommitOutcome, error) {
	if r == nil || r.client == nil {
		return 0, fmt.Errorf("setting repository is unavailable")
	}
	if commit.BindingKey == "" || commit.RevisionKey == "" || commit.Claim.ID <= 0 || commit.Claim.RequestFingerprint == "" || commit.Claim.LockedUntil.IsZero() || commit.Claim.ExpiresAt.IsZero() || commit.ExpiresAt.IsZero() {
		return 0, fmt.Errorf("invalid unified payment binding idempotency commit")
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin unified payment binding commit: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	txClient := tx.Client()
	var swapped bool
	if commit.BindingDelete {
		swapped, err = compareAndDeleteSettingValue(ctx, txClient, commit.BindingKey, commit.BindingExpectedValue)
	} else {
		swapped, err = compareAndSetSettingValue(ctx, txClient, commit.BindingKey, commit.BindingExpectedValue, commit.BindingValue)
	}
	if err != nil {
		return 0, fmt.Errorf("compare unified payment binding: %w", err)
	}
	if !swapped {
		return service.UnifiedPaymentBindingIdempotencyCommitVersionConflict, nil
	}
	revisionSwapped, err := compareAndSetSettingValue(ctx, txClient, commit.RevisionKey, commit.RevisionExpectedValue, commit.RevisionValue)
	if err != nil {
		return 0, fmt.Errorf("compare unified payment binding revision: %w", err)
	}
	if !revisionSwapped {
		return service.UnifiedPaymentBindingIdempotencyCommitVersionConflict, nil
	}

	result, err := txClient.ExecContext(ctx, `
		UPDATE idempotency_records
		SET status = $2,
			response_status = $3,
			response_body = $4,
			error_reason = NULL,
			locked_until = NULL,
			expires_at = $5,
			updated_at = NOW()
		WHERE id = $1
			AND status = $6
			AND request_fingerprint = $7
			AND locked_until = $8
			AND expires_at = $9
	`,
		commit.Claim.ID,
		service.IdempotencyStatusSucceeded,
		commit.ResponseStatus,
		commit.ResponseBody,
		commit.ExpiresAt,
		service.IdempotencyStatusProcessing,
		commit.Claim.RequestFingerprint,
		commit.Claim.LockedUntil,
		commit.Claim.ExpiresAt,
	)
	if err != nil {
		return 0, fmt.Errorf("mark unified payment binding idempotency succeeded: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read unified payment binding idempotency result: %w", err)
	}
	if affected != 1 {
		return service.UnifiedPaymentBindingIdempotencyCommitClaimLost, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit unified payment binding: %w", err)
	}
	committed = true
	return service.UnifiedPaymentBindingIdempotencyCommitSucceeded, nil
}

func (r *settingRepository) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	settings, err := r.client.Setting.Query().Where(setting.KeyIn(keys...)).All(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func (r *settingRepository) SetMultiple(ctx context.Context, settings map[string]string) error {
	if len(settings) == 0 {
		return nil
	}

	now := time.Now()
	builders := make([]*ent.SettingCreate, 0, len(settings))
	for key, value := range settings {
		builders = append(builders, r.client.Setting.Create().SetKey(key).SetValue(value).SetUpdatedAt(now))
	}
	return r.client.Setting.
		CreateBulk(builders...).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
}

func (r *settingRepository) GetAll(ctx context.Context) (map[string]string, error) {
	settings, err := r.client.Setting.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func (r *settingRepository) Delete(ctx context.Context, key string) error {
	_, err := r.client.Setting.Delete().Where(setting.KeyEQ(key)).Exec(ctx)
	return err
}
