package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const (
	GoodPutH3Model     = "minimax/h3"
	GoodPutH3FastModel = "minimax/h3-fast"

	GoodPutVideoCreate  GrokMediaEndpoint = "goodput_video_create"
	GoodPutVideoStatus  GrokMediaEndpoint = "goodput_video_status"
	GoodPutVideoContent GrokMediaEndpoint = "goodput_video_content"

	OpenAIEndpointCapabilityGoodPutVideo OpenAIEndpointCapability = "goodput_video"
	GoodPutVideoRequestMaxBytes                                   = 8 << 20
	goodPutVideoOrigin                                            = "https://api.goodputai.cn"
)

func (e GrokMediaEndpoint) IsGoodPutVideo() bool {
	return e == GoodPutVideoCreate || e == GoodPutVideoStatus || e == GoodPutVideoContent
}

func IsGoodPutVideoModel(model string) bool {
	return model == GoodPutH3Model || model == GoodPutH3FastModel
}

func IsGoodPutVideoGroup(group *Group) bool {
	if group == nil || group.Platform != PlatformOpenAI || group.IsSubscriptionType() ||
		!group.IsExclusive || !group.ModelAllowlist.Enabled || len(group.ModelAllowlist.Models) == 0 {
		return false
	}
	for _, model := range group.ModelAllowlist.Models {
		if !IsGoodPutVideoModel(model) {
			return false
		}
	}
	return true
}

func (a *Account) supportsGoodPutVideo() bool {
	if a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return false
	}
	base := strings.TrimSuffix(strings.TrimSpace(a.GetCredential("base_url")), "/")
	if base != goodPutVideoOrigin && base != goodPutVideoOrigin+"/v1" {
		return false
	}
	mapping := a.GetModelMapping()
	for model, target := range mapping {
		if !IsGoodPutVideoModel(model) || target != model {
			return false
		}
	}
	return len(mapping) > 0
}

type goodPutVideoMedia struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type GoodPutVideoRequest struct {
	payload goodPutVideoPayload
	info    GrokMediaRequestInfo
}

type goodPutVideoPayload struct {
	Model          string              `json:"model"`
	Prompt         string              `json:"prompt"`
	Seconds        json.Number         `json:"seconds"`
	Size           string              `json:"size,omitempty"`
	Seed           *int64              `json:"seed,omitempty"`
	InputReference string              `json:"input_reference,omitempty"`
	Media          []goodPutVideoMedia `json:"media,omitempty"`
}

func (r *GoodPutVideoRequest) Info() GrokMediaRequestInfo { return r.info }

func ParseGoodPutVideoRequest(contentType string, body []byte) (*GoodPutVideoRequest, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return nil, errors.New("GoodPut H3 requires application/json; use media URLs or inline data URLs for references")
	}
	if len(body) > GoodPutVideoRequestMaxBytes {
		return nil, errors.New("GoodPut H3 request exceeds the media size limit")
	}
	var req goodPutVideoPayload
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		return nil, errors.New("invalid GoodPut H3 request fields")
	}
	if !IsGoodPutVideoModel(req.Model) {
		return nil, errors.New("model must be minimax/h3 or minimax/h3-fast")
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return nil, errors.New("prompt is required")
	}
	if req.Seconds == "" {
		req.Seconds = "5"
	}
	seconds, err := req.Seconds.Int64()
	fast := req.Model == GoodPutH3FastModel
	minimum := int64(4)
	if fast {
		minimum = 5
	}
	if err != nil || seconds < minimum || seconds > 15 {
		if fast {
			return nil, errors.New("H3 Fast seconds must be an integer from 5 to 15")
		}
		return nil, errors.New("H3 seconds must be an integer from 4 to 15")
	}
	if fast && seconds == 15 {
		// Ask for the documented billable duration directly rather than relying on automatic clamping.
		req.Seconds = "14"
		seconds = 14
	}
	if req.Seed != nil {
		if !fast {
			return nil, errors.New("seed is supported only by H3 Fast")
		}
		if *req.Seed < 0 || *req.Seed > 1<<31-1 {
			return nil, errors.New("H3 Fast seed must be an integer from 0 to 2147483647")
		}
	}
	if !goodPutVideoCanvasAllowed(req.Size, fast) {
		return nil, errors.New("size does not match a supported GoodPut H3 canvas for this model")
	}
	images, err := goodPutVideoReferenceImages(req)
	if err != nil {
		return nil, err
	}
	return &GoodPutVideoRequest{payload: req, info: GrokMediaRequestInfo{
		Model: req.Model, Prompt: req.Prompt, Size: req.Size, N: 1, InputImageURLs: images,
		Resolution: VideoBillingResolution720P, DurationSeconds: int(seconds),
	}}, nil
}

func goodPutVideoCanvasAllowed(size string, fast bool) bool {
	switch size {
	case "", "1344x768", "768x1344":
		return true
	case "1536x672", "1024x768", "768x768", "768x1024":
		return !fast
	default:
		return false
	}
}

func goodPutVideoReferenceImages(req goodPutVideoPayload) ([]string, error) {
	var images []string
	var first, last, references int
	if req.InputReference != "" {
		if !goodPutVideoMediaURL(req.InputReference, "image") {
			return nil, errors.New("input_reference must be a public URL or an inline image data URL")
		}
		first++
		images = append(images, req.InputReference)
	}
	for _, media := range req.Media {
		kind := "image"
		switch media.Type {
		case "first_frame":
			first++
		case "last_frame":
			last++
		case "reference_image", "reference_audio", "reference_video":
			kind = strings.TrimPrefix(media.Type, "reference_")
			references++
		default:
			return nil, errors.New("unsupported GoodPut H3 media type")
		}
		if !goodPutVideoMediaURL(media.URL, kind) {
			return nil, errors.New("media requires a public URL or a matching inline data URL")
		}
		if kind == "image" {
			images = append(images, media.URL)
		}
	}
	if first > 1 || last > 1 || (last > 0 && first == 0) || (references > 0 && first+last > 0) {
		return nil, errors.New("provide a first frame and optional last frame, or reference assets, but do not mix them")
	}
	return images, nil
}

func goodPutVideoMediaURL(value, kind string) bool {
	if strings.HasPrefix(value, "data:"+kind+"/") {
		before, data, found := strings.Cut(value, ",")
		return found && strings.HasSuffix(before, ";base64") && data != ""
	}
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && !urlvalidator.IsBlockedHost(u.Hostname()) && u.User == nil && u.Fragment == ""
}
