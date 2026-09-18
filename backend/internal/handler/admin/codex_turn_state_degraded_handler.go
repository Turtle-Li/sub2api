package admin

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	// The SQL LIMIT is applied before the Go-side variant filter, so it has to
	// leave headroom: otherwise naming-variant groups fill the result slots and
	// push genuine degradation out of the answer.
	codexTurnStateDegradedAccountsScanLimit = 2000
	codexTurnStateDegradedAccountsMaxRows   = 500
)

// codexTurnStateDegradedAccountsQuery is retained from the established
// degraded-account monitor. MATERIALIZED fences PostgreSQL from changing this
// partial-index query into an accounts-driven scan of the full time window.
const codexTurnStateDegradedAccountsQuery = `
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

// CodexTurnStateDegradedHandler exposes the established read-only degradation
// report through the existing administrator-authenticated route.
type CodexTurnStateDegradedHandler struct {
	db  *sql.DB
	now func() time.Time
}

func NewCodexTurnStateDegradedHandler(sqlDB *sql.DB) *CodexTurnStateDegradedHandler {
	return &CodexTurnStateDegradedHandler{
		db:  sqlDB,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// Get returns a stable bridge payload. Query failures deliberately stay
// sanitized so the legacy panel cannot surface database detail.
func (h *CodexTurnStateDegradedHandler) Get(c *gin.Context) {
	window, ok := service.ParseDegradedAccountsWindow(c.Query("window"))
	if !ok {
		response.BadRequest(c, "invalid window")
		return
	}

	degraded, err := h.degradedAccounts(c.Request.Context(), window)
	if err != nil {
		response.Success(c, gin.H{
			"degraded": []service.DegradedAccount{},
			"error":    "unavailable",
		})
		return
	}
	response.Success(c, gin.H{
		"degraded": degraded,
		"error":    "",
	})
}

func (h *CodexTurnStateDegradedHandler) degradedAccounts(ctx context.Context, window time.Duration) ([]service.DegradedAccount, error) {
	if h == nil || h.db == nil {
		return nil, errors.New("degraded account reporting unavailable")
	}
	window = service.ClampDegradedAccountsWindow(window)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	now := time.Now().UTC()
	if h.now != nil {
		now = h.now().UTC()
	}
	start := now.Add(-window)
	rows, err := h.db.QueryContext(ctx, codexTurnStateDegradedAccountsQuery, start, now, domain.PlatformOpenAI, codexTurnStateDegradedAccountsScanLimit)
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
		if len(degraded) >= codexTurnStateDegradedAccountsMaxRows {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return degraded, nil
}
