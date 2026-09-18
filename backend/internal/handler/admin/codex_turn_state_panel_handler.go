package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// CodexTurnStatePanelHandler is an authenticated adapter to the existing
// manager. Scheduling, probing, backoff and persistence remain in that tool.
type CodexTurnStatePanelHandler struct {
	target   *url.URL
	client   *http.Client
	degraded *CodexTurnStateDegradedHandler
}

func NewCodexTurnStatePanelHandler(degraded *CodexTurnStateDegradedHandler) *CodexTurnStatePanelHandler {
	raw := strings.TrimSpace(os.Getenv("CODEX_TURN_STATE_PANEL_URL"))
	if raw == "" {
		raw = "http://127.0.0.1:8787"
	}
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		target = nil
	}
	return &CodexTurnStatePanelHandler{target: target, degraded: degraded, client: &http.Client{
		Timeout: 18 * time.Second,
		// This private local service must not inherit an Internet proxy or redirects.
		Transport:     &http.Transport{Proxy: nil, ResponseHeaderTimeout: 15 * time.Second, MaxIdleConnsPerHost: 4},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

var codexPanelModelPath = regexp.MustCompile(`^/api/accounts/[1-9][0-9]*/[A-Za-z0-9_.:@-]{1,128}$`)
var codexPanelJobPath = regexp.MustCompile(`^/api/jobs/[A-Za-z0-9_-]{1,128}$`)
var codexPanelSourcePath = regexp.MustCompile(`^/api/proxy-sources/[A-Za-z0-9_-]{1,128}$`)

func allowedCodexPanelPath(method, path string) bool {
	if strings.Contains(path, "..") {
		return false
	}
	switch method {
	case http.MethodGet:
		switch path {
		case "/api/state", "/api/degraded", "/api/stats", "/api/history", "/api/jobs", "/api/proxy-sources":
			return true
		}
		return codexPanelJobPath.MatchString(path)
	case http.MethodPost:
		return path == "/api/probe" || path == "/api/accounts" || path == "/api/proxy-sources"
	case http.MethodDelete:
		return codexPanelModelPath.MatchString(path) || codexPanelSourcePath.MatchString(path)
	}
	return false
}

func (h *CodexTurnStatePanelHandler) Proxy(c *gin.Context) {
	path := c.Param("path")
	if !allowedCodexPanelPath(c.Request.Method, path) {
		response.NotFound(c, "Panel route not found")
		return
	}
	if path == "/api/degraded" && h.degraded != nil {
		h.degraded.Get(c)
		return
	}
	if h.target == nil {
		response.Error(c, http.StatusServiceUnavailable, "Invalid panel service configuration")
		return
	}
	var body []byte
	if c.Request.Method == http.MethodPost {
		var err error
		body, err = io.ReadAll(io.LimitReader(c.Request.Body, 64*1024+1))
		if err != nil || len(body) > 64*1024 || !json.Valid(body) {
			response.BadRequest(c, "Invalid panel request")
			return
		}
	}
	target := *h.target
	target.Path = path
	query := url.Values{}
	for _, key := range []string{"force", "start_day", "end_day"} {
		if value := c.Query(key); value != "" {
			if len(value) > 32 {
				response.BadRequest(c, "Invalid panel query")
				return
			}
			query.Set(key, value)
		}
	}
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		response.Error(c, 503, "Panel service unavailable")
		return
	}
	// Never send browser authorization, cookies or Origin to the private daemon.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CTSM-Panel", "1")
	upstream, err := h.client.Do(req)
	if err != nil {
		response.Error(c, 503, "Codex panel service unavailable; check the local manager service")
		return
	}
	defer upstream.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(upstream.Body, 2*1024*1024+1))
	if err != nil || len(payload) > 2*1024*1024 || !json.Valid(payload) {
		response.Error(c, 502, "Invalid panel service response")
		return
	}
	c.Header("Cache-Control", "no-store")
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		status := upstream.StatusCode
		if status != 400 && status != 403 && status != 404 && status != 409 && status != 429 {
			status = 502
		}
		response.Error(c, status, "Panel operation failed; check the manager configuration and retry")
		return
	}
	if path == "/api/state" && h.degraded != nil {
		payload = h.enrichSnapshot(c.Request.Context(), payload)
	}
	c.JSON(upstream.StatusCode, gin.H{"code": 0, "message": "success", "data": json.RawMessage(payload)})
}

// Adapt the legacy snapshot report to this project's authenticated database feed.
// Verdict and latest-per-slot selection match manager.StateManager.snapshot.
func (h *CodexTurnStatePanelHandler) enrichSnapshot(ctx context.Context, payload []byte) []byte {
	var state map[string]any
	if json.Unmarshal(payload, &state) != nil || state == nil {
		return payload
	}
	rows, err := h.degraded.degradedAccounts(ctx, service.DegradedAccountsDefaultWindow)
	if rows == nil {
		rows = []service.DegradedAccount{}
	}
	state["degraded_enabled"] = true
	state["degraded_window"] = "30m"
	state["degraded"] = rows
	state["degraded_error"] = ""
	if err != nil {
		state["degraded_error"] = "unavailable"
	}
	slots := map[string]service.DegradedAccount{}
	for _, row := range rows {
		key := fmt.Sprintf("%d:%s", row.AccountID, strings.ToLower(row.SentModel))
		previous, exists := slots[key]
		if !exists || row.LastSeen.After(previous.LastSeen) {
			slots[key] = row
		}
	}
	accounts, _ := state["accounts"].([]any)
	for _, value := range accounts {
		account, ok := value.(map[string]any)
		if !ok {
			continue
		}
		id, _ := account["id"].(float64)
		models, _ := account["models"].([]any)
		for _, value := range models {
			model, ok := value.(map[string]any)
			if !ok {
				continue
			}
			name, _ := model["model"].(string)
			row, exists := slots[fmt.Sprintf("%d:%s", int64(id), strings.ToLower(name))]
			model["recent_degradation"] = nil
			verdict := "no_record"
			if exists {
				model["recent_degradation"] = row
				updated, _ := model["pinned_updated_at"].(string)
				pinned, parseErr := time.Parse(time.RFC3339Nano, updated)
				verdict = "unknown"
				if parseErr == nil {
					verdict = "handled"
					if row.LastSeen.After(pinned) {
						verdict = "degraded"
					}
				}
			}
			if err != nil {
				verdict = "unknown"
			}
			model["degradation"] = verdict
		}
	}
	encoded, encodeErr := json.Marshal(state)
	if encodeErr != nil {
		return payload
	}
	return encoded
}
