//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type grokMediaSlotsCache struct {
	testutil.StubConcurrencyCache
	mu                                             sync.Mutex
	accounts                                       map[string]int64
	users                                          map[string]int64
	acquired, released, userAcquired, userReleased int
	waiting, denied                                int
	full                                           bool
	queueFull                                      bool
	onWait                                         func()
	duplicateRelease                               bool
	peak                                           int
}

func (s *grokMediaSlotsCache) AcquireAccountSlot(_ context.Context, id int64, _ int, request string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full || s.denied > 0 {
		if s.denied > 0 {
			s.denied--
		}
		return false, nil
	}
	s.accounts[request] = id
	s.acquired++
	if len(s.accounts) > s.peak {
		s.peak = len(s.accounts)
	}
	return true, nil
}

func (s *grokMediaSlotsCache) ReleaseAccountSlot(_ context.Context, _ int64, request string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[request]; !ok {
		s.duplicateRelease = true
	}
	delete(s.accounts, request)
	s.released++
	return nil
}

func (s *grokMediaSlotsCache) AcquireUserSlot(_ context.Context, id int64, _ int, request string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[request] = id
	s.userAcquired++
	return true, nil
}

func (s *grokMediaSlotsCache) ReleaseUserSlot(_ context.Context, _ int64, request string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[request]; !ok {
		s.duplicateRelease = true
	}
	delete(s.users, request)
	s.userReleased++
	return nil
}

func (s *grokMediaSlotsCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	s.mu.Lock()
	if s.queueFull {
		s.mu.Unlock()
		return false, nil
	}
	s.waiting++
	onWait := s.onWait
	s.mu.Unlock()
	if onWait != nil {
		onWait()
	}
	return true, nil
}

func (s *grokMediaSlotsCache) DecrementAccountWaitCount(context.Context, int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waiting--
	return nil
}

func (s *grokMediaSlotsCache) assertReleased(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Empty(t, s.accounts)
	require.Empty(t, s.users)
	require.Equal(t, s.acquired, s.released)
	require.Equal(t, s.userAcquired, s.userReleased)
	require.False(t, s.duplicateRelease)
	require.Zero(t, s.waiting)
}

type grokMediaSlotBindings struct {
	testutil.StubGatewayCache
	owner   int64
	writes  int
	key     string
	billed  map[string]bool
	pending map[string][]byte
}

func (s *grokMediaSlotBindings) SetGrokVideoPendingBilling(_ context.Context, key string, body []byte, _ time.Duration) error {
	if s.pending == nil {
		s.pending = make(map[string][]byte)
	}
	s.pending[key] = append([]byte(nil), body...)
	return nil
}
func (s *grokMediaSlotBindings) GetGrokVideoPendingBilling(_ context.Context, key string) ([]byte, error) {
	return s.pending[key], nil
}

func (s *grokMediaSlotBindings) GetSessionAccountID(_ context.Context, groupID int64, key string) (int64, error) {
	if groupID != 24 || key != s.key {
		return 0, service.ErrStickySessionNotFound
	}
	return s.owner, nil
}
func (s *grokMediaSlotBindings) SetSessionAccountID(_ context.Context, _ int64, key string, owner int64, _ time.Duration) error {
	s.key, s.owner = key, owner
	s.writes++
	return nil
}
func (s *grokMediaSlotBindings) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	s.writes++
	return nil
}
func (s *grokMediaSlotBindings) DeleteSessionAccountID(context.Context, int64, string) error {
	s.writes++
	return nil
}

func (s *grokMediaSlotBindings) ClaimGrokVideoBilled(_ context.Context, key string, _ time.Duration) (bool, error) {
	if s.billed == nil {
		s.billed = make(map[string]bool)
	}
	if s.billed[key] {
		return false, nil
	}
	s.billed[key] = true
	return true, nil
}

type grokMediaSlotRepo struct {
	openAIImagesFailoverAccountRepo
	mismatch bool
}

func (r grokMediaSlotRepo) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	if r.mismatch {
		return r.openAIImagesFailoverAccountRepo.GetByID(ctx, 2)
	}
	return r.openAIImagesFailoverAccountRepo.GetByID(ctx, id)
}

type grokMediaSlotUpstream struct {
	service.HTTPUpstream
	call  func(*http.Request, int64) (*http.Response, error)
	calls int
}

func (u *grokMediaSlotUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.calls++
	return u.call(req, id)
}

type grokMediaSlotProber func(context.Context, int64) (bool, string, error)

func (p grokMediaSlotProber) ProbeMediaEligibility(ctx context.Context, id int64) (bool, string, error) {
	return p(ctx, id)
}

func newGrokMediaSlotHandler(t *testing.T, oauth, mismatch bool, platforms ...string) (*OpenAIGatewayHandler, *grokMediaSlotsCache, *grokMediaSlotBindings, *grokMediaSlotUpstream) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	accounts := make([]service.Account, 3)
	for i := range accounts {
		accounts[i] = service.Account{ID: int64(i + 1), Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 50, Priority: i,
			GroupIDs: []int64{24}, Credentials: map[string]any{"api_key": "test-key", "access_token": "test-token"}}
		if len(platforms) > 0 {
			accounts[i].Platform = platforms[0]
			accounts[i].Credentials["base_url"] = "https://ark.cn-beijing.volces.com/api/v3"
			accounts[i].Credentials["openai_capabilities"] = []string{"seedance"}
			if len(platforms) > 1 && (platforms[1] == service.MuseVideoModel || platforms[1] == service.MuseImageModel) {
				accounts[i].Credentials["base_url"] = "https://muse.example/v1"
				accounts[i].Credentials["model_mapping"] = map[string]any{platforms[1]: platforms[1]}
			}
		}
		if oauth {
			accounts[i].Type = service.AccountTypeOAuth
			accounts[i].Credentials["refresh_token"] = "test-refresh"
			accounts[i].Credentials["expires_at"] = time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)
		}
	}
	slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	concurrency := service.NewConcurrencyService(slots)
	bindings := &grokMediaSlotBindings{owner: 1}
	upstream := &grokMediaSlotUpstream{call: func(*http.Request, int64) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"request_id":"task","status":"pending"}`))}, nil
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.Scheduling.StickySessionWaitTimeout = 20 * time.Millisecond
	cfg.Gateway.Scheduling.StickySessionMaxWaiting = 3
	cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = true
	repo := grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts}, mismatch: mismatch}
	provider := service.NewGrokTokenProvider(repo, nil)
	if oauth {
		_, err := provider.GetAccessToken(context.Background(), &accounts[1])
		require.NoError(t, err)
	}
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, bindings, cfg, nil, concurrency, nil, nil, nil, upstream, nil, nil, provider, nil, nil, nil, nil, nil)
	groupID := int64(24)
	require.NoError(t, gateway.BindGrokMediaVideoRequestAccount(context.Background(), &groupID, "task", 10, 20, 1))
	bindings.writes = 0
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	handler := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	return handler, slots, bindings, upstream
}

func grokMediaSlotContext(ctx context.Context, generation bool) (*gin.Context, *httptest.ResponseRecorder) {
	groupID := int64(24)
	method, path, body := http.MethodGet, "/v1/videos/task", ""
	if generation {
		method, path, body = http.MethodPost, "/v1/videos/generations", `{"model":"grok-imagine-video","prompt":"test","duration":6}`
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	if !generation {
		req.Header.Set("session-id", "unrelated-client-session")
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "request_id", Value: "task"}}
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 20, UserID: 10, GroupID: &groupID,
		Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, AllowImageGeneration: true}, User: &service.User{ID: 10}})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 10, Concurrency: 5})
	return c, w
}

func TestGrokMediaLookupSlotLifecycle(t *testing.T) {
	for _, scenario := range []string{"normal", "mismatch acquired", "mismatch wait", "full", "queue full", "cancel while waiting", "wait then acquired", "upstream error", "cancel", "panic"} {
		t.Run(scenario, func(t *testing.T) {
			h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, strings.HasPrefix(scenario, "mismatch"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "full" || scenario == "mismatch wait" || scenario == "queue full" || scenario == "cancel while waiting" {
				slots.full = true
			}
			if scenario == "queue full" {
				slots.queueFull = true
			}
			if scenario == "cancel while waiting" {
				slots.onWait = cancel
			}
			if scenario == "wait then acquired" {
				slots.denied = 1
			}
			original := upstream.call
			upstream.call = func(req *http.Request, id int64) (*http.Response, error) {
				require.Equal(t, int64(1), id)
				switch scenario {
				case "upstream error":
					return nil, errors.New("upstream unavailable")
				case "cancel":
					cancel()
					return nil, context.Canceled
				case "panic":
					panic("test upstream panic")
				}
				return original(req, id)
			}
			for range 20 {
				c, w := grokMediaSlotContext(ctx, false)
				h.GrokVideoStatus(c)
				slots.assertReleased(t)
				if strings.HasPrefix(scenario, "mismatch") {
					require.Equal(t, 404, w.Code)
				}
				if scenario == "normal" || scenario == "wait then acquired" {
					require.Equal(t, 200, w.Code)
				}
				if scenario == "full" || scenario == "queue full" {
					require.Equal(t, http.StatusTooManyRequests, w.Code)
				}
			}
			require.Zero(t, bindings.writes, "lookups must preserve owner and TTL")
			if scenario == "mismatch acquired" {
				require.Equal(t, 20, slots.released)
			}
			if slots.full {
				require.Zero(t, slots.released)
				require.Zero(t, upstream.calls)
			}
			if scenario == "full" || strings.HasPrefix(scenario, "mismatch") {
				require.Zero(t, upstream.calls)
			}
			switch scenario {
			case "normal", "wait then acquired", "upstream error", "panic":
				require.Equal(t, 20, upstream.calls)
				require.Equal(t, 20, slots.released)
			case "cancel":
				require.Equal(t, 1, upstream.calls)
				require.Equal(t, 1, slots.released)
			}
		})
	}
}

func TestGrokMediaEligibilityReleasesBeforeSwitch(t *testing.T) {
	for _, scenario := range []string{"switch", "exhausted", "cancel during probe"} {
		t.Run(scenario, func(t *testing.T) {
			h, slots, _, upstream := newGrokMediaSlotHandler(t, true, false)
			h.maxAccountSwitches = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probes := 0
			h.grokMediaEligibilityProber = grokMediaSlotProber(func(context.Context, int64) (bool, string, error) {
				probes++
				if scenario == "cancel during probe" {
					cancel()
					require.Eventually(t, func() bool { slots.mu.Lock(); defer slots.mu.Unlock(); return len(slots.accounts) == 0 }, time.Second, time.Millisecond)
				}
				if scenario == "switch" && probes == 2 {
					return true, "eligible", nil
				}
				return false, "billing_unobserved", errors.New("probe failed")
			})
			c, w := grokMediaSlotContext(ctx, true)
			h.GrokVideoGeneration(c)
			slots.assertReleased(t)
			require.Equal(t, 1, slots.peak, "rejected attempts must release before the next selection")
			if scenario == "switch" {
				events, _ := c.Get(service.OpsUpstreamErrorsKey)
				detail, err := json.Marshal(events)
				require.NoError(t, err)
				require.Equal(t, 200, w.Code, "body=%s probes=%d upstream=%d events=%s", w.Body.String(), probes, upstream.calls, detail)
				require.Equal(t, 1, upstream.calls)
			}
			if scenario == "exhausted" {
				require.Equal(t, 503, w.Code)
				require.Equal(t, 2, slots.released)
				require.Zero(t, upstream.calls)
			}
		})
	}
}

func TestMuseVideoSubmissionBindsOwnerBeforeLookup(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, service.MuseVideoModel)
	const nativeID = "task_0123456789abcdef0123"
	const publicID = "muse_" + nativeID
	const referenceImage = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAEklEQVR4nGP4b8zAkPafAUIBACO3BS8QBf+HAAAAAElFTkSuQmCC"
	upstream.call = func(req *http.Request, accountID int64) (*http.Response, error) {
		body := `{"id":"` + nativeID + `","status":"queued"}`
		switch req.Method {
		case http.MethodPost:
			require.Equal(t, "/v1/videos", req.URL.Path)
			var payload map[string]any
			require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
			require.Equal(t, []any{referenceImage}, payload["images"])
			require.Equal(t, float64(6), payload["duration"])
		case http.MethodGet:
			require.Equal(t, "/v1/videos/"+nativeID, req.URL.Path)
			body = `{"id":"` + nativeID + `","status":"processing","progress":15}`
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
		require.Equal(t, int64(1), accountID)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	c, w := grokMediaSlotContext(t.Context(), true)
	key, ok := middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	key.Group.Platform = service.PlatformOpenAI
	price := 0.06
	key.Group.VideoPrice720P = &price
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"muse-video","prompt":"animate this image","reference_image":"`+referenceImage+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.MuseVideoGeneration(c)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), publicID)
	require.NotContains(t, w.Body.String(), referenceImage)
	require.NotEmpty(t, bindings.pending)

	for _, keyID := range []int64{21, 20} {
		c, w := grokMediaSlotContext(t.Context(), false)
		key, ok := middleware2.GetAPIKeyFromContext(c)
		require.True(t, ok)
		key.ID = keyID
		key.Group.Platform = service.PlatformOpenAI
		c.Params = gin.Params{{Key: "request_id", Value: publicID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+publicID, nil)
		h.MuseVideoStatus(c)
		if keyID == 20 {
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "processing")
		} else {
			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			require.Equal(t, 1, upstream.calls)
		}
	}
	require.Equal(t, 2, upstream.calls)
	slots.assertReleased(t)
}

func TestMuseImageTaskUsesSameKeyAndModel(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, service.MuseImageModel)
	const nativeID = "task_0123456789abcdef0123"
	const publicID = "muse_image_" + nativeID
	upstream.call = func(req *http.Request, accountID int64) (*http.Response, error) {
		require.Equal(t, int64(1), accountID)
		require.Equal(t, "muse.example", req.URL.Host)
		var body string
		switch req.Method {
		case http.MethodPost:
			require.Equal(t, "/v1/images/tasks", req.URL.Path)
			payload, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"model":"muse-image","prompt":"Draw a circle","n":1,"response_format":"url","async":true}`, string(payload))
			body = `{"id":"` + nativeID + `","status":"queued","account":"private"}`
		case http.MethodGet:
			require.Equal(t, "/v1/images/tasks/"+nativeID, req.URL.Path)
			body = `{"id":"` + nativeID + `","kind":"image","status":"processing","progress":5}`
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	c, w := grokMediaSlotContext(t.Context(), true)
	key, _ := middleware2.GetAPIKeyFromContext(c)
	key.Group.Platform = service.PlatformOpenAI
	price := 0.10
	key.Group.ImagePrice2K = &price
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"muse-image","prompt":"Draw a circle","async":true}`))
	h.MuseImages(c)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	require.Equal(t, publicID, w.Header().Get("X-Muse-Task-ID"))
	require.NotContains(t, w.Body.String(), "private")

	for _, otherKey := range []int64{21, 20} {
		c, w := grokMediaSlotContext(t.Context(), false)
		key, _ := middleware2.GetAPIKeyFromContext(c)
		key.ID = otherKey
		key.Group.Platform = service.PlatformOpenAI
		c.Params = gin.Params{{Key: "task_id", Value: publicID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+publicID, nil)
		h.MuseImageStatus(c)
		if otherKey == 21 {
			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			require.Equal(t, 1, upstream.calls)
		} else {
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "processing")
		}
	}
	slots.assertReleased(t)
	require.Equal(t, 2, upstream.calls)
}

func TestGrokMediaVideoLookupOwnerIsolation(t *testing.T) {
	for _, other := range []string{"user", "api key", "group", "task"} {
		t.Run(other, func(t *testing.T) {
			h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false)
			c, w := grokMediaSlotContext(context.Background(), false)
			key, ok := middleware2.GetAPIKeyFromContext(c)
			require.True(t, ok)
			switch other {
			case "user":
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 11, Concurrency: 5})
			case "api key":
				key.ID = 21
			case "group":
				groupID := int64(25)
				key.GroupID = &groupID
			case "task":
				c.Params = gin.Params{{Key: "request_id", Value: "other-task"}}
			}
			h.GrokVideoStatus(c)
			require.Equal(t, http.StatusNotFound, w.Code)
			slots.assertReleased(t)
			require.Zero(t, slots.acquired)
			require.Zero(t, upstream.calls)
			require.Zero(t, bindings.writes)
		})
	}
}

func TestGrokMediaVideoCompletionStillClaimsBillingOnce(t *testing.T) {
	h, _, bindings, _ := newGrokMediaSlotHandler(t, false, false)
	c, _ := grokMediaSlotContext(context.Background(), false)
	key, ok := middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	subject := middleware2.AuthSubject{UserID: 10, Concurrency: 5}
	result := &service.OpenAIForwardResult{ResponseID: "task", Model: "grok-imagine-video",
		VideoCount: 1, VideoDurationSeconds: 6}
	for i := range 20 {
		bill := prepareGrokVideoCompletionBilling(c.Request.Context(), h, zap.NewNop(), key, subject, "task", result)
		if i == 0 {
			require.NotNil(t, bill)
			require.Equal(t, service.StableGrokVideoBillingRequestID("task"), bill.RequestID)
		} else {
			require.Nil(t, bill)
		}
	}
	require.Len(t, bindings.billed, 1)
}
