# 支付弹窗、本地取消与迟到付款退款（2026-09-22）

状态：本地实现、测试及独立 QA/Review 完成（IMPLEMENTATION_READY）；未提交、推送或部署。任务 `PAYMENT-20260922`，分支 `codex/payment-local-cancel`，基线 `599757cf28a49bbf2fc6f7d5ff9725f75916f9c9`。

## 用户确认的行为

- 支付宝 page.pay 使用独立收银台，不嵌入 iframe，不把网页链接伪装成原生二维码；有效原生二维码仍可正常显示。
- 创建遇到已有待付订单时，读取登录用户原订单并弹窗，展示订单号、商品、金额、支付方式和有效期，提供“继续支付”和“取消订单”。继续支付通过服务端恢复原单，不能仅凭浏览器旧快照付款。
- 取消以本地数据库提交为准。提交成功立即解除待付订单限制，外部关单进入持久后台任务。请求失败/结果不明不能假报取消成功，也不能同时继续展示可付款入口。
- 取消先成功的订单永不重新履约；随后真实到账只走原单全额退款，不扣回未曾发放的余额、订阅或重置卡权益。付款先成功则取消返回 `already_paid`，前端等待履约结果，不能再次显示付款或取消按钮。

本决策替代 September 19 用户取消必须等待支付平台确认才改变本地状态的规则。自然过期、签名验证、金额/范围校验和独立人工复核保护仍保留。

## 状态与并发契约

| 先发生的事实 | 本地决策与后续行为 |
| --- | --- |
| PENDING 取消提交 | 同事务写 CANCELLED、释放优惠券、写唯一关单任务；返回前不调用支付平台 |
| 可信付款先提交 | 保持 PAID/履约事实；取消不得覆盖 |
| CANCELLED 后可信付款 | 保持业务 CANCELLED，另存真实付款事实及唯一无权益扣回退款尝试 |
| 退款已发起后出现独立异常 | 保留人工复核；精确关联的真实退款成功仍必须持久化，不得因调度锁被撤销而丢失 |
| 同一退款出现矛盾终态 | 保留原终态及完整归一化冲突证据，人工复核；不得采用最后通知覆盖前一终态 |

取消、可信付款和优惠券账本竞争同一订单行锁。网络调用在事务外。后台任务和统一退款恢复均使用数据库租约、精确 `claimed_at` 代次和稳定退款标识；旧 Worker 不能完成或重试新代次任务。直接支付通道明确使用稳定商户退款号，不能在重试时生成新退款号；不能证明幂等或可靠查询的通道在网络操作前进入人工复核。

取消期间丢失创建响应的统一支付订单，按原产品订单号、历史金额/类型/方式及作用域查询并绑定原中央 UUID，不创建替代支付单。已取消单的可信查无结果，仅在原截止时间及签名时钟安全窗口过去、无付款和活跃 dispatch 等保守条件满足时结束后台关单任务，业务状态仍为 CANCELLED；网络错误和提前查无结果继续重试。

`PAID_AFTER_CLOSE` 查询自带中央人工复核标记。先验证并记录该专用资金事实，再由中央退款准入区分正常迟到资金和独立异常；不能被通用 review 判断提前截断恢复，也不能泛化为普通 PAID 履约。任务上下文结束后，仅用有时限的清理上下文保存重试/释放调度状态，防止最老慢请求永远占据队首。

## 实现入口

- `frontend/src/views/user/PaymentView.vue`：已有订单信息、认证恢复、取消与失败重试。
- `frontend/src/components/payment/PaymentStatusPanel.vue`：独立收银台、权威取消结果、已付款中间态和终态清理。
- `frontend/src/views/user/UserOrdersView.vue`：订单列表取消结果与本地恢复。
- `backend/internal/service/payment_local_cancellation.go` / `payment_local_cancellation_worker.go`：本地事务、后台关单、直接退款及成功证据。
- `payment_fulfillment.go`、`payment_unified_refund.go`、`payment_unified_webhook.go`、`payment_unified_missing_order.go`：付款分流、无权益退款、可信回调和缺绑定恢复。
- `payment_refund_reconciliation.go` 与 repository 同名 store：统一退款精确代次恢复及回滚检查。

迁移 `256_payment_local_cancellation_recovery.sql` 新增持久关单/直接退款任务；统一支付复用 `cancel_late_payment` 退款尝试。仅该类别允许零权益扣回金额，普通退款的正数约束保留。

中央服务配套变更为迁移 23–24、`contracts/sub2-late-after-close-refund.md`、专项集成测试及其清理 helper。退款请求不增加 JSON 字段，显式意图使用现有 `reason_code=service_not_delivered`、精确原付款全额及稳定退款号；`sub2_cancel_` 前缀只是标识，不构成授权。

## 固定参考与快照

上游 Sub2 固定参考 `bdb42e22f81fcb633ff0a060961211dd2bcb515b`，已检查支付履约及原支付宝弹窗源码。新的本地取消/迟到付款退款是用户明确要求的本地差异，保留 LGPL-3.0 义务。

原中央退款契约已按 `UNIFIED_PAYMENT_INTEGRATION.md` 的固定 blob 检查，包括 `refunds.go` 的 `3db6468b025c8e6b60dde5af0725f5aed36694a3`、测试 `08332f6c61705a99454c75a171d4ed2adef1ce3c` 和 SDK types `9a4829f36b202a9904cfc38b752d31c7f1c63063`。

中央最终固定内容：

| 文件 | Git blob |
| --- | --- |
| 迁移 23 | `ad5ec3e91b6f38e514c1774cf27af995ac2a8150` |
| 迁移 24 | `4f37b47abcdda4d05c940cfbcf362be780b1507d` |
| late PostgreSQL 集成测试 | `eed2d26c4a1b43449a4d8052e0775550d1e57a8b` |
| 退款测试清理 helper 所在文件 | `818a0f9bae3c6fba629ed200065a3e5002180904` |
| late 退款契约 | `a0d456f7eaf23ca9d4ce4306d01ba7e48304424d` |

迁移 24 SHA-256：`b8510c45ddf89e4a0a9a9141c58479da6f9942eb488ea4698678d52049ff2aa2`。它把订单锁、首次提交资格检查和不可变发起授权同事务保存；网络在锁外，随后真实成功按原 PAYIN 入账，独立异常标记保持。

## 验证记录

| 范围 | 结果 |
| --- | --- |
| 前端支付专项 | 139 项通过；类型、i18n 3 项、改动文件 ESLint、生产构建通过 |
| 浏览器真实组件 + 本地 mock | 桌面与 390×844 手机深色检查通过；订单明细/继续/取消、取消后立即新建、503 重试及付款中间态均验证；iframe 数量 0 |
| Sub2 全量 service unit | `go test -p 2 -tags unit ./internal/service -count=1 -timeout=10m` 通过，195.723s |
| Provider / UnifiedPay | 两个包全部 unit 通过 |
| Repository / Routes / Migrations | 退款恢复专项、全部 routes unit、迁移 unit 通过 |
| 真实 PostgreSQL 取消与退款 | `go test -p 1 -tags integration ./internal/service -run '^TestLocalCancellationPostgres' -count=1 -v` 通过，33.695s |
| 真实 PostgreSQL 共享退款租约 | 同 Worker 重领后旧 Complete/Retry 不改新任务，覆盖 balance/subscription/cancel_late_payment，15.551s 通过 |
| 中央独立 QA / Review | PostgreSQL 17.11 普通退款、late 专项、frozen-v8 升级及审查者独立 barrier 重放通过，QA_PASS / REVIEW_PASS（23+24 成套） |
| 前端独立 Review | REVIEW_PASS |
| Sub2 最终独立 QA / Review | QA_PASS / REVIEW_PASS；审查者重跑 PostgreSQL 专项通过，29.038s；无剩余 P1/P2 |

PostgreSQL 专项包括：取消/付款先后争用订单行锁、优惠券释放、新单准入、原单唯一退款、签名成功通知和重复回调、直接退款旧代次拒绝、缺 UUID 找回、查无结果安全边界、`PAID_AFTER_CLOSE` 查询恢复、异常撤销锁后的成功资金事实留存，以及慢请求取消后的持久退避与后续任务推进。

取消迟到付款的统一退款闭环实际发送本地类型化 POST（稳定幂等键、8000 分、service_not_delivered），再处理签名 SUCCEEDED 及重放，证明业务 CANCELLED 保持、仅一笔成功尝试/审计、用户原余额和权益来源不变。直接退款留痕永久测试修复前实际失败：成功后 provider_refund_id 为空；修复后纳入上述通过套件。原取消契约测试也用基线 lifecycle Go overlay 实际呈现红/绿差异。

中央曾发现“先授权→第二笔到账→原退款真实成功被拒绝”的账务缺口；迁移 24 后，审查者独立重放确认仅记录原 PAYIN 的一笔退款/出账/事件并保留人工复核。默认中央全仓 Go 测试通过；数据库集成按具名隔离场景验证，不将一个 DSN 套用于具有不同历史 schema 前提的全部包。

没有真实支付宝/微信付款或退款，没有生产数据库或服务器操作。浏览器付款窗口被本地夹具截获；测试进程/容器已按各测试清理，临时假登录入口已移出工作区。不能把本地验证表述为线上验收。

## 发布和降级边界

网站后端与中央服务必须配套发布（中央 23+24，Sub2 256–257）。迁移 257 的数据库保护在候选启动、切流前生效，阻止旧 Sub2 将 CANCELLED 恢复履约；旧进程按 canonical 蓝绿流程自然排空。持久关单、迟到退款及人工复核尚未收敛时，保留既有 rollback-readiness 栅栏，不删除金融表或审计降级。继续遵守项目蓝绿维护锁、制品验证和备份流程；本文不授权部署。

迁移 23 本轮未单独部署。若它被单独应用并产生在途 late PROCESSING/UNKNOWN 退款，需先排空/对账再应用 24，不能猜测或回填无法证明的历史发起顺序。

知识候选：调度锁决定谁可执行工作，不能用其失效撤销已经发生且精确关联的资金事实；与相互矛盾退款终态的人工复核规则必须分开。仅作为本项目验证记录，不自动进行跨项目知识推广。

最终复审记录：2026-09-22 07:48:54Z，后端审查 manifest `f6afb42301ed2ebe0041efb869792f41fa1360c7dd97a735ac4104235eb26eb1`；审查后关键生产源码 hash 再次匹配，全部改动 Go 文件 gofmt 检查及差异检查通过。最终全代码清单（含前端/后端/测试）SHA-256 `fcb63384a81e62de6aef53c9ea2686748a4113e43cef782ff6bb94a00c6cabcd`。允许进入后续发布准备不等于已授权或已完成部署。

发布阶段新增迁移 257 及旧写入者兼容测试，详细验证与最终上线状态以 `PAYMENT_LOCAL_CANCELLATION_RELEASE_20260922.md` 为准；原业务源码验证记录仍绑定上文快照。
