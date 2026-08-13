package attachment_gateway

import (
	"context"
	"time"
)

const detachedWorkFallbackTimeout = 2 * time.Minute

// detachedWorkContext lets a singleflight cache producer finish after the
// originating client disconnects, while retaining the request's explicit
// deadline. The caller still observes its own cancellation immediately; only
// already-admitted, globally bounded cache work continues. Direct package
// users without a deadline receive the conservative fallback bound.
func detachedWorkContext(ctx context.Context) (context.Context, context.CancelFunc) {
	parent := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(parent, deadline)
	}
	return context.WithTimeout(parent, detachedWorkFallbackTimeout)
}
