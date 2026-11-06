//go:build unit

package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func bridgeFixture(t *testing.T, provider string) (*OpenAIImagesBatchProvider, *Account, string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	require.NoError(t, err)
	dir := t.TempDir()
	log := filepath.Join(dir, "operations")
	executable := filepath.Join(dir, "bridge")
	script := `import json,sys,os
r=json.load(sys.stdin)
p=r['provider']; m='wan2.7-image' if p=='wan' else 'doubao-seedream-5-0-pro-260628'
if r['operation']=='capabilities':
 print(json.dumps({'state':'completed','provider':p,'models':[{'model':m,'profile_version':'2' if p=='wan' else '4','image_sizes':['2K'],'default_image_size':'2K','default_aspect_ratio':'1:1','max_reference_images':9,'response_mime_types':['image/png'],'sizes':[{'image_size':'2K','aspect_ratio':'1:1','size':'2048*2048' if p=='wan' else '2048x2048'}]}]}));sys.exit()
with open(os.environ['BRIDGE_TEST_LOG'],'a') as f:f.write(r['operation']+'\n')
if os.environ.get('BRIDGE_EXPECT_PROXY'):
 assert os.environ['HTTPS_PROXY']==os.environ['BRIDGE_EXPECT_PROXY']
 assert os.environ['NO_PROXY']=='' and 'no_proxy' not in os.environ
if r['operation']=='submit':
 assert r['parameters']['size'] in ['2048*2048','2048x2048']
 if r['prompt']=='die':sys.exit(9)
 if p=='wan':print(json.dumps({'state':'accepted','task_id':'task-123'}));sys.exit()
print(json.dumps({'state':'completed','images':[{'mime_type':'image/png','b64_json':os.environ['BRIDGE_TEST_PNG']}]}))
`
	require.NoError(t, os.WriteFile(executable, []byte("#!"+python+"\n"+script), 0700))
	t.Setenv("BRIDGE_TEST_LOG", log)
	t.Setenv("BRIDGE_TEST_PNG", museTestPNG(t))
	cfg := &config.Config{BatchImage: config.BatchImageConfig{PythonBridgeExecutable: executable, DeliveryCOSPrefix: testOpenAIImageBatchObjectPrefix}}
	p := newBridgeImagesProvider(cfg, newFakeOpenAIImageBatchStore(), &OpenAIGatewayService{concurrencyService: NewConcurrencyService(&museSlotCache{})}, provider)
	a := museTestAccount("https://ark.cn-beijing.volces.com/api/v3")
	a.Credentials["api_key"] = "ark-offline"
	model := "doubao-seedream-5-0-pro-260628"
	if provider == BatchImageProviderWan {
		model = "wan2.7-image"
		a.Credentials["base_url"] = "https://ws-abcdefgh.cn-beijing.maas.aliyuncs.com/api/v1"
		a.Credentials["api_key"] = "sk-offline"
	}
	a.Credentials["model_mapping"] = map[string]any{model: model}
	require.True(t, p.SubmissionAvailable())
	return p, a, log
}
func TestImageBridgeSequentialRecovery(t *testing.T) {
	for _, provider := range []string{BatchImageProviderWan, BatchImageProviderSeedream} {
		for _, prompt := range []string{"image", "die"} {
			t.Run(provider+"/"+prompt, func(t *testing.T) {
				p, a, log := bridgeFixture(t, provider)
				var model string
				for key := range a.GetModelMapping() {
					model = key
				}
				job := &BatchImageJob{BatchID: "imgbatch_bridge", Provider: provider, Model: model, ItemCount: 1}
				accepted, err := p.Submit(t.Context(), job, a, BatchImageInput{BatchID: job.BatchID, Model: model, ImageSize: "2K", AspectRatio: "1:1", Items: []BatchImageInputItem{{CustomID: "one", Prompt: prompt}}})
				require.NoError(t, err)
				job.ProviderJobName = &accepted.ProviderJobName
				job.ProviderInputRef = &accepted.ProviderInputRef
				for range 4 {
					e := p.executor.(*bridgeImageExecutor)
					recovered := *e
					replacement := NewOpenAIImagesBatchProvider(p.store, &recovered, p.objectPrefix)
					replacement.providerName = p.Name()
					p = replacement
					_, err = p.Advance(t.Context(), job, a)
					require.NoError(t, err)
				}
				operations, err := os.ReadFile(log)
				require.NoError(t, err)
				require.Equal(t, 1, strings.Count(string(operations), "submit\n"))
				if prompt == "image" && provider == BatchImageProviderWan {
					require.Contains(t, string(operations), "collect\n")
				}
				body, _, _, err := p.store.Open(t.Context(), p.outputKey(job.BatchID))
				require.NoError(t, err)
				defer body.Close()
				var result map[string]any
				require.NoError(t, json.NewDecoder(body).Decode(&result))
				if prompt == "die" {
					require.Contains(t, result, "error")
				} else {
					require.Contains(t, result, "response")
				}
			})
		}
	}
}
func TestImageBridgeAdmission(t *testing.T) {
	p, _, _ := bridgeFixture(t, BatchImageProviderSeedream)
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	svc.ProviderRegistry = NewBatchImageProviderRegistry(p)
	req := BatchImageSubmitRequest{Model: "doubao-seedream-5-0-pro-260628", Items: []BatchImageSubmitItem{{Prompt: "image"}}}
	normalized, err := svc.validateSubmitRequest(req)
	require.NoError(t, err)
	require.Equal(t, "2K", normalized.ImageSize)
	require.Equal(t, "1:1", normalized.AspectRatio)
	require.False(t, batchImageJobRequiresUpscale(&BatchImageJob{Provider: p.Name(), ImageSize: "2K"}))
	for _, mutate := range []func(*BatchImageSubmitRequest){func(r *BatchImageSubmitRequest) { r.ImageSize = "4K" }, func(r *BatchImageSubmitRequest) { r.AspectRatio = "9:16" }, func(r *BatchImageSubmitRequest) {
		r.Items = []BatchImageSubmitItem{{Prompt: strings.Repeat("字", 6001)}}
	}, func(r *BatchImageSubmitRequest) { r.Provider = BatchImageProviderOpenAI }} {
		bad := req
		mutate(&bad)
		_, err := svc.validateSubmitRequest(bad)
		require.Error(t, err)
	}
	svc.ProviderRegistry = NewBatchImageProviderRegistry()
	_, err = svc.validateSubmitRequest(req)
	require.Error(t, err)
	for _, path := range []string{"python3", "/missing/python"} {
		p := newBridgeImagesProvider(&config.Config{BatchImage: config.BatchImageConfig{PythonBridgeExecutable: path}}, nil, nil, BatchImageProviderWan)
		require.False(t, p.SubmissionAvailable())
	}
}
func TestImageBridgeProxyAndConcurrency(t *testing.T) {
	p, a, log := bridgeFixture(t, BatchImageProviderSeedream)
	e := p.executor.(*bridgeImageExecutor)
	r := OpenAIImageBatchItemRequest{Model: "doubao-seedream-5-0-pro-260628", Prompt: "image", ImageSize: "2K", AspectRatio: "1:1"}
	a.ProxyID = new(int64)
	*a.ProxyID = 123
	_, err := e.Execute(t.Context(), a, r, func() error { t.Fatal("invalid proxy marked attempt"); return nil })
	require.Error(t, err)
	_, err = os.Stat(log)
	require.True(t, os.IsNotExist(err))
	a.ProxyID = nil
	cache := &museSlotCache{}
	e.gateway.concurrencyService = NewConcurrencyService(cache)
	a.Concurrency = 1
	slot, err := e.gateway.tryAcquireAccountSlot(context.Background(), a.ID, 1)
	require.NoError(t, err)
	require.True(t, slot.Acquired)
	_, err = e.Execute(t.Context(), a, r, func() error { t.Fatal("busy account marked attempt"); return nil })
	require.ErrorIs(t, err, errBatchImageOpenAIAccountBusy)
	slot.ReleaseFunc()
	t.Setenv("NO_PROXY", "*")
	t.Setenv("no_proxy", "*")
	t.Setenv("BRIDGE_EXPECT_PROXY", "http://127.0.0.1:1234")
	refs := []string{}
	_, err = e.runner.run(t.Context(), bridgeRequest{Provider: "seedream", Operation: "submit", Prompt: "image", References: &refs, Parameters: &bridgeParameters{Size: "2048x2048"}}, "http://127.0.0.1:1234")
	require.NoError(t, err)
}

func TestImageBridgeRealCoreCapabilities(t *testing.T) {
	executable := os.Getenv("BRIDGE_CORE_PYTHON")
	if executable == "" {
		t.Skip("set BRIDGE_CORE_PYTHON to installed bridge interpreter")
	}
	for _, provider := range []string{BatchImageProviderWan, BatchImageProviderSeedream} {
		p := newBridgeImagesProvider(&config.Config{BatchImage: config.BatchImageConfig{PythonBridgeExecutable: executable, DeliveryCOSPrefix: testOpenAIImageBatchObjectPrefix}}, newFakeOpenAIImageBatchStore(), &OpenAIGatewayService{concurrencyService: NewConcurrencyService(&museSlotCache{})}, provider)
		require.True(t, p.SubmissionAvailable())
		e := p.executor.(*bridgeImageExecutor)
		require.Len(t, e.models, 2)
		for _, model := range e.models {
			require.Equal(t, "2K", model.DefaultImageSize)
			require.Len(t, model.AspectRatios, 9)
			require.NotEmpty(t, model.size("2K", "1:1"))
		}
	}
}
func TestImageBridgePricing(t *testing.T) {
	for _, provider := range []string{BatchImageProviderWan, BatchImageProviderSeedream} {
		p, a, _ := bridgeFixture(t, provider)
		var model string
		for key := range a.GetModelMapping() {
			model = key
		}
		svc, _, _, _, _ := newTestBatchImagePublicService(true)
		svc.ProviderRegistry = NewBatchImageProviderRegistry(p)
		req := BatchImageSubmitRequest{Model: model, ImageSize: "2K", Items: []BatchImageSubmitItem{{Prompt: "image"}}}
		svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0}
		_, err := svc.resolvePricingSnapshot(t.Context(), BatchImageOwner{UserID: 11}, req, provider, a)
		require.Error(t, err)
		svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.25}
		snap, err := svc.resolvePricingSnapshot(t.Context(), BatchImageOwner{UserID: 11}, req, provider, a)
		require.NoError(t, err)
		require.NotNil(t, snap)
	}
}

func TestImageBridgeInvalidProcessOutput(t *testing.T) {
	p, _, _ := bridgeFixture(t, BatchImageProviderWan)
	e := p.executor.(*bridgeImageExecutor)
	python, err := exec.LookPath("python3")
	require.NoError(t, err)
	for _, source := range []string{"print('not-json')", "print('{}')", "print('x'*(35*1024*1024))"} {
		require.NoError(t, os.WriteFile(e.runner.executable, []byte("#!"+python+"\n"+source), 0700))
		disabled := newBridgeImagesProvider(&config.Config{BatchImage: config.BatchImageConfig{PythonBridgeExecutable: e.runner.executable, DeliveryCOSPrefix: testOpenAIImageBatchObjectPrefix}}, p.store, e.gateway, p.Name())
		require.False(t, disabled.SubmissionAvailable())
	}
}
func TestImageBridgeSingleSettlement(t *testing.T) {
	for _, provider := range []string{BatchImageProviderWan, BatchImageProviderSeedream} {
		repo := newFakeBatchImageRepository()
		job := testSettlingBatchImageJob("imgbatch_bridge_settle")
		job.Provider = provider
		job.Model = "wan2.7-image"
		if provider == BatchImageProviderSeedream {
			job.Model = "doubao-seedream-5-0-pro-260628"
		}
		job.SuccessCount = 1
		job.FailCount = 1
		job.ItemCount = 2
		repo.jobs[job.BatchID] = job
		billing := &fakeBatchImageBillingRepo{}
		settle := &BatchImageSettlementService{Repo: repo, BillingRepo: billing, Pricing: &fakeBatchImagePricingResolver{unitPrice: 0.25}}
		result, err := settle.Settle(t.Context(), job.BatchID)
		require.NoError(t, err)
		require.Equal(t, 0.25, result.ActualCost)
		result, err = settle.Settle(t.Context(), job.BatchID)
		require.NoError(t, err)
		require.True(t, result.AlreadySettled)
		require.Len(t, billing.captures, 1)
	}
}
