package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	museMaxReferences       = 10
	museMaxReferenceBytes   = 40 << 20
	museMaxReferenceTotal   = 100 << 20
	museMaxTextBytes        = 64 << 10
	museVideoDefaultSeconds = 6
)

// MuseMediaRequestMaxBytes fits 100 MiB of Base64 references, plus JSON fields.
const MuseMediaRequestMaxBytes int64 = 136 << 20

var errMuseReferenceSize = errors.New("a Muse reference cannot exceed 40 MiB")

// Bound preparation and forwarding of large normalized bodies separately from
// the per-user generation slots. A slot is released after the native POST.
var museLargeMediaSlots = make(chan struct{}, 2)

func (r *MuseMediaRequest) AcquireLargeMediaSlot(ctx context.Context, bodyBytes int) (func(), error) {
	if bodyBytes < 8<<20 {
		large := false
		for _, value := range r.references {
			if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || len(value) >= 8<<20 {
				large = true
				break
			}
		}
		if !large {
			return func() {}, nil
		}
	}
	select {
	case museLargeMediaSlots <- struct{}{}:
		return func() { <-museLargeMediaSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, errors.New("Muse reference processing is busy; retry later")
	}
}

type MuseMediaRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Size           string `json:"size,omitempty"`
	AspectRatio    string `json:"aspect_ratio,omitempty"`
	Duration       int    `json:"duration,omitempty"`
	Resolution     string `json:"resolution,omitempty"`
	Timeout        int    `json:"timeout,omitempty"`
	Extra          string `json:"extra,omitempty"`
	N              int    `json:"n,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Async          bool   `json:"async,omitempty"`

	ReferenceImage string `json:"reference_image,omitempty"`
	ImageURL       any    `json:"image_url,omitempty"`
	Image          any    `json:"image,omitempty"`
	Images         []any  `json:"images,omitempty"`
	Videos         []any  `json:"videos,omitempty"`
	Audios         []any  `json:"audios,omitempty"`
	Files          []any  `json:"files,omitempty"`
	references     []string
}

func ParseMuseMediaRequest(endpoint GrokMediaEndpoint, contentType string, body []byte) (*MuseMediaRequest, error) {
	if int64(len(body)) > MuseMediaRequestMaxBytes {
		return nil, fmt.Errorf("Muse request exceeds %d MiB", MuseMediaRequestMaxBytes>>20)
	}
	var req MuseMediaRequest
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if endpoint == MuseImageEdit && mediaType == "multipart/form-data" {
		if err := req.parseMultipart(multipart.NewReader(bytes.NewReader(body), params["boundary"])); err != nil {
			return nil, err
		}
	} else {
		// Reject a capability the pinned upstream does not implement instead of
		// silently discarding it (for example, a structured last_frame).
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return nil, errors.New("Muse requires a valid JSON object")
		}
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			return nil, errors.New("invalid Muse request fields; use the native media schema")
		}
		if _, err := decoder.Token(); err != io.EOF {
			return nil, errors.New("Muse requires a single valid JSON object")
		}
	}
	if req.Model == "" {
		if endpoint.IsMuseVideo() {
			req.Model = MuseVideoModel
		} else {
			req.Model = MuseImageModel
		}
	}
	if endpoint.IsMuseVideo() && req.Model != MuseVideoModel || endpoint.IsMuseImage() && req.Model != MuseImageModel {
		return nil, ErrMuseMediaModel
	}
	if strings.TrimSpace(req.Prompt) == "" {
		if endpoint != MuseImageEdit {
			return nil, errors.New("Muse requires a prompt")
		}
		req.Prompt = "参考此图片并进行生图创作"
	}
	if len(req.Prompt)+len(req.Extra)+len(req.Size)+len(req.AspectRatio)+len(req.Resolution) > museMaxTextBytes {
		return nil, errors.New("Muse text fields exceed 64 KiB")
	}
	if req.Timeout < 0 || req.Timeout > 600 {
		return nil, errors.New("Muse timeout must be between 1 and 600 seconds when specified")
	}
	if endpoint.IsMuseVideo() {
		if req.Duration < 0 {
			return nil, errors.New("Muse video duration must be positive")
		}
		if req.Duration == 0 {
			req.Duration = museVideoDefaultSeconds
		}
		if req.Async || req.ResponseFormat != "" || req.N != 0 {
			return nil, errors.New("async, response_format and n are image parameters")
		}
	} else {
		if req.Duration != 0 || req.Resolution != "" {
			return nil, errors.New("duration and resolution are video parameters; use size for images")
		}
		if req.ResponseFormat == "" {
			req.ResponseFormat = "url"
		}
		if req.ResponseFormat != "url" && req.ResponseFormat != "b64_json" {
			return nil, errors.New("Muse image response_format must be url or b64_json")
		}
		if req.N == 0 {
			req.N = 1
		}
	}
	if err := req.collectReferences(); err != nil {
		return nil, err
	}
	return &req, nil
}

func (r *MuseMediaRequest) Info() GrokMediaRequestInfo {
	return GrokMediaRequestInfo{
		Model: r.Model, Prompt: strings.TrimSpace(strings.Join([]string{r.Prompt, r.Extra, r.Size, r.AspectRatio, r.Resolution}, "\n")),
		N: 1, SizeTier: NormalizeImageBillingTierOrDefault(r.Size),
		Resolution: VideoBillingResolution720P, DurationSeconds: max(5, r.Duration),
	}
}

func (r *MuseMediaRequest) collectReferences() error {
	var primary string
	for _, value := range []any{r.ReferenceImage, r.ImageURL, r.Image} {
		if primary = museReferenceValue(value); primary != "" {
			break
		}
	}
	if primary != "" {
		if len(r.Images) > 0 {
			return errors.New("images cannot be combined with a legacy single reference")
		}
		r.references = append(r.references, primary)
	}
	for _, group := range [][]any{r.Images, r.Videos, r.Audios, r.Files} {
		if len(r.references)+len(group) > museMaxReferences {
			return errors.New("Muse accepts at most 10 reference files")
		}
		for _, value := range group {
			ref := museReferenceValue(value)
			if ref == "" {
				return fmt.Errorf("invalid Muse reference %d", len(r.references)+1)
			}
			r.references = append(r.references, ref)
		}
	}
	r.ReferenceImage, r.ImageURL, r.Image = "", nil, nil
	r.Images, r.Videos, r.Audios, r.Files = nil, nil, nil, nil
	return nil
}

func museReferenceValue(value any) string {
	if fields, ok := value.(map[string]any); ok {
		value = nil
		for _, key := range []string{"image_url", "video_url", "audio_url", "file_url", "url", "b64_json", "data"} {
			switch candidate := fields[key].(type) {
			case string:
				if candidate == "" {
					continue
				}
				value = candidate
			case map[string]any:
				if len(candidate) == 0 {
					continue
				}
				value = candidate["url"]
			default:
				continue
			}
			break
		}
	}
	s, _ := value.(string)
	return strings.TrimSpace(s)
}

func (r *MuseMediaRequest) parseMultipart(reader *multipart.Reader) error {
	groups := map[string][]any{"image": nil, "image[]": nil, "video": nil, "audio": nil, "file": nil}
	count, uploaded := 0, 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("invalid Muse edit multipart form")
		}
		name := part.FormName()
		if _, reference := groups[name]; reference {
			count++
			if count > museMaxReferences {
				return errors.New("Muse accepts at most 10 reference files")
			}
			limit := museMaxReferenceBytes
			if part.FileName() == "" {
				limit = base64.StdEncoding.EncodedLen(museMaxReferenceBytes) + 256
			}
			data, err := io.ReadAll(io.LimitReader(part, int64(limit)+1))
			if err != nil || len(data) > limit {
				return errMuseReferenceSize
			}
			if part.FileName() == "" {
				groups[name] = append(groups[name], string(data))
			} else {
				uploaded += len(data)
				if uploaded > museMaxReferenceTotal {
					return errors.New("Muse references exceed 100 MiB in total")
				}
				mediaType, err := museReferenceMIME(data, part.Header.Get("Content-Type"), part.FileName())
				if err != nil {
					return err
				}
				groups[name] = append(groups[name], museDataURL(data, mediaType))
			}
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, museMaxTextBytes+1))
		if err != nil || len(data) > museMaxTextBytes {
			return errors.New("invalid Muse edit form field")
		}
		value := string(data)
		switch name {
		case "model":
			r.Model = value
		case "prompt":
			r.Prompt = value
		case "size":
			r.Size = value
		case "aspect_ratio":
			r.AspectRatio = value
		case "extra":
			r.Extra = value
		case "response_format":
			r.ResponseFormat = value
		case "timeout":
			r.Timeout, err = strconv.Atoi(value)
		case "n":
			r.N, err = strconv.Atoi(value)
		case "async":
			switch strings.ToLower(value) {
			case "yes", "on", "y", "t":
				r.Async = true
			case "no", "off", "n", "f":
				r.Async = false
			default:
				r.Async, err = strconv.ParseBool(value)
			}
		}
		if err != nil {
			return errors.New("invalid Muse edit form option")
		}
	}
	for _, name := range []string{"image", "image[]", "video", "audio", "file"} {
		r.Images = append(r.Images, groups[name]...)
	}
	return nil
}

// PrepareReferences sends the same bytes to Muse that were checked locally.
// The public API never asks the upstream to fetch a URL or read a local path.
func (r *MuseMediaRequest) PrepareReferences(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	var moderationImages []string
	total := 0
	for i, value := range r.references {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		dataURL, mediaType, size, err := normalizeMuseReference(ctx, value)
		if err != nil {
			return nil, fmt.Errorf("Muse reference %d: %w", i+1, err)
		}
		total += size
		if total > museMaxReferenceTotal {
			return nil, errors.New("Muse references exceed 100 MiB in total")
		}
		r.Images = append(r.Images, dataURL)
		if strings.HasPrefix(mediaType, "image/") {
			moderationImages = append(moderationImages, dataURL)
		}
	}
	r.references = nil
	return moderationImages, nil
}

func normalizeMuseReference(ctx context.Context, value string) (string, string, int, error) {
	var declared, name string
	var data []byte
	var err error
	var encoded string
	switch {
	case strings.HasPrefix(value, "http://"), strings.HasPrefix(value, "https://"):
		data, declared, name, err = fetchMuseReference(ctx, value)
	case strings.HasPrefix(value, "data:"):
		header, payload, found := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
		if !found {
			return "", "", 0, errors.New("invalid data URL")
		}
		declared, _, _ = strings.Cut(header, ";")
		if strings.Contains(strings.ToLower(header), ";base64") {
			encoded = strings.TrimSpace(payload)
			data, err = decodeMuseReferenceBase64(encoded)
		} else if len(payload) <= 3*museMaxReferenceBytes {
			var decoded string
			decoded, err = url.PathUnescape(payload)
			data = []byte(decoded)
		} else {
			err = errMuseReferenceSize
		}
	default:
		encoded = value
		data, err = decodeMuseReferenceBase64(encoded)
	}
	if err != nil {
		return "", "", 0, err
	}
	if len(data) > museMaxReferenceBytes {
		return "", "", 0, errMuseReferenceSize
	}
	mediaType, err := museReferenceMIME(data, declared, name)
	if err != nil {
		return "", "", 0, err
	}
	if encoded != "" {
		prefix := "data:" + mediaType + ";base64,"
		if len(value) == len(prefix)+len(encoded) && strings.HasPrefix(value, prefix) {
			return value, mediaType, len(data), nil
		}
		return prefix + encoded, mediaType, len(data), nil
	}
	return museDataURL(data, mediaType), mediaType, len(data), nil
}

func decodeMuseReferenceBase64(encoded string) ([]byte, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(museMaxReferenceBytes) {
		return nil, errMuseReferenceSize
	}
	if encoded == "" || strings.ContainsAny(encoded, "\r\n") {
		return nil, errors.New("use a public HTTP(S) URL, a data URL or valid Base64; local paths are not supported")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("use a public HTTP(S) URL, a data URL or valid Base64; local paths are not supported")
	}
	return data, nil
}

func museDataURL(data []byte, mediaType string) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}
