package admin

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func codexTurnStateDegradedRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"account_id", "name", "requested_model", "sent_model", "response_model",
		"occurrences", "first_seen", "last_seen", "ttft_avg_ms",
	})
}

type codexTurnStateCapturedTime struct{ at *time.Time }

func (c codexTurnStateCapturedTime) Match(value driver.Value) bool {
	moment, ok := value.(time.Time)
	if ok {
		*c.at = moment
	}
	return ok
}

func TestCodexTurnStateDegradedHandlerFiltersNamingVariants(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 9, 18, 1, 30, 0, 0, time.UTC)
	var start, end time.Time
	mock.ExpectQuery("upstream_model_mismatch IS TRUE").
		WithArgs(codexTurnStateCapturedTime{&start}, codexTurnStateCapturedTime{&end}, domain.PlatformOpenAI, codexTurnStateDegradedAccountsScanLimit).
		WillReturnRows(codexTurnStateDegradedRows().
			AddRow(int64(7), "acct-variant", "gpt-6-astra", "gpt-6-astra", "gpt-6-astra-2026-03-01", int64(40), now, now, 120.0).
			AddRow(int64(11), "acct-degraded", "gpt-6-astra", "gpt-6-astra", "gpt-5.6-luna", int64(14), now, now, 29400.0).
			AddRow(int64(3), "acct-grok", "grok-4.6", "grok-4.6", "grok-4.6-build", int64(9), now, now, nil))

	handler := NewCodexTurnStateDegradedHandler(db)
	handler.now = func() time.Time { return now }
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/admin/codex-turn-state/degraded-accounts?window=30m", nil)
	handler.Get(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Degraded []service.DegradedAccount `json:"degraded"`
			Error    string                    `json:"error"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Zero(t, payload.Code)
	require.Empty(t, payload.Data.Error)
	require.Len(t, payload.Data.Degraded, 1)
	require.Equal(t, int64(11), payload.Data.Degraded[0].AccountID)
	require.Equal(t, "gpt-5.6-luna", payload.Data.Degraded[0].ResponseModel)
	require.NotNil(t, payload.Data.Degraded[0].TTFTAvgMs)
	require.InDelta(t, 29400.0, *payload.Data.Degraded[0].TTFTAvgMs, 0.001)
	require.Equal(t, 30*time.Minute, end.Sub(start))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCodexTurnStateDegradedHandlerPreservesNullTTFTAndClampsWindow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 9, 18, 1, 30, 0, 0, time.UTC)
	var start, end time.Time
	mock.ExpectQuery("upstream_model_mismatch IS TRUE").
		WithArgs(codexTurnStateCapturedTime{&start}, codexTurnStateCapturedTime{&end}, domain.PlatformOpenAI, codexTurnStateDegradedAccountsScanLimit).
		WillReturnRows(codexTurnStateDegradedRows().
			AddRow(int64(11), "acct", "gpt-6-astra", "gpt-6-astra", "gpt-5.6-luna", int64(2), now, now, nil))

	handler := NewCodexTurnStateDegradedHandler(db)
	handler.now = func() time.Time { return now }
	degraded, err := handler.degradedAccounts(context.Background(), 99*time.Hour)
	require.NoError(t, err)
	require.Len(t, degraded, 1)
	require.Nil(t, degraded[0].TTFTAvgMs)
	require.Equal(t, 6*time.Hour, end.Sub(start))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCodexTurnStateDegradedHandlerKeepsQueryFence(t *testing.T) {
	require.Contains(t, codexTurnStateDegradedAccountsQuery, "AS MATERIALIZED")
	require.Contains(t, codexTurnStateDegradedAccountsQuery, "ul.upstream_model_mismatch IS TRUE")
}

func TestCodexTurnStateDegradedHandlerUsesStableBridgeErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("invalid window", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/admin/codex-turn-state/degraded-accounts?window=invalid", nil)
		NewCodexTurnStateDegradedHandler(nil).Get(context)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	t.Run("database error is sanitized", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		mock.ExpectQuery("upstream_model_mismatch IS TRUE").WillReturnError(errors.New("private database failure"))

		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/admin/codex-turn-state/degraded-accounts", nil)
		NewCodexTurnStateDegradedHandler(db).Get(context)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.JSONEq(t, `{"code":0,"message":"success","data":{"degraded":[],"error":"unavailable"}}`, recorder.Body.String())
		require.NotContains(t, recorder.Body.String(), "private database failure")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing database fails closed without leaking implementation", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/admin/codex-turn-state/degraded-accounts", nil)
		NewCodexTurnStateDegradedHandler(nil).Get(context)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.JSONEq(t, `{"code":0,"message":"success","data":{"degraded":[],"error":"unavailable"}}`, recorder.Body.String())
	})
}
