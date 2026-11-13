package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	MuseVideoModel                                             = "muse-video"
	MuseVideoCreate                   GrokMediaEndpoint        = "muse_video_create"
	MuseVideoStatus                   GrokMediaEndpoint        = "muse_video_status"
	MuseVideoContent                  GrokMediaEndpoint        = "muse_video_content"
	OpenAIEndpointCapabilityMuseVideo OpenAIEndpointCapability = "muse_video"
	museVideoMaxBytes                                          = 100_000_000
)

var (
	museNativeTaskID  = regexp.MustCompile(`^task_[0-9a-f]{20}$`)
	museVideoFilename = regexp.MustCompile(`^[0-9a-f]{32}\.(mp4|webm|mov)$`)
	ErrMuseMediaModel = errors.New("Muse media model is not available on this endpoint")
)

func (e GrokMediaEndpoint) IsMuseVideo() bool {
	return e == MuseVideoCreate || e == MuseVideoStatus || e == MuseVideoContent
}

func IsMuseVideoTaskID(id string) bool {
	return strings.HasPrefix(id, "muse_") && museNativeTaskID.MatchString(strings.TrimPrefix(id, "muse_"))
}

func (s *OpenAIGatewayService) HasMuseVideoPrice(ctx context.Context, key *APIKey) bool {
	if key == nil || key.Group == nil {
		return false
	}
	key = s.apiKeyWithFreshGroupMediaPricing(ctx, key)
	price := key.Group.GetVideoPriceForModel(MuseVideoModel, VideoBillingResolution720P)
	return price != nil && *price > 0
}

type museNativeVideoTask struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Progress int      `json:"progress"`
	Kind     string   `json:"kind"`
	Created  int64    `json:"created_at"`
	Elapsed  *float64 `json:"elapsed"`
	Result   struct {
		Filename string `json:"filename"`
		Bytes    int64  `json:"bytes"`
	} `json:"result"`
}

func (t museNativeVideoTask) public(id string) gin.H {
	out := gin.H{"id": id, "task_id": id, "object": "video.task", "model": MuseVideoModel,
		"status": t.Status, "progress": min(100, max(0, t.Progress)), "created_at": t.Created}
	if t.Elapsed != nil {
		out["elapsed"] = *t.Elapsed
	}
	return out
}

// ForwardMuseVideo calls the configured account only. A create error never
// triggers failover: the native service may already have accepted the task.
func (s *OpenAIGatewayService) ForwardMuseVideo(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, id string, body []byte) (*OpenAIForwardResult, gin.H, error) {
	if !endpoint.IsMuseVideo() || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseVideo) {
		return nil, nil, errors.New("Muse video account is not configured")
	}
	base, proxy, token, err := s.museUpstream(ctx, account)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), 120*time.Second)
	defer cancel()
	started := time.Now()
	target := base + "/videos"
	if endpoint.IsVideoLookupRequest() {
		if !IsMuseVideoTaskID(id) {
			return nil, nil, errors.New("invalid Muse video task ID")
		}
		target += "/" + strings.TrimPrefix(id, "muse_")
	}
	resp, err := s.doMuseRequest(ctx, account, endpoint.httpMethod(), target, proxy, token, "", body)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, museUpstreamError(c, resp.StatusCode, endpoint.IsVideoLookupRequest())
	}
	raw, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, nil, fmt.Errorf("read Muse video task: %w", err)
	}
	var task museNativeVideoTask
	if err := json.Unmarshal(raw, &task); err != nil || !museNativeTaskID.MatchString(task.ID) ||
		(task.Kind != "" && task.Kind != "video") || !validMuseTaskStatus(task.Status) {
		return nil, nil, errors.New("invalid Muse video task response")
	}
	publicID := "muse_" + task.ID
	result := &OpenAIForwardResult{
		ResponseID: publicID, Model: MuseVideoModel, BillingModel: MuseVideoModel,
		UpstreamModel: MuseVideoModel, Duration: time.Since(started),
	}
	if endpoint == MuseVideoCreate {
		return result, task.public(publicID), nil
	}
	if publicID != id {
		return nil, nil, errors.New("Muse video task ID did not match")
	}
	completed := task.Status == "completed"
	if completed && !museVideoFilename.MatchString(task.Result.Filename) {
		return nil, nil, errors.New("invalid Muse video output filename")
	}
	c.Header("Cache-Control", "private, no-store")
	if endpoint == MuseVideoContent {
		if !completed {
			writeGrokMediaErrorResponse(c, http.StatusConflict, "invalid_request_error", "Video content is not ready")
			return nil, nil, errors.New("Muse video output is not ready")
		}
		if err := s.writeMuseVideoContent(ctx, c, account, base, proxy, task.Result.Filename); err != nil {
			return nil, nil, err
		}
	} else {
		out := task.public(id)
		if completed {
			content := grokMediaContentProxyURL(c, id)
			out["url"] = content
			out["video"] = gin.H{"url": content}
			out["result"] = gin.H{"url": content, "kind": "video", "bytes": task.Result.Bytes}
		} else if task.Status == "failed" {
			out["error"] = gin.H{"code": "generation_failed", "message": "Muse could not generate this video"}
		}
		c.JSON(http.StatusOK, out)
	}
	if completed {
		result.VideoCount = 1
	}
	return result, nil, nil
}

func (s *OpenAIGatewayService) museUpstream(ctx context.Context, account *Account) (base, proxy, token string, err error) {
	base, err = s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return "", "", "", errors.New("invalid Muse account base URL")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", "", errors.New("invalid Muse account base URL")
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/v1"
	proxy, err = ResolveAccountProxyURL(account)
	if err != nil {
		return "", "", "", errors.New("Muse account proxy is unavailable")
	}
	token, _, err = s.GetAccessToken(ctx, account)
	if err != nil {
		return "", "", "", errors.New("Muse account credential is unavailable")
	}
	return base, proxy, token, nil
}

func (s *OpenAIGatewayService) doMuseRequest(ctx context.Context, account *Account, method, target, proxy, token, idempotencyKey string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("cannot construct Muse media request")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if method == http.MethodPost {
		// net/http can replay a request with an Idempotency-Key on a stale
		// connection when GetBody is set. Leave ambiguous attempts to callers.
		req.GetBody = nil
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return nil, errors.New("Muse media upstream connection failed")
	}
	return resp, nil
}

func validMuseTaskStatus(status string) bool {
	return status == "queued" || status == "processing" || status == "failed" || status == "completed"
}

func museUpstreamError(c *gin.Context, status int, lookup bool) error {
	switch {
	case status == http.StatusNotFound && lookup:
		writeGrokMediaErrorResponse(c, status, "not_found_error", "Muse task not found")
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		writeGrokMediaErrorResponse(c, status, "invalid_request_error", "Muse rejected the media parameters or references; check format, size and reference video duration")
	case status == http.StatusConflict:
		writeGrokMediaErrorResponse(c, status, "invalid_request_error", "Muse Idempotency-Key was reused with a different request")
	case status == http.StatusTooManyRequests:
		writeGrokMediaErrorResponse(c, status, "rate_limit_error", "Muse is temporarily rate limited")
	}
	return fmt.Errorf("Muse upstream returned HTTP %d", status)
}

func (s *OpenAIGatewayService) writeMuseVideoContent(ctx context.Context, c *gin.Context, account *Account, base, proxy, filename string) error {
	// Ignore provider URLs. Build the native path at the validated account origin.
	resp, err := s.doMuseRequest(ctx, account, http.MethodGet, base+"/media/"+filename, proxy, "", "", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.ContentLength < 12 || resp.ContentLength > museVideoMaxBytes {
		return errors.New("Muse video output is unavailable or exceeds the size limit")
	}
	reader := bufio.NewReader(resp.Body)
	header, err := reader.Peek(12)
	mediaType := museReferenceTypes[filename[strings.LastIndexByte(filename, '.'):]]
	detected := sniffMuseMedia(header)
	if err != nil || (detected != mediaType && !(mediaType == "video/quicktime" && detected == "video/mp4")) {
		return errors.New("Muse output does not match its video format")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", mediaType)
	c.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	c.Status(http.StatusOK)
	MarkResponseCommitted(c)
	if _, err := io.CopyN(c.Writer, reader, resp.ContentLength); err != nil {
		return fmt.Errorf("copy Muse video content: %w", err)
	}
	return nil
}
