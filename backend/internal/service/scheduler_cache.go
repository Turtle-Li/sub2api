package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	SchedulerModeSingle = "single"
	SchedulerModeMixed  = "mixed"
	SchedulerModeForced = "forced"
)

var (
	ErrSchedulerBucketRetired              = errors.New("scheduler bucket retired")
	ErrSchedulerBucketWriteFenced          = errors.New("scheduler bucket write fenced")
	ErrSchedulerGroupLifecycleLeaseInvalid = errors.New("scheduler group lifecycle lease invalid")
	ErrSchedulerGroupLifecycleLeaseLost    = errors.New("scheduler group lifecycle lease lost")
)

// SchedulerBucketWriteToken fences a snapshot writer to one bucket epoch.
// Tokens must be captured before any database load or queued rebuild work.
type SchedulerBucketWriteToken struct {
	Bucket SchedulerBucket
	Epoch  int64
}

func (t SchedulerBucketWriteToken) ValidFor(bucket SchedulerBucket) bool {
	return t.Epoch > 0 && t.Bucket == bucket
}

// SchedulerGroupLifecycleLease identifies one owner of a group's short-lived
// retirement/reopen critical section.
type SchedulerGroupLifecycleLease struct {
	GroupID    int64
	OwnerToken string
}

func (l SchedulerGroupLifecycleLease) ValidFor(groupID int64) bool {
	return groupID > 0 && l.GroupID == groupID && l.OwnerToken != ""
}

type SchedulerBucket struct {
	GroupID  int64
	Platform string
	Mode     string
}

func (b SchedulerBucket) String() string {
	return fmt.Sprintf("%d:%s:%s", b.GroupID, b.Platform, b.Mode)
}

func ParseSchedulerBucket(raw string) (SchedulerBucket, bool) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return SchedulerBucket{}, false
	}
	groupID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return SchedulerBucket{}, false
	}
	if parts[1] == "" || parts[2] == "" {
		return SchedulerBucket{}, false
	}
	return SchedulerBucket{
		GroupID:  groupID,
		Platform: parts[1],
		Mode:     parts[2],
	}, true
}

// SchedulerCache 负责调度快照与账号快照的缓存读写。
type SchedulerCache interface {
	// GetSnapshot 读取快照并返回命中与否（ready + active + 数据完整）。
	GetSnapshot(ctx context.Context, bucket SchedulerBucket) ([]*Account, bool, error)
	// CaptureBucketWriteToken captures the current open epoch without changing
	// retirement state. A tombstoned bucket returns ErrSchedulerBucketRetired.
	CaptureBucketWriteToken(ctx context.Context, bucket SchedulerBucket) (SchedulerBucketWriteToken, error)
	// SetSnapshot 写入快照并切换激活版本。token 必须在 DB load/任务排队前取得。
	SetSnapshot(ctx context.Context, bucket SchedulerBucket, token SchedulerBucketWriteToken, accounts []Account) error
	// RetireBucket persistently tombstones a bucket and fences every older writer.
	// Readers that captured the active version before retirement may finish; new
	// readers observe ready/active as absent.
	RetireBucket(ctx context.Context, bucket SchedulerBucket) error
	// ReopenBucket is the only operation allowed to clear a tombstone. It returns
	// the retirement generation established by RetireBucket; repeated calls for
	// the same generation are idempotent. Callers must serialize a fresh authority
	// check through ReopenBucket with RetireBucket under the same group lifecycle
	// lease; ordinary rebuild paths never call ReopenBucket.
	ReopenBucket(ctx context.Context, bucket SchedulerBucket) (SchedulerBucketWriteToken, error)
	// TryAcquireGroupLifecycleLease serializes authoritative retirement/reopen
	// decisions for one non-zero group across instances.
	TryAcquireGroupLifecycleLease(ctx context.Context, groupID int64, ttl time.Duration) (SchedulerGroupLifecycleLease, bool, error)
	// ReleaseGroupLifecycleLease releases the lease only if its owner token still
	// matches, so an expired holder cannot delete a successor's lease. Missing,
	// expired, mismatched, and already released leases return
	// ErrSchedulerGroupLifecycleLeaseLost.
	ReleaseGroupLifecycleLease(ctx context.Context, lease SchedulerGroupLifecycleLease) error
	// GetAccount 获取单账号快照。
	GetAccount(ctx context.Context, accountID int64) (*Account, error)
	// SetAccount 写入单账号快照（包含不可调度状态）。
	SetAccount(ctx context.Context, account *Account) error
	// DeleteAccount 删除单账号快照。
	DeleteAccount(ctx context.Context, accountID int64) error
	// UpdateLastUsed 批量更新账号的最后使用时间。
	UpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error
	// TryLockBucket 尝试获取分桶重建锁。
	TryLockBucket(ctx context.Context, bucket SchedulerBucket, ttl time.Duration) (bool, error)
	// UnlockBucket 释放分桶重建锁。
	UnlockBucket(ctx context.Context, bucket SchedulerBucket) error
	// ListBuckets 返回已注册的分桶集合。
	ListBuckets(ctx context.Context) ([]SchedulerBucket, error)
	// GetOutboxWatermark 读取 outbox 水位。
	GetOutboxWatermark(ctx context.Context) (int64, error)
	// SetOutboxWatermark 保存 outbox 水位。
	SetOutboxWatermark(ctx context.Context, id int64) error
}

// SchedulerAccountSnapshotRetirer is implemented by caches that can retire an
// account snapshot atomically. Retirement is intentionally stronger than a
// best-effort DeleteAccount: it fences delayed writers so an older snapshot
// cannot recreate a fixed-egress account after a newer mutation has committed.
// The account may be ID-only when no fresh scheduler metadata is available.
type SchedulerAccountSnapshotRetirer interface {
	RetireAccountSnapshot(ctx context.Context, account *Account) error
	RetireDeletedAccountSnapshot(ctx context.Context, accountID int64) error
}

// ShouldRetireFixedEgressSchedulerSnapshot reports the account class whose
// single-account scheduler payload must be fenced rather than repopulated.
// OpenAI OAuth and setup-token parents and credential shadows share the same
// fixed-egress contract; the parent relationship does not change this cache
// safety rule.
func ShouldRetireFixedEgressSchedulerSnapshot(account *Account) bool {
	return !FixedEgressCompatibilityModeEnabled() && account != nil && account.IsOpenAIOAuthLike()
}

// PublishSchedulerAccountSnapshot updates an ordinary account snapshot, but
// retires a fixed-egress OpenAI OAuth/setup-token snapshot. Caches without the
// optional retirement capability fall back to DeleteAccount, preserving the
// interface's fail-safe behavior for alternate implementations.
func PublishSchedulerAccountSnapshot(ctx context.Context, cache SchedulerCache, account *Account) error {
	if cache == nil || account == nil || account.ID <= 0 {
		return nil
	}
	if ShouldRetireFixedEgressSchedulerSnapshot(account) {
		if retirer, ok := cache.(SchedulerAccountSnapshotRetirer); ok {
			return retirer.RetireAccountSnapshot(ctx, account)
		}
		return cache.DeleteAccount(ctx, account.ID)
	}
	return cache.SetAccount(ctx, account)
}

// RetireSchedulerAccountSnapshot fences a fixed-egress account when the
// caller only has its ID (for example, a parent+shadow proxy CAS). A concrete
// cache can preserve safe metadata when supplied through Publish; ID-only
// retirement still atomically removes the full routing payload.
func RetireSchedulerAccountSnapshot(ctx context.Context, cache SchedulerCache, accountID int64) error {
	if cache == nil || accountID <= 0 {
		return nil
	}
	if FixedEgressCompatibilityModeEnabled() {
		return cache.DeleteAccount(ctx, accountID)
	}
	if retirer, ok := cache.(SchedulerAccountSnapshotRetirer); ok {
		return retirer.RetireAccountSnapshot(ctx, &Account{ID: accountID})
	}
	return cache.DeleteAccount(ctx, accountID)
}

// RetireDeletedSchedulerAccountSnapshot installs a terminal fence for an
// account that no longer exists. Account IDs are never reused, so no delayed
// protected or ordinary publisher may reopen its metadata or routing payload.
func RetireDeletedSchedulerAccountSnapshot(ctx context.Context, cache SchedulerCache, accountID int64) error {
	if cache == nil || accountID <= 0 {
		return nil
	}
	if FixedEgressCompatibilityModeEnabled() {
		return cache.DeleteAccount(ctx, accountID)
	}
	if retirer, ok := cache.(SchedulerAccountSnapshotRetirer); ok {
		return retirer.RetireDeletedAccountSnapshot(ctx, accountID)
	}
	return cache.DeleteAccount(ctx, accountID)
}
