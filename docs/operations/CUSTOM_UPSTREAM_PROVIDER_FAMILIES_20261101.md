# 自定义上游供应商与服务模型族（设计评估）

2026-11-01 设计评估。**实施状态：方案 A 已落地，见
[WORKBUDDY_PLATFORM_20261101.md](WORKBUDDY_PLATFORM_20261101.md)（登记 `workbuddy`
为第 4 个多模型聚合平台）。本文保留方案对比与 L1/L2 后续路径。**

结论：**设计成立，但不应该做成「自定义渠道的账号可加入所有分组」，而应该做成「供应商声明可服务的模型族」。** 本文给出现状证据、根因、三档方案、推荐方案的 schema 与逐文件接缝、必须守住的边界、验收与回滚。

## 1. 结论摘要

| 问题 | 结论 |
| --- | --- |
| 现在的痛点是否真实 | 是。cline / opencode_go / command_code 三个平台被硬编码为「多模型聚合」，所以能进任意模型厂商分组；其他走 OpenAI 兼容协议的上游（WorkBuddy）不在这个白名单里，只能进同平台分组或在 composite 里绕 |
| 「自定义渠道」这个设计能不能做 | 能做。缺的维度只有一个：供应商能服务哪些模型族。现在的 `IsMultiModelPlatform` 就是这个维度的硬编码版本，把它换成声明即可 |
| 「所有分组都能添加」是否合适 | 不合适，但用户想要的效果可以达成：把 `serving_families` 填成全部模型族，效果等价于「所有国模分组都能加它」，同时仍受最终闸约束。真正的「所有分组」会破坏分组的定价 / 配额 / 白名单语义，并让调度桶从 1+N 退化成全平台扫描 |
| 推荐落地层级 | L2 供应商注册表（`upstream_providers` + `serving_families`）。L1（账号 extra 内联声明）可作为 1~2 天的过渡 |
| 是否要动数据库 | L1 不动库；L2 加一张注册表，不加平台 CHECK 约束（242 已移除平台白名单约束） |

## 2. 现状与证据

### 2.1 为什么 Cline 账号能同时挂四个国模分组

绑定兼容性只有一处判定：

- `backend/internal/domain/platforms.go:100-105` `IsGroupAccountPlatformCompatible(groupPlatform, accountPlatform)`
  - `group == account` → 允许
  - `group == composite` → 允许
  - `IsModelProviderPlatform(group) && IsMultiModelPlatform(account)` → 允许

而：

- `backend/internal/domain/platforms.go:88-92` `IsMultiModelPlatform` 硬编码 = `opencode_go | command_code | cline`
- `backend/internal/domain/platforms.go:96-98` `IsModelProviderPlatform` = 具体平台且非聚合平台且非 antigravity

于是 cline 账号天然落在第三个分支，可被 kimi / deepseek / zhipu / minimax 等任意分组添加。

### 2.2 请求期还有第二道闸，所以跨族不会乱转发

- `backend/internal/service/account.go:1009-1023` `SupportsGroupPlatformUpstreamModel`：跨族时要求 `Type == apikey` 且 `DetectModelPlatform(映射后的上游模型名) == groupPlatform`
- `backend/internal/service/multi_model_group.go:66-80` 在选择期复核
- `backend/internal/service/gateway_scheduling.go:2604` 在 hydration 后复核
- `backend/internal/service/multi_model_group.go:112-140` 在序列化发送边界上复核（`validateMultiModelGroupUpstream`）

`DetectModelPlatform`（`backend/internal/service/composite_platform.go:91`）按模型名前缀识别模型族，未知一律 fail closed。

**这就是关键**：跨族安全性从来不是靠「平台白名单」，而是靠「模型族判定」。平台白名单只决定候选池，不决定能否转发。

### 2.3 为什么 WorkBuddy 卡住

WorkBuddy 账号（生产历史账号 232，见 `deploy/CLINE_UNIFIED_GROUPS_20261023.md`）走 OpenAI 兼容协议，但语义上不是 openai、也不是 deepseek。它要么被建成 `platform=openai`（只能进 openai / composite 分组），要么被建成 `platform=deepseek`（只能进 deepseek 分组）。两种建法都拿不到「一个账号、多个国模分组各自添加」的能力，因为：

- 它不在 `IsMultiModelPlatform` 的三值集合里
- 它的 platform 必然等于某一个模型族，于是 `group == account` 只能命中一个分组

### 2.4 调度侧的成本约束已经写进过设计

`docs/operations/MULTI_MODEL_ACCOUNT_GROUPS_20261009.md`：

> There are only three supported multi-model providers; investigate batching if measured scheduler p95 materially rises or this set expands. … Up to three additional cached bucket reads are made per provider-group selection.

对应实现：

- `backend/internal/service/gateway_scheduling.go:1020-1021` `platforms := domain.GroupAccountPlatforms(platform)`
- `backend/internal/service/openai_gateway_scheduling.go:1520-1536` 逐平台读桶并去重
- `backend/internal/domain/platforms.go:107-113` `GroupAccountPlatforms` 追加三个聚合平台

任何「让 workbuddy 也进候选池」的方案都会让这里多一次缓存桶读。设计必须显式接受这个成本，并给它一个上界。

## 3. 根因：一个 `platform` 字段承担了两个语义

| 语义 | 消费方 | 值域 |
| --- | --- | --- |
| **模型族 / family** | 分组的定价（`channel_model_pricing.platform`）、用户平台配额（`user_platform_quotas.platform`）、模型广场、分组模型白名单、用量归属 | 封闭集合 |
| **上游供应商 / provider** | 账号的 base_url、协议、额度探测、冷却策略、usage 解析、官方 host 判定 | 开放集合 |

`IsMultiModelPlatform` 就是这两个语义之间的硬编码桥梁：它断言「这个平台的账号可以服务任意模型族」。所以「新增一个聚合上游」目前必须改代码，改的正是这根桥。

**设计目标**：把桥从布尔断言换成服务族声明，且不改变「族判定是最终安全闸」这一既有契约。

## 4. 方案对比

### 方案 A：把 workbuddy 登记为第 4 个聚合平台（1~2 天）

改动：

1. `backend/internal/domain/constants.go` 加 `PlatformWorkbuddy`
2. `backend/internal/domain/platforms.go` `platformList` 加一行（`Gateway: PlatformGatewayOpenAI`），`IsMultiModelPlatform` 加 `PlatformWorkbuddy`
3. 若需要默认 base_url：`backend/internal/service/provider_profile.go:96` `providerProfiles` 加一条
4. `UPDATE_PLATFORM_CATALOG=1 go test -tags unit ./internal/service/ -run TestFrontendBuiltinPlatformCatalogInSync` 重生成 `frontend/src/constants/platformCatalog.builtin.json`
5. `frontend/src/constants/platforms.ts:22` `MULTI_MODEL_PLATFORMS` 加 `'workbuddy'`

优点：改动小、语义正确、与 cline 完全同构，前端选项自动出现（`platformCatalog.ts` 从生成的 json 派生）。

缺点：每来一个新聚合上游都要发版。这正是用户想避免的。

### 方案 B（推荐）：供应商注册表 + 服务族声明（3~5 天）

核心：

- 平台清单从「代码里的 13 条」变成「注册表里的 N 条」，每条多一个 `serving_families` 与一个 `is_model_family` 标志
- `IsMultiModelPlatform(id)` 退化为 `len(serving_families(id)) > 1`
- `GroupAccountPlatforms(family)` = `{family} ∪ {code | family ∈ serving_families(code)}`，由注册表派生，带缓存
- `IsGroupAccountPlatformCompatible` 去掉硬编码三值，改为查注册表
- 最终闸（`SupportsGroupPlatformUpstreamModel` + `DetectModelPlatform`）**完全不动**

效果：新增一个 WorkBuddy 类型的上游时，只在注册表加一行 `serving_families=['openai','deepseek','kimi','zhipu','minimax']`，无需发版，前端选项、候选池、绑定校验全部自动生效。

### 方案 C（不建议）：完全自定义平台 CRUD

把 `gateway`、`api_protocol`、`competence probe`、`channel monitor`、`plugin binding scope`、`composite_precedence` 全部开放给运营配置。

不建议的原因：`platform` 是横切标识，至少 13 处派生逻辑消费它（`platformList`、`IsModelProviderPlatform`、`IsMultiModelPlatform`、`CompositePrecedencePlatformIDs`、`GroupAccountPlatforms`、`GROUP_PLATFORM_OPTIONS`、渠道定价 `platform`、`user_platform_quotas.platform`、`channel_monitors.provider`、`sub2api_plugin_bindings.platform`、`usage_log` 归属、模型广场、`cn_provider` 人民币展示）。把它变成用户数据后，任何一处漏改都会产生静默错误。而它带来的唯一增量能力（自定义 gateway）并没有业务需求——WorkBuddy 走的就是 openai 网关。

## 5. 推荐方案 B 的详细设计

### 5.1 数据模型

```sql
CREATE TABLE upstream_providers (
    id                BIGSERIAL PRIMARY KEY,
    code              TEXT NOT NULL UNIQUE,          -- workbuddy / cline / opencode_go / kimi ...
    display_name      TEXT NOT NULL,
    gateway           TEXT NOT NULL,                 -- openai | anthropic | gemini | antigravity
    is_model_family   BOOLEAN NOT NULL DEFAULT TRUE, -- 能否作为分组平台 / 配额 / 定价的取值
    serving_families  TEXT[] NOT NULL,               -- 该供应商可服务的模型族
    default_base_url  TEXT,                          -- 空则必须由账号 credentials.base_url 提供
    api_protocol      TEXT,                          -- chat_completions | responses | anthropic | adaptive
    cn_provider       BOOLEAN NOT NULL DEFAULT FALSE,
    litellm_provider  TEXT,
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order        INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

预置迁移（等价于当前行为，零行为变化）：

```sql
INSERT INTO upstream_providers (code, display_name, gateway, is_model_family, serving_families) VALUES
  ('anthropic','Anthropic','anthropic',TRUE,'{anthropic}'),
  ('openai','OpenAI','openai',TRUE,'{openai}'),
  ('gemini','Gemini','gemini',TRUE,'{gemini}'),
  ('antigravity','Antigravity','antigravity',TRUE,'{antigravity}'),
  ('grok','Grok','openai',TRUE,'{grok}'),
  ('kimi','Kimi','openai',TRUE,'{kimi}'),
  ('zhipu','Zhipu GLM','openai',TRUE,'{zhipu}'),
  ('deepseek','DeepSeek','openai',TRUE,'{deepseek}'),
  ('minimax','MiniMax','openai',TRUE,'{minimax}'),
  ('opencode_go','OpenCode','openai',TRUE,'{anthropic,openai,gemini,grok,kimi,zhipu,deepseek,minimax}'),
  ('typesafe','TypeSafe / Jev','anthropic',TRUE,'{typesafe}'),
  ('command_code','Command Code','openai',TRUE,'{anthropic,openai,gemini,grok,kimi,zhipu,deepseek,minimax}'),
  ('cline','Cline','openai',TRUE,'{anthropic,openai,gemini,grok,kimi,zhipu,deepseek,minimax}');
```

WorkBuddy 上线只需运营侧一行：

```sql
INSERT INTO upstream_providers (code, display_name, gateway, is_model_family, serving_families, api_protocol)
VALUES ('workbuddy','WorkBuddy','openai',FALSE,'{openai,deepseek,kimi,zhipu,minimax}','chat_completions');
```

`is_model_family = FALSE` 很关键：它让 workbuddy 出现在「账号平台」下拉里，但**不**出现在「分组平台」「配额平台」「定价平台」下拉里。否则运营可以建出一个 `platform=workbuddy` 的分组，而该分组没有定价、没有配额、没有广场卡片——这是当前架构里最容易埋的坑。

### 5.2 判定规则替换

```go
// 之前
func IsGroupAccountPlatformCompatible(groupPlatform, accountPlatform string) bool {
    return groupPlatform == accountPlatform || groupPlatform == PlatformComposite ||
        (IsModelProviderPlatform(groupPlatform) && IsMultiModelPlatform(accountPlatform))
}

// 之后
func IsGroupAccountPlatformCompatible(groupPlatform, accountPlatform string) bool {
    if groupPlatform == accountPlatform || groupPlatform == PlatformComposite {
        return true
    }
    if !IsModelProviderPlatform(groupPlatform) {
        return false
    }
    return registry.ServesFamily(accountPlatform, groupPlatform)
}
```

`GroupAccountPlatforms` 同上改造，返回值保持集合语义，顺序稳定（注册表 `sort_order`），便于调度去重与缓存键稳定。

### 5.3 前端零改动的前提

`backend/internal/service/platform_catalog.go:45-57` `BuildPlatformCatalog()` 已经生成 `frontend/src/constants/platformCatalog.builtin.json`，前端 `platformCatalog.ts` 全部选项从它派生。要把「零改动」保持住，需要：

1. `PlatformCatalogEntry` 增加 `is_model_family` 与 `serving_families`
2. `frontend/src/constants/platforms.ts:22` 删掉硬编码的 `MULTI_MODEL_PLATFORMS`，改为 `listPlatforms().filter(p => p.serving_families.includes(group))`
3. `GROUP_PLATFORM_OPTIONS` 改为 `filter(p => p.is_model_family)`

第 1 步做在 json 生成里，是为了让「前端跟后端清单一致」这条已有测试（`TestFrontendBuiltinPlatformCatalogInSync`）继续兜住漂移。

若注册表要走运行时读取（不发版即生效），则 `platformCatalog.ts` 需要在登录后拉一次 `/api/v1/platform-catalog`；此时该 json 退化为内置回退值，测试改为校验「内置值等于注册表默认值」。

### 5.4 逐文件接缝清单

| 文件 | 位置 | 改动 |
| --- | --- | --- |
| `backend/internal/domain/platforms.go` | `platformList` 44-59 | 数据源改为注册表快照，保留内置默认值作为回退 |
| 同上 | `IsMultiModelPlatform` 88-92 | 改为 `len(serving_families) > 1` |
| 同上 | `IsModelProviderPlatform` 96-98 | 增加 `is_model_family` 判定 |
| 同上 | `IsGroupAccountPlatformCompatible` 100-105 | 去掉三值硬编码，查注册表 |
| 同上 | `GroupAccountPlatforms` 107-113 | 由注册表派生 + 缓存 |
| 同上 | `CompositePrecedencePlatformIDs` 122-140 | 只遍历 `is_model_family = TRUE` 的行 |
| `backend/internal/service/account.go` | `SupportsGroupPlatformUpstreamModel` 1009-1023 | **不改**（最终闸） |
| `backend/internal/service/multi_model_group.go` | 66-140 | **不改** |
| `backend/internal/service/gateway_scheduling.go` | 1020-1021 | 使用新的 `GroupAccountPlatforms`；保留 `hasForcePlatform` 短路 |
| `backend/internal/service/openai_gateway_scheduling.go` | 1520-1536 | 同上；新增桶数量上限断言日志 |
| `backend/internal/service/provider_profile.go` | `providerProfiles` 96 | 合并进注册表（`default_base_url` + `api_protocol`），`LookupProviderProfile` 改为查注册表 |
| `backend/internal/service/platform_catalog.go` | 45-57 | 输出 `is_model_family` / `serving_families` |
| `backend/internal/service/admin_group.go` | 119 | 使用新判定（已有调用点，无需逻辑改动） |
| `backend/internal/service/admin_account.go` | 1207 / 1824-1990 | 混合渠道检查目前只认 antigravity/anthropic，新供应商不触发，保持现状 |
| `frontend/src/constants/platforms.ts` | 22, 36-46 | 删硬编码，改派生；分组选项 filter `is_model_family` |
| `frontend/src/constants/platformCatalog.ts` | 20-30 | `PlatformSpec` 增字段（可选，若走运行时拉取） |
| `frontend/src/components/account/credentialsBuilder.ts` | 615 | `OFFICIAL_ACCOUNT_API_PLATFORMS` 不改；自定义供应商走 `base_url` 手填，已有路径 |

## 6. 必须守住的边界

1. **族判定是最终闸，且必须保持 fail closed。** 任何「账号声明服务 kimi，于是不再检查模型名」的简化都会造成跨族上游 400/404，甚至把 A 厂模型计到 B 厂分组价上。`SupportsGroupPlatformUpstreamModel` 与三处发送边界复核一处都不能少。
2. **分组平台只允许模型族。** `is_model_family = FALSE` 的供应商不得建组、不得写 `user_platform_quotas.platform`、不得写 `channel_model_pricing.platform`、不得作为 composite route 的 `target_platform`。这四处的取值校验要显式加。
3. **调度桶读要有上界。** 每个分组的候选池 = `1 + 该族被多少供应商声明`。方案 B 落地时要记录实际读次数，并在 `serving_families` 过度膨胀（例如某族被 5 个以上供应商声明）时给出告警，而不是静默拖慢 p95。
4. **配额、定价、用量归属一律按 group.platform。** 供应商只影响转发配置与 `upstream_provider` 归因，不影响计费身份。这是 `MULTI_MODEL_ACCOUNT_GROUPS_20261009.md` AC4 的既有契约。
5. **协议转换成本要算清。** WorkBuddy 若只有 Chat Completions 端点，那么 Responses（Codex）与 Messages（Claude Code）入口需要转换链；`api_protocol=chat_completions` + `ProviderRoutingByInbound` 可以覆盖，但流式、count_tokens、cache 字段要保持既有行为，需要按 `multi_model_group.go` 里的既有分支逐条验。
6. **不使用「借用 cline 平台指向 workbuddy host」这种临时代法。** `IsCline()` 有 11 处分支（`cline.go:40`、`cline_upstream_service.go:482/491`、`ratelimit_service.go:424`、`cn_provider_balance_service.go:129/276`、`cn_provider_quota_service.go:309`、`account.go:1748`、`cline_upstream_pin.go:188` 等）。多数被 `isOfficialClineHost` 兜住，但 `cline_upstream_pin.go:188`、wallet 冷却键与 Pass 测试模型选择不受 host 限制，会出现难排查的行为漂移。

## 7. 验收

必须同时满足（沿用 `MULTI_MODEL_ACCOUNT_GROUPS_20261009.md` 的 AC1–AC5 并加上供应商维度）：

- AC1 `workbuddy` 账号可被 deepseek / kimi / zhipu / minimax 分组分别添加，且不需要复制账号、不需要改分组 platform
- AC2 同一账号在四个分组共享并发与冷却；分组倍率、渠道价卡、用户平台配额仍按分组 platform 生效
- AC3 请求 `kimi-k3` 命中 workbuddy 账号并通过；请求 `gpt-5` 在 kimi 分组下 fail closed，不发上游 HTTP
- AC4 账号映射到错误族的别名被拒（例如账号把 `kimi-k3` 映射到 `deepseek-v4`，kimi 分组必须拒绝）
- AC5 Messages / Chat Completions / Responses 三入口走 workbuddy 的既有转发 profile，流式与非流式一致；Messages 入口的协议转换有覆盖
- AC6 `platform=workbuddy` 无法建组、无法写入配额与定价、无法作为 composite target（负向用例）
- AC7 既有三个聚合平台行为零回归（原 37 个源文件的回归集全绿）
- AC8 分组候选池桶读次数符合预期上界，p95 无显著上升
- AC9 `TestFrontendBuiltinPlatformCatalogInSync` 通过；前端账号平台下拉出现 workbuddy，分组平台下拉不出现

测试落点建议：

- `backend/internal/domain/platforms_test.go`（族展开、未知族、is_model_family）
- `backend/internal/service/multi_model_group_test.go`（跨族正向 / 错误映射 / 发送边界）
- `backend/internal/service/openai_gateway_scheduling_test.go`（桶展开与去重、force platform 短路）
- `backend/internal/handler/gateway_handler_*_test.go`（三入口转发与原生 usage 记录）
- `frontend/src/constants/__tests__/platforms.spec.ts`（已有 `acme_router` 用例可直接扩展成 `workbuddy`）

## 8. 分阶段落地

| 阶段 | 工作量 | 产出 | 风险 |
| --- | --- | --- | --- |
| L0 运营过渡 | 0 | 一个 WorkBuddy 账号按所需族各建一份副本（共用 base_url + key），分别绑到对应分组 | 并发与冷却不共享、统计分散。**现状即如此，不解决诉求** |
| L1 账号内联声明 | 1~2 天 | `account.extra.serving_families`；`IsGroupAccountPlatformCompatible` 增加该参数的变体；`GroupAccountPlatforms` 读注册表快照 | 无 DB 迁移；但平台列表仍是硬编码，仍要发版才能加供应商 |
| L2 供应商注册表 | 3~5 天 | `upstream_providers` 表 + 内置回退 + 平台清单运行时拉取 + 前端派生 | 需要一处启动期注册表加载与热更新，避免调度读到半旧快照 |
| L3 Admin 页 | 2~3 天 | 供应商 CRUD、`serving_families` 多选、绑定兼容性提示、分组页显示「该族可由哪些供应商供应」 | 主要是 UI 与校验，无路由风险 |

建议先做 L1 验证判定链与测试形态，L2 随后；L3 等 L2 稳定后再开。

## 9. 回滚

- L1：`serving_families` 缺失时退化为 `{account.platform}`，等价旧行为，无需回滚代码
- L2：注册表加载失败时使用内置 13 条默认值，即当前行为；`enabled = FALSE` 可逐供应商停用
- 数据：不重写任何账号 platform、不改分组 platform、不迁移历史绑定。停用供应商后，已绑定的账号会因不兼容而在调度期被跳过（fail closed），不会错转发
- 部署：沿用 `deploy/README.md` 的 canonical blue/green 流程；停用的是供应商行而不是版本回退

## 10. 一句话回答「这个设计是否可以」

可以。但把「自定义渠道 → 所有分组」改成「供应商声明可服务的模型族」之后，它从一个危险的全局放宽变成一次干净的字段语义拆分：分组仍然是定价与配额身份，供应商只负责转发，模型族判定继续作为最终安全闸。而用户真正想要的效果——一个 WorkBuddy 账号被多个国模分组各自添加——只需要一行 `serving_families` 配置就能达成。
