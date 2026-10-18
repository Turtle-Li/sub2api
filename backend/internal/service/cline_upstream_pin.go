package service

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Cline Pass 上游钉住（结论移植自 cline-pass-switcher 的实测记录）。
//
// Cline Pass 订阅模型（cline-pass/*）在 Cline 网关之后分成两条路由管道，钉住写法完全不同：
//   - 直连管道（direct，后端 OpenRouter）：请求体顶层 provider.only/order/sort 生效，
//     响应顶层带 provider 与真实 model；
//   - 规划器管道（planner，后端 Vercel AI Gateway）：顶层 provider.* 会被 Cline 网关丢弃，
//     必须用 providerOptions.gateway.only/order/sort，响应带 provider_metadata.gateway.routing。
//
// 管道归属由 Cline 侧决定、可能随时间变化，因此以「账号 Extra 里的每模型探测快照」为准；
// 快照缺失（pipeline 为空）时两种形式同时注入，各自取用、互不干扰。
//
// Extra 结构（管理员通过 /admin/accounts/:id/cline-upstreams 写入，均为每模型对象）：
//
//	cline_upstream_pins: {
//	  "cline-pass/deepseek-v4-flash": {"mode":"strict","upstreams":["deepseek"],"exclude":[],"sort":""}
//	}
//	cline_upstream_meta: {
//	  "cline-pass/deepseek-v4-flash": {"pipeline":"planner","upstreams":["alibaba","baseten"],...}
//	}
//
// 注入是纯函数且只对 Cline 账号生效：没有配置时原样返回入参，非 Cline 账号零开销。
const (
	// ClineUpstreamPinsExtraKey 是账号 Extra 里每模型钉住配置的键。
	ClineUpstreamPinsExtraKey = "cline_upstream_pins"
	// ClineUpstreamMetaExtraKey 是账号 Extra 里每模型探测快照（管道/渠道清单/校验状态）的键。
	ClineUpstreamMetaExtraKey = "cline_upstream_meta"

	// ClinePinModeOff 不注入上游偏好（exclude/sort 仍按自动模式换算）。
	ClinePinModeOff = "off"
	// ClinePinModeStrict 严格钉住单个上游：only=[首个未排除渠道]。
	ClinePinModeStrict = "strict"
	// ClinePinModePreferred 优先+回退：order=勾选顺序，网关按序尝试。
	ClinePinModePreferred = "preferred"

	// ClinePipelineDirect / ClinePipelinePlanner 是两条路由管道。
	ClinePipelineDirect  = "direct"
	ClinePipelinePlanner = "planner"

	// ClineSortCost / ClineSortTTFT / ClineSortTPS 是渠道排序偏好。
	ClineSortCost = "cost"
	ClineSortTTFT = "ttft"
	ClineSortTPS  = "tps"

	// ClineUpstreamStatus* 是单个渠道实测后的分类（与 cline-pass-switcher 的校验地图一致）：
	// ok 可用 / limited 共享池限流 / bad 不可钉 / auth 账号鉴权问题 / unknown 未判定。
	ClineUpstreamStatusOK      = "ok"
	ClineUpstreamStatusLimited = "limited"
	ClineUpstreamStatusBad     = "bad"
	ClineUpstreamStatusAuth    = "auth"
	ClineUpstreamStatusUnknown = "unknown"

	// clineUpstreamMaxListLen 限制单个列表长度，避免异常配置无限放大请求体。
	clineUpstreamMaxListLen = 16
)

// ClineUpstreamPin 是单个模型的钉住配置。
type ClineUpstreamPin struct {
	Mode      string   `json:"mode"`
	Upstreams []string `json:"upstreams"`
	Exclude   []string `json:"exclude"`
	Sort      string   `json:"sort"`
}

// IsZero 报告配置是否不做任何注入。
func (p ClineUpstreamPin) IsZero() bool {
	return len(p.Upstreams) == 0 && len(p.Exclude) == 0 && strings.TrimSpace(p.Sort) == ""
}

// ClineUpstreamStatus 是单个渠道的实测状态（校验/钉住失败时自动学习）。
type ClineUpstreamStatus struct {
	Status    string `json:"status"`
	MS        int64  `json:"ms"`
	Note      string `json:"note"`
	CheckedAt int64  `json:"checked_at"`
}

// ClineUpstreamMeta 是单个模型的探测快照。
type ClineUpstreamMeta struct {
	Pipeline      string                         `json:"pipeline"`
	Pinnable      bool                           `json:"pinnable"`
	Upstreams     []string                       `json:"upstreams"`
	Statuses      map[string]ClineUpstreamStatus `json:"statuses"`
	LastProvider  string                         `json:"last_provider"`
	CanonicalSlug string                         `json:"canonical_slug"`
	LastError     string                         `json:"last_error"`
	// HarvestNote 是渠道清单采集说明：成功为空，失败时是网关原始错误片段。
	HarvestNote string `json:"harvest_note"`
	ProbedAt    int64  `json:"probed_at"`
	ValidatedAt int64  `json:"validated_at"`
}

// ClineUpstreamPinForAccount 读取账号在指定模型上的钉住配置；ok=false 表示未配置。
func ClineUpstreamPinForAccount(account *Account, model string) (ClineUpstreamPin, bool) {
	if account == nil || account.Extra == nil {
		return ClineUpstreamPin{}, false
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return ClineUpstreamPin{}, false
	}
	pins, ok := account.Extra[ClineUpstreamPinsExtraKey].(map[string]any)
	if !ok {
		return ClineUpstreamPin{}, false
	}
	raw, ok := pins[model].(map[string]any)
	if !ok {
		return ClineUpstreamPin{}, false
	}
	pin := ClineUpstreamPin{
		Mode:      normalizeClinePinMode(clineAnyString(raw["mode"])),
		Upstreams: clineAnyStringSlice(raw["upstreams"]),
		Exclude:   clineAnyStringSlice(raw["exclude"]),
		Sort:      normalizeClineSort(clineAnyString(raw["sort"])),
	}
	return pin, true
}

// ClineUpstreamMetaForAccount 读取账号在指定模型上的探测快照。
func ClineUpstreamMetaForAccount(account *Account, model string) (ClineUpstreamMeta, bool) {
	if account == nil || account.Extra == nil {
		return ClineUpstreamMeta{}, false
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return ClineUpstreamMeta{}, false
	}
	metas, ok := account.Extra[ClineUpstreamMetaExtraKey].(map[string]any)
	if !ok {
		return ClineUpstreamMeta{}, false
	}
	raw, ok := metas[model].(map[string]any)
	if !ok {
		return ClineUpstreamMeta{}, false
	}
	meta := ClineUpstreamMeta{
		Pipeline:      normalizeClinePipeline(clineAnyString(raw["pipeline"])),
		Upstreams:     clineAnyStringSlice(raw["upstreams"]),
		LastProvider:  clineAnyString(raw["last_provider"]),
		CanonicalSlug: clineAnyString(raw["canonical_slug"]),
		LastError:     clineAnyString(raw["last_error"]),
		HarvestNote:   clineAnyString(raw["harvest_note"]),
		Pinnable:      clineAnyBool(raw["pinnable"]),
		ProbedAt:      clineAnyInt64(raw["probed_at"]),
		ValidatedAt:   clineAnyInt64(raw["validated_at"]),
	}
	if statuses, ok := raw["statuses"].(map[string]any); ok {
		meta.Statuses = make(map[string]ClineUpstreamStatus, len(statuses))
		for upstream, value := range statuses {
			entry, ok := value.(map[string]any)
			if !ok {
				continue
			}
			meta.Statuses[upstream] = ClineUpstreamStatus{
				Status:    normalizeClineUpstreamStatus(clineAnyString(entry["status"])),
				MS:        clineAnyInt64(entry["ms"]),
				Note:      clineAnyString(entry["note"]),
				CheckedAt: clineAnyInt64(entry["checked_at"]),
			}
		}
	}
	return meta, true
}

// ApplyClineUpstreamPin 按账号配置给 Chat Completions 请求体注入上游偏好。
//
// 非 Cline 账号、无该模型配置、或配置为空时原样返回入参（不产生任何分配）。
// 注入失败（请求体不是 JSON 对象）时同样原样返回，绝不阻断转发。
func ApplyClineUpstreamPin(account *Account, body []byte) []byte {
	if account == nil || !account.IsCline() || len(body) == 0 {
		return body
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		return body
	}
	pin, ok := ClineUpstreamPinForAccount(account, model)
	if !ok || pin.IsZero() {
		return body
	}
	meta, _ := ClineUpstreamMetaForAccount(account, model)
	return injectClineUpstreamPrefs(body, pin, meta)
}

// injectClineUpstreamPrefs 按管道注入上游偏好：
//   - 规划器管道（Vercel AI Gateway）：providerOptions.gateway.{only,order,sort}；
//   - 直连管道（OpenRouter）：顶层 provider.{only,order,sort}；
//   - 管道未知：两种形式同时注入，各自取用、互不干扰。
//
// 排除列表统一换算成 only 白名单（网关不支持 exclude/ignore，实测被静默忽略）：
// 自动模式下 only=已知渠道-排除；优先+回退模式下 order=[勾选...] 且 only=已知渠道-排除；
// 严格钉住天然排除其他渠道，不需要白名单。
func injectClineUpstreamPrefs(body []byte, pin ClineUpstreamPin, meta ClineUpstreamMeta) []byte {
	excluded := make(map[string]struct{}, len(pin.Exclude))
	for _, upstream := range pin.Exclude {
		excluded[upstream] = struct{}{}
	}

	// 只有 strict/preferred 才使用勾选顺序；off 模式忽略勾选、仅保留 exclude/sort。
	var wanted []string
	if pin.Mode == ClinePinModeStrict || pin.Mode == ClinePinModePreferred {
		for _, upstream := range pin.Upstreams {
			if _, skip := excluded[upstream]; skip {
				continue
			}
			wanted = append(wanted, upstream)
		}
	}

	var allow []string
	if len(pin.Exclude) > 0 {
		for _, upstream := range meta.Upstreams {
			if _, skip := excluded[upstream]; skip {
				continue
			}
			allow = append(allow, upstream)
		}
	}

	if len(wanted) == 0 && len(allow) == 0 && pin.Sort == "" {
		return body
	}

	out := body
	pipeline := meta.Pipeline
	useGateway := pipeline == "" || pipeline == ClinePipelinePlanner
	useOpenRouter := pipeline == "" || pipeline == ClinePipelineDirect

	if useGateway {
		if len(wanted) > 0 {
			if pin.Mode == ClinePinModeStrict {
				out = setClineJSONField(out, "providerOptions.gateway.only", []string{wanted[0]})
			} else {
				out = setClineJSONField(out, "providerOptions.gateway.order", wanted)
				if len(allow) > 0 {
					out = setClineJSONField(out, "providerOptions.gateway.only", allow)
				}
			}
		} else if len(allow) > 0 {
			out = setClineJSONField(out, "providerOptions.gateway.only", allow)
		}
		if sort := clineGatewaySort(pin.Sort); sort != "" {
			out = setClineJSONField(out, "providerOptions.gateway.sort", sort)
		}
	}

	if useOpenRouter {
		if len(wanted) > 0 {
			if pin.Mode == ClinePinModeStrict {
				out = setClineJSONField(out, "provider.only", []string{wanted[0]})
			} else {
				out = setClineJSONField(out, "provider.order", wanted)
				if len(allow) > 0 {
					out = setClineJSONField(out, "provider.only", allow)
				}
			}
		} else if len(allow) > 0 {
			out = setClineJSONField(out, "provider.only", allow)
		}
		if sort := clineOpenRouterSort(pin.Sort); sort != "" {
			out = setClineJSONField(out, "provider.sort", sort)
		}
	}

	return out
}

// setClineJSONField 写一个嵌套字段并保留请求体其余部分；失败时原样返回。
func setClineJSONField(body []byte, path string, value any) []byte {
	next, err := sjson.SetBytes(body, path, value)
	if err != nil {
		return body
	}
	return next
}

// clineGatewaySort 返回 Vercel AI Gateway 的排序取值（原样）。
func clineGatewaySort(sort string) string {
	switch sort {
	case ClineSortCost, ClineSortTTFT, ClineSortTPS:
		return sort
	default:
		return ""
	}
}

// clineOpenRouterSort 返回 OpenRouter 的排序取值（命名不同）。
func clineOpenRouterSort(sort string) string {
	switch sort {
	case ClineSortCost:
		return "price"
	case ClineSortTTFT:
		return "latency"
	case ClineSortTPS:
		return "throughput"
	default:
		return ""
	}
}

// ClineUpstreamCandidate 是按配置展开后的实际候选顺序（供管理端预览与测试使用）。
func ClineUpstreamCandidate(pin ClineUpstreamPin, meta ClineUpstreamMeta) (wanted []string, allow []string) {
	excluded := make(map[string]struct{}, len(pin.Exclude))
	for _, upstream := range pin.Exclude {
		excluded[upstream] = struct{}{}
	}
	if pin.Mode == ClinePinModeStrict || pin.Mode == ClinePinModePreferred {
		for _, upstream := range pin.Upstreams {
			if _, skip := excluded[upstream]; skip {
				continue
			}
			wanted = append(wanted, upstream)
		}
	}
	if len(pin.Exclude) > 0 {
		for _, upstream := range meta.Upstreams {
			if _, skip := excluded[upstream]; skip {
				continue
			}
			allow = append(allow, upstream)
		}
	}
	return wanted, allow
}

func normalizeClinePinMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ClinePinModeStrict:
		return ClinePinModeStrict
	case ClinePinModePreferred:
		return ClinePinModePreferred
	default:
		return ClinePinModeOff
	}
}

func normalizeClinePipeline(pipeline string) string {
	switch strings.ToLower(strings.TrimSpace(pipeline)) {
	case ClinePipelineDirect:
		return ClinePipelineDirect
	case ClinePipelinePlanner:
		return ClinePipelinePlanner
	default:
		return ""
	}
}

func normalizeClineSort(sort string) string {
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case ClineSortCost:
		return ClineSortCost
	case ClineSortTTFT:
		return ClineSortTTFT
	case ClineSortTPS:
		return ClineSortTPS
	default:
		return ""
	}
}

func normalizeClineUpstreamStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case ClineUpstreamStatusOK, ClineUpstreamStatusLimited, ClineUpstreamStatusBad,
		ClineUpstreamStatusAuth, ClineUpstreamStatusUnknown:
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return ClineUpstreamStatusUnknown
	}
}

// clineAnyString / clineAnyStringSlice / clineAnyBool / clineAnyInt64 读取 JSONB 反序列化后的
// map[string]any（Extra 的存储形态），缺失或类型不符时返回零值。
func clineAnyString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case nil:
		return ""
	default:
		return ""
	}
}

func clineAnyStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if _, dup := seen[text]; dup {
			continue
		}
		seen[text] = struct{}{}
		out = append(out, text)
		if len(out) >= clineUpstreamMaxListLen {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func clineAnyBool(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func clineAnyInt64(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		return 0
	}
}
