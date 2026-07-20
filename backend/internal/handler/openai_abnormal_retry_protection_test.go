package handler

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestOpenAIAbnormalRetryProtectionUsesAnyConfiguredBandwidthDirection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		rxLimit       *float64
		txLimit       *float64
		rxMbps        float64
		txMbps        float64
		wantCongested bool
		wantMaxUtil   float64
	}{
		{
			name:          "rx only can trigger protection",
			rxLimit:       floatPtrForOpenAIRetryProtectionTest(10),
			rxMbps:        9.5,
			wantCongested: true,
			wantMaxUtil:   95,
		},
		{
			name:          "tx only can trigger protection",
			txLimit:       floatPtrForOpenAIRetryProtectionTest(5),
			txMbps:        4.75,
			wantCongested: true,
			wantMaxUtil:   95,
		},
		{
			name:          "uses higher configured direction",
			rxLimit:       floatPtrForOpenAIRetryProtectionTest(20),
			txLimit:       floatPtrForOpenAIRetryProtectionTest(5),
			rxMbps:        10,
			txMbps:        4.6,
			wantCongested: true,
			wantMaxUtil:   92,
		},
		{
			name:          "below threshold does not trigger",
			rxLimit:       floatPtrForOpenAIRetryProtectionTest(10),
			txLimit:       floatPtrForOpenAIRetryProtectionTest(5),
			rxMbps:        8,
			txMbps:        4,
			wantCongested: false,
			wantMaxUtil:   80,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runtime := openAIAbnormalRetryRuntime{enabled: true, triggerPct: 90}
			settings := &service.OpsNetworkBandwidthSettings{
				Enabled:     true,
				RXLimitMbps: tc.rxLimit,
				TXLimitMbps: tc.txLimit,
			}
			summary := &service.OpsNetworkBandwidthSummary{
				Enabled: true,
				RXMbps:  tc.rxMbps,
				TXMbps:  tc.txMbps,
			}

			applyOpenAIAbnormalRetryBandwidthSummary(&runtime, settings, summary)

			if runtime.congested != tc.wantCongested {
				t.Fatalf("congested = %v, want %v", runtime.congested, tc.wantCongested)
			}
			if runtime.maxUtilPct != tc.wantMaxUtil {
				t.Fatalf("maxUtilPct = %v, want %v", runtime.maxUtilPct, tc.wantMaxUtil)
			}
		})
	}
}

func TestOpenAIAbnormalRetryStoreCountsWithinTTLWindow(t *testing.T) {
	store := &openAIAbnormalRetryStore{entries: make(map[string]openAIAbnormalRetryEntry)}
	now := time.Unix(100, 0)
	window := time.Minute
	bodyBytes := int64(15 * 1000 * 1000)

	if got := store.register("same-body", bodyBytes, now, window); got.count != 1 || got.totalBytes != bodyBytes {
		t.Fatalf("first register = %#v, want count=1 total=%d", got, bodyBytes)
	}
	if got := store.register("same-body", bodyBytes, now.Add(10*time.Second), window); got.count != 2 || got.totalBytes != bodyBytes*2 {
		t.Fatalf("second register within window = %#v, want count=2 total=%d", got, bodyBytes*2)
	}
	if got := store.register("same-body", bodyBytes, now.Add(2*time.Minute), window); got.count != 1 || got.totalBytes != bodyBytes {
		t.Fatalf("register after expiry = %#v, want count=1 total=%d", got, bodyBytes)
	}
}

func TestComputeOpenAIAbnormalRetryBudgetUsesConfiguredBandwidth(t *testing.T) {
	t.Parallel()

	settings := &service.OpsNetworkBandwidthSettings{
		RXLimitMbps: floatPtrForOpenAIRetryProtectionTest(100),
		TXLimitMbps: floatPtrForOpenAIRetryProtectionTest(5),
	}

	got := computeOpenAIAbnormalRetryBudgetBytes(settings, time.Minute, 90)
	want := int64(5 * 1_000_000 * 60 * 0.9 / 8)
	if got != want {
		t.Fatalf("computed budget = %d, want %d", got, want)
	}

	settings.TXLimitMbps = floatPtrForOpenAIRetryProtectionTest(20)
	gotHigherBandwidth := computeOpenAIAbnormalRetryBudgetBytes(settings, time.Minute, 90)
	if gotHigherBandwidth <= got {
		t.Fatalf("higher bottleneck bandwidth should increase threshold: got %d after %d", gotHigherBandwidth, got)
	}
}

func TestComputeOpenAIAbnormalRetryFingerprintCandidateUsesAllowedRepeats(t *testing.T) {
	t.Parallel()

	settings := &service.OpsNetworkBandwidthSettings{
		TXLimitMbps: floatPtrForOpenAIRetryProtectionTest(5),
	}
	budget := computeOpenAIAbnormalRetryBudgetBytes(settings, time.Minute, 90)

	got := computeOpenAIAbnormalRetryFingerprintCandidateBytes(budget, 1)
	want := budget / 3
	if got != want {
		t.Fatalf("candidate threshold = %d, want %d", got, want)
	}

	gotMoreRepeats := computeOpenAIAbnormalRetryFingerprintCandidateBytes(budget, 3)
	if gotMoreRepeats >= got {
		t.Fatalf("more allowed repeats should lower candidate threshold: got %d after %d", gotMoreRepeats, got)
	}
}

func TestComputeOpenAIAbnormalRetryEffectiveCandidateUsesMinBodyBytes(t *testing.T) {
	t.Parallel()

	settings := &service.OpsNetworkBandwidthSettings{
		TXLimitMbps: floatPtrForOpenAIRetryProtectionTest(5),
	}
	budget := computeOpenAIAbnormalRetryBudgetBytes(settings, time.Minute, 90)
	dynamicCandidate := computeOpenAIAbnormalRetryFingerprintCandidateBytes(budget, 1)

	got := computeOpenAIAbnormalRetryEffectiveCandidateBytes(budget, 1, dynamicCandidate+1024)
	if got != dynamicCandidate+1024 {
		t.Fatalf("effective candidate should use min body lower bound: got %d want %d", got, dynamicCandidate+1024)
	}

	got = computeOpenAIAbnormalRetryEffectiveCandidateBytes(budget, 1, 1024)
	if got != dynamicCandidate {
		t.Fatalf("effective candidate should keep higher dynamic candidate: got %d want %d", got, dynamicCandidate)
	}
}

type stubOpenAIAbnormalRetryRegistrar struct {
	registration service.OpenAIAbnormalRetryRegistration
	err          error
}

func (s stubOpenAIAbnormalRetryRegistrar) Register(context.Context, string, string, string, int64, time.Duration) (service.OpenAIAbnormalRetryRegistration, error) {
	return s.registration, s.err
}

func TestOpenAIAbnormalRetryRegistrarResultMapsToHandlerState(t *testing.T) {
	got := registerOpenAIAbnormalRetryFingerprint(
		context.Background(),
		stubOpenAIAbnormalRetryRegistrar{registration: service.OpenAIAbnormalRetryRegistration{
			Count:          1,
			TotalBytes:     15 * 1000 * 1000,
			BucketCount:    4,
			BucketBytes:    60 * 1000 * 1000,
			DistinctHashes: 4,
		}},
		"same-fingerprint",
		"same-bucket",
		"hash-1",
		15*1000*1000,
		time.Unix(500, 0),
		time.Minute,
	)
	if got.stateStore != "redis" || got.entry.count != 1 || got.bucket.count != 4 || !got.bucket.highCardinality {
		t.Fatalf("registrar result = %#v, want mapped redis state with high-cardinality bucket", got)
	}
}

func TestOpenAIAbnormalRetryRedisRegisterFallsBackToMemory(t *testing.T) {
	got := registerOpenAIAbnormalRetryFingerprint(
		context.Background(),
		stubOpenAIAbnormalRetryRegistrar{err: errors.New("redis unavailable")},
		"same-fingerprint",
		"same-bucket",
		"hash-1",
		15*1000*1000,
		time.Unix(600, 0),
		time.Minute,
	)
	if got.stateStore != "memory" || !got.redisFallback || got.redisError == "" {
		t.Fatalf("fallback result = %#v, want memory redis fallback with error", got)
	}
}

func TestOpenAIAbnormalRetryBodySizeBucket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		bodyBytes int64
		want      string
	}{
		{bodyBytes: 0, want: "0"},
		{bodyBytes: 1, want: "le_1mb"},
		{bodyBytes: 1024 * 1024, want: "le_1mb"},
		{bodyBytes: 1024*1024 + 1, want: "le_2mb"},
		{bodyBytes: 15 * 1024 * 1024, want: "le_15mb"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := openAIAbnormalRetryBodySizeBucket(tc.bodyBytes); got != tc.want {
				t.Fatalf("bucket = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestOpenAIAbnormalRetryRequestFingerprint(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte("x"), 15*1024*1024)
	baseFingerprint := openAIAbnormalRetryRequestFingerprint(body)
	same := append([]byte(nil), body...)
	if got, want := baseFingerprint, openAIAbnormalRetryRequestFingerprint(same); got != want {
		t.Fatalf("same body fingerprint mismatch: got %s want %s", got, want)
	}

	firstChanged := append([]byte(nil), body...)
	firstChanged[10] = 'a'
	if got := openAIAbnormalRetryRequestFingerprint(firstChanged); got == baseFingerprint {
		t.Fatalf("fingerprint should change when first chunk changes")
	}

	middleChanged := append([]byte(nil), body...)
	middleChanged[len(middleChanged)/2] = 'b'
	if got := openAIAbnormalRetryRequestFingerprint(middleChanged); got == baseFingerprint {
		t.Fatalf("fingerprint should change when middle chunk changes")
	}

	lastChanged := append([]byte(nil), body...)
	lastChanged[len(lastChanged)-10] = 'c'
	if got := openAIAbnormalRetryRequestFingerprint(lastChanged); got == baseFingerprint {
		t.Fatalf("fingerprint should change when last chunk changes")
	}
}

func TestOpenAIAbnormalRetryFastFingerprintDetectsUnsampledMutation(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte("x"), 15*1024*1024)
	mutated := append([]byte(nil), body...)
	mutated[2*1024*1024] = 'z'

	if got, want := openAIAbnormalRetryRequestFingerprint(mutated), openAIAbnormalRetryRequestFingerprint(body); got == want {
		t.Fatalf("fingerprint failed to notice same-length middle mutation")
	}
}

func TestOpenAIAbnormalRetryProtectionBlocksOnlyAfterCumulativeBudget(t *testing.T) {
	t.Parallel()

	settings := &service.OpsNetworkBandwidthSettings{
		TXLimitMbps: floatPtrForOpenAIRetryProtectionTest(5),
	}
	runtime := openAIAbnormalRetryRuntime{
		congested:   true,
		budgetBytes: computeOpenAIAbnormalRetryBudgetBytes(settings, time.Minute, 90),
		maxRepeats:  1,
	}
	bodyBytes := int64(15 * 1000 * 1000)
	store := &openAIAbnormalRetryStore{entries: make(map[string]openAIAbnormalRetryEntry)}
	now := time.Unix(200, 0)

	first := store.register("same-body", bodyBytes, now, time.Minute)
	if first.count > runtime.maxRepeats && first.totalBytes > runtime.budgetBytes {
		t.Fatalf("first request should not be blocked: %#v budget=%d", first, runtime.budgetBytes)
	}
	second := store.register("same-body", bodyBytes, now.Add(10*time.Second), time.Minute)
	if second.count > runtime.maxRepeats && second.totalBytes > runtime.budgetBytes {
		t.Fatalf("second 15MB request should remain below 5Mbps/60s/90%% budget: %#v budget=%d", second, runtime.budgetBytes)
	}
	third := store.register("same-body", bodyBytes, now.Add(20*time.Second), time.Minute)
	if third.count <= runtime.maxRepeats || third.totalBytes <= runtime.budgetBytes {
		t.Fatalf("third 15MB request should exceed cumulative budget: %#v budget=%d", third, runtime.budgetBytes)
	}
}

func BenchmarkOpenAIAbnormalRetryRequestFingerprint15MB(b *testing.B) {
	body := bytes.Repeat([]byte("x"), 15*1024*1024)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if got := openAIAbnormalRetryRequestFingerprint(body); got == "" {
			b.Fatal("empty fingerprint")
		}
	}
}

func floatPtrForOpenAIRetryProtectionTest(v float64) *float64 {
	return &v
}
