package admin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestCodexTurnStateSnapshotUsesLatestObservationAndPinTime(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Date(2026, 9, 18, 1, 30, 0, 0, time.UTC)
	mock.ExpectQuery("upstream_model_mismatch IS TRUE").WillReturnRows(codexTurnStateDegradedRows().
		AddRow(int64(7), "sample", "gpt-x", "gpt-x", "gpt-y", int64(4), now, now.Add(-time.Hour), nil).
		AddRow(int64(7), "sample", "gpt-x", "gpt-x", "gpt-z", int64(1), now, now, nil))
	h := &CodexTurnStatePanelHandler{degraded: NewCodexTurnStateDegradedHandler(db)}
	result := h.enrichSnapshot(context.Background(), []byte(`{"accounts":[{"id":7,"models":[{"model":"GPT-X","pinned_updated_at":"2026-09-18T01:00:00Z"},{"model":"gpt-q"}]}]}`))
	var state map[string]any
	require.NoError(t, json.Unmarshal(result, &state))
	models := state["accounts"].([]any)[0].(map[string]any)["models"].([]any)
	require.Equal(t, "degraded", models[0].(map[string]any)["degradation"])
	require.Equal(t, "no_record", models[1].(map[string]any)["degradation"])
	require.Equal(t, true, state["degraded_enabled"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCodexTurnStateSnapshotQueryFailureIsUnknown(t *testing.T) {
	h := &CodexTurnStatePanelHandler{degraded: NewCodexTurnStateDegradedHandler(nil)}
	result := h.enrichSnapshot(context.Background(), []byte(`{"accounts":[{"id":7,"models":[{"model":"gpt-x"}]}]}`))
	require.Contains(t, string(result), `"degradation":"unknown"`)
	require.Contains(t, string(result), `"degraded_error":"unavailable"`)
	require.Equal(t, "null", string(h.enrichSnapshot(context.Background(), []byte(`null`))))
}
