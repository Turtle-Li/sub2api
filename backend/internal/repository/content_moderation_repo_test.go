package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBuildContentModerationLogWhere_BlockedIncludesAllBlockActions(t *testing.T) {
	where, args := buildContentModerationLogWhere(service.ContentModerationLogFilter{Result: "blocked"})

	require.Empty(t, args)
	sql := strings.Join(where, " AND ")
	require.Contains(t, sql, "l.action IN ('block', 'keyword_block', 'hash_block')")
	require.NotContains(t, sql, "l.action = 'block'")
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesHashBlock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND action <> 'hash_block'")).
		WithArgs(int64(1001), since, false).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	count, err := repo.CountFlaggedByUserSince(context.Background(), 1001, since, false)

	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesCyberPolicyWhenRequested(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND ($3::bool IS FALSE OR action <> 'cyber_policy')")).
		WithArgs(int64(1001), since, true).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	count, err := repo.CountFlaggedByUserSince(context.Background(), 1001, since, true)

	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryGetLogReturnsFullPromptOnlyOnDetail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	createdAt := time.Now().UTC()
	columns := []string{
		"id", "request_id", "user_id", "user_email", "api_key_id", "api_key_name", "group_id", "group_name",
		"endpoint", "provider", "model", "mode", "action", "flagged", "highest_category", "highest_score",
		"category_scores", "threshold_snapshot", "input_excerpt", "full_prompt", "upstream_latency_ms", "error",
		"violation_count", "auto_banned", "email_sent", "status", "queue_delay_ms", "matched_keyword", "created_at",
	}
	mock.ExpectQuery(regexp.QuoteMeta("l.input_excerpt, l.full_prompt, l.upstream_latency_ms")).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			int64(42), "req-42", int64(9), "user@example.com", int64(7), "codex-pro", int64(3), "group",
			"/v1/responses", "openai", "gpt-5.6-terra", "post_upstream", "cyber_policy", true, "cyber_policy", 1.0,
			[]byte(`{"cyber_policy":1}`), []byte(`{}`), "redacted excerpt", "full unredacted prompt", int64(12), "upstream error",
			1, false, false, "active", int64(2), "", createdAt,
		))

	repo := NewContentModerationRepository(db)
	log, err := repo.GetLog(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "full unredacted prompt", log.FullPrompt)
	require.Equal(t, "redacted excerpt", log.InputExcerpt)
	require.Equal(t, "cyber_policy", log.Action)
	require.NoError(t, mock.ExpectationsWereMet())
}
