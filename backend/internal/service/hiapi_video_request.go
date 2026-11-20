package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const (
	HiAPISeedanceMiniModel                                      = "seedance-2.0-mini"
	HiAPIVideoCreate                   GrokMediaEndpoint        = "hiapi_video_create"
	HiAPIVideoStatus                   GrokMediaEndpoint        = "hiapi_video_status"
	HiAPIVideoContent                  GrokMediaEndpoint        = "hiapi_video_content"
	OpenAIEndpointCapabilityHiAPIVideo OpenAIEndpointCapability = "hiapi_video"
	HiAPIVideoRequestMaxBytes                                   = 32 << 10
	hiAPIVideoOrigin                                            = "https://api.hiapi.ai"
	HiAPIVideoTaskPath                                          = "/v1/videos/"
)

var hiAPIVideoTaskID = regexp.MustCompile(`^hav_[0-9a-f]{32}$`)

func IsHiAPIVideoTaskID(id string) bool { return hiAPIVideoTaskID.MatchString(id) }

func (e GrokMediaEndpoint) IsHiAPIVideo() bool {
	return e == HiAPIVideoCreate || e == HiAPIVideoStatus || e == HiAPIVideoContent
}

func IsHiAPIVideoGroup(group *Group) bool {
	return group != nil && group.Platform == PlatformOpenAI && !group.IsSubscriptionType() &&
		group.ModelAllowlist.Enabled && len(group.ModelAllowlist.Models) == 1 && group.ModelAllowlist.Models[0] == HiAPISeedanceMiniModel
}

func (a *Account) supportsHiAPIVideo() bool {
	if a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return false
	}
	base := strings.TrimSuffix(strings.TrimSpace(a.GetCredential("base_url")), "/")
	if base != hiAPIVideoOrigin && base != hiAPIVideoOrigin+"/v1" {
		return false
	}
	mapping := a.GetModelMapping()
	return len(mapping) == 1 && mapping[HiAPISeedanceMiniModel] == HiAPISeedanceMiniModel
}

type hiAPIVideoInput struct {
	Prompt        string `json:"prompt"`
	FirstFrameURL string `json:"first_frame_url,omitempty"`
	LastFrameURL  string `json:"last_frame_url,omitempty"`
	Resolution    string `json:"resolution"`
	Duration      int    `json:"duration"`
	AspectRatio   string `json:"aspect_ratio"`
	GenerateAudio *bool  `json:"generate_audio"`
}

type HiAPIVideoRequest struct {
	model string
	input hiAPIVideoInput
}

func ParseHiAPIVideoRequest(contentType string, body []byte) (*HiAPIVideoRequest, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" || len(body) > HiAPIVideoRequestMaxBytes {
		return nil, errors.New("HiAPI video requires a JSON body of at most 32 KiB")
	}
	var wire struct {
		Model string `json:"model"`
		hiAPIVideoInput
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		return nil, errors.New("invalid HiAPI video parameters; reference media, alternate routes, callbacks and storage overrides are not supported")
	}
	if promptLength := utf8.RuneCountInString(strings.TrimSpace(wire.Prompt)); wire.Model != HiAPISeedanceMiniModel || promptLength < 3 || promptLength > 20000 {
		return nil, errors.New("model must be seedance-2.0-mini and prompt must be 3 to 20000 characters")
	}
	if wire.Duration < 4 || wire.Duration > 15 || (wire.Resolution != "480p" && wire.Resolution != "720p") {
		return nil, errors.New("duration must be an integer from 4 to 15 and resolution must be 480p or 720p")
	}
	switch wire.AspectRatio {
	case "1:1", "4:3", "3:4", "16:9", "9:16", "21:9", "adaptive":
	default:
		return nil, errors.New("aspect_ratio must be 1:1, 4:3, 3:4, 16:9, 9:16, 21:9 or adaptive")
	}
	if wire.GenerateAudio == nil {
		return nil, errors.New("generate_audio must be explicitly set to true or false")
	}
	if (wire.FirstFrameURL != "" && !validHiAPIVideoURL(wire.FirstFrameURL)) ||
		(wire.LastFrameURL != "" && (wire.FirstFrameURL == "" || !validHiAPIVideoURL(wire.LastFrameURL))) {
		return nil, errors.New("frames must be public HTTPS image URLs; last_frame_url requires first_frame_url")
	}
	return &HiAPIVideoRequest{model: wire.Model, input: wire.hiAPIVideoInput}, nil
}

func validHiAPIVideoURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" &&
		(u.Port() == "" || u.Port() == "443") && !urlvalidator.IsBlockedHost(u.Hostname())
}

func (r *HiAPIVideoRequest) Info() GrokMediaRequestInfo {
	info := GrokMediaRequestInfo{Model: r.model, Prompt: r.input.Prompt, N: 1, Resolution: r.input.Resolution,
		DurationSeconds: r.input.Duration, AspectRatio: r.input.AspectRatio}
	for _, frameURL := range []string{r.input.FirstFrameURL, r.input.LastFrameURL} {
		if frameURL != "" {
			info.InputImageURLs = append(info.InputImageURLs, frameURL)
		}
	}
	return info
}

func (r *HiAPIVideoRequest) marshal() ([]byte, error) {
	return json.Marshal(struct {
		Model string          `json:"model"`
		Input hiAPIVideoInput `json:"input"`
	}{Model: r.model, Input: r.input})
}
