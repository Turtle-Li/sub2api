# Paid fallback accounts, phase 1

Task: paid-fallback-phase1-20261022. Type: feature. Priority: availability with explicit paid admission. Baseline: fork/main `08c54a9d1b325645e315537bcedb33c103dad132`.

The owner approved phase 1 and requires validation without disturbing customer traffic. HTTP and WebSocket use one account policy. Existing connections keep their account; no live connection or provider-owned response state is migrated.

## Contract

- R1 / AC1: `accounts.extra.paid_fallback=true` marks a paid reserve. It is excluded from ordinary selection even when the feature is disabled. Existing accounts default to ordinary. Invalid non-false markers fail closed. Generic account edits must preserve an omitted marker.
- R2 / AC2: An administrator enables an explicit group and API-key allowlist. Missing, invalid or unavailable settings deny paid admission. Initial supported group platforms are OpenAI and DeepSeek. Both scheduler modes and HTTP/WS share this policy.
- R3 / AC3: Try ordinary accounts first, including their wait plan. Only evidence of ordinary upstream failure or runtime unavailability permits the paid pass. Busy capacity, unknown exclusions, profit rejection, unsupported model/endpoint/transport, channel restrictions, invalid requests and dependency errors do not authorize spending.
- R4 / AC4: Paid selection retains group/model/capability/privacy/quota constraints. Only the profit margin gate may be exempted, after explicit paid admission. Recheck authorization after waiting and at WS turn boundaries. A disabled policy stops new work; already transmitted streams may finish.
- R5 / AC5: Ordinary recovery wins over paid session affinity for new stateless requests/connections. Non-replayable continuation state is not moved to another account. The normal retry cap permits at most one additional paid selection when ordinary candidates are exhausted, never a new ordinary retry cycle. Existing bounded same-account and protocol retries remain in effect; this is not a limit of one transport attempt or one billable operation.
- R6 / AC6: Persist paid usage attribution at request time with existing account cost snapshots. Customer charges are unchanged. Failed attempts without provider usage have unknown cost and must not be reported as free. Operational events distinguish selection, forwarding and measured usage.
- R7 / AC7: Local fake upstreams cover GPT and DeepSeek, both scheduler modes, failure/recovery, busy pools, kill switch, isolation and stateful continuation. A later live smoke uses a new exclusive DeepSeek group, new test key and unshared upstream accounts; production/customer fault injection is prohibited.

The marker reuses the existing account Extra persistence/snapshot/import contract instead of generating a new account column. Only a single reserve tier is supported. Scope is an administrator API in phase 1; a dedicated settings UI and hard spending caps are later work. Reverting code must preserve the marker and paid usage history. A pre-feature binary must not serve groups containing paid reserves: disable/unbind those reserves before incompatible rollback.

## Dependency plan

| Task | Depends on | Writer | Output / gate |
| --- | --- | --- | --- |
| T1 | none | root | Confirmed contract, fixed upstream comparison and isolation plan |
| T2 | T1 | root | Marker, fail-closed settings, red/green unit tests (AC1–2) |
| T3 | T2 | root | Ordinary/paid selection, failure evidence, terminal checks and bounded retry (AC3–5) |
| T4 | T3 | root | Immutable usage attribution, administrator API and operational events (AC6) |
| T5 | T2–4 | root developer verification; independent read-only review when available | Focused and impacted checks, fake-upstream journeys, final evidence (AC7) |

No parallel writers. Changed contracts invalidate affected tests and downstream evidence. Architecture reference: parent `docs/sub2api-fallback-account-review-20261022.md`; mapped-provider behavior remains governed by `docs/operations/MULTI_MODEL_ACCOUNT_GROUPS_20261009.md` and fixed upstream `f2669c8cf62555cd92389b3f55920e9e6e7c6ff2`.

Local implementation and verification are authorized. No production deployment, real account/key creation, credential retrieval, commit or push is part of this authorization. Stop local work when AC1–7 have recorded evidence and the reviewable change is ready. Live DeepSeek smoke is a separate last-step approval against the concrete candidate.

2026-10-22 授权更新：用户明确要求提交、上线并用现有 WB/Cline 账号调用 DeepSeek 测试。后续发布复用项目的精确主分支 GitHub Actions 蓝绿流程；测试使用独立分组、Key 和账号副本。默认以 WB 普通、Cline 付费兜底覆盖策略，副本之外的账号配置不变。此更新取代上一段中尚未授权提交和上线的状态。

发布准备已整合 `fork/main` 的 `cf90a3d52ac6d6d936ccdf74426e56352f360fcd`，保留 ModelTrace 调度状态修复。更新后的 37 个生产文件集合 SHA-256 为 `c5baa473b6489015cc6dd724bcccd3b335e6d9e9ff002eab2b6ac561d115adf5`。新增 WB 原生 DeepSeek 映射到 Cline Pass 兜底的两调度器回归，与现有多模型调度测试共同通过 `-race`（4.103s）。下文初次本地开发的哈希和回归仍作为历史支持证据，不代替集成后验收。

## Change report and verification

一期代码已实现，默认关闭。此文档记录实现边界和隔离验收步骤；当前提交、部署与真实验收状态见文末记录。开发者验证不等于真实供应商验收。

### 实际范围

- 账号标记：`accounts.extra.paid_fallback`。缺省或布尔值 `false` 为普通账号，其余已存在的值（包括错误类型和 `null`）均隔离到付费池。普通账号编辑若未提交此字段，数据库合并保留原标记。影子账号不能以普通身份借用已标记的母账号凭据；需要参与付费兜底的影子账号也必须显式标记。
- 策略配置：`paid_fallback_settings`，使用现有管理员鉴权。仅在全局开关开启、分组与认证 Key 同时命中白名单时允许选择付费账号。缺少配置、损坏配置和依赖读取失败均拒绝付费调用。
- 调度：OpenAI、DeepSeek 文本兼容链路，兼容高级与旧调度器。先执行普通池的选择和等待；普通账号运行故障、限流或运行冷却等造成池不可用时再尝试付费池。并发满载继续原等待计划。手工禁用、请求不合法、毛利拒绝、模型或传输不兼容等不会成为付费理由。
- 转发：保留分组、模型、端点能力、隐私、配额、通道和传输检查。付费授权只豁免毛利比较，不接受非法成本倍率。等待结束重新校验账号与策略；真实 HTTP 发出前再次读取付费开关。
- HTTP 与 WS：是同一种账号策略，不需要用户分开配置。HTTP 每次请求选择账号，WS 在建连时选择账号，在后续轮次检查授权；原有连接不迁移，携带 `previous_response_id` 的继续请求保持原账号绑定。普通池恢复后，新的无状态请求和新连接优先普通账号。DeepSeek 的 HTTP 验收可覆盖共用调度策略，具体账号仍须原生支持所选传输协议。
- 费用：`usage_logs.paid_fallback` 保存请求发生时的归因，管理员用量 DTO 暴露该字段。沿用原有成本快照，不改变客户收费、余额、订阅或分组归属。付费失败且未返回 usage 时，费用未知，不能当作零费用。

本期没有专用前端面板、飞书通知、持久化失败尝试账本、预算预警或硬预算熔断；这些是后续范围。专用图片接口、Responses 内嵌生图工具或生图模型、legacy compact 以及其他原生平台不开放付费兜底；已绑定付费账号的 WS 连接也拒绝后续生图轮次。已有的有界同账号重试仍可能产生多次上游请求，一次额外付费选择不代表只会付费一次。

### 管理接口

接口复用现有管理后台鉴权，不接收客户端自行声明的 Key 身份。

| 方法和路径 | 用途 | 请求体 |
| --- | --- | --- |
| `GET /api/v1/admin/settings/paid-fallback` | 查询当前策略 | 无 |
| `PUT /api/v1/admin/settings/paid-fallback` | 替换完整策略 | `enabled`、`group_ids`、`api_key_ids` |
| `PUT /api/v1/admin/accounts/:id/paid-fallback` | 设置账号类别 | `{"enabled":true}` 或 `{"enabled":false}` |

策略示例，`7`、`11` 只是占位 ID，实际操作时必须换成新建测试分组和专用测试 Key 的 ID：

```json
{"enabled":true,"group_ids":[7],"api_key_ids":[11]}
```

紧急关闭应更新策略为 `enabled:false`，可以保留白名单便于后续恢复。**不要通过清除账号标记关闭兜底**：清除标记会让该账号回到普通池。标记接口关闭表示“改成普通账号”，不表示“停止调用”。

### 可观察性与成本查询

事件区分 `paid_fallback_selected`、`paid_fallback_upstream_attempt`、`paid_fallback_upstream_result` 和 `paid_fallback_websocket_turn_admitted`；已有用量日志输出 `[PAID FALLBACK USAGE]` 及估算成本。选择事件不代表已请求上游；WS 轮次准入事件不代表已产生费用；HTTP 结果事件也不代表已拿到最终 usage。未知费用事件带有 `cost_known=false`，一期不提供跨进程持久化尝试审计保证。

下列只读查询用于统计已有 usage 的估算上游成本，**不是供应商最终账单**。它不能覆盖供应商未返回 usage 的失败请求，也不应使用客户 `actual_cost` 代替上游成本：

```sql
SELECT account_id,
       count(*) AS usage_rows,
       sum(coalesce(account_stats_cost, total_cost)
           * coalesce(account_rate_multiplier, 1)) AS estimated_upstream_usd
FROM usage_logs
WHERE paid_fallback = TRUE
  AND created_at >= CURRENT_DATE
GROUP BY account_id;
```

迁移 `263_usage_log_paid_fallback.sql` 仅增加默认 `false` 的布尔列，没有给历史用量重分类，也没有在线构建大表索引。Ent 产物和手写插入、扫描及查询列同步更新，避免批量写入错位。

### 开发阶段验证证据

最终候选提交为 `6ac894ae123ec0e294d3650fd0d16d43a14a1c76`。相对基线 `cf90a3d52ac6d6d936ccdf74426e56352f360fcd`，生产源码集合为 39 个已修改或新增的 Go/SQL 文件（排除 `_test.go`，包含 Ent 产物及上线检查所需的两处既有 lint 修正）。按仓库相对路径排序，对每个文件的 `路径 + NUL + 文件内容 + NUL` 计算 SHA-256：`d27d29fa959f95fff15a86b8a093a00585dc6f30ea617eab54b88c01d5cbd875`。分支为 `codex/paid-fallback-phase1-20261022`。

| 验收 | 证据与结果 |
| --- | --- |
| AC1–2 | 标记错误类型隔离、默认关闭、双白名单、管理员策略验证与标记接口测试；旧 Extra 更新保留标记回归 |
| AC3 | 两种调度器的故障、恢复、等待、未知排除、毛利拒绝、模型及依赖失败边界测试 |
| AC4–5 | 等待后停用或非法倍率拒绝；WS 共用选择策略、每轮开关与新毛利上下文；付费继续请求保留绑定；付费母账号与影子隔离 |
| AC6 | 用量插入形状、参数数量、读回扫描与旧成本字段回归；历史付费归因独立于账号后续标记 |
| AC7 | `httptest` 本地真实 HTTP 转发：GPT/DeepSeek、流式与非流式、普通上游 503 后转付费、普通恢复、选择后关闭且无额外付费请求。凭据为测试假值，未调用真实模型 |

开发阶段完成 service、repository、handler、admin、dto、middleware、routes 七个包的完整回归；P2 修正后 service 报告 18,184 个测试及子用例通过，其他六包分别为 1,215、1,895、804、36、380、124。最终文本/生图边界与既有 WS 场景的针对性 `-race` 检查通过（service 33.920s、handler 6.300s）。上线检查修正后，service 387、admin 26 个针对性用例及 `go vet` 再次通过。

本地 WS 证据覆盖策略和轮次准入逻辑，真实供应商建连与多轮持续使用仍需单独验收。数据库迁移与真实 DeepSeek HTTP 验收以本文后续的上线记录为准。

复现检查在仓库 `backend/` 下执行：

```sh
go test -tags=unit ./internal/service ./internal/repository ./internal/handler ./internal/handler/admin ./internal/handler/dto ./internal/server/middleware ./internal/server/routes -count=1
go test -race -tags=unit ./internal/service -run '^TestPaidFallback|^TestParentHealthyForShadow|^TestGatewayProfitControlSelectionCarriesGateToHandlerContext$' -count=1
go vet -tags=unit ./internal/service ./internal/repository ./internal/handler/... ./internal/server/...
go build -o /tmp/sub2api-paid-fallback-phase1-server ./cmd/server
```

上线检查使用 Go 1.27.3，以及校验官方发行校验和后临时运行的 `golangci-lint v2.14.0`，按 CI 默认参数报告 `0 issues`。未安装用户已拒绝的 `gopls`。依赖安全更新涉及前端锁文件，30 个测试文件、668 个用例、类型检查与生产构建通过；没有产品界面改动。

### 后续 DeepSeek 隔离试跑

用户已于 2026-10-22 明确授权提交、上线并使用现有 WB/Cline 的 DeepSeek 能力进行隔离测试。测试仅复制原账号配置到专用账号记录，WB 为普通池、Cline 为付费池，使用新建专用分组和 Key；供应商额度仍共享，因此仅执行少量串行短文本请求。以下为验收方法，实际结果以本文后续上线记录为准。

1. 默认关闭策略部署候选版本。先确认原有客户请求正常，新增迁移与普通用量写入正常。
2. 新建专用测试用户、DeepSeek 分组、测试 Key，以及仅绑定该组的普通测试账号和付费测试账号；不得复用客户组、客户 Key、共享账号记录或母/影子关系。使用小输出上限、串行单请求测试，并单独记录真实供应商费用。
3. 先标记付费账号，验证开关关闭时它不参与普通调度。策略仅填测试组 ID 与测试 Key ID 后开启；普通账号健康时仍由普通账号执行。
4. 仅让专用普通测试账号使用无效测试凭据产生 401，确认请求进入付费账号。分别测非流式和流式。恢复普通测试账号的有效凭据并清理其测试故障状态，新无状态请求应立即回普通池。不要通过手工暂停普通账号来模拟触发条件。
5. 测试另一把未入白名单的专用 Key，确认同组也不会付费兜底。选择后关闭全局策略，验证新请求停止付费；已经发出的请求可完成。恢复原白名单后再关闭，确认停用是可重复操作。
6. 核对账号实际命中、usage 标记、成本快照和供应商账单。任何组外命中、Key 外命中、普通健康却付费、费用异常或关闭后新付费调用，立即关闭策略并停止扩大范围。

独立 DeepSeek 试跑证明共同策略与 HTTP 转发链路；后续启用真实 GPT WS 前，还需用专用 GPT 测试账号验收实际建连、每轮停用与 `previous_response_id` 继续请求。不能把 DeepSeek HTTP 结果当作 WS 实际连接验收。

### 停止与回滚

优先关闭策略，保留账号标记和用量归因。需要回退到不识别付费标记的旧二进制时，必须先将付费账号停用并从服务分组解绑，确认没有待发的新任务，再回退程序；保留新增列，不回写历史用量。单纯关新策略不能保证旧二进制隔离付费账号。

knowledge_candidate: no; project-specific controlled rollout.

### 上线前独立审查修正

合入 `fork/main` 的 `cf90a3d52ac6d6d936ccdf74426e56352f360fcd` 后，独立只读审查在候选提交 `19cdc2c22d5bc3ad1d2ce5fa3a4d83075c51090b` 上发现两处需修正的问题：

- Chat Completions 入口未把选号许可传到最终 HTTP 请求，发送前关闭策略不能生效。现与 Responses/Messages 一致传递选号上下文，并增加真实 Handler 入口回归；测试未手动注入付费许可，修复前能复现关闭后仍发送。
- 新增的付费继续请求预读在绑定账号删除或缓存回退失败时直接返回错误，影响策略关闭时的原有普通继续请求。预读失败现交回原调度流程；普通池的付费隔离过滤仍生效，已确认的付费继续请求仍保持原凭据绑定。

付费兜底仅允许 Chat Completions、文本 Responses/Messages 及共用文本 WS 策略；Embeddings、独立搜索、图像、Live、Seedance 和旧版 compact 不进入付费池。新增 Embeddings/搜索边界用例在修复前均能复现意外选中付费账号。

复审通过后进一步收紧原生 Responses 生图：共用 `responses` 能力不能单独区分文本与内嵌生图，因此额外检查生图意图、原生工具模型及请求模型。新请求与付费继续请求的四个回归场景均在修复前复现了进入付费池；WS 后续轮次在解析生图意图后直接拒绝。

### 首轮上线检查

最终代码提交 `73a39c96daca29a688bf7c6117459ede22badcbb` 已推送自有仓库主分支。

- [CI 39669404108](https://github.com/Turtle-Li/sub2api/actions/runs/39669404108)：后端完整单元和集成测试、前端、默认构建标签 lint、部署脚本、发布工具与容器挂载检查全部通过。
- [安全扫描 39669407855](https://github.com/Turtle-Li/sub2api/actions/runs/39669407855)：前后端均通过。Go 工具链及所有构建镜像固定到 1.27.3，前端 `follow-redirects` 固定到 1.16.1，未增加安全例外。
- 独立只读复审和 QA 均为 `PASS`，核对了文内 39 文件生产源码哈希；额外的退款审计、Cline 和调度器 lint 修正保持原有行为。
- [AWS 标准蓝绿发布 39671596729](https://github.com/Turtle-Li/sub2api/actions/runs/39671596729) 已成功，源提交与上述检查一致。Caddy 已切到健康的 blue 实例；迁移 263 的布尔列、非空及默认 false 均已核对，新实例已有普通用量正常写入。

### 首轮真实 DeepSeek 验收发现与修复

2026-10-22 04:55 CST，在专用用户 81、独占组 33 内，使用 WB 副本 237、付费 Cline 副本 238、白名单 Key 131 与对照 Key 132。策略关闭时的非流式和策略开启时的流式均成功走 WB，普通用量归因正确。

随后仅将 WB 副本改为无效测试凭据。上游真实返回 401，现有认证错误处理将账号持久化为 `error`，重选时返回 502，没有付费供应商调用。根因是兜底证明查询使用的 `ListByGroup` 只返回 `active` 账号，漏掉了刚被标错的普通账号；此前测试替身返回全部状态，未还原仓储契约。测试已自动关闭策略、停用专用用户/组/副本、撤销两把 Key 并清理缓存，两个 Key 均验证返回 401。

修复复用现有 `ListAllWithFilters`，限定同一分组并纳入错误状态，保留既有停用、模型、白名单、利润和依赖失败检查。测试替身同步正常状态查询契约，新增两种调度器下 401/403 状态迁移及后续请求回归；四个场景均已在修复前复现失败。测试同时还原 `SetError` 将 `schedulable` 设为 false 的行为，并验证仍处限流期的手工停用账号不会成为付费准入依据。

修复提交 `6ac894ae123ec0e294d3650fd0d16d43a14a1c76` 已推送主分支，四文件差异哈希为 `9c03caf08ee07c479b265e8b586f0d08243aeb9ffbcf79bae9c6add36783004f`。本地 service 竞态测试及 Handler 最终发送竞态测试通过，最终测试补强后的 service 针对性竞态测试 4.667s；格式与 vet 通过。独立复审和 QA 在 05:14 CST 均通过，额外用旧源码复现四个失败，并通过变异测试确认“错误且不可调度仍可兜底”“已停用且限流不可兜底”两条边界确实受测试保护。真实 401 经过完整网关和仓储的重跑仍是发布后的验收条件。

### 认证故障修复后的上线检查

- [CI 39677361275](https://github.com/Turtle-Li/sub2api/actions/runs/39677361275)：六个任务全部通过，包含完整后端单元和集成测试、前端、lint、部署脚本、发布工具与容器挂载验证。
- [安全扫描 39677363986](https://github.com/Turtle-Li/sub2api/actions/runs/39677363986)：前后端均通过。
- 两次检查均对应修复源码 `6ac894ae123ec0e294d3650fd0d16d43a14a1c76`，与独立复审最终四文件哈希一致。
- [AWS 标准蓝绿重发 39679358942](https://github.com/Turtle-Li/sub2api/actions/runs/39679358942) 已成功。05:42 CST 核对 Caddy 实际目标为健康的 `sub2api-green`，OCI 源码标签与上述修复提交完全一致，镜像 `sub2api:auto-20261021-144046-6ac894ae`，镜像 ID `sha256:331645c339b4d6d5f0f087467f3c990eeabcfc8e8a58c06432b565941b094e24`；零重启、零 OOM，启动后未出现 panic/fatal，公共 `/health` 为 200。迁移列与默认值正确，策略及所有测试资源在重跑前均为关闭状态。

### 当前验收状态

发布已完成，完整真实供应商验收尚未完成。修复版启动后已核对 8 条测试组外普通用量正常写入，测试范围外付费用量为 0。重跑时两次本机 Vault 认证均未授予任务凭据，固定消费者没有开始执行，因此修复版的 WB 故障转 Cline、对照 Key 拒绝、关闭与恢复矩阵仍待本机主动确认后重跑。

首轮已实测的结果仅为 WB 健康时普通非流式和流式成功；首轮故障切换失败及修复过程见上文。当前没有真实 Cline 付费调用成功证据，不能将单元测试、独立复审或发布成功当作这项真实验收通过。

策略保持 `enabled=false`，专用组、用户、账号副本均停用；两把临时 Key 已在首轮清理时撤销并验证 401，认证失败的重跑未重新激活它们。Vault 项目仅保留隐藏字段与审计引用。后续仍使用同一固定哈希消费者和专用范围，运行结束必须再次执行撤销、缓存失效、两个 Key 的 401 校验及原账号指纹比对。
