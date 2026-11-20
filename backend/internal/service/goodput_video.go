package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"regexp"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	goodPutVideoTaskPrefix = "gpv_"
	videoDownloadMaxBytes  = 100_000_000
)

var (
	errGoodPutVideoNotDispatched = errors.New("GoodPut video request was not dispatched")
	goodPutNativeVideoID         = regexp.MustCompile(`^video_[A-Za-z0-9_.~=-]{1,768}$`)
	goodPutPublicVideoID         = regexp.MustCompile(`^gpv_[0-9a-f]{32}$`)
)

func IsGoodPutVideoTaskID(id string) bool {
	return goodPutPublicVideoID.MatchString(id)
}

func (s *OpenAIGatewayService) HasGoodPutVideoPrice(ctx context.Context, key *APIKey, model string) bool {
	return s.hasGoodPutVideoPrice(ctx, s.apiKeyWithFreshGroupMediaPricing(ctx, key), model)
}

func (s *OpenAIGatewayService) hasGoodPutVideoPrice(ctx context.Context, key *APIKey, model string) bool {
	if key == nil || !IsGoodPutVideoGroup(key.Group) {
		return false
	}
	if resolved := s.resolveOpenAIChannelPricing(ctx, model, key); resolved != nil {
		if resolved.Mode != BillingModeVideo {
			return false
		}
		if resolved.Source == PricingSourceGroup {
			if !slices.Contains(resolved.channelPricing.Models, model) {
				return false
			}
			// A group video card takes precedence over legacy prices at settlement.
			quote, err := s.billingService.CalculateCostUnified(CostInput{
				Ctx: ctx, Model: model, GroupID: key.GroupID, Group: key.Group,
				UsageUnits: 1, SizeTier: VideoBillingResolution720P, RateMultiplier: 1,
				Resolver: s.resolver, Resolved: resolved,
			})
			return err == nil && quote.TotalCost > 0 && !math.IsInf(quote.TotalCost, 0)
		}
	}
	price := key.Group.GetVideoPriceForModel(model, VideoBillingResolution720P)
	return price != nil && *price > 0 && !math.IsInf(*price, 0)
}

type GoodPutVideoError struct {
	Status             int
	Code               string
	Message            string
	submissionRejected bool
}

func (e *GoodPutVideoError) Error() string { return e.Message }

type GoodPutVideoTask struct {
	nativeID string
	state    string
	seconds  int
	progress int
	created  int64
}

func (t GoodPutVideoTask) ID() string {
	digest := sha256.Sum256([]byte(t.nativeID))
	return goodPutVideoTaskPrefix + hex.EncodeToString(digest[:16])
}

func (t GoodPutVideoTask) NativeID() string { return t.nativeID }
func (t GoodPutVideoTask) Completed() bool  { return t.state == "completed" }
func (t GoodPutVideoTask) Seconds() int     { return t.seconds }

func (t GoodPutVideoTask) Public(model string) gin.H {
	out := gin.H{"id": t.ID(), "object": "video", "model": model, "status": t.state,
		"progress": min(100, max(0, t.progress)), "created_at": t.created}
	switch t.state {
	case "completed":
		out["usage"] = gin.H{"duration_seconds": t.seconds}
		out["video_url"] = "/v1/videos/" + t.ID() + "/content"
	case "failed":
		out["error"] = gin.H{"code": "generation_failed", "message": "GoodPut did not generate this video"}
	}
	return out
}

func (s *OpenAIGatewayService) GetGoodPutVideoTask(ctx context.Context, account *Account, nativeID string) (GoodPutVideoTask, error) {
	if !goodPutNativeVideoID.MatchString(nativeID) {
		return GoodPutVideoTask{}, &GoodPutVideoError{Status: 404, Code: "not_found_error", Message: "Video task not found"}
	}
	return s.sendGoodPutVideo(ctx, account, http.MethodGet, goodPutVideoOrigin+"/v1/videos/"+nativeID, nativeID, nil)
}

func (s *OpenAIGatewayService) prepareGoodPutVideoSubmission(ctx context.Context, account *Account, create *GoodPutVideoRequest) ([]byte, error) {
	if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGoodPutVideo) || account.GetModelMapping()[create.info.Model] != create.info.Model {
		return nil, errors.New("GoodPut video account does not provide this model")
	}
	if err := s.checkGoodPutVideoCatalogPrice(ctx, account, create.info.Model); err != nil {
		return nil, err
	}
	body, err := json.Marshal(create.payload)
	if err != nil {
		return nil, fmt.Errorf("encode GoodPut video request: %w", err)
	}
	return body, nil
}

func (s *OpenAIGatewayService) sendGoodPutVideo(ctx context.Context, account *Account, method, target, nativeID string, body []byte) (GoodPutVideoTask, error) {
	resp, err := s.doGoodPutVideoRequest(ctx, account, method, target, body)
	if err != nil {
		return GoodPutVideoTask{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeGoodPutVideoTask(resp.Body, nativeID)
}

func decodeGoodPutVideoTask(reader io.Reader, expectedNativeID string) (GoodPutVideoTask, error) {
	var receipt struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Progress  int    `json:"progress"`
		CreatedAt int64  `json:"created_at"`
		Usage     struct {
			Seconds int `json:"duration_seconds"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(reader, 256<<10)).Decode(&receipt); err != nil ||
		!goodPutNativeVideoID.MatchString(receipt.ID) || (expectedNativeID != "" && receipt.ID != expectedNativeID) {
		return GoodPutVideoTask{}, errors.New("invalid GoodPut video receipt")
	}
	switch receipt.Status {
	case "queued", "in_progress", "failed":
		return GoodPutVideoTask{nativeID: receipt.ID, state: receipt.Status, progress: receipt.Progress, created: receipt.CreatedAt}, nil
	case "completed":
		if receipt.Usage.Seconds >= 4 && receipt.Usage.Seconds <= 15 {
			return GoodPutVideoTask{nativeID: receipt.ID, state: receipt.Status, seconds: receipt.Usage.Seconds, progress: 100, created: receipt.CreatedAt}, nil
		}
	}
	return GoodPutVideoTask{}, errors.New("invalid GoodPut video status or billable duration")
}

// WriteGoodPutVideoContent is called only after the bound task reports completion.
// It does not follow result links or forward the GoodPut Key to a storage origin.
func (s *OpenAIGatewayService) WriteGoodPutVideoContent(ctx context.Context, c *gin.Context, account *Account, task GoodPutVideoTask) error {
	if !task.Completed() || !goodPutNativeVideoID.MatchString(task.nativeID) {
		return errors.New("cannot download an incomplete GoodPut video task")
	}
	resp, err := s.doGoodPutVideoRequest(ctx, account, http.MethodGet, goodPutVideoOrigin+"/v1/videos/"+task.nativeID+"/content", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return writeBoundVideoMP4(c, resp)
}

func writeBoundVideoMP4(c *gin.Context, resp *http.Response) error {
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || resp.ContentLength > videoDownloadMaxBytes ||
		(contentType != "video/mp4" && contentType != "application/octet-stream") {
		return errors.New("video content is unavailable or exceeds the download limit")
	}
	reader := bufio.NewReader(resp.Body)
	if prefix, err := reader.Peek(12); err != nil || string(prefix[4:8]) != "ftyp" {
		return errors.New("upstream did not return a valid MP4 download")
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Type", "video/mp4")
	c.Header("X-Content-Type-Options", "nosniff")
	if resp.ContentLength >= 0 {
		c.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	c.Status(http.StatusOK)
	MarkResponseCommitted(c)
	written, err := io.Copy(c.Writer, io.LimitReader(reader, videoDownloadMaxBytes))
	if err != nil {
		return fmt.Errorf("copy video content: %w", err)
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return errors.New("incomplete video download")
	}
	if resp.ContentLength < 0 && written == videoDownloadMaxBytes {
		if _, err := reader.Peek(1); !errors.Is(err, io.EOF) {
			return errors.New("video download exceeded its limit or did not terminate cleanly")
		}
	}
	return nil
}

func (s *OpenAIGatewayService) doGoodPutVideoRequest(ctx context.Context, account *Account, method, target string, body []byte) (*http.Response, error) {
	if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGoodPutVideo) {
		return nil, fmt.Errorf("invalid GoodPut video account: %w", errGoodPutVideoNotDispatched)
	}
	if _, err := s.validateUpstreamBaseURL(goodPutVideoOrigin); err != nil {
		return nil, fmt.Errorf("GoodPut video origin is unavailable under the outbound URL policy: %w", errGoodPutVideoNotDispatched)
	}
	proxy, err := ResolveAccountProxyURL(account)
	if err != nil {
		return nil, fmt.Errorf("GoodPut video account proxy is unavailable: %w", errGoodPutVideoNotDispatched)
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil || token == "" {
		return nil, fmt.Errorf("GoodPut video account credential is unavailable: %w", errGoodPutVideoNotDispatched)
	}
	ctx = WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx))
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cannot construct GoodPut video request: %w", errGoodPutVideoNotDispatched)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.GetBody = nil
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return nil, errors.New("connection to GoodPut video failed; do not replay an uncertain submission automatically")
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return nil, classifyGoodPutVideoError(resp)
}

func classifyGoodPutVideoError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return &GoodPutVideoError{Status: resp.StatusCode, Code: "invalid_request_error", Message: "GoodPut rejected the video parameters or references", submissionRejected: true}
	case http.StatusUnauthorized, http.StatusForbidden:
		return &GoodPutVideoError{Status: 502, Code: "upstream_error", Message: "GoodPut rejected account access", submissionRejected: true}
	case http.StatusRequestEntityTooLarge:
		return &GoodPutVideoError{Status: 413, Code: "invalid_request_error", Message: "GoodPut rejected the video request size", submissionRejected: true}
	case http.StatusNotFound:
		return &GoodPutVideoError{Status: 404, Code: "not_found_error", Message: "The task or model is unavailable from GoodPut", submissionRejected: true}
	case http.StatusTooManyRequests:
		var payload struct {
			Error struct{ Code string } `json:"error"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&payload); err == nil && payload.Error.Code == "credit_balance_exhausted" {
			return &GoodPutVideoError{Status: 429, Code: "credit_balance_exhausted", Message: "The GoodPut account has insufficient balance", submissionRejected: true}
		}
		return &GoodPutVideoError{Status: 429, Code: "rate_limit_error", Message: "The GoodPut account is rate limited or has reached its quota", submissionRejected: true}
	default:
		return errors.New("GoodPut video upstream did not accept the request")
	}
}
