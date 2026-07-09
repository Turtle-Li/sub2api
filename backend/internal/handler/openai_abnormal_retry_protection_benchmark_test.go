package handler

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func BenchmarkOpenAIAbnormalRetryHashSizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{name: "3MB", size: 3 * 1000 * 1000},
		{name: "12MB", size: 12 * 1000 * 1000},
		{name: "15MB", size: 15 * 1000 * 1000},
		{name: "50MB", size: 50 * 1000 * 1000},
	}
	for _, tc := range sizes {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			body := bytes.Repeat([]byte("x"), tc.size)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = service.HashUsageRequestPayload(body)
			}
		})
	}
}

func BenchmarkOpenAIAbnormalRetryFastFingerprintSizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{name: "3MB", size: 3 * 1000 * 1000},
		{name: "12MB", size: 12 * 1000 * 1000},
		{name: "15MB", size: 15 * 1000 * 1000},
		{name: "50MB", size: 50 * 1000 * 1000},
	}
	for _, tc := range sizes {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			body := bytes.Repeat([]byte("x"), tc.size)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = openAIAbnormalRetryRequestFingerprint(body)
			}
		})
	}
}

func BenchmarkOpenAIAbnormalRetryCandidateGate(b *testing.B) {
	settings := &service.OpsNetworkBandwidthSettings{
		TXLimitMbps: floatPtrForOpenAIRetryProtectionBenchmark(5),
	}
	budget := computeOpenAIAbnormalRetryBudgetBytes(settings, time.Minute, 90)
	candidateBytes := computeOpenAIAbnormalRetryFingerprintCandidateBytes(budget, 1)
	bodySizes := []int64{
		3 * 1000 * 1000,
		12 * 1000 * 1000,
		15 * 1000 * 1000,
	}

	for _, bodySize := range bodySizes {
		bodySize := bodySize
		b.Run(fmt.Sprintf("body_%dMB", bodySize/1000/1000), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			var shouldHash bool
			for i := 0; i < b.N; i++ {
				shouldHash = bodySize >= candidateBytes
			}
			_ = shouldHash
		})
	}
}

func BenchmarkOpenAIAbnormalRetryStoreSameFingerprint(b *testing.B) {
	store := &openAIAbnormalRetryStore{entries: make(map[string]openAIAbnormalRetryEntry)}
	now := time.Unix(300, 0)
	bodyBytes := int64(15 * 1000 * 1000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.register("api-key:path:body-hash", bodyBytes, now.Add(time.Duration(i)*time.Millisecond), time.Minute)
	}
}

func floatPtrForOpenAIRetryProtectionBenchmark(v float64) *float64 {
	return &v
}
