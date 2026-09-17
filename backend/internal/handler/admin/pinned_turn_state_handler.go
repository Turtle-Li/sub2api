package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type setPinnedCodexTurnStateRequest struct {
	Model     string                                       `json:"model"`
	State     string                                       `json:"state"`
	ExpiresAt *string                                      `json:"expires_at"`
	StateLen  int                                          `json:"state_len"`
	States    map[string]service.PinnedCodexTurnStateEntry `json:"states"`
}

// GetPinnedCodexTurnStates GET /api/v1/admin/accounts/:id/pinned-turn-states
func (h *AccountHandler) GetPinnedCodexTurnStates(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	states, err := h.adminService.GetPinnedCodexTurnStates(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if states == nil {
		states = make(map[string]service.PinnedCodexTurnStateEntry)
	}
	response.Success(c, states)
}

// SetPinnedCodexTurnStates PUT /api/v1/admin/accounts/:id/pinned-turn-states
func (h *AccountHandler) SetPinnedCodexTurnStates(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req setPinnedCodexTurnStateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// 批量形式
	if len(req.States) > 0 {
		updated, err := h.adminService.SetPinnedCodexTurnStates(c.Request.Context(), accountID, req.States)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, updated.GetPinnedCodexTurnStates())
		return
	}

	// 单模型形式
	model := strings.TrimSpace(req.Model)
	if model == "" {
		response.ErrorFrom(c, infraerrors.New(http.StatusBadRequest, "INVALID_MODEL", "model must not be empty"))
		return
	}
	state := strings.TrimSpace(req.State)
	if state == "" {
		response.ErrorFrom(c, infraerrors.New(http.StatusBadRequest, "INVALID_STATE", "state must not be empty"))
		return
	}

	entry := service.PinnedCodexTurnStateEntry{
		State:    state,
		StateLen: req.StateLen,
	}
	if entry.StateLen == 0 {
		entry.StateLen = len(state)
	}
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		s := strings.TrimSpace(*req.ExpiresAt)
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			entry.ExpiresAt = &t
		} else if t, err := time.Parse(time.RFC3339, s); err == nil {
			entry.ExpiresAt = &t
		} else {
			response.ErrorFrom(c, infraerrors.New(http.StatusBadRequest, "INVALID_EXPIRES_AT", "expires_at must be RFC3339 timestamp"))
			return
		}
	}

	updated, err := h.adminService.SetPinnedCodexTurnState(c.Request.Context(), accountID, model, entry)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated.GetPinnedCodexTurnStates())
}

// DeletePinnedCodexTurnState DELETE /api/v1/admin/accounts/:id/pinned-turn-states/:model
func (h *AccountHandler) DeletePinnedCodexTurnState(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	model := c.Param("model")
	updated, err := h.adminService.DeletePinnedCodexTurnState(c.Request.Context(), accountID, model)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated.GetPinnedCodexTurnStates())
}

// DeleteAllPinnedCodexTurnStates DELETE /api/v1/admin/accounts/:id/pinned-turn-states
func (h *AccountHandler) DeleteAllPinnedCodexTurnStates(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	updated, err := h.adminService.DeletePinnedCodexTurnState(c.Request.Context(), accountID, "all")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated.GetPinnedCodexTurnStates())
}
