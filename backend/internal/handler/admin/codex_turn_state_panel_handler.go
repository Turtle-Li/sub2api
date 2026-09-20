package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// CodexTurnStatePanelHandler is an authenticated adapter to the existing
// manager. Scheduling, probing, backoff and persistence remain in that tool.
type CodexTurnStatePanelHandler struct {
	target    *url.URL
	client    *http.Client
	tokenFile string
}

func NewCodexTurnStatePanelHandler() *CodexTurnStatePanelHandler {
	raw := strings.TrimSpace(os.Getenv("CODEX_TURN_STATE_PANEL_URL"))
	if raw == "" {
		raw = "http://127.0.0.1:8787"
	}
	target, err := url.Parse(raw)
	if err != nil || !validCodexPanelTarget(target) {
		target = nil
	}
	return &CodexTurnStatePanelHandler{target: target, tokenFile: strings.TrimSpace(os.Getenv("CODEX_TURN_STATE_PANEL_TOKEN_FILE")), client: &http.Client{
		Timeout: 18 * time.Second,
		// This private local service must not inherit an Internet proxy or redirects.
		Transport:     &http.Transport{Proxy: nil, ResponseHeaderTimeout: 15 * time.Second, MaxIdleConnsPerHost: 4},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

const codexPanelTokenMaxBytes = 4096

var errInvalidCodexPanelToken = errors.New("invalid Codex panel token")

func validCodexPanelTarget(target *url.URL) bool {
	return target != nil && target.Host != "" && target.User == nil && (target.Scheme == "http" || target.Scheme == "https") && target.RawQuery == "" && target.Fragment == "" && (target.Path == "" || target.Path == "/")
}

// A configured bearer token can only be sent to a literal local/private IPv4
// endpoint or localhost. Do not resolve arbitrary host names here.
func isCodexPanelTokenTarget(target *url.URL) bool {
	if !validCodexPanelTarget(target) {
		return false
	}
	host := target.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func readCodexPanelToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errInvalidCodexPanelToken
	}
	defer file.Close()

	contents, err := io.ReadAll(io.LimitReader(file, codexPanelTokenMaxBytes+1))
	if err != nil || len(contents) > codexPanelTokenMaxBytes {
		return "", errInvalidCodexPanelToken
	}
	token := strings.TrimSpace(string(contents))
	if token == "" || len(token) > codexPanelTokenMaxBytes || strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return "", errInvalidCodexPanelToken
	}
	return token, nil
}

func (h *CodexTurnStatePanelHandler) doWithoutRedirect(req *http.Request) (*http.Response, error) {
	if h.client == nil {
		return nil, errors.New("Codex panel client is unavailable")
	}
	client := *h.client
	client.Jar = nil
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(req)
}

var codexPanelModelPath = regexp.MustCompile(`^/api/accounts/[1-9][0-9]*/[A-Za-z0-9_.:@-]{1,128}$`)
var codexPanelJobPath = regexp.MustCompile(`^/api/jobs/[A-Za-z0-9_-]{1,128}$`)
var codexPanelSourcePath = regexp.MustCompile(`^/api/proxy-sources/[A-Za-z0-9_-]{1,128}$`)
var codexPanelSourceEnabledPath = regexp.MustCompile(`^/api/proxy-sources/[a-f0-9]{32}/enabled$`)

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
		return path == "/api/probe" || path == "/api/accounts" || path == "/api/proxy-sources" || codexPanelSourceEnabledPath.MatchString(path)
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
	if h.target == nil || !validCodexPanelTarget(h.target) {
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

	var token string
	if h.tokenFile != "" {
		if !isCodexPanelTokenTarget(h.target) {
			response.Error(c, http.StatusServiceUnavailable, "Invalid panel service configuration")
			return
		}
		var err error
		token, err = readCodexPanelToken(h.tokenFile)
		if err != nil {
			response.Error(c, http.StatusServiceUnavailable, "Codex panel service unavailable; check the local manager service")
			return
		}
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Panel service unavailable")
		return
	}
	// Never send browser authorization, cookies or Origin to the private daemon.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CTSM-Panel", "1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	upstream, err := h.doWithoutRedirect(req)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Codex panel service unavailable; check the local manager service")
		return
	}
	defer upstream.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(upstream.Body, 2*1024*1024+1))
	if err != nil || len(payload) > 2*1024*1024 || !json.Valid(payload) {
		response.Error(c, http.StatusBadGateway, "Invalid panel service response")
		return
	}
	c.Header("Cache-Control", "no-store")
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		status := upstream.StatusCode
		if status != http.StatusBadRequest && status != http.StatusForbidden && status != http.StatusNotFound && status != http.StatusConflict && status != http.StatusTooManyRequests {
			status = http.StatusBadGateway
		}
		response.Error(c, status, "Panel operation failed; check the manager configuration and retry")
		return
	}
	c.JSON(upstream.StatusCode, gin.H{"code": 0, "message": "success", "data": json.RawMessage(payload)})
}
