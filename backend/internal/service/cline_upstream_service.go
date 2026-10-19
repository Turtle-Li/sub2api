package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

// ClineUpstreamService 提供 Cline Pass 订阅模型的上游枚举、校验、钉住与测试能力，
// 结论与写法移植自 cline-pass-switcher（详见 cline_upstream_pin.go 的说明）。
//
// 与数据面共用同一份账号凭据与代理绑定：探测请求走 HTTPUpstream，因此账号的代理、
// 并发槽位与请求头覆写与真实转发一致。所有写操作只落 account.Extra 的两个键，
// 不需要数据库迁移。
const (
	// clineRecommendedModelsURL 是 Cline 客户端列订阅模型用的公开接口（无需鉴权）。
	clineRecommendedModelsURL = "https://" + clineAPIHost + "/api/v1/ai/cline/recommended-models"
	// clineUpstreamProbeTimeout 单次探测（真实推理）的上游超时。
	clineUpstreamProbeTimeout = 120 * time.Second
	// clineUpstreamValidateTimeout 单渠道校验的超时（只发最小请求）。
	clineUpstreamValidateTimeout = 60 * time.Second
	// clineUpstreamModelsTimeout 拉取模型清单的超时。
	clineUpstreamModelsTimeout = 30 * time.Second
	// clineUpstreamProbeMaxTokens 探测请求的最大输出，够回读路由元数据即可。
	clineUpstreamProbeMaxTokens = 256
	// clineUpstreamHarvestMaxTokens 假渠道探测的输出上限（请求必然在路由层失败）。
	clineUpstreamHarvestMaxTokens = 16
	// clineUpstreamValidateMaxTokens 单渠道校验的输出上限。
	clineUpstreamValidateMaxTokens = 16
	// clineUpstreamProbeModelLimit 单次批量探测的模型数量上限。
	clineUpstreamProbeModelLimit = 20
	// clineUpstreamValidateConcurrency 单渠道校验的并发度。
	clineUpstreamValidateConcurrency = 5
	// clineUpstreamModelsCacheTTL 模型清单的内存缓存时长。
	clineUpstreamModelsCacheTTL = time.Hour
	// clineUpstreamProbePlaceholder 假渠道名：让网关在路由层报错并列出可用渠道。
	clineUpstreamProbePlaceholder = "__probe__"
)

// clineUpstreamAvailableProvidersPattern 解析规划器管道错误文本里的渠道清单。
var clineUpstreamAvailableProvidersPattern = regexp.MustCompile(`(?i)Available providers(?:\s+are)?\s*:\s*([^.]+)`)

// clineUpstreamProviderSlugPattern 过滤错误文本里混入的 JSON 片段，只保留 slug 形状的渠道名。
var clineUpstreamProviderSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ErrClineUpstreamUnavailable 表示 Cline 上游钉住服务未启用。
var ErrClineUpstreamUnavailable = infraerrors.ServiceUnavailable(
	"CLINE_UPSTREAM_UNAVAILABLE", "Cline upstream pinning is unavailable",
)

// ClineUpstreamService 是 Cline 上游钉住的管理端服务。
type ClineUpstreamService struct {
	accountRepo  AccountRepository
	proxyRepo    ProxyRepository
	httpUpstream HTTPUpstream
	cfg          *config.Config

	modelsMu       sync.Mutex
	modelsCache    []ClineUpstreamModelInfo
	modelsCachedAt time.Time
}

// NewClineUpstreamService 构造 Cline 上游钉住服务。
func NewClineUpstreamService(
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	httpUpstream HTTPUpstream,
	cfg *config.Config,
) *ClineUpstreamService {
	return &ClineUpstreamService{
		accountRepo:  accountRepo,
		proxyRepo:    proxyRepo,
		httpUpstream: httpUpstream,
		cfg:          cfg,
	}
}

// ClineUpstreamModelInfo 是模型清单里的一项。
type ClineUpstreamModelInfo struct {
	ID     string `json:"id"`
	Source string `json:"source"` // subscription=订阅清单 / catalog=账号目录
}

// ClineUpstreamModelState 是单个模型的完整状态（管理端读模型）。
type ClineUpstreamModelState struct {
	Model      string            `json:"model"`
	Source     string            `json:"source"`
	Pin        ClineUpstreamPin  `json:"pin"`
	Meta       ClineUpstreamMeta `json:"meta"`
	Candidates []string          `json:"candidates"`
	Configured bool              `json:"configured"`
}

// ClineUpstreamState 是账号级的钉住状态快照。
type ClineUpstreamState struct {
	AccountID   int64                     `json:"account_id"`
	AccountName string                    `json:"account_name"`
	Platform    string                    `json:"platform"`
	Supported   bool                      `json:"supported"`
	BaseURL     string                    `json:"base_url"`
	Models      []ClineUpstreamModelState `json:"models"`
	FetchedAt   int64                     `json:"fetched_at"`
	ModelsError string                    `json:"models_error,omitempty"`
}

// ClineUpstreamProbeResult 是单模型探测结果。
type ClineUpstreamProbeResult struct {
	Model          string   `json:"model"`
	OK             bool     `json:"ok"`
	Error          string   `json:"error,omitempty"`
	Pipeline       string   `json:"pipeline"`
	Pinnable       bool     `json:"pinnable"`
	ActualProvider string   `json:"actual_provider"`
	CanonicalSlug  string   `json:"canonical_slug"`
	Fallbacks      []string `json:"fallbacks"`
	Plan           string   `json:"plan"`
	MS             int64    `json:"ms"`
	Upstreams      []string `json:"upstreams"`
	HarvestNote    string   `json:"harvest_note,omitempty"`
	// ResponseModel 是上游响应顶层声明的模型；RoutingRaw 是网关路由元数据原文。
	ResponseModel string `json:"response_model,omitempty"`
	RoutingRaw    string `json:"routing_raw,omitempty"`
}

// ClineUpstreamValidateResult 是单模型的全渠道校验地图。
type ClineUpstreamValidateResult struct {
	Model     string                         `json:"model"`
	Pipeline  string                         `json:"pipeline"`
	Upstreams []string                       `json:"upstreams"`
	Statuses  map[string]ClineUpstreamStatus `json:"statuses"`
	Summary   map[string]int                 `json:"summary"`
}

// ClineUpstreamTestResult 是单次钉住验证结果。
type ClineUpstreamTestResult struct {
	Model string `json:"model"`
	OK    bool   `json:"ok"`
	// Adherence 报告这次请求是否真的按钉住执行：adopted/fallback/not-adopted/violated/unresolved。
	Adherence          string   `json:"adherence"`
	Error              string   `json:"error,omitempty"`
	MS                 int64    `json:"ms"`
	Pipeline           string   `json:"pipeline"`
	ActualProvider     string   `json:"actual_provider"`
	ActualProviderName string   `json:"actual_provider_name"`
	CanonicalSlug      string   `json:"canonical_slug"`
	Content            string   `json:"content"`
	Targets            []string `json:"targets"`
	Exclude            []string `json:"exclude"`
	Candidates         []string `json:"candidates"`
}

// GetState 返回账号的钉住配置与探测快照（模型清单失败时仍返回已配置项）。
func (s *ClineUpstreamService) GetState(ctx context.Context, accountID int64) (*ClineUpstreamState, error) {
	account, err := s.loadClineAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	state := &ClineUpstreamState{
		AccountID:   account.ID,
		AccountName: account.Name,
		Platform:    account.Platform,
		Supported:   s.supportsUpstreamProbe(account),
		BaseURL:     strings.TrimSpace(account.GetOpenAIBaseURL()),
		FetchedAt:   time.Now().Unix(),
	}

	models := map[string]string{} // model -> source
	if state.Supported {
		listed, listErr := s.listModels(ctx, account)
		if listErr != nil {
			state.ModelsError = listErr.Error()
		}
		for _, item := range listed {
			if _, exists := models[item.ID]; !exists {
				models[item.ID] = item.Source
			}
		}
	}
	// 已配置或已探测的模型即使不在清单里也要展示，避免配置「消失」。
	for model := range clinePinsFromExtra(account.Extra) {
		if _, exists := models[model]; !exists {
			models[model] = "configured"
		}
	}
	for model := range clineMetasFromExtra(account.Extra) {
		if _, exists := models[model]; !exists {
			models[model] = "configured"
		}
	}

	state.Models = make([]ClineUpstreamModelState, 0, len(models))
	for model, source := range models {
		pin, _ := ClineUpstreamPinForAccount(account, model)
		meta, _ := ClineUpstreamMetaForAccount(account, model)
		candidates, _ := ClineUpstreamCandidate(pin, meta)
		state.Models = append(state.Models, ClineUpstreamModelState{
			Model:      model,
			Source:     source,
			Pin:        normalizeClinePinForJSON(pin),
			Meta:       normalizeClineMetaForJSON(meta),
			Candidates: normalizeClineListForJSON(candidates),
			Configured: !pin.IsZero(),
		})
	}
	sortClineUpstreamModels(state.Models)
	return state, nil
}

// normalizeClineListForJSON 保证列表字段序列化成 [] 而不是 null：Go 的 nil slice
// 会编码成 JSON null，前端按数组使用时会抛 "Cannot read properties of null"。
func normalizeClineListForJSON(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func normalizeClinePinForJSON(pin ClineUpstreamPin) ClineUpstreamPin {
	pin.Upstreams = normalizeClineListForJSON(pin.Upstreams)
	pin.Exclude = normalizeClineListForJSON(pin.Exclude)
	return pin
}

func normalizeClineMetaForJSON(meta ClineUpstreamMeta) ClineUpstreamMeta {
	meta.Upstreams = normalizeClineListForJSON(meta.Upstreams)
	if meta.Statuses == nil {
		meta.Statuses = map[string]ClineUpstreamStatus{}
	}
	return meta
}

// UpdatePins 全量覆盖账号的钉住配置（只写 cline_upstream_pins，不动探测快照）。
func (s *ClineUpstreamService) UpdatePins(ctx context.Context, accountID int64, pins map[string]ClineUpstreamPin) error {
	account, err := s.loadClineAccount(ctx, accountID)
	if err != nil {
		return err
	}
	normalized := make(map[string]any, len(pins))
	for model, pin := range pins {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		clean := ClineUpstreamPin{
			Mode:      normalizeClinePinMode(pin.Mode),
			Upstreams: sanitizeClineUpstreamList(pin.Upstreams),
			Exclude:   sanitizeClineUpstreamList(pin.Exclude),
			Sort:      normalizeClineSort(pin.Sort),
		}
		if clean.IsZero() {
			continue
		}
		normalized[model] = map[string]any{
			"mode":      clean.Mode,
			"upstreams": clean.Upstreams,
			"exclude":   clean.Exclude,
			"sort":      clean.Sort,
		}
	}
	if len(normalized) == 0 {
		return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{ClineUpstreamPinsExtraKey: nil})
	}
	return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{ClineUpstreamPinsExtraKey: normalized})
}

// Probe 探测指定模型（为空时探测清单里的全部订阅模型），刷新渠道清单与管道类型。
func (s *ClineUpstreamService) Probe(ctx context.Context, accountID int64, models []string) ([]ClineUpstreamProbeResult, error) {
	account, err := s.loadClineAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if err := s.requireProbeSupport(account); err != nil {
		return nil, err
	}
	targets := sanitizeClineUpstreamList(models)
	if len(targets) == 0 {
		listed, listErr := s.listModels(ctx, account)
		if listErr != nil {
			return nil, listErr
		}
		for _, item := range listed {
			if strings.HasPrefix(item.ID, clinePassModelPrefix) {
				targets = append(targets, item.ID)
			}
		}
	}
	if len(targets) == 0 {
		return nil, infraerrors.BadRequest("CLINE_UPSTREAM_NO_MODELS", "no cline-pass model to probe")
	}
	if len(targets) > clineUpstreamProbeModelLimit {
		targets = targets[:clineUpstreamProbeModelLimit]
	}

	results := make([]ClineUpstreamProbeResult, 0, len(targets))
	metaUpdates := make(map[string]any, len(targets))
	for _, model := range targets {
		result := s.probeModel(ctx, account, model)
		result.Upstreams = normalizeClineListForJSON(result.Upstreams)
		result.Fallbacks = normalizeClineListForJSON(result.Fallbacks)
		results = append(results, result)
		metaUpdates[model] = s.mergeProbeMeta(account, model, result)
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		ClineUpstreamMetaExtraKey: mergeClineMetaExtra(account.Extra, metaUpdates),
	}); err != nil {
		slog.Warn("cline_upstream_probe_persist_failed", "account_id", account.ID, "error", err)
	}
	return results, nil
}

// Validate 把模型的每个已知渠道各钉一次，标记真实可用性。
func (s *ClineUpstreamService) Validate(ctx context.Context, accountID int64, model string) (*ClineUpstreamValidateResult, error) {
	account, err := s.loadClineAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if err := s.requireProbeSupport(account); err != nil {
		return nil, err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, infraerrors.BadRequest("CLINE_UPSTREAM_MODEL_REQUIRED", "model is required")
	}
	meta, _ := ClineUpstreamMetaForAccount(account, model)
	if len(meta.Upstreams) == 0 {
		return nil, infraerrors.BadRequest("CLINE_UPSTREAM_NOT_PROBED", "probe the model first to learn its upstream list")
	}

	statuses := s.validateUpstreams(ctx, account, model, meta)
	summary := map[string]int{}
	merged := map[string]any{}
	for upstream, status := range statuses {
		// not-adopted 单独成列会让「不可用」被拆成两个数字；按 bad 汇总，行上保留精确判定。
		bucket := status.Status
		if bucket == ClineUpstreamStatusNotAdopted {
			bucket = ClineUpstreamStatusBad
		}
		summary[bucket]++
		merged[upstream] = map[string]any{
			"status":     status.Status,
			"ms":         status.MS,
			"note":       status.Note,
			"checked_at": status.CheckedAt,
		}
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		ClineUpstreamMetaExtraKey: mergeClineMetaExtra(account.Extra, map[string]any{
			model: map[string]any{
				"pipeline":     meta.Pipeline,
				"pinnable":     meta.Pinnable,
				"upstreams":    meta.Upstreams,
				"statuses":     merged,
				"validated_at": time.Now().Unix(),
			},
		}),
	}); err != nil {
		slog.Warn("cline_upstream_validate_persist_failed", "account_id", account.ID, "error", err)
	}
	if statuses == nil {
		statuses = map[string]ClineUpstreamStatus{}
	}
	return &ClineUpstreamValidateResult{
		Model:     model,
		Pipeline:  meta.Pipeline,
		Upstreams: normalizeClineListForJSON(meta.Upstreams),
		Statuses:  statuses,
		Summary:   summary,
	}, nil
}

// Test 用给定的临时钉住配置发一条真实请求，验证网关是否采纳（不落库）。
func (s *ClineUpstreamService) Test(ctx context.Context, accountID int64, model string, pin ClineUpstreamPin) (*ClineUpstreamTestResult, error) {
	account, err := s.loadClineAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if err := s.requireProbeSupport(account); err != nil {
		return nil, err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, infraerrors.BadRequest("CLINE_UPSTREAM_MODEL_REQUIRED", "model is required")
	}
	clean := ClineUpstreamPin{
		Mode:      normalizeClinePinMode(pin.Mode),
		Upstreams: sanitizeClineUpstreamList(pin.Upstreams),
		Exclude:   sanitizeClineUpstreamList(pin.Exclude),
		Sort:      normalizeClineSort(pin.Sort),
	}
	meta, _ := ClineUpstreamMetaForAccount(account, model)

	body := map[string]any{
		"model":      model,
		"messages":   []map[string]any{{"role": "user", "content": "Reply with the word OK"}},
		"max_tokens": clineUpstreamProbeMaxTokens,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	raw = injectClineUpstreamPrefs(raw, clean, meta)

	started := time.Now()
	status, respBody, callErr := s.chatCompletion(ctx, account, raw, clineUpstreamProbeTimeout)
	result := &ClineUpstreamTestResult{
		Model:      model,
		Targets:    normalizeClineListForJSON(clean.Upstreams),
		Exclude:    normalizeClineListForJSON(clean.Exclude),
		MS:         time.Since(started).Milliseconds(),
		Pipeline:   meta.Pipeline,
		Candidates: normalizeClineListForJSON(candidatesForTest(clean, meta)),
	}
	if callErr != nil {
		result.Error = callErr.Error()
		return result, nil
	}
	if status < 200 || status >= 300 {
		result.Error = truncate(strings.TrimSpace(string(respBody)), 400)
		return result, nil
	}
	routing := parseClineUpstreamRouting(respBody)
	result.ActualProvider = routing.finalProvider
	result.ActualProviderName = routing.finalProviderName
	result.CanonicalSlug = routing.canonicalSlug
	result.Content = truncate(routing.content, 120)
	if routing.pipeline != "" {
		result.Pipeline = routing.pipeline
	}
	// 钉住是否被采纳才是这次测试要回答的问题：由别的渠道服务就是没生效。
	adherence := EvaluateClinePinAdherence(clean, routing.finalProvider)
	result.Adherence = string(adherence)
	result.OK = ClinePinAdherenceOK(adherence)
	if !result.OK {
		result.Error = "pin not adopted: served by " + firstNonEmpty(routing.finalProvider, "(unknown)")
	}
	return result, nil
}

// ---------- 内部实现 ----------

type clineUpstreamRouting struct {
	content           string
	pipeline          string
	canonicalSlug     string
	finalProvider     string
	finalProviderName string
	fallbacks         []string
	plan              string
	// responseModel 是响应顶层声明的模型（直连管道为 OpenRouter 真实 id）。
	responseModel string
	// routingRaw 是网关路由元数据的原始 JSON，用于解释「为什么只有这个渠道」。
	routingRaw string
}

func (s *ClineUpstreamService) loadClineAccount(ctx context.Context, accountID int64) (*Account, error) {
	if s == nil || s.accountRepo == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "CLINE_UPSTREAM_UNAVAILABLE", "cline upstream service is not enabled")
	}
	if accountID <= 0 {
		return nil, infraerrors.BadRequest("CLINE_UPSTREAM_INVALID_ACCOUNT", "invalid account id")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, infraerrors.NotFound("CLINE_UPSTREAM_ACCOUNT_NOT_FOUND", "account not found")
	}
	if !account.IsCline() {
		return nil, infraerrors.BadRequest("CLINE_UPSTREAM_NOT_CLINE", "account is not a cline account")
	}
	return account, nil
}

// supportsUpstreamProbe 报告账号能否走官方 Cline 网关做探测：API Key 账号 + 官方主机。
// 自定义中转的 Key 不发往官方端点，也不做上游枚举（钉住配置仍可手工填写）。
func (s *ClineUpstreamService) supportsUpstreamProbe(account *Account) bool {
	return account != nil && account.IsCline() && account.Type == AccountTypeAPIKey && isOfficialClineHost(account.GetOpenAIBaseURL())
}

func (s *ClineUpstreamService) requireProbeSupport(account *Account) error {
	if !s.supportsUpstreamProbe(account) {
		return infraerrors.BadRequest("CLINE_UPSTREAM_UNSUPPORTED_ACCOUNT",
			"upstream probing requires a Cline API-key account pointing at the official api.cline.bot host")
	}
	if strings.TrimSpace(account.GetCNAPIKey()) == "" {
		return infraerrors.BadRequest("CLINE_UPSTREAM_NO_APIKEY", "account api_key is empty")
	}
	return nil
}

// listModels 汇总订阅模型（公开接口）与账号目录模型，带内存缓存。
func (s *ClineUpstreamService) listModels(ctx context.Context, account *Account) ([]ClineUpstreamModelInfo, error) {
	s.modelsMu.Lock()
	if len(s.modelsCache) > 0 && time.Since(s.modelsCachedAt) < clineUpstreamModelsCacheTTL {
		cached := append([]ClineUpstreamModelInfo(nil), s.modelsCache...)
		s.modelsMu.Unlock()
		return cached, nil
	}
	s.modelsMu.Unlock()

	seen := map[string]struct{}{}
	models := make([]ClineUpstreamModelInfo, 0, 32)
	appendModel := func(id, source string) {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		models = append(models, ClineUpstreamModelInfo{ID: id, Source: source})
	}

	var firstErr error
	// 订阅清单：Cline 客户端同源的公开接口，无需鉴权。
	if status, body, err := s.publicGet(ctx, clineRecommendedModelsURL); err != nil {
		firstErr = err
	} else if status >= 200 && status < 300 {
		list := gjson.GetBytes(body, "clinePass")
		if !list.IsArray() {
			list = gjson.GetBytes(body, "data.clinePass")
		}
		for _, item := range list.Array() {
			id := item.String()
			if item.IsObject() {
				id = item.Get("id").String()
			}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), clinePassModelPrefix) {
				appendModel(id, "subscription")
			}
		}
	} else {
		firstErr = fmt.Errorf("recommended-models returned HTTP %d", status)
	}

	// 账号目录：只列订阅模型前缀，避免把目录模型淹没进钉住表。
	if status, body, err := s.accountGetModels(ctx, account); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else if status >= 200 && status < 300 {
		for _, item := range gjson.GetBytes(body, "data").Array() {
			id := strings.TrimSpace(item.Get("id").String())
			if strings.HasPrefix(strings.ToLower(id), clinePassModelPrefix) {
				appendModel(id, "catalog")
			}
		}
	}

	if len(models) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, infraerrors.New(http.StatusBadGateway, "CLINE_UPSTREAM_NO_MODELS", "no cline-pass model returned by Cline")
	}

	s.modelsMu.Lock()
	s.modelsCache = append([]ClineUpstreamModelInfo(nil), models...)
	s.modelsCachedAt = time.Now()
	s.modelsMu.Unlock()
	return models, nil
}

// probeModel 探测单个模型：真实请求回读管道与实际上游，再用假渠道拉取可用渠道清单。
func (s *ClineUpstreamService) probeModel(ctx context.Context, account *Account, model string) ClineUpstreamProbeResult {
	body, err := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]any{{"role": "user", "content": "Reply with the word OK"}},
		"max_tokens": clineUpstreamProbeMaxTokens,
	})
	if err != nil {
		return ClineUpstreamProbeResult{Model: model, Error: err.Error()}
	}
	started := time.Now()
	status, respBody, callErr := s.chatCompletion(ctx, account, body, clineUpstreamProbeTimeout)
	result := ClineUpstreamProbeResult{Model: model, MS: time.Since(started).Milliseconds()}
	if callErr != nil {
		result.Error = callErr.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Error = truncate(strings.TrimSpace(string(respBody)), 400)
		return result
	}
	routing := parseClineUpstreamRouting(respBody)
	result.OK = true
	result.Pipeline = routing.pipeline
	result.Pinnable = routing.pipeline != ""
	result.ActualProvider = routing.finalProvider
	result.CanonicalSlug = routing.canonicalSlug
	result.Fallbacks = routing.fallbacks
	result.Plan = truncate(routing.plan, 240)
	result.ResponseModel = routing.responseModel
	result.RoutingRaw = routing.routingRaw

	harvest := s.harvestAvailableProviders(ctx, account, model, routing.pipeline)
	result.HarvestNote = harvest.Note
	// 只有网关真的按 only 过滤过，才说明该模型存在可选的渠道池；否则是单一上游，
	// 钉住没有意义（也不能钉）。
	result.Pinnable = harvest.Selectable
	result.Upstreams = dedupeClineUpstreams(append(append([]string{}, harvest.Providers...), routing.fallbacks...))
	return result
}

// clineUpstreamHarvest 是一次渠道清单采集的结果。
type clineUpstreamHarvest struct {
	// Providers 是候选渠道；Selectable 为 false 时它是「唯一实际上游」而不是可选项。
	Providers []string
	// Selectable 为 true 表示网关确实按 only 过滤并在路由层报错，说明该模型有可选渠道池。
	Selectable bool
	// Note 是采集说明：失败或「网关忽略限制」时给出原始证据。
	Note string
}

// harvestAvailableProviders 故意携带不存在的 only，让网关在路由层报错并列出可用渠道
// （不产生 token 消耗）。
//
// 三种结果要分开处理，否则会把「没有可选渠道」误判成「可钉住」：
//  1. 网关按 only 过滤并报错 → 拿到可选渠道池（Selectable=true）；
//  2. 网关直接忽略 only 并正常应答 → 该模型是单一上游（如 private/* 贡献者模型），
//     没有可钉的渠道池；此时从成功响应里回读实际上游，作为唯一候选返回；
//  3. 其它错误 → 原样记录错误片段。
//
// 两条管道的写法不同，先按探测到的管道发，再补发另一种，避免管道误判导致漏采。
func (s *ClineUpstreamService) harvestAvailableProviders(ctx context.Context, account *Account, model, pipeline string) clineUpstreamHarvest {
	forms := []string{pipeline}
	if pipeline == ClinePipelineDirect {
		forms = append(forms, ClinePipelinePlanner)
	} else {
		forms = append(forms, ClinePipelineDirect)
	}

	var (
		ignoredNote string
		observed    []string
		lastErrNote string
	)
	for _, form := range forms {
		providers, note, selectable, observedProvider := s.harvestWithForm(ctx, account, model, form)
		if selectable && len(providers) > 0 {
			return clineUpstreamHarvest{Providers: providers, Selectable: true}
		}
		if observedProvider != "" {
			ignoredNote = note
			observed = []string{observedProvider}
			break
		}
		if note != "" {
			lastErrNote = note
		}
	}
	if len(observed) > 0 {
		return clineUpstreamHarvest{Providers: observed, Selectable: false, Note: ignoredNote}
	}
	if lastErrNote == "" {
		lastErrNote = "gateway did not report an error for the placeholder provider"
	}
	return clineUpstreamHarvest{Note: truncate(lastErrNote, 400)}
}

// harvestWithForm 用单一管道写法发一次占位渠道请求。
// 返回 (渠道清单, 说明, 是否为可选渠道池, 网关忽略限制时回读到的实际上游)。
func (s *ClineUpstreamService) harvestWithForm(ctx context.Context, account *Account, model, pipeline string) ([]string, string, bool, string) {
	base := map[string]any{
		"model":      model,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		"max_tokens": clineUpstreamHarvestMaxTokens,
	}
	payload := base
	if pipeline == ClinePipelineDirect {
		payload["provider"] = map[string]any{"only": []string{clineUpstreamProbePlaceholder}}
	} else {
		payload["providerOptions"] = map[string]any{"gateway": map[string]any{"only": []string{clineUpstreamProbePlaceholder}}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err.Error(), false, ""
	}
	status, respBody, callErr := s.chatCompletion(ctx, account, body, clineUpstreamValidateTimeout)
	if callErr != nil {
		return nil, callErr.Error(), false, ""
	}
	message := clineUpstreamErrorMessage(status, respBody)
	if message == "" {
		// 网关忽略了 only 并正常应答：该模型没有可选渠道池，回读实际上游。
		actual := parseClineUpstreamRouting(respBody).finalProvider
		note := "gateway ignored the placeholder provider restriction: this model has a single upstream and cannot be pinned"
		if actual != "" {
			note = "gateway ignored the placeholder provider restriction; single upstream observed: " + actual
		}
		return nil, note, false, actual
	}
	if providers := parseClineAvailableProviders(message, pipeline); len(providers) > 0 {
		return providers, "", true, ""
	}
	return nil, truncate(message, 240), false, ""
}

// validateUpstreams 并发地把每个渠道各钉一次，分类真实可用性。
func (s *ClineUpstreamService) validateUpstreams(ctx context.Context, account *Account, model string, meta ClineUpstreamMeta) map[string]ClineUpstreamStatus {
	statuses := make(map[string]ClineUpstreamStatus, len(meta.Upstreams))
	var mu sync.Mutex
	sem := make(chan struct{}, clineUpstreamValidateConcurrency)
	var wg sync.WaitGroup
	now := time.Now().Unix()

	for _, upstream := range meta.Upstreams {
		wg.Add(1)
		go func(upstream string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			body, err := json.Marshal(map[string]any{
				"model":      model,
				"messages":   []map[string]any{{"role": "user", "content": "hi"}},
				"max_tokens": clineUpstreamValidateMaxTokens,
				"provider":   map[string]any{"only": []string{upstream}},
				"providerOptions": map[string]any{
					"gateway": map[string]any{"only": []string{upstream}},
				},
			})
			if err != nil {
				mu.Lock()
				statuses[upstream] = ClineUpstreamStatus{Status: ClineUpstreamStatusUnknown, Note: err.Error(), CheckedAt: now}
				mu.Unlock()
				return
			}
			started := time.Now()
			status, respBody, callErr := s.chatCompletion(ctx, account, body, clineUpstreamValidateTimeout)
			entry := ClineUpstreamStatus{MS: time.Since(started).Milliseconds(), CheckedAt: now}
			switch {
			case callErr != nil:
				entry.Status = ClineUpstreamStatusUnknown
				entry.Note = truncate(callErr.Error(), 160)
			case status >= 200 && status < 300:
				// 200 不是钉住生效的证据：网关会静默丢弃无法识别的 only 并自行选渠道。
				// 唯一诚实的判据是响应回读到的实际渠道是否就是被钉的那一个。
				actual := parseClineUpstreamRouting(respBody).finalProvider
				switch {
				case actual == "":
					entry.Status = ClineUpstreamStatusUnknown
					entry.Note = "response did not name the serving channel; the pin could not be verified"
				case normalizeClineSlug(actual) == normalizeClineSlug(upstream):
					entry.Status = ClineUpstreamStatusOK
				default:
					entry.Status = ClineUpstreamStatusNotAdopted
					entry.Note = truncate("pin not adopted: served by "+actual, 160)
				}
			default:
				message := clineUpstreamErrorMessage(status, respBody)
				entry.Status = classifyClineUpstreamError(message)
				entry.Note = truncate(message, 160)
			}
			mu.Lock()
			statuses[upstream] = entry
			mu.Unlock()
		}(upstream)
	}
	wg.Wait()
	return statuses
}

// mergeProbeMeta 把探测结果合并进已有的 Extra 快照（保留历史校验状态）。
func (s *ClineUpstreamService) mergeProbeMeta(account *Account, model string, result ClineUpstreamProbeResult) map[string]any {
	previous, _ := ClineUpstreamMetaForAccount(account, model)
	statuses := map[string]any{}
	for upstream, status := range previous.Statuses {
		statuses[upstream] = map[string]any{
			"status":     status.Status,
			"ms":         status.MS,
			"note":       status.Note,
			"checked_at": status.CheckedAt,
		}
	}
	upstreams := previous.Upstreams
	if len(result.Upstreams) > 0 {
		upstreams = result.Upstreams
	}
	entry := map[string]any{
		"pipeline":       result.Pipeline,
		"pinnable":       result.Pinnable,
		"upstreams":      upstreams,
		"statuses":       statuses,
		"last_provider":  result.ActualProvider,
		"canonical_slug": result.CanonicalSlug,
		"probed_at":      time.Now().Unix(),
	}
	if result.Error != "" {
		entry["last_error"] = truncate(result.Error, 240)
	} else {
		entry["last_error"] = ""
	}
	// 采集说明是「为什么只有这几个渠道」的原始证据，必须随快照落库，否则刷新页面就丢了。
	entry["harvest_note"] = truncate(result.HarvestNote, 400)
	entry["response_model"] = truncate(result.ResponseModel, 200)
	entry["routing_raw"] = truncate(result.RoutingRaw, 800)
	if previous.ValidatedAt != 0 {
		entry["validated_at"] = previous.ValidatedAt
	}
	return entry
}

// ---------- 低层 HTTP ----------

// chatCompletion 以账号凭据向 Cline Chat Completions 端点发一条请求。
func (s *ClineUpstreamService) chatCompletion(ctx context.Context, account *Account, body []byte, timeout time.Duration) (int, []byte, error) {
	target := buildOpenAIChatCompletionsURL(account.GetOpenAIBaseURL())
	validated, err := cnValidateProbeURL(s.cfg, target)
	if err != nil {
		return 0, nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, validated, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCNAPIKey()))
	account.ApplyHeaderOverrides(req.Header)
	return s.doUpstream(req, account)
}

// accountGetModels 用账号凭据拉取 Cline 模型目录。
func (s *ClineUpstreamService) accountGetModels(ctx context.Context, account *Account) (int, []byte, error) {
	target := buildOpenAIEndpointURL(account.GetOpenAIBaseURL(), "/v1/models")
	validated, err := cnValidateProbeURL(s.cfg, target)
	if err != nil {
		return 0, nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, clineUpstreamModelsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, validated, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCNAPIKey()))
	account.ApplyHeaderOverrides(req.Header)
	return s.doUpstream(req, account)
}

// publicGet 请求无需鉴权的公开接口。
func (s *ClineUpstreamService) publicGet(ctx context.Context, target string) (int, []byte, error) {
	validated, err := cnValidateProbeURL(s.cfg, target)
	if err != nil {
		return 0, nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, clineUpstreamModelsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, validated, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpUpstream.Do(req, "", 0, 1)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, cnQuotaMaxBodyBytes))
	return resp.StatusCode, body, nil
}

// doUpstream 经账号绑定的代理发出请求并读取受限长度的响应体。
func (s *ClineUpstreamService) doUpstream(req *http.Request, account *Account) (int, []byte, error) {
	resp, err := s.httpUpstream.Do(req, s.resolveProxyURL(req.Context(), account), account.ID, maxInt(account.Concurrency, 1))
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, cnQuotaMaxBodyBytes))
	return resp.StatusCode, body, nil
}

func (s *ClineUpstreamService) resolveProxyURL(ctx context.Context, account *Account) string {
	if account == nil || account.ProxyID == nil {
		return ""
	}
	if account.Proxy != nil {
		return account.Proxy.URL()
	}
	if s != nil && s.proxyRepo != nil {
		if proxy, err := s.proxyRepo.GetByID(ctx, *account.ProxyID); err == nil && proxy != nil {
			account.Proxy = proxy
			return proxy.URL()
		}
	}
	return ""
}

// ---------- 解析与分类 ----------

// parseClineUpstreamRouting 从 Cline 响应里回读管道类型与实际上游。
//   - 规划器管道（Vercel AI Gateway）：provider_metadata.gateway.routing（canonicalSlug/finalProvider）；
//   - 直连管道（OpenRouter）：顶层 provider（显示名）与 model（真实 OpenRouter ID）。
func parseClineUpstreamRouting(body []byte) clineUpstreamRouting {
	payload := body
	if data := gjson.GetBytes(body, "data"); data.IsObject() && data.Get("choices").Exists() {
		payload = []byte(data.Raw)
	}
	message := gjson.GetBytes(payload, "choices.0.message")
	routing := message.Get("provider_metadata.gateway.routing")
	if !routing.Exists() {
		routing = gjson.GetBytes(payload, "provider_metadata.gateway.routing")
	}
	direct := ""
	if value := gjson.GetBytes(payload, "provider"); value.Type == gjson.String {
		direct = strings.TrimSpace(value.String())
	}

	result := clineUpstreamRouting{
		content:           message.Get("content").String(),
		finalProviderName: direct,
		plan:              routing.Get("planningReasoning").String(),
	}
	if final := strings.TrimSpace(routing.Get("finalProvider").String()); final != "" {
		result.pipeline = ClinePipelinePlanner
		result.finalProvider = final
		result.finalProviderName = final
	} else if direct != "" {
		result.pipeline = ClinePipelineDirect
		result.finalProvider = clineProviderSlug(direct)
	}
	result.canonicalSlug = strings.TrimSpace(routing.Get("canonicalSlug").String())
	if result.canonicalSlug == "" {
		if model := strings.TrimSpace(gjson.GetBytes(payload, "model").String()); strings.Contains(model, "/") {
			result.canonicalSlug = model
		}
	}
	for _, item := range routing.Get("fallbacksAvailable").Array() {
		if text := strings.TrimSpace(item.String()); text != "" {
			result.fallbacks = append(result.fallbacks, text)
		}
	}
	result.responseModel = strings.TrimSpace(gjson.GetBytes(payload, "model").String())
	// 路由元数据原样留档：私有/单一上游模型只有这一段能说明它到底被谁服务。
	if routing.Exists() {
		result.routingRaw = truncate(strings.TrimSpace(routing.Raw), 800)
	} else if metadata := gjson.GetBytes(payload, "provider_metadata"); metadata.Exists() {
		result.routingRaw = truncate(strings.TrimSpace(metadata.Raw), 800)
	}
	return result
}

// clineUpstreamErrorMessage 把上游响应归一成错误文本；成功响应返回空串。
func clineUpstreamErrorMessage(status int, body []byte) string {
	if value := gjson.GetBytes(body, "error"); value.Exists() {
		if value.Type == gjson.String {
			return strings.TrimSpace(value.String())
		}
		return strings.TrimSpace(value.Raw)
	}
	if dataErr := gjson.GetBytes(body, "data.error"); dataErr.Exists() {
		if dataErr.Type == gjson.String {
			return strings.TrimSpace(dataErr.String())
		}
		return strings.TrimSpace(dataErr.Raw)
	}
	if status < 200 || status >= 300 {
		return truncate(strings.TrimSpace(string(body)), 400)
	}
	return ""
}

// parseClineAvailableProviders 从「假渠道」报错里解析可用渠道清单。
//   - 规划器管道：错误文本里的 "Available providers are: a, b, c"；
//   - 直连管道：错误文本里内嵌 JSON 的 error.metadata.available_providers。
func parseClineAvailableProviders(message, pipeline string) []string {
	_ = pipeline // 两条管道的错误体都可能是 JSON，且实测管道判定可能与清单来源不一致
	// 1) 错误文本里内嵌 JSON 的 available_providers（直连管道实测，规划器管道也可能出现）。
	if index := strings.Index(message, "{"); index >= 0 {
		payload := message[index:]
		for _, path := range []string{
			"error.metadata.available_providers",
			"metadata.available_providers",
			"error.available_providers",
			"available_providers",
		} {
			providers := gjson.Get(payload, path)
			if !providers.IsArray() {
				continue
			}
			out := make([]string, 0, len(providers.Array()))
			for _, item := range providers.Array() {
				if token := normalizeClineProviderToken(item.String()); clineUpstreamProviderSlugPattern.MatchString(token) {
					out = append(out, token)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}

	// 2) 规划器管道的自然语言清单："Available providers are: a, b, c."
	matches := clineUpstreamAvailableProvidersPattern.FindStringSubmatch(message)
	if len(matches) < 2 {
		return nil
	}
	out := make([]string, 0, 8)
	for _, token := range strings.Split(matches[1], ",") {
		// 错误文本里可能混入 JSON 片段（如 ","type":"invalid_request_error"）或引号/括号，
		// 统一去掉包裹字符后再按 slug 形状过滤。
		if token = normalizeClineProviderToken(token); clineUpstreamProviderSlugPattern.MatchString(token) {
			out = append(out, token)
		}
	}
	return out
}

// normalizeClineProviderToken 去掉引号、方括号、反引号与空白，得到裸渠道名。
func normalizeClineProviderToken(raw string) string {
	return strings.Trim(strings.TrimSpace(raw), "[]\"'`")
}

// classifyClineUpstreamError 把钉住失败分类（与 cline-pass-switcher 的校验地图一致）。
func classifyClineUpstreamError(message string) string {
	lower := strings.ToLower(message)
	switch {
	case lower == "":
		return ClineUpstreamStatusUnknown
	case strings.Contains(lower, "empty response content"):
		// 请求已到达模型（推理耗尽 max_tokens 导致内容为空），渠道本身可用。
		return ClineUpstreamStatusOK
	case strings.Contains(lower, "429"), strings.Contains(lower, "rate-limited"),
		strings.Contains(lower, "rate limited"), strings.Contains(lower, "temporarily rate"):
		return ClineUpstreamStatusLimited
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "re-authenticate"),
		strings.Contains(lower, "invalid api"), strings.Contains(lower, "401"):
		return ClineUpstreamStatusAuth
	case strings.Contains(lower, "invalid_request"), strings.Contains(lower, "modelid"),
		strings.Contains(lower, "no allowed providers"), strings.Contains(lower, "no available providers"),
		strings.Contains(lower, "not found"), strings.Contains(lower, "unsupported"):
		return ClineUpstreamStatusBad
	default:
		return ClineUpstreamStatusUnknown
	}
}

// clineProviderSlug 把直连管道的显示名归一成渠道 slug（GMICloud -> gmicloud）。
func clineProviderSlug(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), "-"))
}

func candidatesForTest(pin ClineUpstreamPin, meta ClineUpstreamMeta) []string {
	wanted, allow := ClineUpstreamCandidate(pin, meta)
	if len(wanted) > 0 {
		return wanted
	}
	return allow
}

// mergeClineMetaExtra 在已有快照上做「每模型浅合并」，避免覆盖其它模型的快照。
func mergeClineMetaExtra(extra map[string]any, updates map[string]any) map[string]any {
	merged := map[string]any{}
	if existing, ok := extra[ClineUpstreamMetaExtraKey].(map[string]any); ok {
		for model, value := range existing {
			merged[model] = value
		}
	}
	for model, value := range updates {
		existingEntry, _ := merged[model].(map[string]any)
		nextEntry, _ := value.(map[string]any)
		if existingEntry == nil || nextEntry == nil {
			merged[model] = value
			continue
		}
		entry := map[string]any{}
		for key, item := range existingEntry {
			entry[key] = item
		}
		for key, item := range nextEntry {
			entry[key] = item
		}
		merged[model] = entry
	}
	return merged
}

func clinePinsFromExtra(extra map[string]any) map[string]struct{} {
	out := map[string]struct{}{}
	if extra == nil {
		return out
	}
	pins, ok := extra[ClineUpstreamPinsExtraKey].(map[string]any)
	if !ok {
		return out
	}
	for model := range pins {
		out[model] = struct{}{}
	}
	return out
}

func clineMetasFromExtra(extra map[string]any) map[string]struct{} {
	out := map[string]struct{}{}
	if extra == nil {
		return out
	}
	metas, ok := extra[ClineUpstreamMetaExtraKey].(map[string]any)
	if !ok {
		return out
	}
	for model := range metas {
		out[model] = struct{}{}
	}
	return out
}

func sanitizeClineUpstreamList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= clineUpstreamMaxListLen {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dedupeClineUpstreams(values []string) []string {
	return sanitizeClineUpstreamList(values)
}

func sortClineUpstreamModels(models []ClineUpstreamModelState) {
	// 已配置的排前面，其余按模型名排序，保证管理端列表稳定。
	sort.SliceStable(models, func(i, j int) bool {
		return clineModelLess(models[i], models[j])
	})
}

func clineModelLess(left, right ClineUpstreamModelState) bool {
	if left.Configured != right.Configured {
		return left.Configured
	}
	return left.Model < right.Model
}
