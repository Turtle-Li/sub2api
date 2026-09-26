# BPS 上游（Basis Points）维护手册

> 目的：让后续迭代新功能、排查和修复问题时不必重新摸索。改动 BPS 相关代码后请同步更新本文件（尤其是“未解决问题”和“常量”两节）。
> 最后更新：2026-09-27（对应 fork/main 31bce3733）。

## 1. 是什么、为什么

- BPS = `https://bps.openai.com/basispoints/api/responses`，ChatGPT-in-Excel 插件的后端。用 Codex OAuth（ChatGPT）令牌即可调用，与 Codex 原路径（chatgpt.com/backend-api/codex）额度与降智表现不同。
- 目标：对指定账号（当前生产为账号 69，分组 16）让尽可能多的 Codex 请求走 BPS，**全部在服务端解决**，不要求用户改 `config.toml`。
- 原则：BPS 是“优先尝试”的上游。**在向客户端写出任何字节之前的失败一律透明回退原路径**；写出之后的失败在同一条 SSE 流里接续原路径（native continuation）。

## 2. 参考上游与来源

| 项目 | 本地参考克隆 | 用途 / 已采纳内容 |
|---|---|---|
| hloolx/codex2api（MIT） | `/tmp/bpsr/hloolx_codex2api` | `basispoints` 协议包的最初来源：路由、工具循环身份与历史重放、envelope 格式、工具目录、HTTPS 图片引用。提交列表见 `backend/internal/service/basispoints/NOTICE.md` |
| ranxi2001/sub2api | `/tmp/bpsr/ranxi2001_sub2api` | 本仓库 `basispoints` 包的直接来源（同为 sub2api 结构）。已同步 v2.8.13（生产 3e345632f）的 #73/#78，以及 f35414792、f405108e3 的工具传输纠正（tool_repair） |
| JaxsonWang/cpa-plugin-oai-basispoints | `/tmp/bpsr/JaxsonWang_cpa-plugin-oai-basispoints` | 仅作对照（05b2d97）：tool/args envelope 样例。未采用其全局 call-ID 缓存与单工具提取 |
| Kaixxrua/excel-codex-bridge | `/tmp/bpsr/Kaixxrua_excel-codex-bridge` | 仅作对照：Excel 插件请求头与协议行为 |

`/tmp` 下的克隆会丢失，需要时重新 `git clone`。**来源声明必须维护在 `basispoints/NOTICE.md`**，移植新提交时追加一段说明“来自哪个提交、本地差异是什么”。

### ranxi2001 尚未合并、可评估的提交

| 提交 | 内容 | 状态 / 建议 |
|---|---|---|
| 10c0f90d8 | attachments、`unknown_tool_repair.go`、`tool_schema.go`（参数 schema 校验） | 未合并。unknown_tool_repair 可评估（模型调用目录外工具时纠正）；schema 校验会增加纠正次数，需先看数据 |
| 60f813d06 | — | 未评估 |
| c2d27c12c | 历史重放 / 图片参数校验 | 未合并，图片由本地 R2 外链化处理，冲突需逐项看 |
| 3d335f6a3 | 限流隔离、replay 过期 | 未合并，值得评估（当前 replay 缓存策略见 `bpsReplay`） |
| 5c1839b28、b69230e6a、231d46c4a、dd0af4e40、aa741006a | FUNCTION_CODE / cmd 相关 | 本地大概率已用另一方式覆盖（FUNCTION_CODE 同一标记承载 code 或 cmd），合并前逐条对照测试 |

## 3. 整体流程

```
Codex 请求 ─► openAIBPSAttemptFor（廉价判定，不触网）
             ├─ 不适用 → recordSkip(reason) → 原路径
             └─ attempt
                 ─► doOpenAIUpstreamPreferBPS
                     ─► tryOpenAIBPSUpstream
                         1. 内联图片 → bpsImageExternalizer 上传 R2（失败 → skip inline_image）
                         2. prepareBPSRequestBody → basispoints.Prepare（失败 → skip unsupported_request）
                         3. sendOpenAIBPSRequest（400 invalid_encrypted_content → 删加密推理重试一次）
                         4. newBPSBridgeStreamWithRepair → bpsPrimedBody 预读（hold 模式）
                         5. primeUntilOutput：首产出前失败/超时 → 回退原路径
                     └─ 回退：doOpenAIUpstream（原路径）
```

### 3.1 协议转换（`backend/internal/service/basispoints/`，约 5.7k 行）

- BPS 只认 Excel 插件的原生工具 `run_officejs`。客户端（Codex）的全部工具写进提示词目录（`catalog.go`、`request.go`），模型调用 `run_officejs` 并按“传输”格式携带真实调用，代理再还原为 Codex 工具调用。
- 传输类型（`tools.go`、`envelope.go`、`function_code_transport.go`、`custom_transport.go`）：
  - **FUNCTION**：`code` 是 JSON envelope `{"name","arguments"}`。
  - **FUNCTION_CODE**：summary `codex2api.function_code/NAME`，`code` 放原始代码或命令（本地同一标记同时承载 `code` 与 `cmd` 字段），元数据 JSON 放 `extended_summary`。
  - **CUSTOM**：summary `codex2api.custom/NAME`，`code` 放原始输入。
- 历史重放：`Prepare(body, scope, &bpsReplay)` 按会话 scope 缓存/还原工具调用身份，BPS 侧看到的是 `run_officejs` 历史。
- 恢复逻辑：`recoverUnmarkedFunctionCode` 等（`plan.go`、`invocation_recovery`）在调用无歧义时自动还原丢失标记的调用。
- 流转换（`stream.go`）：文本增量透传；**工具事件扣留到 `response.completed` 校验通过后才下发**（`StreamWithToolRepair`）。
- 工具传输纠正（`tool_repair.go`）：终态校验失败时，在同一 BPS 会话追加失败输出 + 每个调用一个 `function_call_output {"executed":false,"error":{"code":"invalid_client_tool_transport",...}}` + 一条 developer 纠正消息，最多 `maxToolRepairs = 2` 次。纠正前先关闭上游（释放并发租约）。只上报最后一次续跑的 usage（不求和，避免上下文被重复计算）。
- 路由判定 `route.go NativeFallbackReason`：`image_generation` 工具、实时联网 `web_search`（`external_web_access` 或 `search_context_size=high`，除非开启 live_search 开关）、`tool_choice` 指向这两者 → 原路径。

### 3.2 服务层（`backend/internal/service/openai_bps_*.go`）

| 文件 | 职责 |
|---|---|
| `openai_bps_upstream.go` | 常量、模型白名单、熔断、会话冷却、会话上下文记录、跳过/失败原因、`openAIBPSAttemptFor`、`tryOpenAIBPSUpstream`、`bpsToolRepair`、`sendOpenAIBPSRequest` |
| `openai_bps_stream.go` | `bpsPrimedBody`：预读与 hold 模式、首产出截止、原路径接续（`startContinuation`/`fillNative`） |
| `openai_bps_codex_compact.go` | Codex 自动压缩：manifest 上限、用量抬高提示、context_limit 恢复 |
| `openai_bps_config.go` | 动态配置（settings 表，进程内缓存 15s，读失败按关闭） |
| `openai_bps_monitor.go` | 内存监控环（200 事件 + 100 失败）、熔断状态、面板数据 |
| `openai_bps_probe.go` / `_runner.go` | 管理面板“探测”：同一 prompt 分别走 BPS / 原路径对比 |
| `openai_gateway_response_handling.go` | `applyBPSCodexCompactHintToSSELine`（在 processSSELine 中） |
| `openai_gateway_forward.go`（~1336 行附近） | 成功后 `recordSuccess` / `observeBPSSkippedContext` |

Hold 模式（`openai_bps_stream.go`）：
- `bpsHoldFirstOutput`：流式且无工具，首个模型产出即放行。
- `bpsHoldTools`：流式且有工具，扣到工具调用完成或超过缓冲上限。
- `bpsHoldTerminal`：非流式，预读到终态。
- `bpsHoldLimit = 45s`；首产出预算 `bpsFirstOutputBudget = 20s`（配置了首输出超时时取其一半）。

### 3.3 可靠性机制

- **按账号熔断**：连续 3 次失败打开 10 分钟（`bpsBreakerThreshold`、`bpsBreakerOpenDuration`）。面板可手动重置。
- **会话冷却**：同一会话 BPS 失败后 3 分钟内直接走原路径（`bpsSessionCooldown`）。scope = `account:%d/key:%d/thread:<identity>`。
- **格式类失败豁免**：`bpsIsToolFormatFailure` 为真（模型纠正后仍写错传输格式）时不推进熔断、不设冷却；纠正请求本身网络/HTTP 失败仍按账号问题处理。
- **context_stall**：首产出超时且上一轮上下文 ≥ 15 万时标记该会话 stalled，不计入账号熔断。

### 3.4 上下文上限与 Codex 自动压缩

事实：BPS 在约 **207k token**（按 BPS 计数，含约 25k 工具目录）处**静默不产出也不报错**，只能等首产出超时。

| 常量 | 值 | 含义 |
|---|---|---|
| `bpsContextTokenLimit` | 200_000 | 上一轮达到此值 → 本轮 skip `context_limit` |
| `bpsContextStallTokens` | 150_000 | 首产出超时时视为上下文过大的门槛 |
| `bpsContextShrinkRatio` | 0.8 | 请求体缩小到 80% 以下视为已压缩，恢复 BPS |
| `bpsContextNativeResumeTokens` | 150_000 | 跳过轮次原路径用量低于此值 → 恢复 BPS |
| `bpsCodexAutoCompactTokenLimit` | 185_000 | 压缩门槛：`/models` manifest 上限（只下调），也是回报用量提示压缩的门槛。**必须低于 `bpsContextTokenLimit`** |
| `bpsCodexCompactHintTokens` | 1_050_000 | 终态 input ≥ 185k 时回给客户端的 input/total，迫使 Codex 立即压缩 |

- 上下文规模只用 `bpsUsageContextTokens`（= `InputTokens`，**已含缓存**，切勿再加 `CacheReadInputTokens`；曾因此大量误跳过，见 72e4b9143）。
- Codex 0.158 对 API Key 自定义 provider 不拉 `/models`，manifest 上限到不了客户端，所以主要靠“抬高回报用量”触发压缩；计费用量取原始事件，不受影响。
- Codex 对自定义 provider 的压缩请求是携带完整上下文的普通 `/responses` 请求（不是 `isCompactRequest`）。压缩门槛曾与跳过门槛同为 200k，压缩请求必然被 `context_limit` 跳过、落到缓存冷的原路径（2026-09-27 实测 75s、170s）。现压缩门槛为 185k（按 7 天数据估算约 12% 的压缩请求仍会被跳过，180k 约 8%、190k 约 21%、195k 约 42%）：压缩请求在 185k~200k 间仍走 BPS 热缓存；只有单轮从 <185k 直接跳到 ≥200k 才会再出现 `context_limit`。
- `/responses/compact` 端点的请求（`isCompactRequest`）仍走原路径（skip `compact`）。

### 3.5 联网搜索

- 托管 `web_search`（实时）BPS 无法执行 → 原路径（skip `native_tool`，detail `web_search`）。
- 面板开关 `openai_bps_upstream_live_search`：开启后这类请求也走 BPS，省略搜索工具并提示模型搜索不可用。
- 更优方案：Codex 0.157+ 的 standalone web search（`web.run` → `/alpha/search`），搜索不再是 hosted tool，请求可正常走 BPS。

### 3.6 图片

- 内联 `data:` 图片先由附件网关 `bpsImageExternalizer` 上传 R2 转为 HTTPS 链接；未配置或部分失败 → skip `inline_image`（不计熔断）。
- 未导入 ranxi 的本地图片中转与结构化输出校验；`text.format` 为 `json_object`/`json_schema` 的请求 Prepare 报错 → skip `unsupported_request`。

### 3.7 模型与请求头

- 白名单 `bpsUpstreamModels`（归一化后的上游模型名）：`gpt-6-astra`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`。其余（如 `gpt-6-sol`/`gpt-6-luna`）走原路径。
- effort：请求未带时写入回退 effort；BPS 不支持的档位（如 `max`）由 `basispoints.NormalizeEffort` 映射为 `xhigh`，实际值记录在事件 `AppliedEffort`。
- 请求头伪装为 Windows Edge 上的 Excel 插件（`bpsStaticHeaders`），需要 `chatgpt-account-id`；走账号自身代理。

## 4. 配置与管理面板

- settings 表键：`openai_bps_upstream_enabled`、`openai_bps_upstream_account_ids`（JSON 数组）、`openai_bps_upstream_live_search`。
- 管理 API：`/api/v1/admin/bps-upstream`（GET 概览、PUT `/config`、POST `/accounts/:id/reset-breaker`、`/probes` 增删查）。
- 前端：`frontend/src/views/admin/BpsUpstreamView.vue`、`components/admin/bps/BpsProbePanel.vue`（“降智修复”面板）。
- 紧急降级：面板关闭总开关或把账号移出列表，15 秒内生效，无需发版。

## 5. 跳过 / 失败原因速查

跳过（直接原路径，不计熔断）：

| reason | 含义 | 写 ops 日志 |
|---|---|---|
| unsupported_model / not_oauth / compact / image_generation / messages_bridge | 按配置或请求类型必然 | 否（只计内存） |
| client_tool_mapping | 客户端工具映射请求 | 是 |
| native_tool | 实时搜索或图片生成工具（detail 给出具体原因） | 是 |
| breaker_open | 账号熔断中 | 是 |
| inline_image | 图片外链化失败 | 是 |
| unsupported_request | Prepare 失败（结构化输出、item_reference 等） | 是 |
| session_cooldown | 会话 3 分钟冷却中 | 是 |
| context_limit | 会话上下文达到上限 | 是 |

失败（BPS 已发起）：

| reason | 含义 | 计熔断 |
|---|---|---|
| network | 连接失败 / 响应头超过截止 | 是 |
| http_status | 非 2xx 或非 SSE | 是 |
| stream_before_output | 首产出前流错误或超时 | 是（格式类除外） |
| handler_before_output | 处理器在输出前失败 | 是 |
| native_continuation | 已输出后 BPS 失败，同流接续原路径 | 否 |
| context_stall | 上下文过大导致首产出超时 | 否 |

结果：`success` / `fallback` / `skipped` / `error_after_output`。

## 6. 排查手段

- 日志：`ops_system_logs` 中 message 以 `[OpenAI BPS]` 开头；纠正为 `[OpenAI BPS] tool transport correction (account: N): <校验错误>`。
- 内存监控：管理面板概览（重启即清空）。
- 生产只读查询（SQL 首行必须是 `SET default_transaction_read_only=on;`）：

```bash
ssh sub2api-db 'sudo -n docker exec -i sub2api-migration-postgres sh -c "psql -U \"\$POSTGRES_USER\" -d \"\${POSTGRES_DB:-\$POSTGRES_USER}\" -X"' < /tmp/q.sql
```

```sql
SET default_transaction_read_only=on;
-- 近 6 小时 BPS 日志
SELECT created_at, level, left(message, 300)
FROM ops_system_logs
WHERE message LIKE '[OpenAI BPS]%' AND created_at > now() - interval '6 hours'
ORDER BY created_at DESC LIMIT 100;
-- 上下文规模：usage_logs 的 input_tokens 已扣除缓存读与缓存写，要全部加回
SELECT created_at, model, input_tokens + cache_read_tokens + cache_creation_tokens AS ctx, output_tokens
FROM usage_logs WHERE account_id = 69 ORDER BY created_at DESC LIMIT 50;
```

- 应用容器：`ssh sub2api-aws-candidate 'sudo -n docker ps'`（蓝绿 `sub2api-blue` / `sub2api-green` 交替）。
- 单测：`cd backend && go test ./internal/service/basispoints/ && go test ./internal/service/ -run 'BPS|Bps'`。

## 7. 发版流程

1. 在 worktree 分支完成改动并本地测试（本地测试**不代表**授权推送/部署）。
2. 另一任务（Grok/代理池）也会推 `fork/main`：推送前 `git fetch fork && git rebase fork/main`，重新跑测试，并 `gh run list -R Turtle-Li/sub2api --workflow sub2api-production-deploy.yml -L 2` 确认没有进行中的部署。
3. 由仓库所有者执行推送与蓝绿部署：

```bash
git push fork HEAD:main && gh workflow run sub2api-production-deploy.yml -R Turtle-Li/sub2api --ref main -f build_only=false -f deployment_target=aws-candidate
```

## 8. 风险

- **上游策略风险**：BPS 是 Excel 插件私有接口，请求头伪装、`run_officejs` 协议、上下文上限都可能随时变化。缓解：总开关 + 账号列表 + 熔断 + 输出前透明回退。
- **账号风险**：用 Codex OAuth 令牌访问非 Codex 客户端接口，存在被识别/限制的可能；只放少量账号参与。
- **格式错误**：模型可能写错传输格式。当前靠提示词 + 纠正（最多 2 次）+ 失败回退兜底；纠正增加延迟与 BPS 用量。
- **工具扣留延迟**：带工具的流式响应要等 `response.completed` 才下发工具调用；纠正期间客户端只看到已输出的文本。
- **静默卡住**：上下文约 207k 时 BPS 不报错，只能靠首产出超时发现（浪费最多 20s）。依赖压缩提示提前规避。
- **用量回报被改写**：压缩提示会把回给客户端的 input/total 改为 1,050,000；客户端展示会异常，计费不受影响。若 Codex 改变压缩触发逻辑，可能导致反复压缩或不压缩。
- **内存状态**：熔断、冷却、会话上下文、replay 缓存都在进程内，蓝绿切换/重启后清空，多实例间不共享。
- **图片外链**：图片上传 R2 后以 HTTPS 链接给 BPS，涉及外部存储与链接有效期。

## 9. 未解决问题 / 后续方向

1. **纠正成功率未量化**：需要一段较长运行后统计 `tool transport correction` 次数与随后的 success / fallback 比例，决定是否需要更强的提示或 schema 校验。
2. **纠正期间无首产出超时**：纠正请求只受请求 ctx 约束，若 BPS 在纠正时卡住，客户端会等待较久。可考虑给纠正单独加截止。
3. **纠正请求的 token 未计费**：只上报最后一次续跑 usage，前面的失败轮次与纠正轮次消耗未记入 usage_logs。
4. **压缩门槛 185k 的效果待验证（不理想可降到 180k）**：观察 `context_limit` 跳过是否基本消失、BPS 上压缩请求的耗时与摘要质量。
5. **207k 静默上限**：依赖 Codex 压缩；不同客户端（非 Codex、不响应用量提示）仍会遇到一次 context_stall。
6. **JaxsonWang 的 references 式传输**：可评估作为减少格式错误的替代方案。
7. **ranxi 10c0f90d8 的 unknown_tool_repair / tool_schema**：见第 2 节。
8. **状态持久化**：熔断与会话记录可考虑放 Redis，避免发版后短时间内重复踩坑。
9. **用量展示**：压缩提示改写的 usage 会被客户端展示，是否需要只在接近上限时改写（当前即如此）或改用其他信号，待 Codex 版本演进再评估。

## 10. 改动检查清单

- 新移植上游代码 → 更新 `basispoints/NOTICE.md` 与本文第 2 节。
- 新增跳过/失败原因 → 更新常量、`bpsRoutineSkips`、前端 i18n（`frontend/src/i18n/locales/*/admin/bpsUpstream.ts`、`ops.ts`）与本文第 5 节。
- 改上下文相关常量 → 同步检查 `openai_bps_codex_compact.go` 与第 3.4 节，并用生产 usage_logs 验证。
- 用量计算一律用 `bpsUsageContextTokens`。
- 任何失败路径都要确认：输出前 → 回退原路径；输出后 → 接续原路径；是否应计入熔断/冷却。
