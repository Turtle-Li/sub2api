package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	DesktopToolSchemaVersion = 1
	maxDesktopTools          = 64
	maxDesktopToolSettings   = 32

	defaultDesktopToolCatalogJSON = `{
		"schema_version":1,
		"version":7,
		"tools":[
			{
				"id":"developer_mode",
				"name":"Codex 开发者增强",
				"description":"仅作用于 Codex。分别配置搜索探索与异步开发的模型和思考等级，并写入全局 AGENTS.md 调度规则。",
				"icon":"agent",
				"sort_order":10,
				"enabled":true,
				"ui":{"type":"switch"},
				"action":{"type":"config_update","target":"codex","profile":"agents"},
				"defaults":{"enabled":false,"research_model":"gpt-5.6-terra","research_reasoning_effort":"low","sub_agent_model":"gpt-5.6-sol","reasoning_effort":"high","sub_agent_count":3},
				"settings":[
					{"id":"research_model","label":"搜索 / 探索模型","description":"用于代码检索、资料查找、日志分析和其他只读探索","type":"select","default":"gpt-5.6-terra","options_source":"codex_models","options":[{"label":"GPT-5.6 Luna","value":"gpt-5.6-luna","description":"速度快，适合目标明确、结果容易核对的探索任务。"},{"label":"GPT-5.6 Terra","value":"gpt-5.6-terra","description":"能力与速度更均衡，适合复杂分析。"},{"label":"GPT-5.6 Sol","value":"gpt-5.6-sol","description":"适合需要更深分析与判断的复杂探索。"}]},
					{"id":"research_reasoning_effort","label":"搜索 / 探索思考等级","description":"只控制搜索、查文件和只读分析子任务","type":"select","default":"low","options":[{"label":"低","value":"low","description":"低延迟，适合常规检索与查文件。"},{"label":"中","value":"medium","description":"需要整理上下文或交叉核对时使用。"},{"label":"高","value":"high","description":"适合多来源、较复杂的只读分析。"},{"label":"极高","value":"xhigh","description":"增加分析与检查深度。"},{"label":"最高","value":"max","description":"深度优先，耗时与消耗最高。"}]},
					{"id":"sub_agent_model","label":"异步开发模型","description":"用于边界清晰的异步实现、修改和验证任务","type":"select","default":"gpt-5.6-sol","options_source":"codex_models","options":[{"label":"GPT-5.6 Sol","value":"gpt-5.6-sol","description":"最高能力，适合架构调整与异步开发。"},{"label":"GPT-5.6 Terra","value":"gpt-5.6-terra","description":"能力、速度和资源占用更均衡。"},{"label":"GPT-5.6 Luna","value":"gpt-5.6-luna","description":"适合目标明确、验收标准清晰的快速实现。"}]},
					{"id":"reasoning_effort","label":"异步开发思考等级","description":"只控制异步实现与验证子任务","type":"select","default":"high","options":[{"label":"低","value":"low","description":"速度优先，适合明确的小改动。"},{"label":"中","value":"medium","description":"速度和分析深度较均衡。"},{"label":"高","value":"high","description":"适合复杂实现与严格复核。"},{"label":"极高","value":"xhigh","description":"增加推理与检查深度。"},{"label":"最高","value":"max","description":"深度优先，耗时与消耗最高。"}]},
					{"id":"sub_agent_count","label":"最大并发 Agent","description":"并发总数包含当前主任务；推荐 3，复杂并行任务可设为 5，通常无需超过 5","type":"number","default":3,"min":1,"max":8}
				]
			},
			{
				"id":"codex_websocket",
				"name":"Codex 输出传输",
				"description":"仅作用于 Codex。WebSocket 延迟更低、连续会话更顺滑；HTTP 流式兼容性更好，也更容易排查代理或网络问题。",
				"icon":"network",
				"sort_order":20,
				"enabled":true,
				"ui":{"type":"switch"},
				"action":{"type":"config_update","target":"codex","profile":"transport"},
				"defaults":{"enabled":true},
				"settings":[]
			}
		]
	}`
)

var developerModeSettingIDs = [...]string{
	"research_model",
	"research_reasoning_effort",
	"sub_agent_model",
	"reasoning_effort",
	"sub_agent_count",
}

var preservedDeveloperModeDefaults = [...]string{
	"enabled",
	"research_model",
	"research_reasoning_effort",
	"sub_agent_model",
	"reasoning_effort",
	"sub_agent_count",
}

var (
	desktopToolIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	desktopModelIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,128}$`)
)

// DesktopToolCatalog is a data-only catalog. It cannot contain executable
// code, file paths, shell commands, or arbitrary configuration targets.
type DesktopToolCatalog struct {
	SchemaVersion int                     `json:"schema_version"`
	Version       int64                   `json:"version"`
	Tools         []DesktopToolDefinition `json:"tools"`
}

type DesktopToolCatalogVersion struct {
	SchemaVersion int   `json:"schema_version"`
	Version       int64 `json:"version"`
}

type DesktopToolDefinition struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Icon        string                 `json:"icon"`
	SortOrder   int                    `json:"sort_order"`
	Enabled     bool                   `json:"enabled"`
	UI          DesktopToolUI          `json:"ui"`
	Action      DesktopToolAction      `json:"action"`
	Defaults    map[string]interface{} `json:"defaults"`
	Settings    []DesktopToolSetting   `json:"settings"`
}

type DesktopToolUI struct {
	Type string `json:"type"`
}

type DesktopToolAction struct {
	Type    string `json:"type"`
	Target  string `json:"target"`
	Profile string `json:"profile,omitempty"`
}

type DesktopToolSetting struct {
	ID            string              `json:"id"`
	Label         string              `json:"label"`
	Description   string              `json:"description,omitempty"`
	Type          string              `json:"type"`
	Default       interface{}         `json:"default,omitempty"`
	Options       []DesktopToolOption `json:"options,omitempty"`
	OptionsSource string              `json:"options_source,omitempty"`
	Min           *int                `json:"min,omitempty"`
	Max           *int                `json:"max,omitempty"`
}

// DesktopToolOption accepts both the compact string form and the labelled
// object form from the protocol, then always returns a canonical object.
type DesktopToolOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

// desktopToolCatalogCompareAndSetRepository is intentionally narrower than
// SettingRepository. Updating the desktop tool catalog is the only setting
// mutation that needs to compare the exact persisted value, so adding this to
// the global settings contract would force unrelated consumers to implement a
// concurrency primitive they do not use.
type desktopToolCatalogCompareAndSetRepository interface {
	CompareAndSet(ctx context.Context, key, expectedValue, value string) (bool, error)
}

func (o *DesktopToolOption) UnmarshalJSON(data []byte) error {
	var compact string
	if err := json.Unmarshal(data, &compact); err == nil {
		o.Label = compact
		o.Value = compact
		return nil
	}
	type optionAlias DesktopToolOption
	var object optionAlias
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("option must be a string or label/value object")
	}
	*o = DesktopToolOption(object)
	return nil
}

func DefaultDesktopToolCatalog() DesktopToolCatalog {
	var catalog DesktopToolCatalog
	if err := json.Unmarshal([]byte(defaultDesktopToolCatalogJSON), &catalog); err != nil {
		panic(fmt.Sprintf("invalid built-in desktop tool catalog: %v", err))
	}
	normalized, err := NormalizeDesktopToolCatalog(catalog)
	if err != nil {
		panic(fmt.Sprintf("invalid built-in desktop tool catalog: %v", err))
	}
	return normalized
}

func NormalizeDesktopToolCatalog(catalog DesktopToolCatalog) (DesktopToolCatalog, error) {
	if catalog.SchemaVersion == 0 {
		catalog.SchemaVersion = DesktopToolSchemaVersion
	}
	if catalog.SchemaVersion != DesktopToolSchemaVersion {
		return DesktopToolCatalog{}, fmt.Errorf("unsupported schema_version %d", catalog.SchemaVersion)
	}
	if catalog.Version < 1 {
		return DesktopToolCatalog{}, fmt.Errorf("version must be at least 1")
	}
	if catalog.Tools == nil {
		catalog.Tools = []DesktopToolDefinition{}
	}
	if len(catalog.Tools) > maxDesktopTools {
		return DesktopToolCatalog{}, fmt.Errorf("too many tools (maximum %d)", maxDesktopTools)
	}

	seenTools := make(map[string]struct{}, len(catalog.Tools))
	for index := range catalog.Tools {
		tool, err := normalizeDesktopTool(catalog.Tools[index])
		if err != nil {
			return DesktopToolCatalog{}, fmt.Errorf("tool %d: %w", index+1, err)
		}
		if _, exists := seenTools[tool.ID]; exists {
			return DesktopToolCatalog{}, fmt.Errorf("duplicate tool id %q", tool.ID)
		}
		seenTools[tool.ID] = struct{}{}
		catalog.Tools[index] = tool
	}
	sort.SliceStable(catalog.Tools, func(i, j int) bool {
		if catalog.Tools[i].SortOrder == catalog.Tools[j].SortOrder {
			return catalog.Tools[i].ID < catalog.Tools[j].ID
		}
		return catalog.Tools[i].SortOrder < catalog.Tools[j].SortOrder
	})
	return catalog, nil
}

func normalizeDesktopTool(tool DesktopToolDefinition) (DesktopToolDefinition, error) {
	tool.ID = strings.ToLower(strings.TrimSpace(tool.ID))
	tool.Name = strings.TrimSpace(tool.Name)
	tool.Description = strings.TrimSpace(tool.Description)
	tool.Icon = strings.ToLower(strings.TrimSpace(tool.Icon))
	tool.UI.Type = strings.ToLower(strings.TrimSpace(tool.UI.Type))
	tool.Action.Type = strings.ToLower(strings.TrimSpace(tool.Action.Type))
	tool.Action.Target = strings.ToLower(strings.TrimSpace(tool.Action.Target))
	tool.Action.Profile = strings.ToLower(strings.TrimSpace(tool.Action.Profile))
	if tool.Action.Profile == "" {
		tool.Action.Profile = "agents"
	}

	if !desktopToolIDPattern.MatchString(tool.ID) {
		return DesktopToolDefinition{}, fmt.Errorf("invalid id")
	}
	if err := validateDesktopToolText(tool.Name, "name", 80, true); err != nil {
		return DesktopToolDefinition{}, err
	}
	if err := validateDesktopToolText(tool.Description, "description", 240, false); err != nil {
		return DesktopToolDefinition{}, err
	}
	if tool.Icon == "" {
		tool.Icon = "tool"
	}
	if !desktopToolIDPattern.MatchString(tool.Icon) {
		return DesktopToolDefinition{}, fmt.Errorf("icon must be a built-in icon identifier")
	}
	if tool.SortOrder < -10000 || tool.SortOrder > 10000 {
		return DesktopToolDefinition{}, fmt.Errorf("sort_order must be between -10000 and 10000")
	}
	if tool.UI.Type != "switch" && tool.UI.Type != "button" {
		return DesktopToolDefinition{}, fmt.Errorf("unsupported ui type %q", tool.UI.Type)
	}
	if tool.Action.Type != "config_update" || tool.Action.Target != "codex" ||
		(tool.Action.Profile != "agents" && tool.Action.Profile != "transport") {
		return DesktopToolDefinition{}, fmt.Errorf("only allowlisted codex config_update profiles are supported")
	}
	if len(tool.Settings) > maxDesktopToolSettings {
		return DesktopToolDefinition{}, fmt.Errorf("too many settings (maximum %d)", maxDesktopToolSettings)
	}
	if tool.Defaults == nil {
		tool.Defaults = map[string]interface{}{}
	}
	if rawEnabled, exists := tool.Defaults["enabled"]; exists {
		if _, ok := rawEnabled.(bool); !ok {
			return DesktopToolDefinition{}, fmt.Errorf("defaults.enabled must be a boolean")
		}
	} else {
		tool.Defaults["enabled"] = false
	}

	seenSettings := make(map[string]struct{}, len(tool.Settings))
	allowedDefaults := map[string]struct{}{"enabled": {}}
	for index := range tool.Settings {
		setting, normalizedDefault, err := normalizeDesktopToolSetting(tool.Settings[index])
		if err != nil {
			return DesktopToolDefinition{}, fmt.Errorf("setting %d: %w", index+1, err)
		}
		if setting.ID == "enabled" {
			return DesktopToolDefinition{}, fmt.Errorf("setting id %q is reserved", setting.ID)
		}
		if _, exists := seenSettings[setting.ID]; exists {
			return DesktopToolDefinition{}, fmt.Errorf("duplicate setting id %q", setting.ID)
		}
		seenSettings[setting.ID] = struct{}{}
		allowedDefaults[setting.ID] = struct{}{}

		if existing, exists := tool.Defaults[setting.ID]; exists {
			existing, err = normalizeDesktopToolValue(existing, setting)
			if err != nil {
				return DesktopToolDefinition{}, fmt.Errorf("defaults.%s: %w", setting.ID, err)
			}
			if !reflect.DeepEqual(existing, normalizedDefault) {
				return DesktopToolDefinition{}, fmt.Errorf("defaults.%s conflicts with the setting default", setting.ID)
			}
		}
		if setting.Type != "button" {
			tool.Defaults[setting.ID] = normalizedDefault
		}
		setting.Default = normalizedDefault
		tool.Settings[index] = setting
	}
	for key := range tool.Defaults {
		if _, ok := allowedDefaults[key]; !ok {
			return DesktopToolDefinition{}, fmt.Errorf("defaults contains undeclared setting %q", key)
		}
	}
	return tool, nil
}

func normalizeDesktopToolSetting(setting DesktopToolSetting) (DesktopToolSetting, interface{}, error) {
	setting.ID = strings.ToLower(strings.TrimSpace(setting.ID))
	setting.Label = strings.TrimSpace(setting.Label)
	setting.Description = strings.TrimSpace(setting.Description)
	setting.Type = strings.ToLower(strings.TrimSpace(setting.Type))
	setting.OptionsSource = strings.ToLower(strings.TrimSpace(setting.OptionsSource))
	if !desktopToolIDPattern.MatchString(setting.ID) {
		return DesktopToolSetting{}, nil, fmt.Errorf("invalid id")
	}
	if err := validateDesktopToolText(setting.Label, "label", 80, true); err != nil {
		return DesktopToolSetting{}, nil, err
	}
	if err := validateDesktopToolText(setting.Description, "description", 240, false); err != nil {
		return DesktopToolSetting{}, nil, err
	}

	switch setting.Type {
	case "switch":
		setting.Options = nil
		setting.OptionsSource = ""
		setting.Min = nil
		setting.Max = nil
	case "select":
		setting.Min = nil
		setting.Max = nil
		if len(setting.Options) == 0 || len(setting.Options) > 64 {
			return DesktopToolSetting{}, nil, fmt.Errorf("select must contain 1 to 64 options")
		}
		seen := make(map[string]struct{}, len(setting.Options))
		for index := range setting.Options {
			option := setting.Options[index]
			option.Label = strings.TrimSpace(option.Label)
			option.Value = strings.TrimSpace(option.Value)
			option.Description = strings.TrimSpace(option.Description)
			if option.Label == "" {
				option.Label = option.Value
			}
			if err := validateDesktopToolText(option.Label, "option label", 80, true); err != nil {
				return DesktopToolSetting{}, nil, err
			}
			if option.Value == "" || len(option.Value) > 128 {
				return DesktopToolSetting{}, nil, fmt.Errorf("option value must contain 1 to 128 bytes")
			}
			if err := validateDesktopToolText(option.Description, "option description", 240, false); err != nil {
				return DesktopToolSetting{}, nil, err
			}
			if _, exists := seen[option.Value]; exists {
				return DesktopToolSetting{}, nil, fmt.Errorf("duplicate option value %q", option.Value)
			}
			seen[option.Value] = struct{}{}
			setting.Options[index] = option
		}
		if setting.OptionsSource != "" && setting.OptionsSource != "codex_models" {
			return DesktopToolSetting{}, nil, fmt.Errorf("unsupported options_source %q", setting.OptionsSource)
		}
	case "number":
		setting.Options = nil
		setting.OptionsSource = ""
		if setting.Min == nil || setting.Max == nil {
			return DesktopToolSetting{}, nil, fmt.Errorf("number requires min and max")
		}
		if *setting.Min < -100000 || *setting.Max > 100000 || *setting.Min > *setting.Max {
			return DesktopToolSetting{}, nil, fmt.Errorf("number range is invalid")
		}
	case "button":
		setting.Options = nil
		setting.OptionsSource = ""
		setting.Min = nil
		setting.Max = nil
		setting.Default = nil
		return setting, nil, nil
	default:
		return DesktopToolSetting{}, nil, fmt.Errorf("unsupported type %q", setting.Type)
	}

	value, err := normalizeDesktopToolValue(setting.Default, setting)
	if err != nil {
		return DesktopToolSetting{}, nil, fmt.Errorf("default: %w", err)
	}
	return setting, value, nil
}

func normalizeDesktopToolValue(value interface{}, setting DesktopToolSetting) (interface{}, error) {
	switch setting.Type {
	case "switch":
		if typed, ok := value.(bool); ok {
			return typed, nil
		}
		return nil, fmt.Errorf("must be a boolean")
	case "select":
		typed, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("must be a string")
		}
		for _, option := range setting.Options {
			if option.Value == typed {
				return typed, nil
			}
		}
		if setting.OptionsSource == "codex_models" && desktopModelIDPattern.MatchString(typed) {
			return typed, nil
		}
		return nil, fmt.Errorf("must match one of the declared options")
	case "number":
		number, ok := desktopToolInteger(value)
		if !ok {
			return nil, fmt.Errorf("must be an integer")
		}
		if setting.Min == nil || setting.Max == nil || number < *setting.Min || number > *setting.Max {
			return nil, fmt.Errorf("must be between %d and %d", *setting.Min, *setting.Max)
		}
		return number, nil
	case "button":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", setting.Type)
	}
}

func desktopToolInteger(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		if int64(int(typed)) != typed {
			return 0, false
		}
		return int(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < math.MinInt || typed > math.MaxInt {
			return 0, false
		}
		return int(typed), true
	case json.Number:
		number, err := typed.Int64()
		if err != nil || int64(int(number)) != number {
			return 0, false
		}
		return int(number), true
	default:
		return 0, false
	}
}

func validateDesktopToolText(value, field string, maxRunes int, required bool) error {
	if required && value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s is too long (maximum %d characters)", field, maxRunes)
	}
	return nil
}

func ParseDesktopToolCatalog(raw string) (DesktopToolCatalog, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultDesktopToolCatalog(), nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var catalog DesktopToolCatalog
	if err := decoder.Decode(&catalog); err != nil {
		return DesktopToolCatalog{}, fmt.Errorf("decode desktop tool catalog: %w", err)
	}
	normalized, err := NormalizeDesktopToolCatalog(catalog)
	if err != nil {
		return DesktopToolCatalog{}, err
	}
	return upgradeDesktopDeveloperModeContract(normalized)
}

func upgradeDesktopDeveloperModeContract(catalog DesktopToolCatalog) (DesktopToolCatalog, error) {
	toolIndex := -1
	for index := range catalog.Tools {
		if catalog.Tools[index].ID == "developer_mode" {
			toolIndex = index
			break
		}
	}
	if toolIndex < 0 || desktopDeveloperModeContractIsCurrent(catalog.Tools[toolIndex]) {
		return catalog, nil
	}

	previous := catalog.Tools[toolIndex]
	bundled := DefaultDesktopToolCatalog()
	var replacement *DesktopToolDefinition
	for index := range bundled.Tools {
		if bundled.Tools[index].ID == "developer_mode" {
			replacement = &bundled.Tools[index]
			break
		}
	}
	if replacement == nil {
		return DesktopToolCatalog{}, fmt.Errorf("built-in developer_mode tool is missing")
	}
	replacement.Enabled = previous.Enabled
	replacement.SortOrder = previous.SortOrder

	for _, key := range preservedDeveloperModeDefaults {
		previousValue, exists := previous.Defaults[key]
		if !exists {
			continue
		}
		if key == "enabled" {
			if enabled, ok := previousValue.(bool); ok {
				replacement.Defaults[key] = enabled
			}
			continue
		}
		for settingIndex := range replacement.Settings {
			setting := replacement.Settings[settingIndex]
			if setting.ID != key {
				continue
			}
			normalizedValue, valueErr := normalizeDesktopToolValue(previousValue, setting)
			if valueErr == nil {
				replacement.Settings[settingIndex].Default = normalizedValue
				replacement.Defaults[key] = normalizedValue
			}
			break
		}
	}

	catalog.Tools[toolIndex] = *replacement
	if catalog.Version == math.MaxInt64 {
		return DesktopToolCatalog{}, fmt.Errorf("desktop tool catalog version cannot be incremented")
	}
	catalog.Version++
	if catalog.Version < bundled.Version {
		catalog.Version = bundled.Version
	}
	return NormalizeDesktopToolCatalog(catalog)
}

func desktopDeveloperModeContractIsCurrent(tool DesktopToolDefinition) bool {
	if tool.Action.Type != "config_update" || tool.Action.Target != "codex" || tool.Action.Profile != "agents" {
		return false
	}
	if _, exists := tool.Defaults["agent_strategy"]; exists {
		return false
	}
	settings := make(map[string]struct{}, len(tool.Settings))
	for _, setting := range tool.Settings {
		if setting.ID == "agent_strategy" {
			return false
		}
		settings[setting.ID] = struct{}{}
	}
	for _, required := range developerModeSettingIDs {
		if _, exists := tool.Defaults[required]; !exists {
			return false
		}
		if _, exists := settings[required]; !exists {
			return false
		}
	}
	return true
}

func ActiveDesktopToolCatalog(catalog DesktopToolCatalog) DesktopToolCatalog {
	active := make([]DesktopToolDefinition, 0, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		if tool.Enabled {
			active = append(active, tool)
		}
	}
	catalog.Tools = active
	return catalog
}

func (s *SettingService) GetDesktopToolCatalog(ctx context.Context, includeDisabled bool) (DesktopToolCatalog, error) {
	catalog, _, err := s.loadDesktopToolCatalog(ctx)
	if err != nil {
		return DesktopToolCatalog{}, err
	}
	if !includeDisabled {
		catalog = ActiveDesktopToolCatalog(catalog)
	}
	return catalog, nil
}

// loadDesktopToolCatalog returns both the normalized catalog and the exact raw
// database value used to derive it. The latter is the optimistic-concurrency
// token for UpdateDesktopToolCatalog: comparing a normalized catalog alone
// would allow a stale writer to overwrite another admin's change.
//
// A missing setting deliberately has an empty raw token. The repository treats
// that token as an insert-only CAS, so two first writers of the built-in v7
// default cannot both create a competing v8 catalog.
func (s *SettingService) loadDesktopToolCatalog(ctx context.Context) (DesktopToolCatalog, string, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDesktopTools)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultDesktopToolCatalog(), "", nil
		}
		return DesktopToolCatalog{}, "", fmt.Errorf("get desktop tool catalog: %w", err)
	}
	catalog, err := ParseDesktopToolCatalog(raw)
	if err != nil {
		return DesktopToolCatalog{}, "", fmt.Errorf("invalid stored desktop tool catalog: %w", err)
	}
	return catalog, raw, nil
}

func (s *SettingService) UpdateDesktopToolCatalog(
	ctx context.Context,
	expectedVersion int64,
	tools []DesktopToolDefinition,
) (DesktopToolCatalog, error) {
	if expectedVersion <= 0 {
		return DesktopToolCatalog{}, infraerrors.BadRequest(
			"INVALID_DESKTOP_TOOLS_VERSION",
			"expected_version must be a positive catalog version",
		)
	}
	current, expectedRaw, err := s.loadDesktopToolCatalog(ctx)
	if err != nil {
		return DesktopToolCatalog{}, err
	}
	if expectedVersion != current.Version {
		return DesktopToolCatalog{}, infraerrors.Conflict(
			"DESKTOP_TOOLS_VERSION_CONFLICT",
			"工具目录已被其他管理员更新，请刷新后重试",
		)
	}
	if current.Version == math.MaxInt64 {
		return DesktopToolCatalog{}, infraerrors.BadRequest(
			"DESKTOP_TOOLS_VERSION_EXHAUSTED",
			"desktop tool catalog version cannot be incremented",
		)
	}
	next, err := NormalizeDesktopToolCatalog(DesktopToolCatalog{
		SchemaVersion: DesktopToolSchemaVersion,
		Version:       current.Version + 1,
		Tools:         tools,
	})
	if err != nil {
		return DesktopToolCatalog{}, infraerrors.BadRequest("INVALID_DESKTOP_TOOLS", err.Error())
	}
	if reflect.DeepEqual(current.Tools, next.Tools) {
		return current, nil
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return DesktopToolCatalog{}, fmt.Errorf("encode desktop tool catalog: %w", err)
	}
	casRepo, ok := s.settingRepo.(desktopToolCatalogCompareAndSetRepository)
	if !ok {
		return DesktopToolCatalog{}, infraerrors.ServiceUnavailable(
			"DESKTOP_TOOLS_CAS_UNAVAILABLE",
			"工具目录的原子更新暂不可用，请稍后重试",
		)
	}
	swapped, err := casRepo.CompareAndSet(ctx, SettingKeyDesktopTools, expectedRaw, string(encoded))
	if err != nil {
		return DesktopToolCatalog{}, fmt.Errorf("update desktop tool catalog: %w", err)
	}
	if !swapped {
		return DesktopToolCatalog{}, infraerrors.Conflict(
			"DESKTOP_TOOLS_VERSION_CONFLICT",
			"工具目录已被其他管理员更新，请刷新后重试",
		)
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return next, nil
}
