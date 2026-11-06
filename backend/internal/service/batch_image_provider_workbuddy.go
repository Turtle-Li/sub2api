package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type workBuddyImageExecutor struct{ gateway *OpenAIGatewayService }

func newWorkBuddyImagesProvider(store BatchImageProviderObjectStore, prefix string, gateway *OpenAIGatewayService) *OpenAIImagesBatchProvider {
	p := NewOpenAIImagesBatchProvider(store, &workBuddyImageExecutor{gateway: gateway}, prefix)
	p.providerName = BatchImageProviderWorkBuddy
	return p
}

func (e *workBuddyImageExecutor) Execute(ctx context.Context, a *Account, r OpenAIImageBatchItemRequest, before func() error) (*OpenAIImageBatchItemResult, error) {
	if !batchImageAccountSupportsProviderModel(BatchImageProviderWorkBuddy, a, r.Model) {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	if err := validateWorkBuddyImageRequest(r); err != nil {
		return nil, err
	}
	if e.gateway == nil || e.gateway.httpUpstream == nil || e.gateway.concurrencyService == nil {
		return nil, errors.New("WorkBuddy runtime unavailable")
	}
	if _, err := ResolveAccountProxyURL(a); err != nil {
		return nil, err
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
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), 10*time.Minute)
	defer cancel()
	endpoint, err := url.Parse(strings.TrimRight(strings.TrimSpace(a.GetCredential("base_url")), "/"))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, ErrBatchImageProviderInvalidInput
	}
	operation := "generations"
	payload := struct {
		Model          string   `json:"model"`
		Prompt         string   `json:"prompt"`
		N              int      `json:"n"`
		ResponseFormat string   `json:"response_format"`
		Image          []string `json:"image,omitempty"`
	}{Model: r.Model, Prompt: r.Prompt, N: 1, ResponseFormat: "b64_json"}
	for _, ref := range r.ReferenceImages {
		operation = "edits"
		payload.Image = append(payload.Image, "data:"+ref.MimeType+";base64,"+base64.StdEncoding.EncodeToString(ref.Data))
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/v1") + "/v1/images/" + operation
	endpoint.RawPath = ""
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+batchImageProviderAPIKey(a))
	req.Header.Set("Content-Type", "application/json")
	if before != nil {
		if err := before(); err != nil {
			return nil, err
		}
	}
	resp, err := e.gateway.doOpenAIModelUpstream(req, "", a)
	if err != nil {
		return nil, errors.New("WorkBuddy submission outcome unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("WorkBuddy image request failed")
	}
	limit := int64(base64.StdEncoding.EncodedLen(museImageMaxBytes) + 65536)
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("WorkBuddy image response invalid")
	}
	var result struct {
		Data []struct {
			Base64 string `json:"b64_json"`
			URL    string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil || len(result.Data) != 1 {
		return nil, errors.New("WorkBuddy image response invalid")
	}
	encoded := result.Data[0].Base64
	if encoded == "" {
		encoded, err = e.gateway.fetchOpenAIImageURLBase64(ctx, a, result.Data[0].URL)
		if err != nil {
			return nil, errors.New("WorkBuddy image download failed")
		}
	}
	return decodeMuseImage(encoded)
}
