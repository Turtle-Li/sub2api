# 付费兜底：下一阶段与次日验收

## 当前状态与授权

用户于 2026-10-22 要求：“如果测试没有问题，你继续执行下一阶段，我睡觉去了，明早我测试一下。”这是通过测试后的继续授权。真实 Cline 兜底验收通过后可继续执行；尚未通过时，仅完成不依赖该结果的调查和准备。

当前状态为 `READY`，G0 已在缓存修复 `ad222a8c47c09514ed91d81594a2a7b328ee2309` 上通过。标准蓝绿发布 `39731109696` 成功；新代后台接管、新缓存标记及健康均已核对。原生 Vault 授权后的同一固定消费者退出 0，七项矩阵及清理全部通过；实际两次 Cline 付费发送与 200 结果、历史用量 904203/904226 均相符。策略及测试资源已关闭，两把 Key 均验证 401，原账号指纹不变，测试组外付费用量为 0。用户的条件继续授权现已生效，可进入 T1。

权威约束和发布证据见 [一期记录](PAID_FALLBACK_PHASE1_20261022.md)。本文是后续执行准备，不代表下一阶段产品代码已经实现或真实验收已经通过。

## G0：进入下一阶段前的真实验收

固定使用专用用户 81、独占 DeepSeek 分组 33、WB 普通副本 237、Cline 付费副本 238、白名单 Key 131 和对照 Key 132。原账号 WB 232、Cline 221 不注入故障、不修改配置。测试结束关闭全局策略、停用测试资源并撤销两把 Key。

| 用例 | 必须观察到的结果 |
| --- | --- |
| 关闭策略，WB 健康，非流式 | WB 237 成功，付费用量标记为 false |
| 开启策略，WB 健康，流式 | WB 237 成功，付费用量标记为 false |
| 仅令 WB 副本发生真实 401，白名单 Key，非流式 | Cline 238 成功，付费用量标记为 true |
| 保持 WB 故障，换对照 Key 132 | 不调用付费账号，不产生付费用量 |
| 保持 WB 故障，关闭策略 | 不调用付费账号 |
| 开启策略，WB 故障，白名单 Key，流式 | Cline 238 成功，收到有效内容及流结束，用量标记为 true |
| 恢复 WB 副本凭据与运行状态 | 新请求回到 WB 237，付费用量标记为 false |
| 清理与隔离 | 两把测试 Key 返回 401；测试范围外付费调用/用量为 0；两个原账号指纹不变 |

每项核对响应、实际账号、分组、Key 和用量；真实发送事件与用量不是同一计数。费用仅按既有快照估算，供应商未返回用量的失败调用费用未知。Key 的消费额度不是上游供应商硬支出上限，测试仍须少量、串行、短输出。

若 Cline 失败，先区分供应商能力、固定 upstream pin、模型别名及兜底准入失败；不能为通过测试解除白名单、放宽故障证据或扩大客户分组。Cline 副本目前使用固定模型别名 `cline-pass/deepseek-v4.1-flash-1017`，供应商成功以真实响应为准。

### 明早恢复测试

在本机任务 checkout 中，确认 `.omo/evidence/paid-fallback/smoke-request.json` 和 `live-test.py` 仍属于本任务；前者权限为 0600，后者为 0700。请求文件只包含 Vault 条目、字段到环境变量映射和固定消费者信息，不包含 Key。

消费者 SHA-256：`78e4a907d39cf64f2b18f0dd1536cafbfea3cc7e1541c9d08da58f3fc493997d`。校验请求中的哈希与实际文件一致后，通过既有受审入口运行：

```sh
infra-vault unlock-run -- run-item-env-file "$PWD/.omo/evidence/paid-fallback/smoke-request.json"
```

需要所有者在本机完成有意的 Vault / Touch ID 授权；不需要在聊天里提供密码或 Key。无授权时不更换取密路径。若仍无弹窗，保留失败结果并继续处理本机 Vault 授权问题。

消费者在 `finally` 中撤销测试资源并验证 Key 返回 401。进程中断时，可从同一 checkout 执行不取密的清理：

```sh
python3 .omo/evidence/paid-fallback/live-test.py cleanup
```

无 Vault 注入的清理可关闭资源，但不能代替携带两把 Key 的 HTTP 401 校验。成功运行后核对 Caddy 当前源提交、实际付费发送事件、用量和原账号指纹，将 G0 各项结果写回一期记录及私有 Registry，再删除本次非秘密注入请求文件。历史用量和账号付费标记保留。

## 下一阶段目标与验收编号

沿用原设计中管理端、Ops 统计及通知的方向，按可单独验收的结果分批推进；保持一期故障准入、客户计费和 HTTP/WS 共用策略不变。

- N1：管理员可在现有设置页查看和编辑全局开关、分组与 Key 双白名单。未保存、保存失败、空白名单及读取失败的状态清晰；关闭入口始终可用。页面不展示 Key 明文。
- N2：账号列表显示付费兜底标记，编辑入口可显式修改；批量编辑具有“保持 / 设为兜底 / 设为普通”三态。省略字段保持原值，不得因编辑其他字段解除隔离。取消标记会使账号重新具备普通池资格，需清楚说明后果。
- N3：管理员用量显示历史 `paid_fallback` 快照，支持同口径筛选与成本估算；账号后来取消标记不改变历史归因。普通/兜底容量分开显示，原普通可用性口径不计入付费账号。
- N4：记录真实付费尝试及结果，区分选择、排队、实际发送和已知用量。首次调用、持续故障、恢复及可配置费用软阈值可通知；按分组与模型隔离事件，通知失败可观察，不能阻塞模型响应。
- N5：通知和费用展示复用现有发送与成本快照口径。金额阈值未配置时不自行填写；软阈值默认只告警，关闭策略停止新的付费发送。硬预算封顶另行确定并发预留及超额语义，不随本阶段隐式开启。

## 执行图与代码入口

所有写入由主执行者串行完成；仅独立只读调查或独立 QA/Review 可并行。G0 → T1 → T2；T2 → T3、T4；T3 + T4 → T5。T3、T4 是逻辑上独立的结果，不授权两个写入者同时修改同一 checkout。

| 任务 / 依赖 | 输入与允许改动位置 | 产出 / 验收 | 验证、失败与回退 |
| --- | --- | --- | --- |
| T1 / G0 | 一期合同；`backend/internal/handler/admin/paid_fallback.go`、`backend/internal/service/paid_fallback.go`；现有账号/用量/Ops 契约 | 冻结 N1–N5 对应 API 与 DTO；确认哪些现有接口可复用；持久化尝试事件方案需先完成边界审查 | 不修改调度规则；成本、事件语义或模型映射有冲突时先解决，重新核对固定上游参照 |
| T2 / T1 | `frontend/src/api/admin/{accounts,settings}.ts`、`frontend/src/types/index.ts`、`frontend/src/views/admin/{AccountsView,SettingsView}.vue`、账号编辑/批量组件及中英文语言文件；必要的既有 admin API | N1–N2；设置卡片、账号标记和安全批量操作 | 组件与接口边界测试；真实浏览器验证读取/保存/失败/关闭与批量保持；先默认关闭上线。回退 UI 不清除后端标记 |
| T3 / T2 | `backend/internal/pkg/usagestats/usage_log_types.go`、`backend/internal/repository/usage_log_repo_{query,stats}.go`、`backend/internal/handler/admin/usage_handler.go`；`frontend/src/api/admin/usage.ts`、`frontend/src/views/admin/UsageView.vue`、现有 Ops 组件 | N3；后端筛选与聚合同口径，前端标记/费用/容量 | 真实数据库覆盖混合普通/付费记录、分页、时间边界、账号改标记；检查查询计划，避免无界扫描或直接在线建大索引。回退保留迁移与历史数据 |
| T4 / T2 | `backend/internal/service/paid_fallback_attempt.go`；现有 Ops/通知服务及仓储；如需新增表使用届时下一可用迁移编号 | N4–N5；有幂等标识的真实尝试事件、异步通知与软阈值；不把成功用量表冒充全部尝试账本 | 覆盖发送前拒绝不计调用、实际失败仍留痕、多实例去重、重启、通知重试、Redis/数据库失败、恢复与新调用竞争。通知失效不得扩大付费准入；回退停止通知，保留事件 |
| T5 / T3 + T4 | 冻结候选、上述证据、项目标准蓝绿流程 | 独立 Review/QA、同源码 CI/安全扫描、隔离 DeepSeek 重验及发布记录 | 任一范围外付费、普通健康却付费、关闭后新发送或客户计费变化均不发布/停止扩大。优先关闭策略；兼容回退保留标记及用量，旧二进制前先停用并解绑付费账号 |

通知首先复用现有邮件发送边界（`ops_alert_evaluator_service.go` / `NotificationEmailSendInput`），不引入新的凭据保存方式。现有 `FeishuPaymentTextSender` 是可复用发送器，但不因它存在就扩大收件人或发送渠道；通知方案冻结后再选择已配置渠道，并按任务授权执行。

### 适用 Gate 与失效条件

`applicable_gates`：G0 真实 DeepSeek 验收；涉及事件持久化的边界审查；独立代码审查；数据库/接口/组件与真实浏览器 QA；同源码 CI 和安全扫描；标准生产发布及上线后隔离烟测。浏览器验收在 N1–N3 的产品界面上执行，不以截图或单元测试替代实际操作。

调度/转发准入或供应商映射发生变化，G0 证据失效；成本快照或查询发生变化，N3 证据失效；实际发送钩子或事件幂等键变化，N4–N5 及通知并发证据失效；候选源码变化，重新核对对应检查与部署源提交。

停止条件：G0（现已通过）未满足时不开始 T1 之后的产品实现；下一阶段只有 N1–N5 均有证据、适用 Gate 通过、隔离线上验收完成后才算完成。DeepSeek HTTP 验收不代表真实 GPT WS 建连或续链已验收；扩大到 GPT WS 前仍需专用账号场景。

```yaml
knowledge_candidate: no
knowledge_candidate_reason: project-specific conditional rollout and acceptance handoff
candidate_type: not-applicable
origin_repo: Turtle-Li/sub2api
origin_path: docs/features/PAID_FALLBACK_NEXT_STAGE_20261022.md
origin_revision: 6ac894ae123ec0e294d3650fd0d16d43a14a1c76
source_change_set: paid-fallback-phase1-20261022
classification_suggestion: not-applicable
```
