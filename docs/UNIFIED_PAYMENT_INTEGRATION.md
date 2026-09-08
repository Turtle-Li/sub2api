# Sub2 × 统一支付服务接入

## 责任边界

Sub2 创建并持有业务订单、用户余额/订阅和履约状态。统一支付服务持有支付订单与可信资金
状态，接收支付宝/微信异步通知，再把签名事件投递给 Sub2。浏览器返回不作为支付成功
依据，也不触发发货。

浏览器始终返回 Sub2 自己管理的固定页面：

```text
https://www.turtleligpt.com/payment/result
```

该页面使用发起支付前已写入浏览器的短期恢复快照，继续轮询 Sub2 本地订单。统一支付服务
不决定产品页面，也不会向返回 URL 附加业务订单参数。

## 沙箱数据流

1. Sub2 先创建本地 `PENDING` 订单，并生成唯一 `out_trade_no`。
2. Sub2 后端用 `pay-v1` + Ed25519 调用统一支付服务的 `POST /v1/payment-orders`；金额只以整数分发送。
3. 前端打开返回的 `checkout_url`；微信 Native 订单同时返回 `checkout_code_url`，由 Sub2
   作为二维码内容展示。
4. 支付宝/微信异步通知统一支付服务；统一支付服务验签并落库。
5. 统一支付服务向 Sub2 投递 `POST /api/v1/payment/webhook/unified`。
6. Sub2 验证原始正文签名、事件时间窗、环境/组织/产品/应用作用域、支付订单 ID、业务订单号和整数分金额。
7. PostgreSQL inbox 以 `event_id` 和 `(payment_order_id, sequence)` 全副本去重，并用每个支付单的
   `max_processed_sequence` 与活动处理租约阻止旧支付事件或并发执行；通过后，Sub2 调用原有幂等发货逻辑。
   退款终态允许较小 sequence 进入对应退款尝试的幂等处理，游标仍只增不减，避免另一笔退款的
   较新事件掩盖尚未处理的资金事实。

`payment.order.paid_after_close` 只记录异常证据，不会发货。关闭状态未知时本地订单保持待确认，
不会猜测为未支付。回调原文不写数据库或日志，inbox 只保存白名单标识和 SHA-256。

当前固定公网地址：

- 统一支付服务 API：`https://pay.totools.cn`
- 支付宝沙箱异步通知：`https://pay.totools.cn/channel/v1/alipay/notify/alipay-shared-sandbox`
- 统一支付 → Sub2 Webhook：`https://api.turtleligpt.com/api/v1/payment/webhook/unified`
- 浏览器返回页：`https://www.turtleligpt.com/payment/result`

## 契约来源与固定快照

本适配器以同一工作区的 `统一支付服务` 项目为权威契约源；检查时该仓库基线为
`6f26137586132f5fcf5c0cc96931fc78461a94eb`。由于 SDK 当前仍是源项目工作树材料，以下
Git blob ID 作为本次接入的可复现固定快照：

- `sdk/go/payclient/client.go`：`3dd14896ff9d06e2e5cecfea0cca9210aac47e24`
- `sdk/go/payclient/types.go`：`9a4829f36b202a9904cfc38b752d31c7f1c63063`
- `sdk/go/payclient/webhook.go`：`b7e32459b4298eea72a0cd054ff409d614be05a9`
- `sdk/go/payclient/strictjson.go`：`52874ff1396066b419eb0f2e0cfdae338085f86c`
- `sdk/go/payclient/client_test.go`：`92fc86ca1d762fc84022f4eec930d8c3819f4f8a`
- `sdk/go/payclient/webhook_test.go`：`57f8f467ec97600133dd5b94228ae94af22f7216`
- `contracts/webhook-event.schema.json`：`1e310eeb5a072ca16e2a5916f86b001238d2675a`
- `contracts/openapi.yaml`：`0bcfa34ee58bcd8f346a4bac86db9e2d89829c48`
- `internal/service/refunds.go`：`3db6468b025c8e6b60dde5af0725f5aed36694a3`
- `internal/service/refunds_test.go`：`08332f6c61705a99454c75a171d4ed2adef1ce3c`
- `cmd/payment-vault-agent/main.go`：`8efe1e50f728eaf91c423904ee50f5e8c4ab4b1e`
- `cmd/payment-vault-agent/main_test.go`：`1626d11bf2c9a89477c14b46d2e9117baaefb369`
- `internal/secrets/vault_agent.go`：`0f83e6ea8aca48ee92ef377239c349620502e432`
- `internal/secrets/vault_agent_test.go`：`252ef74cacd152399e58ebcd8753756499e52b22`

采用行为包括 `pay-v1` 十行签名、原始 request-target、严格 JSON、Webhook 四行签名、
五分钟时间窗和范围校验。有意差异是 Sub2 只开放支付宝和微信、由现有 Provider 接口适配下单/
查单/关单，并由 Sub2 PostgreSQL 实现 SDK 明确留给产品侧的持久去重与 sequence 栅栏。
退款使用可选 `UnifiedRefundProvider`，保留传统同步 `Provider.Refund` 的统一支付拒绝实现。
本轮采用固定 blob 中已实现的退款 `POST 202` 和查询契约；不能把旧基线提交中的规划态
`501` 当作当前契约。REST 退款响应有环境/组织/产品范围，应用由请求签名及原支付单关联约束；
Webhook 仍显式携带并校验应用范围。
源项目当前没有单独许可证文件；这次是同一所有者内部项目间的契约复用，若未来对外分发
独立 SDK，必须先补许可证与 NOTICE 决策。

## 配置与密钥

运行时变量见 `deploy/.env.example`。其中：

- `UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF`：Sub2 自有 Ed25519 私钥的 Vault 引用；不是密钥值。
- `UNIFIED_PAYMENT_VAULT_AGENT_SOCKET`：固定指向共享只读卷中的内存代理 Unix socket。
- `UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON`：统一支付服务 Webhook 公钥集合，不是私钥。
- `UNIFIED_PAYMENT_PAYMENT_METHODS`：允许 Sub2 暴露的统一支付方式，应用配置默认 `alipay,wechat_pay`。
  已有 Compose/蓝绿沙箱发布路径未显式指定时只保留 `alipay`，扩展方式必须显式配置并在中央
  服务中匹配相同环境的可用通道。公共配置脚本兼容原 12 行输入，并分别接受可选的方式和
  `UNIFIED_PAYMENT_WEBHOOK_URL`（最多 14 行）；缺少后者的旧 managed block 保持可读，蓝绿候选
  容器会收到固定的 `https://api.turtleligpt.com/api/v1/payment/webhook/unified`。
- `UNIFIED_PAYMENT_RETURN_URL`：必须精确等于 Sub2 的 `/payment/result`，不能用通配符。
- `UNIFIED_PAYMENT_WEBHOOK_URL`：必须精确等于固定的公网 HTTPS Webhook 地址，不能填内网、
  `.local`、`.localhost` 或其他端口。

仓库只记录 Vault 引用和不含秘密的部署说明。不得把私钥写入 `.env` 模板、配置文件、镜像层、
日志或备份归档。

Sub2 请求签名私钥和支付服务 Webhook 签名私钥是两套独立 Ed25519 密钥。创建工具
`sub2api-payment-vault-request` 的 JSON 输出只能直接管道送入 `infra-vault create-item`；不得重定向
到文件或显示在终端。该工具默认 `--profile sandbox`；`--profile live` 仅生成 `app.sub2.live` 的
新建 Vault 项请求元数据和独立密钥对，不会启用路由、注入密钥或执行发布。运行时由 SHA-256 固定的 `sub2api-payment-vault-activate` 一次性校验两组
公私钥关系，随后把私钥分别送入两端的内存代理，只把公钥登记到数据库和 Sub2 公共配置。

后台来源为空表示“自动选择（优先统一支付）”；`unified_alipay` / `unified_wxpay` 明确选择
统一支付，官方/易支付来源则固定到实际服务商。后台“已配置”表示运行时能力已载入，
不代表真实通道支付或退款验收通过。`payment_unified_enabled` / `payment_unified_methods`
只从服务器读取，不能从保存设置请求修改；旧版按钮 enabled 字段不作为新路由开关。

### 绑定版本与二进制回滚

管理员保存的主键 `payment_unified_public_binding_v1` 保持旧版可读的原始
`StoredIntegration` JSON；选择手动配置时仍删除这个键。因此回滚到旧二进制时，它可以继续直接
读取原始 JSON，或在键不存在时回退到部署配置。新二进制把 revision、原始 JSON 的 SHA-256 和
手动模式 tombstone 写到独立的
`payment_unified_public_binding_revision_v1` 键。旧二进制忽略该键。

新二进制只在一个 PostgreSQL 事务中同时比较并写入主键、revision 键和精确的 idempotency lease；
网络同步不持有数据库锁。这样已过期的执行器、延迟的 DELETE 重试和丢失响应后的重放都不能把较新的
绑定删除或回退。主键不存在不是足够的版本条件：手动模式的 revision tombstone 也必须一致。

首次启用这个版本前，先记录这两个**不含私钥**设置键的公共快照，并排空所有仍会写入旧主键的旧 API
副本。蓝绿期间不能同时让旧二进制和新二进制接收绑定 POST/DELETE；回滚时先排空新写入者，再恢复旧
流量。旧二进制可立即读取主键或其删除状态，revision 键无需删除。恢复新二进制前，必须重新排空旧
写入者，并以同一受控事务恢复已审计的主键和匹配的 revision 快照。

如果主键的 SHA-256 与 revision 元数据不一致，新二进制返回
`UNIFIED_PAYMENT_BINDING_METADATA_MISMATCH`，不会自动“重基”或猜测哪个写入者正确。此时停止所有
绑定写入者，保留两个键及相关审计记录，确定权威公共配置后再用经过审查的双键 CAS 恢复；不得只删
revision 键或在旧写入者仍在线时修复。

`UNIFIED_PAYMENT_ENABLED` 只是 Sub2 运行时 gateway 开关，可在 live 注册/绑定阶段显式保持 `false`，
注册完成后以同一严格公共配置显式改为 `true`。它不修改 `PaymentConfig.Enabled`、购买页面开关或任何
产品履约设置。

## 管理员余额退款

迁移 `237_unified_payment_refund_attempts.sql` 仅新增两张表：退款尝试与追加式结果证据。
原有 `(order_id, action)` 发货审计唯一约束保持不变。执行流程：

1. 在本地订单行锁内验证原支付范围和可退款状态，保存独立 `product_refund_no`、幂等键、
   通道整数分、余额最小单位、扣回选择及请求摘要，将订单置为 `REFUND_PENDING` 后提交。
2. 事务外发送中央退款请求。`202`、`APPROVED`、`PROCESSING`、`UNKNOWN` 或任何无法确认的
   HTTP 结果均不扣回余额、不释放本地活动尝试。重启或人工查单时复用相同退款号和请求正文；
   已知远端退款 ID 时直接查询该记录。
3. 查询或已验签 Webhook 的 `SUCCEEDED` 在订单锁内一次性提交退款终态、可用余额扣回及审计。
   提前到达的 Webhook 可以绑定远端 ID，较晚的 HTTP 受理响应不能覆盖成功状态。
   `FAILED` 保留历史尝试且不扣余额，允许管理员重新发起新的尝试。
4. 金额、方式或远端 ID 不一致、互斥终态、无法关联本地申请的可信退款，均保留证据并阻断该订单
   的后续自动退款处理。余额已被消费而无法完整扣回时，记录实际扣回量并转人工复核。
   已标记人工复核后仍保留后续可信资金事实，不再自动处理权益。
   管理员提交或查单收到成功结果但携带人工处理警告时，后台保留警告提示。

退款证据只保存经校验的归一化白名单字段，包括 event/source、范围、支付与退款关联、
金额、币种、方式、通道状态、失败码和完成时间，不保存原始 Webhook body。
正常结果、终态冲突、无法关联及永久拒绝路径均写入追加式证据；证据写入失败时不永久 ACK。

本阶段仅支持管理员对普通余额订单退款，不开放用户自助权限或不可逆权益退款。
Sub2 仍沿用一次部分退款后的既有终态限制，未新增连续部分退款产品流程；中央服务自己的
并发预占和通道部分退款约束由中央服务负责。所有通道金额采用整数比例和整数舍入，旧产品
浮点 DTO 只在边界转成最小单位，不参与新的通道退款运算。

测试用密钥只在本地生成并用于 `httptest`。本地测试、浏览器配置夹具及 PostgreSQL 并发测试
不等于支付宝沙箱或微信真实付款验收。激活器默认仍保留既有 `sandbox` 流程；`--profile live`
只接受显式的 `--sub2-host sub2api-candidate` 或 `--sub2-host sub2api-new`。live 路径在两端内存
代理完成私钥注入后，只输出公共 enrollment bundle，不执行自动 SQL、运行时配置或普通购买启用。
微信真实 0.01 元仍须在已授权的 live 应用、通道和测试运行环境内单独验收，不能把 live 微信请求
装进 Alipay sandbox 范围。

## 2026-09-07 本地实现验证

状态：`IMPLEMENTATION_READY`。T1–T5 的本地开发与限定独立复审完成；代码和新增迁移
仍在本地工作区。本记录不把本地结果描述为已完成的发布、真实通道验收或用户验收。用户已授权
受控的 live 测试与部署准备，但普通购买入口保持关闭。

- 后端支付相关单元回归通过：`go test -tags=unit ./internal/payment/... ./internal/service ./internal/handler ./internal/handler/admin ./internal/config ./internal/repository -run 'Test.*(Unified|Payment|Refund|VisibleMethod|Gateway|Webhook)' -count=1`。
- 最后证据字段修复后的限定回归通过：`go test -tags=unit ./internal/payment/unifiedpay ./internal/service -run 'Test.*Unified|TestRefundWebhook' -count=1`。
- 最后改动后的 PostgreSQL 竞态检查通过：`go test -race -tags=integration ./internal/repository -run '(TestUnifiedRefundPostgres|TestUnifiedWebhookInboxPostgres)' -count=1`，25.674 秒。支付宝与微信均覆盖 8 并发申请同一退款、查询/签名回调交错及重复、早回调/晚 HTTP 受理响应；另覆盖退款低 sequence 进入业务处理而不回退 cursor。
- 前端设置、支付回跳和付款流程的 6 个测试文件共 96 项通过；新增 `AdminOrdersView.refund.spec.ts` 的 3 项提交/查单提示测试通过；最终 `pnpm typecheck` 通过。
- `deploy/tests/unified-payment-config-test.sh` 和 `deploy/tests/blue-green-external-runtime-mock-test.sh` 均通过；
  此处列出的本地验证没有执行发布脚本或生产迁移。已授权的 live 测试与部署按下面的 profile、
  Vault 和运行时门槛单独执行，普通购买入口仍保持关闭。
- 本地浏览器夹具核对了实际设置页的统一支付配置、来源选择、保存反馈及 390px 宽度下新增配置区域。夹具不连接真实账户，不能据此宣称整页移动端或线上端到端验收。
- 独立审查未发现 P1；可信退款证据字段遗漏的 P2 已修复并经只读复审关闭，限定复审未发现新的可行动 P1/P2。`git diff --check` 与新增 Go 文件格式检查通过。

剩余边界：仅支持管理员普通余额退款，连续部分退款与不可逆权益处理保留既有限制。
在用户已授权的 live 测试与部署范围内，T6 由测试环境 owner 完成运行时激活及支付宝沙箱/微信
实际扫码、付款、关单与退款验收；发布操作者仍须确认部署记录、Vault 注册和环境门槛。真实通道
结果仍待记录，普通购买入口在整个准备和测试期间保持关闭。

`knowledge_candidate: no`；原因：这是本项目接入与验证记录，尚无需要推广的通用规则；
分类建议：保留为项目文档，后续真实验收结果追加到本节。

## 发布顺序与故障边界

1. 固定并预装同一 Sub2 commit 的 `linux/amd64` 镜像；先安装并校验蓝绿发布脚本、公共配置脚本和
   `sub2api-unified-payment-vault-container.sh`。
2. 启动无网络的 `sub2api-payment-vault`，此时它必须处于“等待注入”而非健康状态；共享卷只包含
   `public.sock`，私有注入 socket 位于容器专属 tmpfs。
3. 支付服务 Worker 的内存代理加入 Sub2 Webhook 私钥引用时，只停止 Worker；API、PostgreSQL 和
   公网支付宝/微信回调入口继续运行。运行时、通道、Sub2 Webhook 三个字段全部重新注入后才允许
   Worker 恢复。
4. 默认 `sandbox` 激活器保留既有顺序：先注入两端内存私钥，再事务登记 Sub2 请求公钥、精确
   返回地址、Webhook 公钥和端点，最后写入 Sub2 的无秘密运行时块。live 激活必须显式使用
   `--profile live --sub2-host sub2api-candidate` 或 `--profile live --sub2-host sub2api-new`；它完成
   两端私钥注入后只输出公共 enrollment bundle，并在独立的登记/持有证明步骤前停止。live 路径不
   自动执行 SQL、不改运行时 gateway，也不启用普通购买。任何一步失败均停止，不启动新 Sub2 容器。
5. 只有两端内存代理和支付 Worker 均健康后，才执行 Sub2 蓝绿发布。候选容器必须逐项比对所有
   公共配置、只读 socket 卷和镜像 revision，健康失败不得切换 Caddy。

激活不创建支付单。回滚优先恢复旧 Sub2 流量；不得删除支付数据库、Webhook inbox、审计记录或
Vault 项。任一内存代理重启都会主动清空密钥并 fail-closed，必须由 owner 重新运行固定消费者。

## 启用前检查

- sandbox profile 的支付服务侧产品应用为 `app.sub2.sandbox`，环境为 `sandbox`；live profile 为
  `app.sub2.live` / `live`，并只能使用上述两个显式 Sub2 target alias。
- 支付服务登记 Sub2 请求签名公钥；Sub2 配置支付服务 Webhook 公钥。
- 支付服务的产品 Webhook 目标精确指向 Sub2 `/api/v1/payment/webhook/unified`。
- 支付服务允许的返回 URL 精确包含 Sub2 `/payment/result`。
- `UNIFIED_PAYMENT_ENABLED` 在 live 注册和绑定时可以保持 `false`，完成公共登记和持有证明后才可
  显式开启；它不改变 `PaymentConfig.Enabled`。普通购买入口在 live 测试和部署准备期间保持关闭，
  不需要创建伪造的本地支付服务商实例。
- 先完成回调重复、乱序、延迟、签名错误、金额不符和超时未知状态演练，再切换生产环境。
