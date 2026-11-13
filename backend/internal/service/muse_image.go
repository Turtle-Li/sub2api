package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	MuseImageModel = "muse-image"

	MuseImageGenerate GrokMediaEndpoint = "muse_image_generate"
	MuseImageEdit     GrokMediaEndpoint = "muse_image_edit"
	MuseImageCreate   GrokMediaEndpoint = "muse_image_create"
	MuseImageStatus   GrokMediaEndpoint = "muse_image_status"
	MuseImageContent  GrokMediaEndpoint = "muse_image_content"

	OpenAIEndpointCapabilityMuseImage OpenAIEndpointCapability = "muse_image"
	museImageIDPrefix                                          = "muse_image_"
	museImageResultMaxBytes                                    = 40 << 20
)

var museImageFilename = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|jpeg|gif|webp|heic|heif|avif)$`)

func (e GrokMediaEndpoint) IsMuseImage() bool {
	switch e {
	case MuseImageGenerate, MuseImageEdit, MuseImageCreate, MuseImageStatus, MuseImageContent:
		return true
	default:
		return false
	}
}

func (e GrokMediaEndpoint) IsMuseMedia() bool {
	return e.IsMuseVideo() || e.IsMuseImage()
}

func IsMuseImageTaskID(id string) bool {
	return strings.HasPrefix(id, museImageIDPrefix) && museNativeTaskID.MatchString(strings.TrimPrefix(id, museImageIDPrefix))
}

func (s *OpenAIGatewayService) HasMuseImagePrice(ctx context.Context, key *APIKey, tier string) bool {
	if key == nil || key.Group == nil {
		return false
	}
	key = s.apiKeyWithFreshGroupMediaPricing(ctx, key)
	price := key.Group.GetImagePrice(tier)
	return price != nil && *price > 0
}

// Native Muse keys are global to one installation. Scope them to the public
// owner before forwarding so two customers cannot receive the same receipt.
func MuseImageIdempotencyKey(key *APIKey, endpoint GrokMediaEndpoint, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > 256 || key == nil || key.GroupID == nil {
		return "", errors.New("invalid Muse Idempotency-Key; maximum length is 256 bytes")
	}
	return fmt.Sprintf("sub2-%x", sha256.Sum256(fmt.Appendf(nil, "%d:%d:%d:%s:%s", key.UserID, key.ID, *key.GroupID, endpoint, value))), nil
}

type MuseImageTask struct {
	NativeID  string   `json:"id"`
	Status    string   `json:"status"`
	Kind      string   `json:"kind"`
	Progress  int      `json:"progress"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
	Created   int64    `json:"created"`
	Elapsed   *float64 `json:"elapsed"`
	Size      string   `json:"size"`
	Result    struct {
		Filename string `json:"filename"`
	} `json:"result"`
	Data []struct {
		RevisedPrompt string `json:"revised_prompt"`
		Size          string `json:"size"`
		Kind          string `json:"kind"`
		Bytes         int64  `json:"bytes"`
		Base64        string `json:"b64_json"`
	} `json:"data"`
}

func (t *MuseImageTask) ID() string {
	return museImageIDPrefix + t.NativeID
}

func (t *MuseImageTask) ImageResponse(c *gin.Context) gin.H {
	item := t.Data[0]
	data := gin.H{"revised_prompt": item.RevisedPrompt, "size": item.Size, "kind": "image", "bytes": item.Bytes}
	if item.Base64 != "" {
		data["b64_json"] = item.Base64
	} else {
		prefix := ""
		if strings.HasPrefix(c.Request.URL.Path, "/v1/") {
			prefix = "/v1"
		}
		data["url"] = prefix + "/images/tasks/" + url.PathEscape(t.ID()) + "/content"
	}
	return gin.H{"created": t.Created, "data": []gin.H{data}}
}

func (t *MuseImageTask) TaskResponse(c *gin.Context, includeImage bool) gin.H {
	out := gin.H{"id": t.ID(), "task_id": t.ID(), "object": "image.task", "model": MuseImageModel,
		"status": t.Status, "progress": min(100, max(0, t.Progress)), "created_at": t.CreatedAt}
	if t.UpdatedAt != 0 {
		out["updated_at"] = t.UpdatedAt
	}
	if t.Elapsed != nil {
		out["elapsed"] = *t.Elapsed
	}
	if t.Status == "failed" {
		out["error"] = gin.H{"code": "generation_failed", "message": "Muse could not generate this image"}
	}
	if includeImage && t.Status == "completed" {
		for key, value := range t.ImageResponse(c) {
			out[key] = value
		}
	}
	return out
}

func (t *MuseImageTask) UsageResult() *OpenAIForwardResult {
	var duration time.Duration
	if t.CreatedAt > 0 {
		duration = time.Since(time.Unix(t.CreatedAt, 0))
		if duration < 0 {
			duration = 0
		}
	}
	return &OpenAIForwardResult{
		RequestID: "muse-image:" + t.NativeID, ResponseID: t.ID(),
		Model: MuseImageModel, BillingModel: MuseImageModel, UpstreamModel: MuseImageModel,
		ImageCount: 1, ImageSize: NormalizeImageBillingTierOrDefault(t.Size), ImageInputSize: t.Size,
		Duration: duration,
	}
}

// Both synchronous and asynchronous public images use the native task API.
// Waiting for a synchronous result polls the accepted task instead of replaying
// a generation when the original client or provider connection goes away.
func (s *OpenAIGatewayService) ForwardMuseImage(ctx context.Context, c *gin.Context, account *Account, id, idempotencyKey string, body []byte) (*MuseImageTask, error) {
	if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseImage) {
		return nil, errors.New("Muse image account is not configured")
	}
	base, proxy, token, err := s.museUpstream(ctx, account)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), 120*time.Second)
	defer cancel()
	started := time.Now()
	method, target := http.MethodPost, base+"/images/tasks"
	if id != "" {
		if !IsMuseImageTaskID(id) {
			return nil, errors.New("invalid Muse image task ID")
		}
		method, target = http.MethodGet, target+"/"+strings.TrimPrefix(id, museImageIDPrefix)
	}
	resp, err := s.doMuseRequest(ctx, account, method, target, proxy, token, idempotencyKey, body)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, museUpstreamError(c, resp.StatusCode, id != "")
	}
	raw, err := readUpstreamResponseBodyLimited(resp.Body, (museImageResultMaxBytes*4/3)+(1<<20))
	if err != nil {
		return nil, fmt.Errorf("read Muse image task: %w", err)
	}
	var task MuseImageTask
	if err := json.Unmarshal(raw, &task); err != nil || !museNativeTaskID.MatchString(task.NativeID) ||
		!validMuseTaskStatus(task.Status) || (task.Kind != "" && task.Kind != "image") ||
		(id != "" && task.ID() != id) {
		return nil, errors.New("invalid Muse image task response")
	}
	if id != "" && task.Status == "completed" &&
		(!museImageFilename.MatchString(task.Result.Filename) || len(task.Data) != 1 ||
			task.Data[0].Bytes <= 0 || task.Data[0].Bytes > museImageResultMaxBytes ||
			(task.Data[0].Kind != "" && task.Data[0].Kind != "image")) {
		return nil, errors.New("invalid Muse image output")
	}
	if id != "" && task.Status == "completed" && task.Data[0].Base64 != "" {
		item := task.Data[0]
		if len(item.Base64) != base64.StdEncoding.EncodedLen(int(item.Bytes)) {
			return nil, errors.New("invalid Muse image output length")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(item.Base64)
		mediaType := museReferenceTypes[path.Ext(task.Result.Filename)]
		if err != nil || int64(len(data)) != item.Bytes || !validMuseImageType(data, mediaType, sniffMuseMedia(data)) {
			return nil, errors.New("invalid Muse Base64 image output")
		}
	}
	return &task, nil
}

func (s *OpenAIGatewayService) WriteMuseImageContent(ctx context.Context, c *gin.Context, account *Account, task *MuseImageTask) error {
	filename := task.Result.Filename
	if task.Status != "completed" || !museImageFilename.MatchString(filename) ||
		!account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseImage) {
		return errors.New("invalid Muse image content request")
	}
	base, proxy, _, err := s.museUpstream(ctx, account)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), 60*time.Second)
	defer cancel()
	resp, err := s.doMuseRequest(ctx, account, http.MethodGet, base+"/media/"+filename, proxy, "", "", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > museImageResultMaxBytes {
		return errors.New("Muse image output is unavailable")
	}
	data, err := readUpstreamResponseBodyLimited(resp.Body, museImageResultMaxBytes)
	mediaType := museReferenceTypes[path.Ext(filename)]
	if err != nil || !validMuseImageType(data, mediaType, sniffMuseMedia(data)) {
		return errors.New("Muse output does not match its image format")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Length", strconv.Itoa(len(data)))
	c.Data(http.StatusOK, mediaType, data)
	return nil
}

// Reuse the async media cache and the SQL usage ledger, with disjoint keys
// for image admission, ownership and settlement.
func (s *OpenAIGatewayService) ReserveMuseImageSubmission(ctx context.Context, key *APIKey, scopedKey string, accountID int64) error {
	if scopedKey == "" {
		return nil
	}
	if s == nil || s.cache == nil {
		return errors.New("Muse image submission cache is unavailable")
	}
	bindingID := "muse-image-admission:" + scopedKey
	lease := grokVideoPendingBillingKey(bindingID, key.UserID, key.ID)
	claimed, err := s.cache.ClaimGrokVideoBilled(ctx, lease, 15*time.Second)
	if err != nil || !claimed {
		return errors.New("a request with this Muse Idempotency-Key is being admitted")
	}
	defer func() { _ = s.cache.ReleaseGrokVideoBilled(ctx, lease) }()
	bound, err := s.ResolveGrokMediaVideoRequestAccount(ctx, key.GroupID, bindingID, key.UserID, key.ID)
	if err == nil {
		if bound == accountID {
			return nil
		}
		return errors.New("this Muse Idempotency-Key belongs to a different account")
	}
	if !errors.Is(err, ErrStickySessionNotFound) {
		return errors.New("could not verify the Muse submission binding")
	}
	return s.BindGrokMediaVideoRequestAccount(ctx, key.GroupID, bindingID, key.UserID, key.ID, accountID)
}

func (s *OpenAIGatewayService) ResolveMuseImageSubmission(ctx context.Context, key *APIKey, scopedKey string) (int64, error) {
	id, err := s.ResolveGrokMediaVideoRequestAccount(ctx, key.GroupID, "muse-image-admission:"+scopedKey, key.UserID, key.ID)
	if errors.Is(err, ErrStickySessionNotFound) {
		return 0, nil
	}
	return id, err
}

func (s *OpenAIGatewayService) SettleMuseImage(ctx context.Context, input *OpenAIRecordUsageInput) error {
	if s == nil || s.cache == nil || s.usageBillingRepo == nil || input == nil || input.Result == nil || input.APIKey == nil {
		return errors.New("Muse image settlement is unavailable")
	}
	result, key := input.Result, input.APIKey
	task := grokVideoPendingBillingKey("muse-image-settlement:"+result.ResponseID, key.UserID, key.ID)
	if task == "" {
		return errors.New("invalid Muse image settlement key")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	if settled, err := s.cache.GetGrokVideoPendingBilling(ctx, task); err != nil || len(settled) != 0 {
		return err
	}
	claimed, err := s.cache.ClaimGrokVideoBilled(ctx, task, 20*time.Second)
	if err != nil || !claimed {
		return errors.New("Muse image settlement is in progress")
	}
	if err := s.RecordUsage(ctx, input); err != nil {
		return err
	}
	// The durable ledger deduplicates if Redis loses this completion marker.
	return s.cache.SetGrokVideoPendingBilling(ctx, task, []byte("settled"), grokVideoPendingBillingTTL(s.cfg))
}
