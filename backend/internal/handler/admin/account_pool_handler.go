package admin

import (
	"strconv"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AccountPoolHandler manages display-only account pools.
// Groups/scheduling stay bound to member accounts; pool-level group edits are
// done by the frontend through the existing bulk-update endpoint with filters.pool.
type AccountPoolHandler struct {
	poolService *service.AccountPoolService
}

func NewAccountPoolHandler(poolService *service.AccountPoolService) *AccountPoolHandler {
	return &AccountPoolHandler{poolService: poolService}
}

type accountPoolResponse struct {
	ID        int64                     `json:"id"`
	Name      string                    `json:"name"`
	Platform  string                    `json:"platform"`
	Notes     *string                   `json:"notes"`
	Stats     service.AccountPoolStats  `json:"stats"`
	Usage     *service.AccountPoolUsage `json:"usage"`
	CreatedAt time.Time                 `json:"created_at"`
	UpdatedAt time.Time                 `json:"updated_at"`
}

type createAccountPoolRequest struct {
	Name     string  `json:"name" binding:"required"`
	Platform string  `json:"platform" binding:"required"`
	Notes    *string `json:"notes"`
}

type updateAccountPoolRequest struct {
	Name  *string `json:"name"`
	Notes *string `json:"notes"`
}

type accountPoolMembersRequest struct {
	AccountIDs []int64 `json:"account_ids" binding:"required"`
}

func toAccountPoolResponse(summary service.AccountPoolSummary) accountPoolResponse {
	return accountPoolResponse{
		ID:        summary.Pool.ID,
		Name:      summary.Pool.Name,
		Platform:  summary.Pool.Platform,
		Notes:     summary.Pool.Notes,
		Stats:     summary.Stats,
		Usage:     summary.Usage,
		CreatedAt: summary.Pool.CreatedAt,
		UpdatedAt: summary.Pool.UpdatedAt,
	}
}

func parseAccountPoolID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_ACCOUNT_POOL_ID", "invalid account pool id"))
		return 0, false
	}
	return id, true
}

// List returns pools with member stats and cached rolling usage.
// GET /api/v1/admin/account-pools?platform=
func (h *AccountPoolHandler) List(c *gin.Context) {
	summaries, err := h.poolService.List(c.Request.Context(), c.Query("platform"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]accountPoolResponse, len(summaries))
	for i := range summaries {
		out[i] = toAccountPoolResponse(summaries[i])
	}
	response.Success(c, out)
}

// Get returns one pool with its stats.
// GET /api/v1/admin/account-pools/:id
func (h *AccountPoolHandler) Get(c *gin.Context) {
	id, ok := parseAccountPoolID(c)
	if !ok {
		return
	}
	summary, err := h.poolService.Get(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toAccountPoolResponse(*summary))
}

// Create creates an empty pool.
// POST /api/v1/admin/account-pools
func (h *AccountPoolHandler) Create(c *gin.Context) {
	var req createAccountPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pool, err := h.poolService.Create(c.Request.Context(), req.Name, req.Platform, req.Notes)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toAccountPoolResponse(service.AccountPoolSummary{Pool: *pool}))
}

// Update edits pool name/notes.
// PUT /api/v1/admin/account-pools/:id
func (h *AccountPoolHandler) Update(c *gin.Context) {
	id, ok := parseAccountPoolID(c)
	if !ok {
		return
	}
	var req updateAccountPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if _, err := h.poolService.Update(c.Request.Context(), id, req.Name, req.Notes); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	summary, err := h.poolService.Get(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toAccountPoolResponse(*summary))
}

// Delete dissolves the pool; member accounts are kept and released.
// DELETE /api/v1/admin/account-pools/:id
func (h *AccountPoolHandler) Delete(c *gin.Context) {
	id, ok := parseAccountPoolID(c)
	if !ok {
		return
	}
	if err := h.poolService.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// AddMembers moves accounts into the pool (from no pool or another pool).
// POST /api/v1/admin/account-pools/:id/members
func (h *AccountPoolHandler) AddMembers(c *gin.Context) {
	id, ok := parseAccountPoolID(c)
	if !ok {
		return
	}
	var req accountPoolMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	affected, err := h.poolService.AssignAccounts(c.Request.Context(), id, req.AccountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"affected": affected})
}

// RemoveMembers releases accounts from whatever pool they are in.
// POST /api/v1/admin/account-pools/members/remove
func (h *AccountPoolHandler) RemoveMembers(c *gin.Context) {
	var req accountPoolMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	affected, err := h.poolService.ReleaseAccounts(c.Request.Context(), req.AccountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"affected": affected})
}

// ListAccountIDs returns member ids, optionally filtered by list status
// (used for "purge error accounts" and "delete pool with members").
// GET /api/v1/admin/account-pools/:id/account-ids?status=
func (h *AccountPoolHandler) ListAccountIDs(c *gin.Context) {
	id, ok := parseAccountPoolID(c)
	if !ok {
		return
	}
	ids, err := h.poolService.ListAccountIDs(c.Request.Context(), id, c.Query("status"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"account_ids": ids})
}
