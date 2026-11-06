//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type publicWiringObjectStore struct{ *fakeOpenAIImageBatchStore }

func (*publicWiringObjectStore) PresignPut(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (*publicWiringObjectStore) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}
func (*publicWiringObjectStore) Head(context.Context, string) (int64, string, error) {
	return 0, "", nil
}

func TestProvideBatchImagePublicServiceNativeRuntime(t *testing.T) {
	bridge, _, _ := bridgeFixture(t, BatchImageProviderWan)
	executable := bridge.executor.(*bridgeImageExecutor).runner.executable
	for _, tc := range []struct {
		name        string
		gateway     *OpenAIGatewayService
		bridgePath  string
		ready       bool
		bridgeReady bool
	}{
		{"shared_runtime", &OpenAIGatewayService{httpUpstream: &museTestUpstream{}, concurrencyService: NewConcurrencyService(&museSlotCache{})}, executable, true, true},
		{"bridge_disabled", &OpenAIGatewayService{httpUpstream: &museTestUpstream{}, concurrencyService: NewConcurrencyService(&museSlotCache{})}, "", true, false},
		{"nil_gateway", nil, executable, false, false},
		{"missing_concurrency", &OpenAIGatewayService{httpUpstream: &museTestUpstream{}}, executable, false, false},
		{"missing_transport", &OpenAIGatewayService{concurrencyService: NewConcurrencyService(&museSlotCache{})}, executable, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{BatchImage: config.BatchImageConfig{DeliveryCOSPrefix: testOpenAIImageBatchObjectPrefix, PythonBridgeExecutable: tc.bridgePath}}
			store := &publicWiringObjectStore{newFakeOpenAIImageBatchStore()}
			svc := ProvideBatchImagePublicService(nil, nil, nil, nil, nil, nil, nil, nil, store, tc.gateway, cfg)
			for _, name := range []string{BatchImageProviderMuse, BatchImageProviderWorkBuddy, BatchImageProviderWan, BatchImageProviderSeedream} {
				provider, ok := svc.ProviderRegistry.Get(name)
				require.True(t, ok)
				want := tc.ready
				if isImageBridgeProvider(name) {
					want = tc.bridgeReady
				}
				require.Equal(t, want, batchImageProviderSubmissionAvailable(provider), name)
			}
			muse, _ := svc.ProviderRegistry.Get(BatchImageProviderMuse)
			require.Same(t, tc.gateway, muse.(*OpenAIImagesBatchProvider).executor.(*museImageBatchExecutor).gateway)
		})
	}
}
