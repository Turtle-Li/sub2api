package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/image/webp"
)

const (
	MuseVideoModel                                             = "muse-video"
	MuseVideoCreate                   GrokMediaEndpoint        = "muse_video_create"
	MuseVideoStatus                   GrokMediaEndpoint        = "muse_video_status"
	MuseVideoContent                  GrokMediaEndpoint        = "muse_video_content"
	OpenAIEndpointCapabilityMuseVideo OpenAIEndpointCapability = "muse_video"
	museVideoDuration                                          = 5
	museVideoMaxBytes                                          = 100_000_000
	museVideoReferenceMaxBytes                                 = 8 << 20
	museVideoReferenceMaxDimension                             = 4096
)

var (
	museNativeVideoID = regexp.MustCompile(`^task_[0-9a-f]{20}$`)
	museVideoFilename = regexp.MustCompile(`^[0-9a-f]{32}\.mp4$`)
	ErrMuseVideoModel = errors.New("Videos API is not supported for this platform")
)

func (e GrokMediaEndpoint) IsMuseVideo() bool {
	return e == MuseVideoCreate || e == MuseVideoStatus || e == MuseVideoContent
}

func IsMuseVideoTaskID(id string) bool {
	return strings.HasPrefix(id, "muse_") && museNativeVideoID.MatchString(strings.TrimPrefix(id, "muse_"))
}

func (s *OpenAIGatewayService) HasMuseVideoPrice(ctx context.Context, key *APIKey) bool {
	if key == nil || key.Group == nil {
		return false
	}
	key = s.apiKeyWithFreshGroupMediaPricing(ctx, key)
	price := key.Group.GetVideoPriceForModel(MuseVideoModel, VideoBillingResolution720P)
	return price != nil && *price > 0
}

type museVideoRequest struct {
	Model          string  `json:"model"`
	Prompt         string  `json:"prompt"`
	Duration       int     `json:"duration"`
	Size           string  `json:"size"`
	ReferenceImage *string `json:"reference_image,omitempty"`
}

func ParseMuseVideoRequest(body []byte) (GrokMediaRequestInfo, []byte, error) {
	var info GrokMediaRequestInfo
	var selector struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &selector); err != nil {
		return info, nil, errors.New("Muse video requires a JSON object")
	}
	if selector.Model != MuseVideoModel {
		return info, nil, ErrMuseVideoModel
	}
	var req museVideoRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return info, nil, errors.New("Muse video accepts model, prompt, duration, size and one optional reference_image string")
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" || len(req.Prompt) > 16000 {
		return info, nil, errors.New("Muse video requires a prompt of 1–16000 bytes")
	}
	if req.Duration == 0 {
		req.Duration = museVideoDuration
	}
	if req.Size == "" {
		req.Size = "16:9"
	}
	if req.Duration != museVideoDuration || (req.Size != "16:9" && req.Size != "9:16") {
		return info, nil, errors.New("Muse video supports 5 seconds and size 16:9 or 9:16")
	}
	info = GrokMediaRequestInfo{Model: req.Model, Prompt: req.Prompt, DurationSeconds: req.Duration, Resolution: VideoBillingResolution720P}
	if req.ReferenceImage != nil {
		reference := strings.TrimSpace(*req.ReferenceImage)
		if err := validateMuseVideoReferenceImage(reference); err != nil {
			return GrokMediaRequestInfo{}, nil, err
		}
		req.ReferenceImage = &reference
		info.InputImageURLs = []string{reference}
	}
	normalized, err := json.Marshal(req)
	return info, normalized, err
}

func validateMuseVideoReferenceImage(value string) error {
	header, encoded, _ := strings.Cut(value, ",")
	var decodeConfig func(io.Reader) (image.Config, error)
	switch header {
	case "data:image/jpeg;base64":
		decodeConfig = jpeg.DecodeConfig
	case "data:image/png;base64":
		decodeConfig = png.DecodeConfig
	case "data:image/webp;base64":
		decodeConfig = webp.DecodeConfig
	default:
		return errors.New("reference_image must be a JPEG, PNG or WebP base64 data URL")
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(museVideoReferenceMaxBytes) {
		return errors.New("reference_image exceeds 8 MiB")
	}
	if encoded == "" || strings.ContainsAny(encoded, "\r\n") {
		return errors.New("reference_image contains invalid base64")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return errors.New("reference_image contains invalid base64")
	}
	if len(data) > museVideoReferenceMaxBytes {
		return errors.New("reference_image exceeds 8 MiB")
	}
	// Inspect dimensions without expanding the image into a raster in the gateway.
	cfg, err := decodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return errors.New("reference_image does not match its declared image format")
	}
	if cfg.Width > museVideoReferenceMaxDimension || cfg.Height > museVideoReferenceMaxDimension {
		return errors.New("reference_image dimensions cannot exceed 4096 × 4096 pixels")
	}
	return nil
}

type museNativeVideoTask struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Result   struct {
		Filename string `json:"filename"`
	} `json:"result"`
}

func WriteMuseVideoReceipt(c *gin.Context, id string) {
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusAccepted, gin.H{"id": id, "object": "video.task", "model": MuseVideoModel, "status": "queued"})
}

// ForwardMuseVideo calls the configured account only. A create error never
// triggers failover: the native service may already have accepted the task.
func (s *OpenAIGatewayService) ForwardMuseVideo(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, id string, body []byte) (*OpenAIForwardResult, error) {
	if !endpoint.IsMuseVideo() || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseVideo) {
		return nil, errors.New("Muse video account is not configured")
	}
	base, err := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return nil, errors.New("invalid Muse video account base URL")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid Muse video account base URL")
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/v1"
	proxy, err := ResolveAccountProxyURL(account)
	if err != nil {
		return nil, errors.New("Muse video account proxy is unavailable")
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, errors.New("Muse video account credential is unavailable")
	}
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), 60*time.Second)
	defer cancel()
	started := time.Now()
	target := base + "/videos"
	if endpoint.IsVideoLookupRequest() {
		if !IsMuseVideoTaskID(id) {
			return nil, errors.New("invalid Muse video task ID")
		}
		target += "/" + strings.TrimPrefix(id, "muse_")
	}
	resp, err := s.doMuseVideoRequest(ctx, account, endpoint.httpMethod(), target, proxy, token, body)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		switch resp.StatusCode {
		case http.StatusNotFound:
			if endpoint.IsVideoLookupRequest() {
				writeGrokMediaErrorResponse(c, http.StatusNotFound, "not_found_error", "Video request not found")
			}
		case http.StatusTooManyRequests:
			writeGrokMediaErrorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Muse video is temporarily rate limited")
		}
		return nil, fmt.Errorf("Muse video upstream returned HTTP %d", resp.StatusCode)
	}
	raw, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read Muse video task: %w", err)
	}
	var task museNativeVideoTask
	if err := json.Unmarshal(raw, &task); err != nil || !museNativeVideoID.MatchString(task.ID) {
		return nil, errors.New("invalid Muse video task response")
	}
	publicID := "muse_" + task.ID
	result := &OpenAIForwardResult{
		ResponseID: publicID, Model: MuseVideoModel, BillingModel: MuseVideoModel,
		UpstreamModel: MuseVideoModel, Duration: time.Since(started),
	}
	if endpoint == MuseVideoCreate {
		result.VideoResolution = VideoBillingResolution720P
		result.VideoDurationSeconds = museVideoDuration
		return result, nil
	}
	if publicID != id {
		return nil, errors.New("Muse video task ID did not match")
	}
	completed := task.Status == "completed"
	switch task.Status {
	case "queued", "processing", "failed", "completed":
	default:
		return nil, errors.New("unknown Muse video task status")
	}
	if completed && !museVideoFilename.MatchString(task.Result.Filename) {
		return nil, errors.New("invalid Muse video output filename")
	}
	c.Header("Cache-Control", "private, no-store")
	if endpoint == MuseVideoContent {
		if !completed {
			writeGrokMediaErrorResponse(c, http.StatusConflict, "invalid_request_error", "Video content is not ready")
			return nil, errors.New("Muse video output is not ready")
		}
		if err := s.writeMuseVideoContent(ctx, c, account, base, proxy, task.Result.Filename); err != nil {
			return nil, err
		}
	} else {
		out := gin.H{"id": id, "object": "video.task", "model": MuseVideoModel, "status": task.Status, "progress": min(100, max(0, task.Progress))}
		if completed {
			out["url"] = grokMediaContentProxyURL(c, id)
		} else if task.Status == "failed" {
			out["error"] = gin.H{"code": "generation_failed", "message": "Muse could not generate this video"}
		}
		c.JSON(http.StatusOK, out)
	}
	if completed {
		result.VideoCount = 1
	}
	return result, nil
}

func (s *OpenAIGatewayService) doMuseVideoRequest(ctx context.Context, account *Account, method, target, proxy, token string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("cannot construct Muse video request")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return nil, errors.New("Muse video upstream connection failed")
	}
	return resp, nil
}

func (s *OpenAIGatewayService) writeMuseVideoContent(ctx context.Context, c *gin.Context, account *Account, base, proxy, filename string) error {
	// Ignore provider URLs. Build the native path at the validated account origin.
	resp, err := s.doMuseVideoRequest(ctx, account, http.MethodGet, base+"/media/"+filename, proxy, "", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.ContentLength < 12 || resp.ContentLength > museVideoMaxBytes {
		return errors.New("Muse video output is unavailable or exceeds the size limit")
	}
	reader := bufio.NewReader(resp.Body)
	header, err := reader.Peek(12)
	if err != nil || string(header[4:8]) != "ftyp" {
		return errors.New("Muse output is not an MP4")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", "video/mp4")
	c.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	c.Status(http.StatusOK)
	MarkResponseCommitted(c)
	if _, err := io.CopyN(c.Writer, reader, resp.ContentLength); err != nil {
		return fmt.Errorf("copy Muse video content: %w", err)
	}
	return nil
}
