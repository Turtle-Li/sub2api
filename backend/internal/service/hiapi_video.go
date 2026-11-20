package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
)

const hiAPIVideoTasksURL = hiAPIVideoOrigin + "/v1/tasks"

var (
	hiAPINativeVideoID         = regexp.MustCompile(`^tk-hiapi-[A-Za-z0-9_-]{8,128}$`)
	errHiAPIVideoNotDispatched = errors.New("HiAPI video request was not dispatched")
)

type HiAPIVideoError struct {
	Status   int
	Code     string
	Message  string
	rejected bool
}

func (e *HiAPIVideoError) Error() string { return e.Message }

type hiAPIVideoReceipt struct {
	ID     string `json:"taskId"`
	Model  string `json:"model"`
	Status string `json:"status"`
	Output []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"output"`
}

func (r hiAPIVideoReceipt) publicID() string {
	digest := sha256.Sum256([]byte(r.ID))
	return "hav_" + hex.EncodeToString(digest[:16])
}

func (r hiAPIVideoReceipt) videoURL() string {
	if r.Status != "success" {
		return ""
	}
	for _, output := range r.Output {
		if output.Type == "video" && validHiAPIVideoURL(output.URL) {
			return output.URL
		}
	}
	return ""
}

func decodeHiAPIVideoReceipt(reader io.Reader, expectedID string) (hiAPIVideoReceipt, error) {
	var envelope struct {
		Code int               `json:"code"`
		Data hiAPIVideoReceipt `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(reader, 256<<10)).Decode(&envelope); err != nil ||
		envelope.Code != http.StatusOK || !hiAPINativeVideoID.MatchString(envelope.Data.ID) {
		return hiAPIVideoReceipt{}, errors.New("HiAPI returned an invalid video receipt")
	}
	if expectedID != "" {
		if envelope.Data.ID != expectedID || envelope.Data.Model != HiAPISeedanceMiniModel {
			return hiAPIVideoReceipt{}, errors.New("HiAPI returned a mismatched video task")
		}
		switch envelope.Data.Status {
		case "queued", "handling", "archiving", "success", "fail":
		default:
			return hiAPIVideoReceipt{}, errors.New("HiAPI returned an unknown video status")
		}
	}
	return envelope.Data, nil
}

func (s *OpenAIGatewayService) sendHiAPIVideo(ctx context.Context, account *Account, nativeID, idempotencyKey string, body []byte) (hiAPIVideoReceipt, error) {
	if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityHiAPIVideo) ||
		(nativeID != "" && !hiAPINativeVideoID.MatchString(nativeID)) {
		return hiAPIVideoReceipt{}, errHiAPIVideoNotDispatched
	}
	if _, err := s.validateUpstreamBaseURL(hiAPIVideoOrigin); err != nil {
		return hiAPIVideoReceipt{}, errHiAPIVideoNotDispatched
	}
	proxy, err := ResolveAccountProxyURL(account)
	if err != nil {
		return hiAPIVideoReceipt{}, errHiAPIVideoNotDispatched
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil || token == "" {
		return hiAPIVideoReceipt{}, errHiAPIVideoNotDispatched
	}
	method, target := http.MethodPost, hiAPIVideoTasksURL
	if nativeID != "" {
		method, target = http.MethodGet, hiAPIVideoTasksURL+"/"+nativeID
	}
	ctx = WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx))
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return hiAPIVideoReceipt{}, errHiAPIVideoNotDispatched
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)
		req.GetBody = nil // No transport replay; this key identifies this reservation at HiAPI.
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return hiAPIVideoReceipt{}, errors.New("HiAPI video connection failed; do not resubmit an uncertain attempt")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return hiAPIVideoReceipt{}, classifyHiAPIVideoError(resp.StatusCode)
	}
	return decodeHiAPIVideoReceipt(resp.Body, nativeID)
}

func classifyHiAPIVideoError(status int) error {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
		return &HiAPIVideoError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "HiAPI rejected the video parameters or frame URLs", rejected: true}
	case http.StatusUnauthorized, http.StatusForbidden:
		return &HiAPIVideoError{Status: http.StatusBadGateway, Code: "upstream_access_rejected", Message: "HiAPI rejected access for the upstream account", rejected: true}
	case http.StatusPaymentRequired:
		return &HiAPIVideoError{Status: http.StatusServiceUnavailable, Code: "upstream_balance_exhausted", Message: "The HiAPI account has insufficient balance", rejected: true}
	case http.StatusNotFound:
		return &HiAPIVideoError{Status: http.StatusNotFound, Code: "not_found_error", Message: "HiAPI video model or task is unavailable", rejected: true}
	case http.StatusTooManyRequests:
		return &HiAPIVideoError{Status: http.StatusTooManyRequests, Code: "rate_limit_error", Message: "The HiAPI account is rate limited", rejected: true}
	default:
		return errors.New("HiAPI video outcome is uncertain; do not resubmit automatically")
	}
}

func (s *OpenAIGatewayService) WriteHiAPIVideoContent(ctx context.Context, c *gin.Context, account *Account, job *DurableVideoJob) error {
	if job.State != "completed" || !hiAPINativeVideoID.MatchString(job.NativeID) {
		return &HiAPIVideoError{Status: http.StatusConflict, Code: "video_not_ready", Message: "The HiAPI video is not available for download"}
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	receipt, err := s.sendHiAPIVideo(ctx, account, job.NativeID, "", nil)
	if err != nil {
		return err
	}
	videoURL := receipt.videoURL()
	if videoURL == "" {
		return errors.New("HiAPI video output is not available")
	}
	if _, err := s.validateOutboundURL(videoURL); err != nil {
		return errors.New("HiAPI video output is unavailable under the outbound URL policy")
	}
	proxy, err := ResolveAccountProxyURL(account)
	if err != nil {
		return errors.New("HiAPI video account proxy is unavailable")
	}
	ctx = WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return errors.New("invalid HiAPI video output URL")
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return errors.New("HiAPI video output could not be downloaded")
	}
	defer func() { _ = resp.Body.Close() }()
	return writeBoundVideoMP4(c, resp)
}
