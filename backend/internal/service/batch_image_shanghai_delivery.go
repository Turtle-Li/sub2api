package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/shanghaidelivery"
)

const (
	BatchImageShanghaiSourceRef  = "studio-local-checkpoint:shanghai:v1"
	BatchImageShanghaiAckedRef   = "studio-local-checkpoint:shanghai:v1:acked"
	batchImageShanghaiTimeout    = 45 * time.Second
	batchImageShanghaiSigningTTL = time.Minute
)

var errShanghaiMiniWon = errors.New("Shanghai already has the Mini result")

func batchImageUsesShanghai(job *BatchImageJob) bool {
	return job != nil && batchImageDerefString(job.ProviderOutputRef) == BatchImageShanghaiSourceRef
}

type batchImageShanghaiSigner interface {
	PresignPut(context.Context, string, time.Duration) (string, error)
	PresignHead(context.Context, string, time.Duration) (string, error)
}

func batchImageShanghaiCanary(cfg *config.Config, job *BatchImageJob) bool {
	return cfg != nil && batchImageShanghaiJobShape(job) && cfg.BatchImage.ShanghaiDeliveryURL != "" &&
		cfg.BatchImage.ShanghaiDeliveryCanaryUserID > 0 && job.UserID == cfg.BatchImage.ShanghaiDeliveryCanaryUserID
}

func batchImageShanghaiJobShape(job *BatchImageJob) bool {
	return job != nil && job.StudioGroupID != nil && *job.StudioGroupID > 0 &&
		job.APIKeyID != nil && *job.APIKeyID == ImageStudioWalletOwnerSlot &&
		job.Provider == BatchImageProviderOpenAI && job.ItemCount == 1 &&
		job.ImageSize == ImageBillingSize2K &&
		(job.Model == "gpt-image-2.6-flare" || job.Model == "gpt-image-2.6-sunburst")
}

var batchImageShanghaiHTTP = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 4 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = batchImageShanghaiTimeout
	return &http.Client{Transport: transport, Timeout: batchImageShanghaiTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}()

type batchImageShanghaiSlot struct {
	endpoint string
	id       string
	client   *http.Client
}

func batchImageShanghaiSlotFor(cfg *config.Config, batchID, customID string) (*batchImageShanghaiSlot, error) {
	if cfg == nil || shanghaidelivery.ValidateControlURL(cfg.BatchImage.ShanghaiDeliveryURL) != nil {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_CONTROL_NOT_CONFIGURED", nil)
	}
	key, err := batchImageUpscaleObjectKey(cfg, batchID, customID, 0, "png")
	if err != nil {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_SLOT_INVALID", err)
	}
	id := shanghaidelivery.SlotID(key)
	return &batchImageShanghaiSlot{endpoint: cfg.BatchImage.ShanghaiDeliveryURL + "/v1/slots/" + id, id: id, client: batchImageShanghaiHTTP}, nil
}

type shanghaiHTTPStatus int

func (s shanghaiHTTPStatus) Error() string { return fmt.Sprintf("Shanghai controller status %d", s) }

func (s *batchImageShanghaiSlot) request(ctx context.Context, method, subpath string, body []byte, contentType string, out any) error {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.endpoint+subpath, r)
	if err != nil {
		return errors.New("Shanghai controller request invalid")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if strings.HasPrefix(subpath, "/data") {
		req.Header.Set("Expect", "100-continue")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("Shanghai controller transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return shanghaiHTTPStatus(resp.StatusCode)
	}
	if out != nil {
		decoder := json.NewDecoder(io.LimitReader(resp.Body, 16384))
		if decoder.Decode(out) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
			return errors.New("Shanghai controller response invalid")
		}
	}
	return nil
}

func (s *batchImageShanghaiSlot) send(ctx context.Context, method, subpath string, input, output any) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return s.request(ctx, method, subpath, payload, "application/json", output)
}

func (s *batchImageShanghaiSlot) status(ctx context.Context) (shanghaidelivery.Slot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var slot shanghaidelivery.Slot
	if err := s.request(ctx, http.MethodGet, "", nil, "", &slot); err != nil {
		return slot, batchImageDeliveryError("shanghai", "SHANGHAI_SLOT_UNAVAILABLE", err)
	}
	if (slot.Image != nil && !validShanghaiImage(slot.Image)) || (slot.Published && slot.Image == nil) ||
		(slot.MiniJobID != "" && !(shanghaidelivery.MiniJob{ID: slot.MiniJobID}).Valid()) {
		return slot, batchImageDeliveryError("shanghai", "SHANGHAI_SLOT_INVALID", nil)
	}
	return slot, nil
}

type BatchImageShanghaiAcknowledgement struct {
	BatchID, CustomID string
}

type batchImageShanghaiAcknowledgementStore interface {
	ListBatchImageShanghaiAcknowledgements(context.Context, int) ([]BatchImageShanghaiAcknowledgement, error)
	ConfirmBatchImageShanghaiAcknowledgement(context.Context, string) error
}

func reconcileBatchImageShanghaiAcknowledgements(ctx context.Context, cfg *config.Config, store batchImageShanghaiAcknowledgementStore, alerts *OperationalAlerts) {
	if cfg == nil || cfg.BatchImage.ShanghaiDeliveryURL == "" || store == nil {
		return
	}
	scan, done := context.WithTimeout(ctx, 3*time.Second)
	pending, err := store.ListBatchImageShanghaiAcknowledgements(scan, 16)
	done()
	if err != nil {
		slog.WarnContext(ctx, "batch_image.shanghai_ack_list_failed")
		return
	}
	for _, item := range pending {
		if ctx.Err() != nil {
			return
		}
		slot, err := batchImageShanghaiSlotFor(cfg, item.BatchID, item.CustomID)
		if err == nil {
			op, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = slot.request(op, http.MethodDelete, "", nil, "", nil)
			if err == nil {
				err = store.ConfirmBatchImageShanghaiAcknowledgement(op, item.BatchID)
			}
			cancel()
		}
		if err != nil {
			alerts.observeBatchImage(ctx, item.BatchID, "shanghai_cleanup", "SHANGHAI_ACK_UNCONFIRMED")
		}
	}
}

func validShanghaiImage(img *shanghaidelivery.Image) bool {
	if img == nil || img.Bytes <= 0 || img.Bytes > shanghaidelivery.MaxImage || !shanghaidelivery.ValidDimensions(img.Width, img.Height) ||
		shanghaidelivery.Extension(img.MimeType) == "" || len(img.SHA256) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(img.SHA256)
	return err == nil
}

func (s *batchImageShanghaiSlot) prepare(ctx context.Context, width, height int) (imageUpscaleSubmitOptions, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var grant shanghaidelivery.Grant
	if err := s.send(ctx, http.MethodPost, "/prepare", shanghaidelivery.Prepare{Width: width, Height: height}, &grant); err != nil {
		return imageUpscaleSubmitOptions{}, batchImageDeliveryError("shanghai", "SHANGHAI_MINI_GRANT_UNAVAILABLE", err)
	}
	u, err := url.Parse(grant.URL)
	if err != nil || u.User != nil || u.Scheme != "https" || u.Port() != shanghaidelivery.IngressPort ||
		!strings.HasSuffix(u.Hostname(), ".ts.net") || u.Path != "/v1/incoming/"+s.id ||
		u.Fragment != "" || len(u.Query()) != 1 || len(u.Query().Get("ticket")) != 43 {
		return imageUpscaleSubmitOptions{}, batchImageDeliveryError("shanghai", "SHANGHAI_MINI_GRANT_INVALID", nil)
	}
	result := *u
	result.RawQuery = ""
	return imageUpscaleSubmitOptions{resultUploadURL: grant.URL, resultURL: result.String(), noSubmitRetry: true}, nil
}

func (s *batchImageShanghaiSlot) recordMini(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var err error
	for attempt := range 3 {
		attemptCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		err = s.send(attemptCtx, http.MethodPut, "/mini-job", shanghaidelivery.MiniJob{ID: id}, nil)
		stop()
		if err == nil {
			return nil
		}
		if attempt != 2 && sleepWithContext(ctx, time.Duration(attempt+1)*150*time.Millisecond) != nil {
			break
		}
	}
	return batchImageDeliveryError("shanghai", "SHANGHAI_MINI_RECEIPT_UNAVAILABLE", err)
}

func (s *batchImageShanghaiSlot) claimCloud(ctx context.Context) (*ImageUpscaleResult, error) {
	err := s.request(ctx, http.MethodPost, "/cloud", nil, "", nil)
	if err == nil {
		return nil, nil
	}
	// A Mini upload may have won the race. A committed but unacknowledged
	// cloud claim is not permission to issue a second Aliyun call.
	if errors.Is(err, shanghaiHTTPStatus(http.StatusConflict)) {
		slot, readErr := s.status(ctx)
		if readErr == nil && slot.Image != nil {
			return shanghaiImageResult(slot.Image), nil
		}
	}
	return nil, batchImageDeliveryError("shanghai", "SHANGHAI_FALLBACK_CLAIM_UNCONFIRMED", err)
}

func shanghaiImageResult(img *shanghaidelivery.Image) *ImageUpscaleResult {
	return &ImageUpscaleResult{MimeType: img.MimeType, Width: img.Width, Height: img.Height, shanghai: img}
}

func (s *batchImageShanghaiSlot) stage(ctx context.Context, checkpoint *BatchImageResultCheckpoint, slot shanghaidelivery.Slot) (*shanghaidelivery.Image, error) {
	digest := sha256.Sum256(checkpoint.Data)
	if len(checkpoint.Data) == 0 || len(checkpoint.Data) > shanghaidelivery.MaxImage {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_STAGE_SIZE_INVALID", nil)
	}
	if slot.Image == nil {
		endpoint := "/data"
		if slot.MiniJobID != "" && !slot.CloudClaimed {
			endpoint += "?mini_job_id=" + url.QueryEscape(slot.MiniJobID)
		}
		if err := s.request(ctx, http.MethodPut, endpoint, checkpoint.Data, checkpoint.MimeType, &slot); err != nil {
			return nil, batchImageDeliveryError("shanghai", "SHANGHAI_STAGE_FAILED", err)
		}
	}
	if !validShanghaiImage(slot.Image) || slot.Image.SHA256 != hex.EncodeToString(digest[:]) ||
		slot.Image.Bytes != int64(len(checkpoint.Data)) || slot.Image.MimeType != checkpoint.MimeType ||
		slot.Image.Width != checkpoint.Width || slot.Image.Height != checkpoint.Height {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_STAGE_CONFLICT", nil)
	}
	return slot.Image, nil
}

type shanghaiUpscaleAttempt struct {
	slot        *batchImageShanghaiSlot
	state       shanghaidelivery.Slot
	batchID     string
	writeFencer BatchImageUpscaleWriteFencer
	alerts      *OperationalAlerts
}

func (a *shanghaiUpscaleAttempt) withWrite(ctx context.Context, timeout time.Duration, run func(context.Context) error) error {
	if a.writeFencer == nil {
		return batchImageDeliveryError("shanghai", "SHANGHAI_WRITE_FENCE_UNAVAILABLE", nil)
	}
	op, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	permit, err := a.writeFencer.BeginBatchImageUpscaleWrite(op, a.batchID)
	if err != nil || permit == nil {
		return batchImageDeliveryError("shanghai", "SHANGHAI_WRITE_FENCE_UNAVAILABLE", err)
	}
	err = run(op)
	return errors.Join(err, permit.Release())
}

func (s *ImageUpscaleService) runShanghaiMini(ctx context.Context, a *shanghaiUpscaleAttempt, source []byte, width, height, scale int) (*ImageUpscaleResult, error) {
	w, h := width*scale, height*scale
	if a.state.MiniFailed {
		return nil, imageUpscaleError("REMOTE_JOB_FAILED", 0, false, nil)
	}
	id := a.state.MiniJobID
	if id == "" && a.state.MiniExpires != nil {
		return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_SUBMIT_UNCONFIRMED", nil)
	}
	key, err := s.loadAPIKey(ctx)
	if err != nil {
		if id != "" {
			return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_POLL_CREDENTIAL_UNAVAILABLE", err)
		}
		a.alerts.observeBatchImage(ctx, a.batchID, "shanghai_mini", "SHANGHAI_MINI_CREDENTIAL_UNAVAILABLE")
		return nil, imageUpscaleError("CREDENTIAL_UNAVAILABLE", 0, false, nil)
	}
	defer clearBytes(key)
	if id == "" {
		if err := s.shanghaiMiniHealth(ctx); err != nil {
			var failure *batchImageDeliveryFailure
			if !errors.As(err, &failure) {
				a.alerts.observeBatchImage(ctx, a.batchID, "shanghai_mini", "SHANGHAI_MINI_UNREACHABLE")
			}
			return nil, err
		}
		var options imageUpscaleSubmitOptions
		if err := a.withWrite(ctx, 5*time.Second, func(op context.Context) error {
			var err error
			options, err = a.slot.prepare(op, w, h)
			return err
		}); err != nil {
			return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_GRANT_UNCONFIRMED", err)
		}
		id, err = s.submitWithOptions(ctx, source, scale, key, options)
		if err != nil {
			var rejection *ImageUpscaleError
			if errors.As(err, &rejection) && rejection.Code == "SUBMIT_RATE_LIMITED" {
				return a.confirmMiniFailure(ctx, "", err)
			}
			return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_SUBMIT_UNCONFIRMED", err)
		}
		if !(shanghaidelivery.MiniJob{ID: id}).Valid() {
			return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_JOB_INVALID", nil)
		}
		if err := a.slot.recordMini(ctx, id); err != nil {
			return nil, err
		}
	}
	details, pollErr := s.waitForCompletionDetails(ctx, id, key)
	state, err := a.slot.status(ctx)
	if err != nil {
		return nil, err
	}
	if state.Image != nil {
		if state.Image.Width != w || state.Image.Height != h {
			return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_DIMENSIONS_INVALID", nil)
		}
		if pollErr != nil {
			a.alerts.observeBatchImage(ctx, a.batchID, "shanghai_mini", "SHANGHAI_MINI_UPLOAD_ACK_LOST")
		}
		return shanghaiImageResult(state.Image), nil
	}
	if details == nil || details.ResultReady == nil || details.ResultUploaded {
		return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_OUTCOME_UNCONFIRMED", pollErr)
	}
	if !*details.ResultReady {
		if upscaleFailureCode(pollErr) == "REMOTE_JOB_FAILED" {
			a.alerts.observeBatchImage(ctx, a.batchID, "shanghai_mini", "SHANGHAI_MINI_FAILED_NO_OUTPUT")
			return a.confirmMiniFailure(ctx, id, pollErr)
		}
		return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_OUTCOME_UNCONFIRMED", pollErr)
	}
	// Only an exceptional recovery sends pixels back to AWS. The same Mini job
	// already has output; failed reads (including 409) are not cloud admission.
	a.alerts.observeBatchImage(ctx, a.batchID, "shanghai_mini", "SHANGHAI_MINI_DIRECT_UPLOAD_FAILED")
	data, contentType, err := s.downloadResult(ctx, id, key)
	if err != nil {
		return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_RESULT_RECOVERY_FAILED", err)
	}
	resultW, resultH, mime, err := decodeUpscaleImageConfig(data)
	if err != nil || resultW != w || resultH != h || (contentType != "" && contentType != mime) {
		clearBytes(data)
		return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_RESULT_INVALID", err)
	}
	return &ImageUpscaleResult{Data: data, MimeType: mime, Width: w, Height: h, Scale: scale}, nil
}

func (a *shanghaiUpscaleAttempt) confirmMiniFailure(ctx context.Context, id string, failure error) (*ImageUpscaleResult, error) {
	err := a.withWrite(ctx, 5*time.Second, func(op context.Context) error {
		return a.slot.send(op, http.MethodPut, "/mini-failed", shanghaidelivery.MiniJob{ID: id}, nil)
	})
	if err != nil {
		return nil, batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_FAILURE_UNCONFIRMED", err)
	}
	return nil, failure
}

func (s *ImageUpscaleService) shanghaiMiniHealth(ctx context.Context) error {
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probe, http.MethodGet, s.endpoint("/health"), nil)
	if err != nil {
		return batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_RECOVERY_UNSUPPORTED", nil)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return imageUpscaleError("MINI_HEALTH_UNREACHABLE", 0, true, nil)
	}
	if resp.StatusCode >= 500 {
		_ = resp.Body.Close()
		return imageUpscaleError("MINI_HEALTH_UNREACHABLE", resp.StatusCode, true, nil)
	}
	var health struct {
		DirectUploadRecovery bool  `json:"direct_upload_recovery"`
		ComfyUp              *bool `json:"comfy_up"`
	}
	body, err := readBoundedResponse(resp, 16<<10)
	if err != nil || resp.StatusCode != http.StatusOK || json.Unmarshal(body, &health) != nil || !health.DirectUploadRecovery {
		return batchImageDeliveryError("shanghai_mini", "SHANGHAI_MINI_RECOVERY_UNSUPPORTED", nil)
	}
	if health.ComfyUp != nil && !*health.ComfyUp {
		return imageUpscaleError("MINI_COMFY_UNAVAILABLE", 0, true, nil)
	}
	return nil
}

func (i *BatchImageResultIndexer) indexShanghai(ctx context.Context, job *BatchImageJob, expected map[string]struct{}, d batchImageUpscaleDelivery) (*BatchImageIndexResult, error) {
	if len(expected) != 1 || d.checkpoints == nil || !batchImageShanghaiJobShape(job) {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_JOB_SCOPE_INVALID", nil)
	}
	var customID string
	for customID = range expected {
	}
	slot, err := batchImageShanghaiSlotFor(d.config, job.BatchID, customID)
	if err != nil {
		return nil, err
	}
	state, err := slot.status(ctx)
	if err != nil {
		return nil, err
	}
	image := state.Image
	if image == nil {
		attempt := &shanghaiUpscaleAttempt{slot: slot, state: state, batchID: job.BatchID, writeFencer: d.writeFencer, alerts: i.Alerts}
		image, err = d.stageShanghaiResult(ctx, job, customID, attempt)
		if err != nil {
			return nil, err
		}
	}
	output, err := d.publishShanghai(ctx, job, customID, slot, image)
	if err != nil {
		return nil, err
	}
	d.thumbnails.run(ctx, job, func(previewCtx context.Context) error {
		data, err := slot.preview(previewCtx)
		if err != nil {
			i.Alerts.observeBatchImage(previewCtx, job.BatchID, "shanghai_preview", "SHANGHAI_PREVIEW_UNAVAILABLE")
			return err
		}
		err = d.thumbnails.service.persistEncoded(previewCtx, job, customID, data)
		if err != nil {
			i.Alerts.observeBatchImage(previewCtx, job.BatchID, "shanghai_preview", "SHANGHAI_PREVIEW_PERSIST_FAILED")
		}
		return err
	})
	now := time.Now()
	return i.commitItems(ctx, job, []CreateBatchImageItemParams{{
		JobID: job.BatchID, CustomID: customID, Status: BatchImageItemStatusSuccess,
		ProviderSourceObject: batchImageOptionalStringPtr(batchImageUpscaleMarker),
		MimeType:             batchImageOptionalStringPtr(output.mimeType), FileExtension: batchImageOptionalStringPtr(output.extension),
		ImageCount: output.imageCount, PreviewSize: output.previewSize, IndexedAt: &now,
	}}, &BatchImageIndexResult{SuccessCount: 1, TotalCount: 1})
}

func (s *batchImageShanghaiSlot) preview(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/thumbnail", nil)
	if err != nil {
		return nil, errors.New("Shanghai preview request invalid")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, errors.New("Shanghai preview transport failed")
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
		_ = resp.Body.Close()
		return nil, errors.New("Shanghai preview unavailable")
	}
	return readBoundedResponse(resp, studioThumbnailMaxStoredBytes)
}

func (d batchImageUpscaleDelivery) stageShanghaiResult(ctx context.Context, job *BatchImageJob, customID string, a *shanghaiUpscaleAttempt) (*shanghaidelivery.Image, error) {
	final, err := d.checkpoints.GetBatchImageCheckpoint(ctx, job.BatchID, customID, BatchImageCheckpointFinal)
	if err != nil {
		return nil, batchImageDeliveryError("final_checkpoint", "IMAGE_FINAL_CHECKPOINT_READ_FAILED", err)
	}
	if final == nil {
		if a.state.CloudClaimed {
			return nil, batchImageDeliveryError("shanghai", "SHANGHAI_ALIYUN_RESULT_UNCONFIRMED", nil)
		}
		result, err := d.upscaler.routeUpscale(ctx, imageUpscaleWorkBatch, upscaleSourceRequest{
			tier: job.ImageSize, owned: true, shanghai: a,
		}, func(loadCtx context.Context) ([]byte, error) {
			return d.loadShanghaiSource(loadCtx, job.BatchID, customID)
		})
		if err != nil {
			var failure *batchImageDeliveryFailure
			if errors.As(err, &failure) {
				return nil, err
			}
			return nil, batchImageDeliveryError("shanghai_upscale", upscaleFailureCode(err), err)
		}
		if result == nil {
			return nil, batchImageDeliveryError("shanghai_upscale", "SHANGHAI_UPSCALE_RESULT_MISSING", nil)
		}
		if result.shanghai != nil {
			return result.shanghai, nil
		}
		defer clearBytes(result.Data)
		final = &BatchImageResultCheckpoint{Data: result.Data, MimeType: result.MimeType, Width: result.Width, Height: result.Height}
		created, err := checkpointBatchImageResult(ctx, d.checkpoints, job.BatchID, customID, BatchImageCheckpointFinal, *final)
		if err != nil {
			return nil, batchImageDeliveryError("final_checkpoint", "IMAGE_FINAL_CHECKPOINT_WRITE_FAILED", err)
		}
		if !created {
			final, err = d.checkpoints.GetBatchImageCheckpoint(ctx, job.BatchID, customID, BatchImageCheckpointFinal)
			if err != nil || final == nil {
				return nil, batchImageDeliveryError("final_checkpoint", "IMAGE_FINAL_CHECKPOINT_READ_FAILED", err)
			}
		}
	}
	defer clearBytes(final.Data)
	var image *shanghaidelivery.Image
	err = a.withWrite(ctx, batchImageShanghaiTimeout, func(op context.Context) error {
		// A restart, an accepted Mini upload, or an unacknowledged previous stage
		// may have won. Never send the image again before reconciling the slot.
		state, err := a.slot.status(op)
		if err != nil {
			return err
		}
		image, err = a.slot.stage(op, final, state)
		return err
	})
	if err != nil {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_STAGE_UNCONFIRMED", err)
	}
	return image, nil
}

func (d batchImageUpscaleDelivery) loadShanghaiSource(ctx context.Context, batchID, customID string) ([]byte, error) {
	source, err := d.checkpoints.GetBatchImageCheckpoint(ctx, batchID, customID, BatchImageCheckpointSource)
	if err != nil || source == nil {
		return nil, batchImageDeliveryError("source_checkpoint", "PROVIDER_RESULT_CHECKPOINT_READ_FAILED", err)
	}
	defer clearBytes(source.Data)
	parsed, err := ExtractBatchImagePartsFromResultLine(source.Data)
	if err != nil || parsed == nil || parsed.CustomID != customID || len(parsed.Images) != 1 {
		return nil, batchImageDeliveryError("source_checkpoint", "SHANGHAI_SOURCE_INVALID", err)
	}
	encoded, err := prepareUpscaleSourceBase64(parsed.Images[0].Base64Data, imageUpscaleMaxSourceBytes)
	if err != nil {
		return nil, batchImageDeliveryError("source_checkpoint", "SHANGHAI_SOURCE_INVALID", err)
	}
	return decodePreparedUpscaleSourceBase64(encoded)
}

func (d batchImageUpscaleDelivery) publishShanghai(ctx context.Context, job *BatchImageJob, customID string, slot *batchImageShanghaiSlot, image *shanghaidelivery.Image) (*batchImageUpscaledOutput, error) {
	signer, ok := d.store.(batchImageShanghaiSigner)
	if !ok || d.writeFencer == nil {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_SIGNER_UNAVAILABLE", nil)
	}
	ext := shanghaidelivery.Extension(image.MimeType)
	key, err := batchImageUpscaleObjectKey(d.config, job.BatchID, customID, 0, ext)
	if err != nil || shanghaidelivery.SlotID(key) != slot.id {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_SLOT_INVALID", err)
	}
	putCtx, cancel := context.WithTimeout(ctx, batchImageShanghaiTimeout)
	defer cancel()
	permit, err := d.writeFencer.BeginBatchImageUpscaleWrite(putCtx, job.BatchID)
	if err != nil || permit == nil {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_WRITE_FENCE_UNAVAILABLE", err)
	}
	put, putErr := signer.PresignPut(putCtx, key, batchImageShanghaiSigningTTL)
	head, headErr := signer.PresignHead(putCtx, key, batchImageShanghaiSigningTTL)
	var verified shanghaidelivery.Slot
	err = errors.Join(putErr, headErr)
	if err == nil {
		err = slot.send(putCtx, http.MethodPost, "/publish", shanghaidelivery.Publish{PutURL: put, HeadURL: head}, &verified)
	}
	err = errors.Join(err, permit.Release())
	if err != nil || !verified.Published || !validShanghaiImage(verified.Image) || *verified.Image != *image {
		return nil, batchImageDeliveryError("shanghai", "SHANGHAI_COS_RECEIPT_UNCONFIRMED", err)
	}
	return &batchImageUpscaledOutput{
		mimeType: image.MimeType, extension: ext, imageCount: 1,
		previewSize: &BatchImagePreviewSize{Width: image.Width, Height: image.Height},
	}, nil
}
