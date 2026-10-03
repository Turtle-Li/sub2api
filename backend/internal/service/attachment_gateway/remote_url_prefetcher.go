package attachment_gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// RemoteURLPrefetchConfig controls the optional conversion of scoped HTTPS
// image URLs into inline image data. The normal optimizer and R2 externalizer
// run after this phase, so downloaded bytes use the same validation, WebP
// policy and content-addressed storage rules as client-provided images.
type RemoteURLPrefetchConfig struct {
	Enabled             bool
	MaxImageBytes       int
	MaxPixels           int64
	MaxImagesPerRequest int
	MaxConcurrent       int
	Timeout             time.Duration
	CacheDir            string
	CacheTTL            time.Duration
	CacheMaxBytes       int64
}

type RemoteURLMetrics struct {
	Enabled         bool
	ImageCount      int
	DownloadedCount int
	RewrittenCount  int
	BytesDownloaded int
	Errors          int
	TimedOut        bool
	CacheHits       int
	CacheMisses     int
	DurationMS      float64
}

type RemoteURLResult struct {
	Body    []byte
	Metrics RemoteURLMetrics
}

type remoteURLPrefetcher struct {
	config RemoteURLPrefetchConfig
	client *http.Client
}

type remoteURLPrefetchOutcome struct {
	index    int
	value    string
	bytes    int
	cacheHit bool
	err      error
}

func NewRemoteURLPrefetcher(config RemoteURLPrefetchConfig) (*remoteURLPrefetcher, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &remoteURLPrefetcher{
		config: config,
		client: newRemoteImageHTTPClient(),
	}, nil
}

func newRemoteURLPrefetcherForTest(config RemoteURLPrefetchConfig, client *http.Client) (*remoteURLPrefetcher, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("attachment gateway: remote image test client is required")
	}
	return &remoteURLPrefetcher{config: config, client: client}, nil
}

func (c RemoteURLPrefetchConfig) withDefaults() RemoteURLPrefetchConfig {
	if c.MaxImageBytes <= 0 {
		c.MaxImageBytes = defaultMaxImageBytes
	}
	if c.MaxPixels <= 0 {
		c.MaxPixels = defaultMaxPixels
	}
	if c.MaxImagesPerRequest <= 0 {
		c.MaxImagesPerRequest = defaultMaxImagesPerRequest
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 2
	}
	if c.Timeout <= 0 {
		c.Timeout = 15 * time.Second
	}
	if strings.TrimSpace(c.CacheDir) == "" {
		c.CacheDir = "data/attachment_cache"
	}
	if c.CacheTTL <= 0 {
		c.CacheTTL = 15 * time.Minute
	}
	if c.CacheMaxBytes <= 0 {
		c.CacheMaxBytes = 512 * 1024 * 1024
	}
	return c
}

func (c RemoteURLPrefetchConfig) validate() error {
	if c.MaxImageBytes <= 0 || c.MaxPixels <= 0 || c.MaxImagesPerRequest <= 0 || c.MaxConcurrent <= 0 || c.Timeout <= 0 || strings.TrimSpace(c.CacheDir) == "" || c.CacheTTL <= 0 || c.CacheMaxBytes <= 0 {
		return errors.New("attachment gateway: invalid remote URL prefetch limits")
	}
	if c.MaxImagesPerRequest > maxImagesPerRequest {
		return fmt.Errorf("attachment gateway: remote URL prefetch image limit must be between 1 and %d", maxImagesPerRequest)
	}
	return nil
}

func (p *remoteURLPrefetcher) Enabled() bool {
	return p != nil && p.config.Enabled && p.client != nil
}

func (p *remoteURLPrefetcher) Prefetch(ctx context.Context, body []byte) (result RemoteURLResult) {
	started := time.Now()
	result.Body = body
	result.Metrics.Enabled = p.Enabled()
	defer func() { result.Metrics.DurationMS = float64(time.Since(started)) / float64(time.Millisecond) }()
	if !p.Enabled() || len(body) == 0 {
		return result
	}
	tokens, truncated, err := collectImageRemoteURLTokens(body, p.config.MaxImagesPerRequest)
	if err != nil {
		result.Metrics.Errors++
		return result
	}
	result.Metrics.ImageCount = len(tokens)
	if truncated {
		result.Metrics.Errors++
	}
	if len(tokens) == 0 {
		return result
	}

	workerCount := min(p.config.MaxConcurrent, len(tokens))
	jobs := make(chan imageURLToken)
	outcomes := make(chan remoteURLPrefetchOutcome, len(tokens))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for token := range jobs {
				raw, valueErr := imageURLTokenValue(body, token)
				if valueErr != nil {
					outcomes <- remoteURLPrefetchOutcome{index: token.start, err: valueErr}
					continue
				}
				value, bytesDownloaded, cacheHit, fetchErr := p.fetch(ctx, raw)
				outcomes <- remoteURLPrefetchOutcome{index: token.start, value: value, bytes: bytesDownloaded, cacheHit: cacheHit, err: fetchErr}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, token := range tokens {
			select {
			case jobs <- token:
			case <-ctx.Done():
				return
			}
		}
	}()
	workers.Wait()
	close(outcomes)

	rewritten := make([]imageURLRewrite, len(tokens))
	byStart := make(map[int]remoteURLPrefetchOutcome, len(tokens))
	for outcome := range outcomes {
		byStart[outcome.index] = outcome
	}
	for index, token := range tokens {
		outcome, ok := byStart[token.start]
		if !ok || outcome.err != nil {
			if outcome.err != nil {
				result.Metrics.Errors++
				if errors.Is(outcome.err, context.DeadlineExceeded) {
					result.Metrics.TimedOut = true
				}
			}
			continue
		}
		if outcome.cacheHit {
			result.Metrics.CacheHits++
		} else {
			result.Metrics.CacheMisses++
			result.Metrics.DownloadedCount++
		}
		result.Metrics.RewrittenCount++
		result.Metrics.BytesDownloaded += outcome.bytes
		rewritten[index] = imageURLRewrite{value: outcome.value, changed: true}
	}
	if err := ctx.Err(); err != nil {
		result.Metrics.TimedOut = errors.Is(err, context.DeadlineExceeded)
	}
	rewrittenBody, changed, rewriteErr := rewriteImageURLTokens(body, tokens, rewritten)
	if rewriteErr != nil {
		result.Metrics.Errors++
		return result
	}
	if changed {
		result.Body = rewrittenBody
	}
	return result
}

func (p *remoteURLPrefetcher) fetch(ctx context.Context, raw string) (string, int, bool, error) {
	u, err := validateRemoteImageURL(raw)
	if err != nil {
		return "", 0, false, err
	}
	if data, ok := p.loadCached(raw); ok {
		mimeType := detectDownloadedImageMIME("", data)
		return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), 0, true, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", 0, false, err
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg;q=0.9,*/*;q=0.1")
	req.Header.Set("User-Agent", "Sub2API-AttachmentGateway/1")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", 0, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", 0, false, fmt.Errorf("remote image returned HTTP %s", resp.Status)
	}
	if resp.ContentLength > int64(p.config.MaxImageBytes) {
		return "", 0, false, errImageTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(p.config.MaxImageBytes)+1))
	if err != nil {
		return "", 0, false, err
	}
	if len(data) == 0 || len(data) > p.config.MaxImageBytes {
		return "", len(data), false, errImageTooLarge
	}
	mimeType := detectDownloadedImageMIME(resp.Header.Get("Content-Type"), data)
	if mimeType == "" {
		return "", len(data), false, errUnsupportedMediaType
	}
	if _, _, _, err := decodeImage(data, mimeType, p.config.MaxPixels); err != nil {
		return "", len(data), false, err
	}
	p.storeCached(raw, data)
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), len(data), false, nil
}

func (p *remoteURLPrefetcher) cachePath(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return filepath.Join(p.config.CacheDir, "remote-url-v1", fmt.Sprintf("%x.img", digest[:]))
}

func (p *remoteURLPrefetcher) loadCached(raw string) ([]byte, bool) {
	path := p.cachePath(raw)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) > p.config.CacheTTL {
		if err == nil {
			_ = os.Remove(path)
		}
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > p.config.MaxImageBytes {
		_ = os.Remove(path)
		return nil, false
	}
	mimeType := detectDownloadedImageMIME("", data)
	if mimeType == "" {
		_ = os.Remove(path)
		return nil, false
	}
	if _, _, _, err := decodeImage(data, mimeType, p.config.MaxPixels); err != nil {
		_ = os.Remove(path)
		return nil, false
	}
	return data, true
}

func (p *remoteURLPrefetcher) storeCached(raw string, data []byte) {
	if int64(len(data)) > p.config.CacheMaxBytes {
		return
	}
	path := p.cachePath(raw)
	if _, err := os.Stat(path); err == nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type cacheEntry struct {
		path    string
		modTime time.Time
		size    int64
	}
	files := make([]cacheEntry, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		size := info.Size()
		if size < 0 {
			continue
		}
		files = append(files, cacheEntry{path: filepath.Join(dir, entry.Name()), modTime: info.ModTime(), size: size})
		total += size
	}
	if total+int64(len(data)) > p.config.CacheMaxBytes {
		sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
		for _, entry := range files {
			if total+int64(len(data)) <= p.config.CacheMaxBytes {
				break
			}
			if os.Remove(entry.path) == nil {
				total -= entry.size
			}
		}
		if total+int64(len(data)) > p.config.CacheMaxBytes {
			return
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".remote-url-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return
	}
	if _, err := tmp.Write(data); err != nil {
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmpPath, path)
}

func validateRemoteImageURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("attachment gateway: remote image URL must be an HTTPS URL without credentials")
	}
	if u.Port() != "" {
		port, portErr := strconv.Atoi(u.Port())
		if portErr != nil || port < 1 || port > 65535 {
			return nil, errors.New("attachment gateway: invalid remote image URL port")
		}
	}
	return u, nil
}

func detectDownloadedImageMIME(contentType string, data []byte) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if _, ok := supportedImageMIMETypes[contentType]; ok && imageBytesMatchMIME(contentType, data) {
		return contentType
	}
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff":
		return "image/jpeg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

func imageBytesMatchMIME(mimeType string, data []byte) bool {
	switch mimeType {
	case "image/png":
		return len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n"
	case "image/jpeg":
		return len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff"
	case "image/webp":
		return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
	default:
		return false
	}
}

func collectImageRemoteURLTokens(body []byte, limit int) ([]imageURLToken, bool, error) {
	if limit <= 0 || !json.Valid(body) {
		return nil, false, errors.New("attachment gateway: invalid remote image request body")
	}
	root := gjson.ParseBytes(body)
	tokens := make([]imageURLToken, 0, min(limit, maxTokenPreallocation))
	seen := make(map[[2]int]struct{}, len(tokens))
	truncated := false
	var walk func(value gjson.Result) bool
	visit := func(value gjson.Result) bool {
		if value.Type != gjson.String || !isRemoteImageURL(value.String()) {
			return true
		}
		start, end := value.Index, value.Index+len(value.Raw)
		key := [2]int{start, end}
		if _, ok := seen[key]; ok {
			return true
		}
		if len(tokens) >= limit {
			truncated = true
			return false
		}
		seen[key] = struct{}{}
		tokens = append(tokens, imageURLToken{start: start, end: end})
		return true
	}
	walk = func(value gjson.Result) bool {
		if value.IsArray() {
			continueWalk := true
			value.ForEach(func(_, child gjson.Result) bool { continueWalk = walk(child); return continueWalk })
			return continueWalk
		}
		if !value.IsObject() {
			return true
		}
		partType := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		imageURL := value.Get("image_url")
		switch partType {
		case "input_image":
			if !visit(imageURL) {
				return false
			}
		case "image_url":
			if imageURL.Type == gjson.String {
				if !visit(imageURL) {
					return false
				}
			} else if imageURL.IsObject() {
				if !visit(imageURL.Get("url")) {
					return false
				}
			}
		}
		continueWalk := true
		value.ForEach(func(_, child gjson.Result) bool {
			if child.IsArray() || child.IsObject() {
				continueWalk = walk(child)
			}
			return continueWalk
		})
		return continueWalk
	}
	walk(root)
	return tokens, truncated, nil
}

func isRemoteImageURL(raw string) bool {
	u, err := validateRemoteImageURL(raw)
	if err != nil || u == nil {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && forbiddenRemoteIP(ip) {
		return false
	}
	return true
}

func newRemoteImageHTTPClient() *http.Client {
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		baseTransport = &http.Transport{}
	}
	base := baseTransport.Clone()
	base.Proxy = nil
	base.DialContext = safeRemoteDialContext
	return &http.Client{
		Transport: base,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("attachment gateway: too many remote image redirects")
			}
			_, err := validateRemoteImageURL(req.URL.String())
			if err != nil {
				return err
			}
			return nil
		},
	}
}

func safeRemoteDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var lastErr error
	for _, ip := range ips {
		if forbiddenRemoteIP(ip.IP) {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("attachment gateway: remote image host resolves to a forbidden address")
}

func forbiddenRemoteIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()
}
