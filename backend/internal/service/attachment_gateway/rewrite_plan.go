package attachment_gateway

import (
	"context"
	"time"
)

// RewritePlan is request-local. It ties discovery and compression results to
// the original immutable request body until the caller decides whether it may
// start the separately timed URL phase. It must not be reused across requests.
type RewritePlan struct {
	gateway      *Gateway
	externalizer *URLExternalizer
	body         []byte
	discovery    attachmentDiscovery
	discoveryErr error
	inspect      bool
	scanned      bool
	images       []optimizedImage
	inlineBytes  int
}

func (g *Gateway) PlanRewrite(body []byte, e *URLExternalizer, inspectPreflight bool) *RewritePlan {
	return &RewritePlan{gateway: g, externalizer: e, body: body, inlineBytes: len(body),
		inspect: inspectPreflight || g.config.RequestBudgetEnabled || g.config.AggregateSmallImageEnabled}
}

func (p *RewritePlan) discover() {
	if !p.scanned {
		limit := max(p.gateway.config.MaxImagesPerRequest, p.externalizer.config.MaxImagesPerRequest)
		p.discovery, p.discoveryErr = discoverAttachments(p.body, limit, p.inspect)
		p.scanned = true
	}
}

func (p *RewritePlan) OriginalInlineStats() (InlineAttachmentStats, error) {
	p.discover()
	return p.discovery.inline, p.discoveryErr
}

// Optimize records the fully specified inline candidate without allocating a
// JSON body for it. InlineBody is the fail-open alternative to Externalize.
func (p *RewritePlan) Optimize(ctx context.Context) Metrics {
	p.images, p.inlineBytes = nil, len(p.body)
	return p.gateway.optimize(ctx, p.body, p).Metrics
}

func (p *RewritePlan) Externalize(ctx context.Context, canWrite func() bool) URLResult {
	result := p.ExternalizeOnly(ctx, canWrite)
	p.MaterializeFallback(&result, nil)
	return result
}

// ExternalizeOnly runs the URL phase without assembling the inline WebP
// fallback body, so callers can sample uploadCtx deadlines before fallback
// serialization and attribute fallback serialization to optimization time.
func (p *RewritePlan) ExternalizeOnly(ctx context.Context, canWrite func() bool) URLResult {
	p.discover()
	return p.externalizer.externalizePlan(ctx, p.body, nil, canWrite, p)
}

// MaterializeFallback builds the deferred inline WebP body only when the URL
// phase left Body nil (e.g. below trigger, storage unavailable, or generation
// switch), charging any serialization work to OptimizeDurationMS.
func (p *RewritePlan) MaterializeFallback(result *URLResult, metrics *Metrics) {
	if result == nil || result.Body != nil {
		return
	}
	started := time.Now()
	result.Body = p.InlineBody()
	if metrics != nil {
		metrics.OptimizeDurationMS += float64(time.Since(started)) / float64(time.Millisecond)
	}
}

func (p *RewritePlan) InlineBody() []byte {
	if len(p.images) == 0 {
		return p.body
	}
	body, _, err := spliceImagePlan(p.body, p.discovery.tokens, p.images, nil)
	if err != nil {
		return p.body
	}
	return body
}

// The first len(urls) entries, when present, are the URL phase's independent
// image limit. Every other optimized token remains inline.
func spliceImagePlan(body []byte, tokens []imageURLToken, images []optimizedImage, urls []imageURLRewrite) ([]byte, bool, error) {
	count := max(len(images), len(urls))
	replacements := make([]imageURLRewrite, count)
	copy(replacements, urls)
	for i, image := range images {
		if !replacements[i].changed && image.Bytes != nil {
			replacements[i] = imageURLRewrite{webp: image.Bytes, changed: true}
		}
	}
	return rewriteImageURLTokens(body, tokens[:count], replacements)
}
