# 付费兜底管理、观测与通知

## 任务与依据

Task ID: PAID-FALLBACK-MANAGEMENT-20261022。类型：已授权功能续期。优先级：一期隔离验收后的管理与运维闭环。

基线 `f05263c9f`；实际已上线代码 `ad222a8c4`。一期 G0 七项真实 DeepSeek 矩阵、两把 Key 撤销和原账号指纹检查均通过，详见一期记录。用户此前的条件继续授权已满足。生产策略和测试资源保持关闭。

沿用 [下一阶段计划](PAID_FALLBACK_NEXT_STAGE_20261022.md) N1–N5。根执行者拥有全部实现；独立只读 QA/Review 审核冻结的变更与证据。前端沿用现有 Vue 管理端、语义组件和明暗主题，不新建设计系统或引入依赖。范围不包含硬预算、真实 GPT WS 供应商验收或扩大客户付费白名单。

## T1：接口与架构决定

2026-10-22 用户纠正产品目标：付费账号是独立于普通优先级的末级兜底池，策略按分组选定并覆盖组内所有用户，不设置 API Key 白名单。普通账号全部因额度、登录或上游故障不可用时才接管并产生通知事件；普通账号恢复后，兜底粘性不得阻止回切。此前双白名单及已绑定付费状态持续使用的约定由本段取代。已有请求可完成；无法跨凭据迁移的 previous_response_id 或 WS 状态须明确要求重新建立会话，并在恢复后禁止新的兜底发送。上线保持策略与通知默认关闭，仅在独立测试分组验收，不自动启用客户分组。

参照已核对：上游 `Wei-Shaw/sub2api@f2669c8cf62555cd92389b3f55920e9e6e7c6ff2` 的 `openai_account_scheduler.go` 中 Select、selectBySessionHash、selectLegacyAccountByPreviousResponse，以及 `openai_account_scheduler_test.go` 的 PreviousResponseSticky / EnabledUsesAdvancedPreviousResponseRouting。保留上游凭据状态边界；本项目有意让普通恢复优先于付费亲和，不能把原 response ID 静默交给另一凭据。既有开源许可义务不变。

管理和历史查询合同已确认，可开始 N1–N3。新增持久化与异步通知的边界在实施 N4 前接受独立审查；不让该审查阻塞独立的管理端实现。

### 管理与历史查询

- 策略继续使用既有 GET/PUT `/admin/settings/paid-fallback`，结构为 enabled/group_ids。分组内所有认证用户均可使用兜底。旧 api_key_ids 字段不再参与判定或输出；调用者 Key ID 仅用于鉴权、既有用量记录和审计归因。
- 增加独立的立即关闭操作，服务端保留有效白名单；读取失败时仍能尝试关闭。配置本身损坏时允许重置为关闭的空配置，数据库依赖错误仍返回错误且付费准入失败关闭。
- 标记保持 `accounts.extra.paid_fallback`。缺省与 false 为普通，任何其他现存 JSON 值均隔离。前端复用同一分类；编辑其他字段不改标记。创建/编辑只在显式选择时写入；批量为保持/兜底/普通三态。取消标记前说明会恢复普通池资格。
- 用户补充的账号管理交互：账号名旁显示琥珀色“付费兜底”标记，优先级单元格下提供普通/兜底快速选择；创建和编辑表单把调度类型与优先级并排，手机上下排列。列表快速保存使用 PUT `/admin/accounts/:id/paid-fallback` 的 enabled 字段，只合并 extra.paid_fallback，保留额度、隐私及传输设置；提交期间禁用，失败恢复原值并显示错误。平台不支持新兜底时隐藏入口，旧隔离标记仍可显式解除。普通优先于兜底，数值优先级仍决定同类池内顺序。
- 创建、修改与批量操作的服务端边界验证新设置为兜底的账号使用 OpenAI gateway；复用平台清单判断，不维护第二份平台列表。旧异常标记在无关编辑中保留。
- 管理员用量新增可选 paid_fallback=true/false 筛选；空值沿用全部。列表、分页、统计和导出一致，历史归因取 usage_logs 快照而非当前账号 Extra。
- 费用复用已记录 `COALESCE(account_stats_cost,total_cost) * COALESCE(account_rate_multiplier,1)`，展示 USD 账号成本估算，与钱包扣费分开。失败无用量的费用未知，不能显示为零或称供应商账单。
- Ops 普通可用数及其比例排除付费账号，另列付费总量和可用数；账号条目保留自身健康状态并明确标记类别。

### 新增尝试记录与通知边界

新增独立空表与 SQL 仓储保存全部兜底模型发送尝试；channel-monitor 是聚合表，usage_logs 不是全部尝试账本。迁移 264 创建审计表，264a 并发创建 paid_fallback 历史用量时间索引，避免每日成本统计扫描所有普通请求；不修改财务数据、退款/支付事故存储或现有用量生成算法。

1. 每次网络尝试使用服务端 UUID，包含实际账号、认证 Key ID、有效分组、请求模型、传输类别、开始及结果时间。无请求正文、凭据、用户提示词或原始上游错误。选号、排队和 WS 准入是不同事件，不能计为已发送。
2. HTTP 在现有 `doUpstreamRequest` 的最终准入之后、调用网络客户端之前落盘；发送前拒绝没有网络尝试。初始持久化失败则不发起新的付费网络请求，沿用依赖不可用时失败关闭的语义。写入有独立短超时，只影响付费上下文。
3. HTTP 结果只代表响应头或传输错误，不等于流完成或供应商计费。进程在请求中崩溃时保留未定结果；不自动重放供应商调用。实际费用来自独立历史用量查询，不以尝试数乘费用，也不因一个请求多次尝试重复累加用量。
4. WS 在实际上游 response.create 写入边界记录轮次；握手、准入、帧写入与模型完成分开标注。复用帧连接包装及现有原始模型快照，不把已经映射的模型当客户请求模型，不改连接绑定或迁移策略。HTTP bridge 复用 HTTP 尝试，不能再重复计作原生 WS 调用。
5. 事故范围为有效 group_id + 请求 model，不能用原生账号平台隔断 WB→Cline→WB 的同一事故。首次真实尝试触发 open，持续调用可按间隔提醒；恢复依赖之后观察到普通成功用量与静默确认窗口，不能以“暂时没有请求”宣称恢复。新付费尝试与恢复事务使用同一范围锁/版本，避免并发误恢复。
6. 通知队列和带 fencing token 的租约落盘，唯一约束防止多实例重复建事件。失败可重试，进程重启可恢复；明确外部邮件在不确定发送结果下可能至少一次交付，不承诺绝对一次。通知失败不得阻塞或重新触发模型调用。
7. 复用现有 Ops 邮件配置、收件人、限流和 NotificationEmailService 发送边界；不扩展收件人，不引入凭据，不自动启用。通知设置与一期策略分开保存，避免旧脚本替换策略时清空通知配置。默认关闭，持续提醒默认 30 分钟、恢复确认窗口默认 60 秒；可选日费用软阈值为 USD 且留空，按 UTC 日、分组和模型统计已记录快照，未知失败费用单列。软阈值只提醒，绝不隐式硬熔断。
8. 后台 worker 遵守 runtimegate.SharedWorkAllowed，使用数据库权威租约与幂等事件。Redis 仅可用于既有辅助限流，不承担审计真相；Redis 故障不改变付费准入。

### 独立架构审查后的执行修正

2026-10-22，独立 CAO reviewer 审查冻结提案（SHA-256 `868087640d1141c6443e1cd9a6077481dce293d80472d45925b676eda9ead9c8`），结果 CHANGES_REQUIRED；报告 `/tmp/paid-fallback-management-architecture-review-20261022.md`。以下修正覆盖初稿对应语义，最终代码审查再次复核。

- WS 逐次发送复核：ctx_pool 在 sendAndRelay 内覆盖 skipBeforeTurn 同轮重试；passthrough 包装全部 response.create 写入；HTTP bridge 仅记录 HTTP。写入错误可能已发出部分字节，记 unknown。
- public_model、forward_model、upstream_model 独立。处理器和每轮快照传入原始模型，不能从 permit 调度模型推断。HTTP 传入实际 accountID，与 permit 不符则拒绝。
- 热路径只做一条限时尝试 INSERT，不修改事故或通知表。结果异步限时更新；超出最长上游等待的未定尝试由 worker 标 unknown，不重放。辅助图片下载不算模型调用。
- 事故由 worker 按数据库时钟处理；恢复后晚提交的新尝试开启下一代。独立表以 claim token、租约和 CAS 保证通知正确性，不复用 Redis 权威 singleton helper。Redis 故障不改变准入或审计。
- attempt UUID 唯一；inbound_request_id 使用服务器 ClientRequestID。HTTP 通过 client:UUID + Key 关联用量，只累加独立 usage 行；无法关联的 WS 费用保留未知。公共模型查询使用 COALESCE(NULLIF(requested_model,''),model)。
- 邮件仅走 NotificationEmailService，发送前再次检查 runtimegate 和 claim。限流延迟重试；SMTP 成功但后续记账失败不能立即重发。发送结果不确定时允许至少一次交付。默认关闭，不向实际收件人发送测试。
- 验证纳入报告 16 类边界：逐次 WS 重试、跨账号上下文、复用客户端 ID、未知费用、UTC 跨日、多实例 claim 和恢复竞争。

### 证据与兼容性

已检查既有 SnapshotService、HTTP 最终发送、WS BeforeTurn/实际帧写入、Ops 告警、NotificationEmailService、独立 Feishu 支付事故仓储及 wire 生命周期。支付事故是持久化租约的参照，不能复用它的业务表或收件渠道。

费用参照：`docs/operations/UPSTREAM_V0210_20260930.md` 固定 upstream `2f3fed2fdb0787141294cec81487a5df30426f7f` 的 account_stats_pricing 源码及测试已检查；本 fork 的 USD 账号成本与结算币种差异仍优先。沿用现有已记录快照公式，不重新实现上游定价。保留 LGPL-3.0 声明和既有许可文件。多模型和 Cline 固定 pin 均不改变。

## 修改前记录与验证

修改目标：管理员能安全配置和查看付费兜底，真实尝试及故障能审计和通知。

修改文件：admin paid_fallback handler/service/routes；账号表单、列表、批量及通用 Extra 验证；usagestats filter/repository 与 usage 组件；Ops availability DTO/组件；新独立尝试/通知服务、仓储、空表迁移及 wire 生命周期。每条路径按需读取，不进行无关重构。

预计影响：管理员 API 与 UI、付费网络边界及后台通知。普通请求不新增同步审计写入；不改客户扣费、模型 pin、账号凭据、原分组、利润门或并发等待规则。

测试：标记 null/异常类型分类与省略保留；白名单读取/保存/失败/立即关闭；批量保持/付费/普通及不兼容平台；历史混合记录过滤/分页/聚合与改标记后的历史不变；真实数据库中多实例幂等、崩溃未知、发送前拒绝、通知重试、恢复竞争及 UTC 日阈值；WS 本地真实帧测试和 HTTP bridge 防重复。真实浏览器验证管理流程、明暗主题及桌面/手机；独立 QA/Review 后运行同源码 CI/安全扫描、标准蓝绿和隔离 DeepSeek 重验。

回退：先关闭策略和通知，不清除账号标记或历史记录；兼容空表保留。若回退至不理解标记的旧程序，先停用并解绑所有付费账号。任何范围外付费、健康普通池付费、关闭后新发送或财务口径变化均停止发布/扩大。

knowledge_candidate: no; project-specific feature and rollout contract.

## N1–N5 最终实现合同

- HTTP 模型调用通过显式 `doOpenAIModelUpstream` 标记；辅助下载、计数等请求不创建模型尝试，但仍复核付费账号 ID 与实时策略。Chat Completions、Responses、Messages、WS 处理器均传递原始 public model。发送前 750ms 内必须持久化成功，插入结果不确定也尝试标为 `not_sent`；任何结果写入失败都不重放供应商请求。
- HTTP 仅记录收到响应头，原生 WS 仅记录帧写入。无法确定是否发送的结果为 unknown。未定记录的清理期限至少 900 秒，并扩展至配置的最长响应头/WS 写入超时加 120 秒；长流读取不等待生成完成再更新响应头结果。
- WS（含 HTTP bridge）的 forward_model 留空，避免把连接首轮调度模型错误用于后续轮次。逐轮 public_model 与实际 payload upstream_model 独立记录。事件费用关联仅限 HTTP；WS/bridge 尝试费用保留未知。日阈值仍按独立的历史付费用量计算，包括已记录的 WS 用量，不能据此称逐尝试费用已知。
- worker 每 30 秒运行，单次 20 秒预算，按数据库时钟处理最多 100 个待归属范围和 100 个开放事件。通知关闭时仍保留发送审计、处理恢复，但不生成通知。汇总仅在准备通知时计算，用尝试 ID 关联已有唯一请求用量；管理报告先限制最近 30 尝试、20 事件，再聚合。
- 邮件使用既有 NotificationEmailService、Ops 收件人/最低级别/恢复偏好和运行态门。每小时上限复用 Ops 数值，兜底邮件由数据库单独计数，0 为不限；不与既有进程内普通 Ops 告警计数合并。租约 2 分钟且有随机 claim token；配置撤销、恢复后取消和超过 24 小时的队列记录标 suppressed，不冒充已投递、不占已投递限额。SMTP 失败指数退避至最多 1 小时；SMTP 已成功、后续记账失败则尽量完成队列，保留外部交付至少一次语义。
- 新表没有自动删除审计历史的保留期。当前仅隔离低流量测试，扩大范围前需结合真实表量核验查询计划与存储增长。未取得供应商账单的费用仍是已有快照估算；软阈值不提供硬预算或额度熔断。

2026-10-22 独立边界审查报告 `/tmp/paid-fallback-management-boundary-review-20261022.md`（CHANGES_REQUIRED）已据实修复：缺失 Chat Completions public model、辅助请求误计、孤儿记录 24 小时延迟、插入结果不确定以及重复用量扫描。追加修复了通知抑制与投递状态、过期邮件取消和 0=不限语义。该审查不是最终验收，最终冻结源码复审与线上记录另附。

本地已观察到：真实 PostgreSQL 的尝试/费用/UTC 阈值/多实例 claim/恢复竞争用例通过；HTTP 入口与发送边界竞态测试通过；实际 Vue 页面连接全量本地 API fixture，策略保存/白名单保留/立即关闭/读取失败后关闭/数字阈值及清空均通过，桌面 1440 与手机 390 明暗主题无页面横向溢出。截图和驱动脚本在 `.omo/evidence/paid-fallback/`。API fixture 的其他设置默认值不足会产生无关 toast，未将其作为整个设置页验收。

2026-10-22 已通过标准蓝绿发布运行版本 `8fecb5956`，候选实例的实际 GPT 请求先成功，再切换流量。随后 DS 隔离验收八个场景全部通过，包括同分组第二把密钥、普通恢复覆盖付费粘性、流式兜底和关闭/分组外拒绝；三个付费尝试归入同一已恢复事件。测试账号、分组和密钥已禁用，两把密钥均返回401，策略与通知保持关闭。发布及清理证据见 [management release](../operations/PAID_FALLBACK_MANAGEMENT_RELEASE_20261022.md)。
