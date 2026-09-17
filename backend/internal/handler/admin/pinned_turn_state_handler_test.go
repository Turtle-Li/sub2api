package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupPinnedTurnStateRouter(stub *stubAdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/admin/accounts/:id/pinned-turn-states", handler.GetPinnedCodexTurnStates)
	router.PUT("/admin/accounts/:id/pinned-turn-states", handler.SetPinnedCodexTurnStates)
	router.DELETE("/admin/accounts/:id/pinned-turn-states/:model", handler.DeletePinnedCodexTurnState)
	router.DELETE("/admin/accounts/:id/pinned-turn-states", handler.DeleteAllPinnedCodexTurnStates)
	return router
}

func TestPinnedCodexTurnStateHandlers(t *testing.T) {
	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	stub := newStubAdminService()
	stub.accounts = []service.Account{
		{
			ID: 11,
			Extra: map[string]any{
				service.PinnedCodexTurnStatesExtraKey: map[string]any{
					"gpt-6-astra": map[string]any{
						"state":      "gAAAAAB_init_292",
						"state_len":  292,
						"expires_at": future,
					},
				},
			},
		},
	}
	router := setupPinnedTurnStateRouter(stub)

	t.Run("GET /admin/accounts/:id/pinned-turn-states returns states", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin/accounts/11/pinned-turn-states", nil)
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Code int                                          `json:"code"`
			Data map[string]service.PinnedCodexTurnStateEntry `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, 0, resp.Code)
		require.Contains(t, resp.Data, "gpt-6-astra")
		require.Equal(t, "gAAAAAB_init_292", resp.Data["gpt-6-astra"].State)
		require.Equal(t, 292, resp.Data["gpt-6-astra"].StateLen)
	})

	t.Run("PUT /admin/accounts/:id/pinned-turn-states sets single model", func(t *testing.T) {
		body := map[string]any{
			"model":      "gpt-6-astra",
			"state":      "gAAAAAB_new_292",
			"expires_at": future,
			"state_len":  292,
		}
		jsonBytes, _ := json.Marshal(body)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/admin/accounts/11/pinned-turn-states", bytes.NewReader(jsonBytes))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Code int                                          `json:"code"`
			Data map[string]service.PinnedCodexTurnStateEntry `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, 0, resp.Code)
		require.Equal(t, "gAAAAAB_new_292", resp.Data["gpt-6-astra"].State)
	})

	t.Run("PUT /admin/accounts/:id/pinned-turn-states sets batch models", func(t *testing.T) {
		body := map[string]any{
			"states": map[string]any{
				"gpt-6-astra": map[string]any{
					"state":      "gAAAAAB_batch_292",
					"state_len":  292,
					"expires_at": future,
				},
				"gpt-5.6-sol": map[string]any{
					"state":      "gAAAAAB_sol_312",
					"state_len":  312,
					"expires_at": future,
				},
			},
		}
		jsonBytes, _ := json.Marshal(body)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/admin/accounts/11/pinned-turn-states", bytes.NewReader(jsonBytes))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Code int                                          `json:"code"`
			Data map[string]service.PinnedCodexTurnStateEntry `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, 0, resp.Code)
		require.Len(t, resp.Data, 2)
		require.Equal(t, "gAAAAAB_batch_292", resp.Data["gpt-6-astra"].State)
		require.Equal(t, "gAAAAAB_sol_312", resp.Data["gpt-5.6-sol"].State)
	})

	t.Run("DELETE /admin/accounts/:id/pinned-turn-states/:model deletes single", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/admin/accounts/11/pinned-turn-states/gpt-5.6-sol", nil)
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("DELETE /admin/accounts/:id/pinned-turn-states deletes all", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/admin/accounts/11/pinned-turn-states", nil)
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("Invalid account ID returns 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin/accounts/invalid/pinned-turn-states", nil)
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("Empty model returns 400", func(t *testing.T) {
		body := map[string]any{
			"state": "gAAAAAB_test",
		}
		jsonBytes, _ := json.Marshal(body)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/admin/accounts/11/pinned-turn-states", bytes.NewReader(jsonBytes))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}
