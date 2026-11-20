package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const imageUpscaleCloudMaxQueue = 8

type upscaleSourceRequest struct {
	scale int
	tier  string
	owned bool
}

// Keep the shared Mini admission limit ahead of potentially large source loads.
// On overflow the independent cloud slot covers both the load and the cloud call.
func (s *ImageUpscaleService) routeUpscale(ctx context.Context, class imageUpscaleWorkClass, req upscaleSourceRequest, load func(context.Context) ([]byte, error)) (*ImageUpscaleResult, error) {
	if !s.Active() {
		return nil, imageUpscaleError("UNAVAILABLE", 0, false, nil)
	}
	if _, tierOK := RequestedImageUpscaleScale(req.tier); !tierOK && req.scale != 2 && req.scale != 4 {
		return nil, imageUpscaleError("INVALID_SCALE", 0, false, nil)
	}
	if load == nil {
		return nil, imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, nil)
	}
	job, cancelJob := imageUpscaleLifecycleContext(ctx, s.cfg)
	defer cancelJob()
	if job.Err() != nil {
		return nil, imageUpscaleError("JOB_TIMEOUT", 0, true, job.Err())
	}
	mini := job
	if s.cloudUpscale != nil {
		deadline, _ := job.Deadline()
		reserve := min(time.Until(deadline)/3, 3*time.Minute)
		var cancel context.CancelFunc
		mini, cancel = context.WithDeadline(job, deadline.Add(-reserve))
		defer cancel()
	}
	// A short burst inside the configured queue is not a reason to spend money.
	miniErr := s.acquireClass(mini, class)
	hasMini := miniErr == nil
	defer func() {
		if hasMini {
			s.release()
		}
	}()
	if !hasMini {
		if err := s.acquireCloud(job, miniErr); err != nil {
			return nil, err
		}
		defer func() { <-s.cloudSlots }()
	}
	source, err := load(job)
	if err != nil {
		return nil, err
	}
	defer func() {
		if req.owned {
			clearBytes(source)
		}
	}()
	if len(source) > imageUpscaleMaxSourceBytes {
		return nil, imageUpscaleError("SOURCE_LIMIT_EXCEEDED", 0, false, nil)
	}
	if req.tier != "" {
		width, height, mime, err := decodeUpscaleImageConfig(source)
		if err != nil {
			return nil, imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, err)
		}
		var needed bool
		req.scale, needed = imageUpscaleScaleForDimensions(req.tier, width, height)
		if !needed {
			req.owned = false
			return &ImageUpscaleResult{Data: source, MimeType: mime, Width: width, Height: height, Scale: 1}, nil
		}
	}
	width, height, _, err := validateUpscaleSource(source)
	if err != nil {
		return nil, err
	}
	if hasMini {
		result, err := s.runMiniUpscale(mini, source, width, height, req.scale)
		if err == nil {
			return result, nil
		}
		miniErr = err
		// Hold Mini admission until cloud admission succeeds: waiting failovers must
		// not accumulate decoded full-size sources outside both memory budgets.
		if err = s.acquireCloud(job, miniErr); err != nil {
			return nil, err
		}
		defer func() { <-s.cloudSlots }()
		s.release()
		hasMini = false
	}
	if job.Err() != nil {
		return nil, imageUpscaleError("JOB_TIMEOUT", 0, true, job.Err())
	}
	reason := upscaleFailureCode(miniErr)
	logger.L().Info("image_upscale.aliyun_fallback", zap.String("mini_code", reason), zap.Int("scale", req.scale))
	result, err := s.cloudUpscale(job, source, req.scale)
	if err != nil {
		logger.L().Warn("image_upscale.aliyun_fallback_failed", zap.String("mini_code", reason), zap.String("cloud_code", upscaleFailureCode(err)), zap.Int("scale", req.scale))
		return nil, err
	}
	return result, nil
}

func (s *ImageUpscaleService) acquireCloud(ctx context.Context, miniErr error) error {
	if s.cloudUpscale == nil {
		return miniErr
	}
	PreventAsyncImageTaskReplay(ctx) // Cloud admission must not replay an already generated source, either.
	if ctx.Err() != nil {
		return imageUpscaleError("JOB_TIMEOUT", 0, true, ctx.Err())
	}
	select {
	case s.cloudSlots <- struct{}{}:
		return nil
	default:
	}
	waiting := s.cloudWaiting.Add(1)
	defer s.cloudWaiting.Add(-1)
	if waiting > imageUpscaleCloudMaxQueue {
		return imageUpscaleError("ALIYUN_BACKPRESSURE", http.StatusServiceUnavailable, true, nil)
	}
	select {
	case s.cloudSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return imageUpscaleError("JOB_TIMEOUT", 0, true, ctx.Err())
	}
}

func upscaleFailureCode(err error) string {
	var e *ImageUpscaleError
	if errors.As(err, &e) {
		return e.Code
	}
	return "UNAVAILABLE"
}
