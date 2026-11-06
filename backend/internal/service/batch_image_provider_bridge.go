package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type bridgeImageExecutor struct {
	store    BatchImageProviderObjectStore
	gateway  *OpenAIGatewayService
	runner   imageBridgeRunner
	provider string
	models   map[string]bridgeModel
}

func newBridgeImagesProvider(cfg *config.Config, store BatchImageProviderObjectStore, gateway *OpenAIGatewayService, provider string) *OpenAIImagesBatchProvider {
	e := &bridgeImageExecutor{store: store, gateway: gateway, provider: provider, models: make(map[string]bridgeModel)}
	if cfg != nil && cfg.BatchImage.PythonBridgeExecutable != "" {
		e.runner.executable = cfg.BatchImage.PythonBridgeExecutable
		caps, err := e.runner.run(context.Background(), bridgeRequest{Provider: e.nativeProvider(), Operation: "capabilities"}, "")
		if err == nil && caps.State == "completed" && caps.Provider == e.nativeProvider() && len(caps.Models) > 0 && len(caps.Models) <= 8 {
			valid := true
			for _, m := range caps.Models {
				if !validBridgeModel(m, provider) {
					valid = false
					break
				}
				for _, s := range m.Sizes {
					if !slices.Contains(m.ImageSizes, s.ImageSize) || s.Size == "" || s.AspectRatio == "" {
						valid = false
					}
					if !slices.Contains(m.AspectRatios, s.AspectRatio) {
						m.AspectRatios = append(m.AspectRatios, s.AspectRatio)
					}
				}
				e.models[m.Model] = m
			}
			if !valid {
				clear(e.models)
			}
		}
	}
	p := NewOpenAIImagesBatchProvider(store, e, batchImageOpenAIStoragePrefix(cfg))
	p.providerName = provider
	return p
}
func (e *bridgeImageExecutor) nativeProvider() string {
	if e.provider == BatchImageProviderWan {
		return "wan"
	}
	return "seedream"
}
func (e *bridgeImageExecutor) available() bool {
	info, err := os.Stat(e.runner.executable)
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0 && e.gateway != nil && e.gateway.concurrencyService != nil && len(e.models) > 0
}
func (e *bridgeImageExecutor) validate(r OpenAIImageBatchItemRequest) error {
	m, ok := e.models[r.Model]
	if !ok || strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(r.Prompt) > 6000 || len(r.ReferenceImages) > m.MaxReferenceImages || m.size(r.ImageSize, r.AspectRatio) == "" || (r.ResponseMimeType != "" && !slices.Contains(m.ResponseMimeTypes, r.ResponseMimeType)) {
		return ErrBatchImageProviderInvalidInput
	}
	for _, ref := range r.ReferenceImages {
		if ref.FileURI != "" || len(ref.Data) == 0 || !slices.Contains([]string{"image/png", "image/jpeg", "image/webp"}, ref.MimeType) {
			return ErrBatchImageProviderInvalidInput
		}
	}
	return nil
}
func (e *bridgeImageExecutor) Execute(ctx context.Context, a *Account, r OpenAIImageBatchItemRequest, before func() error) (*OpenAIImageBatchItemResult, error) {
	if err := e.validate(r); err != nil {
		return nil, err
	}
	m := e.models[r.Model]
	refs := make([]string, 0, len(r.ReferenceImages))
	for _, ref := range r.ReferenceImages {
		refs = append(refs, "data:"+ref.MimeType+";base64,"+base64.StdEncoding.EncodeToString(ref.Data))
	}
	digest := sha256.Sum256([]byte(r.StateKey))
	req := bridgeRequest{Provider: e.nativeProvider(), Operation: "submit", Model: r.Model, Prompt: r.Prompt, References: &refs, Parameters: &bridgeParameters{Size: m.size(r.ImageSize, r.AspectRatio), Profile: m.ProfileVersion, N: 1}, RequestKey: hex.EncodeToString(digest[:])}
	response, err := e.call(ctx, a, req, before)
	if err != nil {
		return nil, err
	}
	if e.provider == BatchImageProviderWan {
		if response.State != "accepted" || !bridgeTaskID.MatchString(response.TaskID) {
			return nil, errors.New("bridge submission outcome unknown")
		}
		receipt := []byte(response.TaskID)
		if err := e.store.Put(ctx, r.StateKey, "text/plain", bytes.NewReader(receipt), int64(len(receipt))); err != nil {
			return nil, errors.New("bridge receipt persistence failed; outcome unknown")
		}
		return nil, errImageBatchItemPending
	}
	return bridgeImageResult(response)
}

var bridgeTaskID = regexp.MustCompile(`^[A-Za-z0-9-]{8,100}$`)

func (e *bridgeImageExecutor) Resume(ctx context.Context, a *Account, r OpenAIImageBatchItemRequest) (*OpenAIImageBatchItemResult, error) {
	if e.provider != BatchImageProviderWan {
		return nil, errors.New("synchronous bridge attempt outcome unknown; replay forbidden")
	}
	exists, err := e.store.Exists(ctx, r.StateKey)
	if err != nil {
		return nil, errImageBatchItemPending
	}
	if !exists {
		return nil, errors.New("bridge attempt outcome unknown; no task receipt")
	}
	body, size, _, err := e.store.Open(ctx, r.StateKey)
	if err != nil {
		return nil, errImageBatchItemPending
	}
	defer body.Close()
	if size < 1 || size > 128 {
		return nil, errors.New("invalid bridge receipt")
	}
	receipt, err := io.ReadAll(io.LimitReader(body, 129))
	if err != nil || !bridgeTaskID.Match(receipt) {
		return nil, errors.New("invalid bridge receipt")
	}
	response, err := e.call(ctx, a, bridgeRequest{Provider: e.nativeProvider(), Operation: "collect", TaskID: string(receipt)}, nil)
	if err != nil {
		return nil, errImageBatchItemPending
	}
	if response.State == "accepted" || response.State == "outcome_unknown" {
		return nil, errImageBatchItemPending
	}
	return bridgeImageResult(response)
}
func bridgeImageResult(response *bridgeResponse) (*OpenAIImageBatchItemResult, error) {
	if response.State != "completed" || len(response.Images) != 1 {
		return nil, errors.New("bridge did not return one confirmed image")
	}
	result, err := decodeMuseImage(response.Images[0].Base64)
	if err != nil {
		return nil, err
	}
	if result.MimeType != response.Images[0].MimeType {
		return nil, errors.New("bridge image MIME mismatch")
	}
	return result, nil
}
func (e *bridgeImageExecutor) call(ctx context.Context, a *Account, req bridgeRequest, before func() error) (*bridgeResponse, error) {
	if !e.available() || a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	if req.Operation == "submit" && !batchImageAccountSupportsProviderModel(e.provider, a, req.Model) {
		return nil, ErrBatchImageProviderInvalidInput
	}
	proxy, err := ResolveAccountProxyURL(a)
	if err != nil {
		return nil, err
	}
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, errors.New("bridge account proxy protocol unsupported")
		}
	}
	slot, err := e.gateway.tryAcquireAccountSlot(ctx, a.ID, a.Concurrency)
	if err != nil {
		return nil, err
	}
	if slot == nil || !slot.Acquired {
		return nil, errBatchImageOpenAIAccountBusy
	}
	if slot.ReleaseFunc != nil {
		defer slot.ReleaseFunc()
	}
	req.Secret = &bridgeSecret{APIKey: batchImageProviderAPIKey(a), Endpoint: a.GetCredential("base_url"), Timeout: 120}
	if before != nil {
		if err := before(); err != nil {
			return nil, err
		}
	}
	return e.runner.run(ctx, req, proxy)
}
