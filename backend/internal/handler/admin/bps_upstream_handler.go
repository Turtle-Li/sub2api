package admin

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type bpsUpstreamConfigStore interface {
	GetOpenAIBPSUpstreamConfig(ctx context.Context) service.OpenAIBPSUpstreamConfig
	UpdateOpenAIBPSUpstreamConfig(ctx context.Context, cfg service.OpenAIBPSUpstreamConfig) (service.OpenAIBPSUpstreamConfig, error)
}

type bpsUpstreamAccountLookup interface {
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]*service.Account, error)
}

// BPSUpstreamHandler 管理 BPS 上游（降智修复）的账号名单与运行监控。
type BPSUpstreamHandler struct {
	configStore bpsUpstreamConfigStore
	accounts    bpsUpstreamAccountLookup
	snapshot    func() service.BPSMonitorSnapshot
	reset       func(accountID int64)
}

func NewBPSUpstreamHandler(settingService *service.SettingService, adminService service.AdminService) *BPSUpstreamHandler {
	return &BPSUpstreamHandler{
		configStore: settingService,
		accounts:    adminService,
		snapshot:    service.BPSUpstreamMonitorSnapshot,
		reset:       service.ResetBPSUpstreamBreaker,
	}
}

type bpsUpstreamAccountView struct {
	ID          int64                    `json:"id"`
	Name        string                   `json:"name"`
	Platform    string                   `json:"platform"`
	Type        string                   `json:"type"`
	Status      string                   `json:"status"`
	Schedulable bool                     `json:"schedulable"`
	Eligible    bool                     `json:"eligible"`
	Missing     bool                     `json:"missing"`
	Stats       *service.BPSAccountStats `json:"stats,omitempty"`
}

type bpsUpstreamOverview struct {
	Config    service.OpenAIBPSUpstreamConfig `json:"config"`
	Policy    service.BPSUpstreamPolicy       `json:"policy"`
	Accounts  []bpsUpstreamAccountView        `json:"accounts"`
	Unlisted  []service.BPSAccountStats       `json:"unlisted_stats"`
	Events    []service.BPSEvent              `json:"events"`
	StartedAt time.Time                       `json:"monitor_started_at"`
	Now       time.Time                       `json:"now"`
}

func bpsEligibleAccount(account *service.Account) bool {
	return account != nil && account.Platform == service.PlatformOpenAI && account.IsOAuth()
}

// GetOverview GET /api/v1/admin/bps-upstream
func (h *BPSUpstreamHandler) GetOverview(c *gin.Context) {
	ctx := c.Request.Context()
	cfg := h.configStore.GetOpenAIBPSUpstreamConfig(ctx)
	snapshot := h.snapshot()

	statsByID := make(map[int64]service.BPSAccountStats, len(snapshot.Accounts))
	for _, stats := range snapshot.Accounts {
		statsByID[stats.AccountID] = stats
	}
	found := map[int64]*service.Account{}
	if len(cfg.AccountIDs) > 0 {
		accounts, err := h.accounts.GetAccountsByIDs(ctx, cfg.AccountIDs)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		for _, account := range accounts {
			if account != nil {
				found[account.ID] = account
			}
		}
	}

	views := make([]bpsUpstreamAccountView, 0, len(cfg.AccountIDs))
	listed := make(map[int64]struct{}, len(cfg.AccountIDs))
	for _, id := range cfg.AccountIDs {
		listed[id] = struct{}{}
		view := bpsUpstreamAccountView{ID: id, Missing: true}
		if account := found[id]; account != nil {
			view = bpsUpstreamAccountView{
				ID:          id,
				Name:        account.Name,
				Platform:    account.Platform,
				Type:        account.Type,
				Status:      account.Status,
				Schedulable: account.Schedulable,
				Eligible:    bpsEligibleAccount(account),
			}
		}
		if stats, ok := statsByID[id]; ok {
			view.Stats = &stats
		}
		views = append(views, view)
	}
	// 从名单移除但本进程内仍有统计的账号单独展示，避免数据凭空消失。
	unlisted := make([]service.BPSAccountStats, 0)
	for _, stats := range snapshot.Accounts {
		if _, ok := listed[stats.AccountID]; !ok {
			unlisted = append(unlisted, stats)
		}
	}

	response.Success(c, bpsUpstreamOverview{
		Config:    cfg,
		Policy:    service.BPSUpstreamPolicyInfo(),
		Accounts:  views,
		Unlisted:  unlisted,
		Events:    snapshot.Events,
		StartedAt: snapshot.StartedAt,
		Now:       time.Now(),
	})
}

const bpsUpstreamMaxAccounts = 1000

type updateBPSUpstreamConfigRequest struct {
	Enabled    bool    `json:"enabled"`
	AccountIDs []int64 `json:"account_ids"`
}

// UpdateConfig PUT /api/v1/admin/bps-upstream/config
func (h *BPSUpstreamHandler) UpdateConfig(c *gin.Context) {
	var req updateBPSUpstreamConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.AccountIDs) > bpsUpstreamMaxAccounts {
		response.BadRequest(c, fmt.Sprintf("too many accounts (max %d)", bpsUpstreamMaxAccounts))
		return
	}
	ctx := c.Request.Context()
	current := h.configStore.GetOpenAIBPSUpstreamConfig(ctx)
	existing := make(map[int64]struct{}, len(current.AccountIDs))
	for _, id := range current.AccountIDs {
		existing[id] = struct{}{}
	}

	// 只校验新加入的账号；已在名单中的账号即使被删除也允许原样保留，便于随后移除。
	added := make([]int64, 0)
	for _, id := range req.AccountIDs {
		if id <= 0 {
			response.BadRequest(c, fmt.Sprintf("invalid account id: %d", id))
			return
		}
		if _, ok := existing[id]; !ok {
			added = append(added, id)
		}
	}
	if len(added) > 0 {
		accounts, err := h.accounts.GetAccountsByIDs(ctx, added)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		eligible := make(map[int64]bool, len(accounts))
		for _, account := range accounts {
			if account != nil {
				eligible[account.ID] = bpsEligibleAccount(account)
			}
		}
		for _, id := range added {
			ok, exists := eligible[id]
			if !exists {
				response.BadRequest(c, fmt.Sprintf("account %d not found", id))
				return
			}
			if !ok {
				response.BadRequest(c, fmt.Sprintf("account %d is not an OpenAI OAuth account", id))
				return
			}
		}
	}

	saved, err := h.configStore.UpdateOpenAIBPSUpstreamConfig(ctx, service.OpenAIBPSUpstreamConfig{Enabled: req.Enabled, AccountIDs: req.AccountIDs})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}

// ResetBreaker POST /api/v1/admin/bps-upstream/accounts/:id/reset-breaker
func (h *BPSUpstreamHandler) ResetBreaker(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	h.reset(id)
	response.Success(c, gin.H{"account_id": id})
}
