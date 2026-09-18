package server

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	// The SQL LIMIT is applied before the Go-side variant filter, so it has to
	// leave headroom: otherwise naming-variant groups fill the result slots and
	// push genuine degradation out of the answer.
	degradedAccountsScanLimit = 2000
	degradedAccountsMaxRows   = 500
)

// degradedAccountsQuery aggregates persisted model mismatches per account and
// model pair.
//
// MATERIALIZED is load-bearing. PostgreSQL 12+ inlines a single-reference CTE
// by default, and inlined the planner can flip to an accounts-driven nested
// loop that reads every OpenAI request in the window instead of only the
// mismatch rows. Fencing the aggregate keeps it on
// idx_usage_logs_upstream_model_mismatch_created_at, whose predicate is
// textually identical to the WHERE clause below and whose leading column makes
// [$1,$2) one contiguous range scan. The cost of the fence is that the platform
// filter cannot be pushed down, so a few non-OpenAI mismatch groups are
// aggregated and then discarded -- cheap at this window size.
//
// The model expressions are the repository's canonical effective-model form
// (see migrations/226_add_usage_log_effective_model_indexes_notx.sql), not the
// forwarding path's upstreamSentModel: the two disagree on what to fall back to.
const degradedAccountsQuery = `
WITH mismatches AS MATERIALIZED (
    SELECT
        ul.account_id                                              AS account_id,
        COALESCE(NULLIF(BTRIM(ul.requested_model), ''), ul.model)  AS requested_model,
        COALESCE(NULLIF(BTRIM(ul.upstream_model), ''), ul.model)   AS sent_model,
        BTRIM(ul.upstream_response_model)                          AS response_model,
        COUNT(*)::bigint                                           AS occurrences,
        MIN(ul.created_at)                                         AS first_seen,
        MAX(ul.created_at)                                         AS last_seen,
        ROUND(AVG(ul.first_token_ms)::numeric, 2)::float8          AS ttft_avg_ms
    FROM usage_logs ul
    WHERE ul.upstream_model_mismatch IS TRUE
      AND ul.created_at >= $1
      AND ul.created_at < $2
      AND ul.account_id > 0
      AND NULLIF(BTRIM(ul.upstream_response_model), '') IS NOT NULL
    GROUP BY 1, 2, 3, 4
)
SELECT
    m.account_id,
    a.name,
    m.requested_model,
    m.sent_model,
    m.response_model,
    m.occurrences,
    m.first_seen,
    m.last_seen,
    m.ttft_avg_ms
FROM mismatches m
JOIN accounts a ON a.id = m.account_id
WHERE a.platform = $3
  AND a.deleted_at IS NULL
ORDER BY m.occurrences DESC, m.last_seen DESC, m.account_id ASC
LIMIT $4`

// DegradedAccounts reports accounts whose upstream answered with a different
// model than the one sent, within the given window. It is read-only: the
// persisted upstream_model_mismatch column is never rewritten, and naming
// variants are filtered here at query time rather than at write time.
func (s *HealthService) DegradedAccounts(ctx context.Context, window time.Duration) ([]service.DegradedAccount, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("degraded account reporting unavailable")
	}
	window = service.ClampDegradedAccountsWindow(window)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	end := time.Now().UTC()
	start := end.Add(-window)

	rows, err := s.db.QueryContext(ctx, degradedAccountsQuery, start, end, domain.PlatformOpenAI, degradedAccountsScanLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	// Non-nil so the JSON body is [] rather than null when nothing degraded.
	degraded := make([]service.DegradedAccount, 0, 32)
	for rows.Next() {
		var (
			row  service.DegradedAccount
			ttft sql.NullFloat64
		)
		if err := rows.Scan(
			&row.AccountID,
			&row.AccountName,
			&row.RequestedModel,
			&row.SentModel,
			&row.ResponseModel,
			&row.Count,
			&row.FirstSeen,
			&row.LastSeen,
			&ttft,
		); err != nil {
			return nil, err
		}
		if !service.ModelDegradationSuspected(row.SentModel, row.ResponseModel) {
			continue
		}
		if ttft.Valid {
			row.TTFTAvgMs = &ttft.Float64
		}
		degraded = append(degraded, row)
		if len(degraded) >= degradedAccountsMaxRows {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return degraded, nil
}
