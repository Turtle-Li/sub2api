package service

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	batchImageMaxSubmitRetries     = 2
	batchImageMaxSubmitRetryDelay  = 10 * time.Second
	batchImageSubmitInitialBackoff = 500 * time.Millisecond
)

// Only an adapter that can prove the submission was refused before acceptance
// may return this error. A timeout or generic failover error is not that proof.
type batchImageTemporarySubmitRejection struct{ retryAfter time.Duration }

func (*batchImageTemporarySubmitRejection) Error() string {
	return "upstream temporarily rejected the image request"
}

func batchImageSubmitRetryDelay(ctx context.Context, attempt int, err error) (time.Duration, bool) {
	var rejection *batchImageTemporarySubmitRejection
	if attempt >= batchImageMaxSubmitRetries || ctx.Err() != nil || !errors.As(err, &rejection) {
		return 0, false
	}
	delay := batchImageSubmitInitialBackoff << attempt
	if rejection.retryAfter > delay {
		delay = rejection.retryAfter
	}
	if delay > batchImageMaxSubmitRetryDelay {
		return 0, false // Never retry sooner than the provider permits.
	}
	delay += rand.N(200 * time.Millisecond)
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= delay {
		return 0, false
	}
	return delay, true
}

func openAIImageSubmitRejection(status int, headers http.Header, body []byte) *batchImageTemporarySubmitRejection {
	if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(headers.Get("X-Should-Retry")), "false") || !gjson.ValidBytes(body) ||
		gjson.GetBytes(body, "data").Exists() || gjson.GetBytes(body, "output").Exists() || gjson.GetBytes(body, "task_id").Exists() {
		return nil
	}
	code, kind := gjson.GetBytes(body, "error.code").String(), gjson.GetBytes(body, "error.type").String()
	switch {
	case status == http.StatusTooManyRequests && code == "rate_limit_exceeded" &&
		(kind == "" || kind == "rate_limit_error" || kind == "requests" || kind == "tokens"):
	case status == http.StatusTooManyRequests && code == "slow_down" && kind == "rate_limit_error":
	case status == http.StatusServiceUnavailable && code == "server_is_overloaded" && kind == "service_unavailable_error":
	default:
		return nil
	}
	return &batchImageTemporarySubmitRejection{retryAfter: batchImageRetryAfter(headers, time.Now())}
}

func batchImageRetryAfter(headers http.Header, now time.Time) time.Duration {
	var delay time.Duration
	for _, value := range headers.Values("Retry-After") {
		value = strings.TrimSpace(value)
		if wait, err := strconv.ParseUint(value, 10, 32); err == nil {
			if seconds := time.Duration(wait) * time.Second; seconds > delay {
				delay = seconds
			}
		} else if errors.Is(err, strconv.ErrRange) {
			return batchImageMaxSubmitRetryDelay + time.Second
		} else if date, err := http.ParseTime(value); err == nil {
			if wait := date.Sub(now); wait > delay {
				delay = wait
			}
		}
	}
	for _, value := range headers.Values("Retry-After-Ms") {
		ms, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
		if errors.Is(err, strconv.ErrRange) {
			return batchImageMaxSubmitRetryDelay + time.Second
		}
		if wait := time.Duration(ms) * time.Millisecond; err == nil && wait > delay {
			delay = wait
		}
	}
	return delay
}
