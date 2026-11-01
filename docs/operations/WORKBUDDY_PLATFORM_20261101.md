# WorkBuddy 平台接入（方案 A）

2026-11-01 实施记录。目标：让走 OpenAI 兼容协议的 WorkBuddy 账号像 Cline 一样，被多个模型厂商分组各自添加，而不需要复制账号。设计取舍与后续数据化方案见 [CUSTOM_UPSTREAM_PROVIDER_FAMILIES_20261101.md](CUSTOM_UPSTREAM_PROVIDER_FAMILIES_20261101.md)。

## 1. 契约

- 新增具体平台 `workbuddy`，网关为 OpenAI（`PlatformGatewayOpenAI`），登记为**多模型聚合平台**（`domain.IsMultiModelPlatform`），与 `opencode_go`、`command_code`、`cline` 同构。
- 因此 WorkBuddy 账号可被任一模型厂商分组添加：`anthropic`、`openai`、`gemini`、`grok`、`kimi`、`zhipu`、`deepseek`、`minimax`、`typesafe`；同时保留 `composite` 与同平台绑定语义。`antigravity`（Gemini 原生）不接受。
- 跨族安全性不变：候选池只由平台归属决定，能否真正转发仍由模型族判定兜底——`Account.SupportsGroupPlatformUpstreamModel` 要求**账号映射后**的上游模型名经 `DetectModelPlatform` 解析出的族等于分组平台，未知或他族一律 fail closed，且在选择期、hydration 后、序列化发送边界各复核一次。
- 只有 Chat Completions：`responses` / `anthropic` 原生端点不登记，Responses 与 Messages 入站由现有转换链转为 Chat Completions（`shouldForwardOpenAIResponsesViaRawChatCompletions` 对 `RoutesProtocolByInbound` + `chat_completions` 返回 true）。
- **不设默认端点**：WorkBuddy 没有公开的 OpenAI 兼容地址，端点完全由部署方提供（自建反代/网关）。`ProviderProfile.RequireAccountBaseURL` 声明这一点：前端把 base_url 标为必填、协议固定为 `chat_completions`；后端 `GetOpenAIBaseURL` 在缺凭证基址时返回空串，**不会**回落到 `api.openai.com`，避免把第三方 Key 发给 OpenAI 官方。
- **模型族识别支持通道前缀**：上游常用 `<通道>:` 区分供应来源（例如反代把官方模型名加 `cn:`）。`DetectModelPlatform` 严格识别失败时会剥掉一层短前缀再试，因此 `cn:deepseek-v4-pro` 仍归属 DeepSeek 族，跨族绑定无需为每个模型额外拼厂商前缀；剥完仍识别不出的名字继续 fail closed。

## 2. 代码改动

| 文件 | 改动 |
| --- | --- |
| `backend/internal/domain/constants.go` | 新增 `PlatformWorkbuddy = "workbuddy"` |
| `backend/internal/domain/platforms.go` | `platformList` 追加一条（Gateway=OpenAI）；`IsMultiModelPlatform` 追加；`GroupAccountPlatforms` 改为由 `IsMultiModelPlatform` 派生，避免以后新增聚合平台漏改候选池展开 |
| `backend/internal/service/domain_constants.go` | service 层别名；`DefaultWorkbuddyBaseURL` 占位常量与注释 |
| `backend/internal/service/provider_profile.go` | 新增 profile：`payg` 单模式、`by_inbound` 分流、只登记 `chat_completions`、无 `DefaultTestModel` |
| `frontend/src/types/index.ts` | `KnownAccountPlatform` 追加 `'workbuddy'` |
| `frontend/src/constants/platforms.ts` | `MULTI_MODEL_PLATFORMS` 追加（绑定兼容性前端镜像） |
| `frontend/src/utils/platformColors.ts` | `Platform` 联合类型 + 13 张配色表 + `isPlatform`（emerald） |
| `frontend/src/utils/keyGroupProviders.ts` | `PROVIDER_BY_PLATFORM` 追加 `workbuddy: 'other'` |
| `frontend/src/components/keys/UseKeyModal.vue` | 平台展示名 `WorkBuddy` |
| `frontend/src/constants/platformCatalog.builtin.json` | 由 `UPDATE_PLATFORM_CATALOG=1 go test -run TestFrontendBuiltinPlatformCatalogInSync` 重生成 |

账号类型、凭据表单不需要改：`CreateAccountModal` 的聚合平台行由 `listPlatforms().filter(spec => !!spec.multi_protocol ...)` 派生，`workbuddy` 自动出现在「OpenCode / Command Code / Cline」同一行。

## 3. 测试改动

- `backend/internal/domain/platforms_test.go`：基准列表补齐，并新增 `TestWorkbuddyBindsToModelProviderGroups`（正向：七个模型厂商分组都可绑定且候选池包含；反向：`workbuddy` 不是模型厂商、`antigravity` 拒绝、同平台与 composite 语义不变）。
- `backend/internal/service/platform_list_test.go`：legacy 基准补齐 `workbuddy`（配额平台、调度快照、composite 匹配、OpenAI 网关归一化、计费探测身份、请求头覆写资格、多协议供应商归属）。
- `backend/internal/service/multi_model_group_test.go`：抽出 `multiModelTestProviders`，把 `workbuddy` 加入跨族调度（adaptive / legacy × 批量 / 非批量）、族判定（同族 / 跨族 / 错误映射 / 未知模型）、模型列表投影的回归矩阵；`multi_model_group_gateway_test.go` 的 6 处同源循环同步扩展（hydration 复核、channel 映射准入、序列化前校验、Messages 归一化等）。
- `backend/internal/handler/gateway_handler_multi_model_unit_test.go`：Messages 入口的归一化矩阵加入 `workbuddy`。
- `backend/internal/service/channel_service_test.go` / `composite_platform_test.go`：composite 与调度桶的穷举列表补齐。
- 前端：`platforms.spec.ts`、`credentialsBuilder.platformCatalog.spec.ts`、`CreateAccountModal.spec.ts`、`UserPlatformQuotaModal.spec.ts`（13 → 14 平台 × 3 窗口 = 42 输入）、`settings.authSourceDefaults.spec.ts`。

## 4. 运营配置

1. 新建账号：平台选 `WorkBuddy`，类型 `apikey`，必须填 `base_url`（WorkBuddy 接入端点，`{base}/chat/completions`）与 `api_key`。只有一个接入模式 `payg`，不需要选账号类型。
2. 模型映射：公开模型名 → 上游模型名。前缀式上游（`cn:<官方名>`）可以一条条按需写，也可以从上游模型列表批量生成（见 §4.1）。**映射后的名字必须能被识别出厂商族**，否则跨族绑定 fail closed：
   - 直接可用：`kimi-k3`、`deepseek-v4.1-flash`、`glm-5.3`、`minimax-m3`、`gpt-5`。
   - 带通道前缀可用：`cn:deepseek-v4-pro`、`relay:kimi-k3`（剥离一层前缀后识别）。
   - 带厂商前缀可用：`deepseek/deepseek-v4.1-flash-sg`、`kimi/kimi-k3`。
   - 不可识别（会被拒绝）：`deepseek-v4.1-flash-sg`、`my-custom-model`、`cn:my-custom-model`。
3. 绑定分组：账号编辑里多选 `DeepSeek`、`KIMI`、`GLM`、`MiniMax` 等模型厂商分组。同一账号在这些分组间共享并发与冷却。
4. 渠道价卡：按**模型族**配置（`deepseek` / `kimi` / `zhipu` / `minimax`），不要为 `workbuddy` 建价卡——分组平台才是定价与配额维度，WorkBuddy 只影响转发与账号归属。
5. **不要建 `platform=workbuddy` 的分组**。它与其他聚合平台一样会出现在分组平台下拉里，但没有对应价卡、用户平台配额与广场卡片，属于死配置。

### 4.1 批量生成前缀映射

上游返回 `cn:deepseek-v4-pro` 这类带通道前缀的名字时，公开名取官方名即可。模型不多就手写；多的话从上游 `/models` 直接生成，粘进账号的模型映射输入框：

```bash
BASE=https://your-relay.example.com/v1   # 账号 base_url
KEY=sk-workbuddy                          # 账号 api_key
curl -s "$BASE/models" -H "Authorization: Bearer $KEY" \
  | jq -c '[.data[].id | select(test("^cn:")) | {(. [3:]): .}] | add'
# {"deepseek-v4-pro":"cn:deepseek-v4-pro","kimi-k3":"cn:kimi-k3",...}
```

前缀不是 `cn:` 时改 `test("^<前缀>:")` 与 `[<前缀长度> + 1:]` 两处即可。生成的 key 就是分组白名单和对外公开的模型名，value 是发往上游的真实名字。

## 5. 与 Cline 的差异

| 能力 | Cline | WorkBuddy |
| --- | --- | --- |
| 默认 base_url | 官方 `https://api.cline.bot/api/v1` | 无默认端点，账号必填（缺失时不回落 openai.com） |
| 默认连接测试模型 | `cline-pass/*` 模型 | 空，连接测试需显式指定模型 |
| 入站协议 | Chat Completions | Chat Completions（协议选择区对无原生端点的供应商隐藏） |
| 额度 / 余额探测 | ClinePass 窗口 + 积分余额（官方主机） | 无，走通用冷却 |
| 上游 402 文案分类冷却 | 有（`ratelimit_cline.go`） | 无 |
| 上游 pin（能力注入） | 有（`cline_upstream_pin.go`） | 无 |

因此本次没有新增 `account_test_service` / `cn_provider_quota_service` / `ratelimit_cn_providers` 的平台登记项；WorkBuddy 账号按 `RoutesProtocolByInbound` + `chat_completions` 走既有的 `testCNProviderChatCompletionsConnection`。

## 6. 调度成本

模型厂商分组的候选池从 `1 + 3` 变为 `1 + 4`（每个分组选择多一次缓存桶读）。`MULTI_MODEL_ACCOUNT_GROUPS_20261009.md` 记录过「只有三个聚合平台，扩张前先看 p95」的约束，本次是第 4 个。若后续继续增加，应按该文档的 batching 建议合并桶读取，而不是继续线性叠加。

## 7. 验证

```
# 后端
go test -tags unit ./internal/service ./internal/handler ./internal/domain ./internal/server/routes -count=1
ok  internal/service 263.915s
ok  internal/handler 54.251s
ok  internal/domain 2.222s
ok  internal/server/routes 21.340s

# 前端
pnpm run typecheck                      # 通过
pnpm vitest run                         # 423 files / 3501 tests 通过
npx eslint <9 个改动文件>                # 无告警
```

前端平台清单一致性由 `TestFrontendBuiltinPlatformCatalogInSync` 强制：`platformCatalog.builtin.json` 与后端 `BuildPlatformCatalog()` 必须逐字节同构。

未覆盖：真实 WorkBuddy 上游的账号测试、真实分组绑定与计费落账、scheduler p95 实测。这些需要生产凭据与授权，不属于本地验证范围。

## 8. 回滚

1. 先解绑并停用所有 `platform=workbuddy` 账号（或保留但停用）。
2. 回滚代码：`platformList` 一行、`IsMultiModelPlatform` 一处、profile 条目、`DefaultWorkbuddyBaseURL`、前端常量与 `platformCatalog.builtin.json`。
3. `GroupAccountPlatforms` 的派生重构可以保留（与本次平台无关，且防止下次漏改）。

未回滚时，若只删代码不动数据：`IsConcretePlatform("workbuddy")` 变 false，该平台账号不再进入调度（fail closed），不会产生错误转发；但账号行与历史绑定仍在库中，重新上线后自动恢复。

注意：`docs/*` 在 `.gitignore` 中被忽略，本文件与同目录其他记录一样需要 `git add -f` 才能进入提交。

## 11. 发布与生产配置核对（2026-11-01 ~ 11-02）

### 11.1 v0.2.20（平台接入）

工作流 [Deploy #41320248062](https://github.com/Turtle-Li/sub2api/actions/runs/41320248062)，8m26s 成功。
镜像 source `8c4d415e`，`sub2api-blue → sub2api-green`，`health=healthy background_mode=activate`，
`app_5xx=0 app_fatal=0 caddy_5xx=0`，Caddy `active_slot=green`。线上 `GET /health` → `{"status":"ok"}`。

发布分支在 fork/main（v0.2.19）基线上重跑：后端 service 272.8s / handler 57.9s / domain 6.1s /
routes 20.6s 全过；前端 `vue-tsc` 通过、429 files / 3653 tests 通过、`pnpm build` 成功。

### 11.2 v0.2.21（同步模型剥离通道前缀）

原因：上游 `/models` 全部返回 `cn:<官方名>`，"同步模型"会把带前缀的名字直接写进白名单与映射，
管理员还要逐条手改成官方名。

- `UpstreamModelCatalog.ModelAliases`（公开名 → 上游原名）；同步/预览接口返回前剥离统一的
  通道前缀，Metadata 键同步改名。
- 编辑账号"同步模型"用别名表生成 `{from: 官方名, to: 上游原名}`，白名单直接是官方名。
- 只在整个列表共享同一个非厂商前缀、去前缀后非空且不重名时启用，否则原样返回。
- 转发仍用上游原名；跨族准入继续由映射后的模型族判定兜底（`cn:` 前缀在 `DetectModelPlatform`
  中剥离一层后识别）。

### 11.3 生产配置核对

账号 246（`workbuddy` / apikey / active）绑定分组 17 DeepSeek、18 KIMI、35 GLM、36 MiniMax，
`api_protocol=chat_completions`、`account_mode=payg`，上游端点由账号 `base_url` 提供（已回填可用）。

**发现并修正**：该账号原有 10 条映射的 **key 也带 `cn:` 前缀**（形如 `cn:deepseek-v4-pro → cn:deepseek-v4-pro`），
而分组白名单是官方名，两者对不上，等于四个分组都拿不到这个账号的供给。已按一一对应改为
`key = 官方名`、`value = cn:原名`（10 条，`UPDATE accounts ... credentials.model_mapping`，
仅限 `id = 246` 且 `base_url` 匹配的记录，改后即时生效——调度快照只缓存账号 ID，实体每次读库）。

上游实测可用模型 18 个（全部 `cn:` 前缀）：`balanced-model`、`deep-model`、`deepseek-v4-pro`、
`deepseek-v4.1-flash`、`fast-model`、`glm-5.2`、`glm-5.3`、`glm-5.3-flash`、`hunyuan-image-alpha`、
`hunyuan-image-alpha-edit`、`hy3`、`hy4-preview`、`kimi-k2.8-preview`、`kimi-k3-1`、`kimi-k3.1-pro`、
`mimo-v2.6-pro`、`minimax-m3.1-flash`、`space-bunny`、`step-5-preview`。

白名单缺口（没有任何 active 账号能供给，需删名或补别名映射；不要用别的型号冒充）：

| 分组 | 白名单里有、无供给的模型 | 说明 |
| --- | --- | --- |
| 17 DeepSeek | `deepseek-flash`、`deepseek-v4.1-flash-sg` | 上游无 `cn:deepseek-flash`；`-sg` 别名随旧 WorkBuddy 账号 232 的映射一起消失 |
| 18 KIMI | `kimi-k3`、`k3`、`k3-256k`、`kimi-for-coding`、`kimi-for-coding-highspeed` | 原生 Kimi 账号 17 已 `inactive`；上游只有 `kimi-k3-1` / `kimi-k3.1-pro` |
| 18 KIMI | `k3.1-pro` | 可加一条别名映射 `k3.1-pro → cn:kimi-k3.1-pro`（同型号另一种写法），需运营确认后再加 |
| 36 MiniMax | `minimax-m3` | 上游只有 `minimax-m3.1-flash` |

已具备供给：`deepseek-v4-pro`、`deepseek-v4.1-flash`（246）、`deepseek-v4.1-flash` + `-1017`（232）、
`glm-5.3`、`glm-5.3-flash`（246）、`kimi-k3.1-pro`（246）、`minimax-m3.1-flash`（246）。

未验证：这四个分组的真实转发（本地 smoke key 绑定的是别的分组，返回的是该分组的
`model_not_found`，不能证明跨组链路）。需要在一个 17/18/35/36 的密钥上各发一次最小请求。

### 11.4 验证备注

`TestInflightEstimate_AccountMappingNoDBAndBoundedMemory` 断言整包运行时的堆增长 < 8MB，对同包其他测试留下的堆状态敏感：
同一基线（`8c4d415e`）全包通过，0.2.21 全包首跑失败、二次通过，单跑该组稳定通过。
它与本次改动（上游模型同步、模型族识别、账号基址）无调用关系，记为既有测试的环境敏感性；
发布流水线不执行 `go test`（仅构建镜像），不受影响。
