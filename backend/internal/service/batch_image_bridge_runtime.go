package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type bridgeSize struct {
	ImageSize   string `json:"image_size"`
	AspectRatio string `json:"aspect_ratio"`
	Size        string `json:"size"`
}
type BatchImageModelCapabilities struct {
	ImageSizes         []string `json:"image_sizes"`
	AspectRatios       []string `json:"aspect_ratios"`
	DefaultImageSize   string   `json:"default_image_size"`
	DefaultAspectRatio string   `json:"default_aspect_ratio"`
	MaxReferenceImages int      `json:"max_reference_images"`
	ResponseMimeTypes  []string `json:"response_mime_types"`
}
type bridgeModel struct {
	Model          string `json:"model"`
	ProfileVersion string `json:"profile_version"`
	BatchImageModelCapabilities
	Sizes []bridgeSize `json:"sizes"`
}
type bridgeResponse struct {
	State      string        `json:"state"`
	Provider   string        `json:"provider"`
	Models     []bridgeModel `json:"models"`
	TaskID     string        `json:"task_id"`
	TaskStatus string        `json:"task_status"`
	Images     []struct {
		MimeType string `json:"mime_type"`
		Base64   string `json:"b64_json"`
	} `json:"images"`
}
type bridgeSecret struct {
	APIKey   string `json:"api_key"`
	Endpoint string `json:"endpoint_url"`
	Timeout  int    `json:"timeout_seconds"`
}
type bridgeParameters struct {
	Size    string `json:"size"`
	Profile string `json:"profile_version"`
	N       int    `json:"n"`
}
type bridgeRequest struct {
	Provider   string            `json:"provider"`
	Operation  string            `json:"operation"`
	Model      string            `json:"model,omitempty"`
	Prompt     string            `json:"prompt,omitempty"`
	References *[]string         `json:"references,omitempty"`
	Parameters *bridgeParameters `json:"parameters,omitempty"`
	RequestKey string            `json:"request_key,omitempty"`
	TaskID     string            `json:"task_id,omitempty"`
	Secret     *bridgeSecret     `json:"secret,omitempty"`
}
type imageBridgeRunner struct{ executable string }
type bridgeLimitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *bridgeLimitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("bridge output limit")
	}
	return b.Buffer.Write(p)
}
func (r imageBridgeRunner) run(ctx context.Context, request bridgeRequest, proxy string) (*bridgeResponse, error) {
	if !filepath.IsAbs(r.executable) {
		return nil, errors.New("bridge executable unavailable")
	}
	info, err := os.Stat(r.executable)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		return nil, errors.New("bridge executable unavailable")
	}
	input, err := json.Marshal(request)
	if err != nil || len(input) > 48*1024*1024 {
		return nil, ErrBatchImageProviderInvalidInput
	}
	timeout := 130 * time.Second
	if request.Operation == "capabilities" {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.executable, "-m", "sub2_image_bridge")
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = os.Environ()
	if proxy != "" {
		env := make([]string, 0, len(cmd.Env))
		for _, entry := range cmd.Env {
			key, _, _ := strings.Cut(entry, "=")
			switch strings.ToUpper(key) {
			case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
				continue
			}
			env = append(env, entry)
		}
		cmd.Env = append(env, "HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy, "ALL_PROXY="+proxy, "NO_PROXY=")
	}
	output := &bridgeLimitedBuffer{limit: 34 * 1024 * 1024}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, errors.New("bridge process outcome unknown")
	}
	var response bridgeResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		return nil, errors.New("bridge response outcome unknown")
	}
	return &response, nil
}
func imageBridgeProviderForModel(model string) string {
	switch model {
	case "wan2.7-image", "wan2.7-image-pro":
		return BatchImageProviderWan
	case "doubao-seedream-5-0-lite-260128", "doubao-seedream-5-0-pro-260628":
		return BatchImageProviderSeedream
	}
	return ""
}
func isImageBridgeProvider(provider string) bool {
	return provider == BatchImageProviderWan || provider == BatchImageProviderSeedream
}
func (m bridgeModel) size(tier, ratio string) string {
	for _, s := range m.Sizes {
		if s.ImageSize == tier && s.AspectRatio == ratio {
			return s.Size
		}
	}
	return ""
}

var bridgeNativeSize = regexp.MustCompile(`^[1-9][0-9]{2,4}[*x][1-9][0-9]{2,4}$`)

func validBridgeModel(m bridgeModel, provider string) bool {
	profile := "4"
	if provider == BatchImageProviderWan {
		profile = "2"
	}
	if imageBridgeProviderForModel(m.Model) != provider || m.ProfileVersion != profile || m.MaxReferenceImages < 0 || m.MaxReferenceImages > 14 || len(m.ImageSizes) == 0 || len(m.ImageSizes) > 3 || len(m.Sizes) == 0 || len(m.Sizes) > 100 || m.DefaultImageSize != "2K" || m.DefaultAspectRatio != "1:1" || m.size(m.DefaultImageSize, m.DefaultAspectRatio) == "" || len(m.ResponseMimeTypes) == 0 || len(m.ResponseMimeTypes) > 3 {
		return false
	}
	for _, mime := range m.ResponseMimeTypes {
		if !slices.Contains([]string{"image/png", "image/jpeg", "image/webp"}, mime) {
			return false
		}
	}
	for _, tier := range m.ImageSizes {
		if !slices.Contains([]string{"1K", "2K", "4K"}, tier) {
			return false
		}
	}
	seen := make(map[string]bool)
	for _, size := range m.Sizes {
		key := size.ImageSize + "/" + size.AspectRatio
		if seen[key] || !slices.Contains(m.ImageSizes, size.ImageSize) || !bridgeNativeSize.MatchString(size.Size) || !slices.Contains([]string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9"}, size.AspectRatio) {
			return false
		}
		seen[key] = true
	}
	return true
}
