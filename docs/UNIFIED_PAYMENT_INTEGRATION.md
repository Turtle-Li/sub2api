# Sub2 × 统一支付服务接入

## 责任边界

Sub2 创建并持有业务订单、用户余额/订阅和履约状态。统一支付服务持有支付订单与可信资金
状态，接收支付宝异步通知，再把签名事件投递给 Sub2。支付宝浏览器返回不作为支付成功
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
3. 前端打开返回的 `checkout_url`，由支付宝收银台展示扫码入口。
4. 支付宝异步通知统一支付服务；统一支付服务验签并落库。
5. 统一支付服务向 Sub2 投递 `POST /api/v1/payment/webhook/unified`。
6. Sub2 验证原始正文签名、事件时间窗、环境/组织/产品/应用作用域、支付订单 ID、业务订单号和整数分金额。
7. PostgreSQL inbox 以 `event_id` 和 `(payment_order_id, sequence)` 全副本去重，并用每个支付单的
   `max_processed_sequence` 与活动处理租约阻止乱序或并发执行；通过后，Sub2 调用原有幂等发货逻辑。

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
- `sdk/go/payclient/types.go`：`b5014a44d0f4baed54ebf60e4ef6d46eb4bfd6e6`
- `sdk/go/payclient/webhook.go`：`b7e32459b4298eea72a0cd054ff409d614be05a9`
- `sdk/go/payclient/strictjson.go`：`52874ff1396066b419eb0f2e0cfdae338085f86c`
- `sdk/go/payclient/client_test.go`：`92fc86ca1d762fc84022f4eec930d8c3819f4f8a`
- `sdk/go/payclient/webhook_test.go`：`57f8f467ec97600133dd5b94228ae94af22f7216`
- `contracts/webhook-event.schema.json`：`1e310eeb5a072ca16e2a5916f86b001238d2675a`
- `cmd/payment-vault-agent/main.go`：`8efe1e50f728eaf91c423904ee50f5e8c4ab4b1e`
- `cmd/payment-vault-agent/main_test.go`：`1626d11bf2c9a89477c14b46d2e9117baaefb369`
- `internal/secrets/vault_agent.go`：`0f83e6ea8aca48ee92ef377239c349620502e432`
- `internal/secrets/vault_agent_test.go`：`252ef74cacd152399e58ebcd8753756499e52b22`

采用行为包括 `pay-v1` 十行签名、原始 request-target、严格 JSON、Webhook 四行签名、
五分钟时间窗和范围校验。有意差异是 Sub2 只开放支付宝、由现有 Provider 接口适配下单/
查单/关单，并由 Sub2 PostgreSQL 实现 SDK 明确留给产品侧的持久去重与 sequence 栅栏。
源项目当前没有单独许可证文件；这次是同一所有者内部项目间的契约复用，若未来对外分发
独立 SDK，必须先补许可证与 NOTICE 决策。

## 配置与密钥

运行时变量见 `deploy/.env.example`。其中：

- `UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF`：Sub2 自有 Ed25519 私钥的 Vault 引用；不是密钥值。
- `UNIFIED_PAYMENT_VAULT_AGENT_SOCKET`：固定指向共享只读卷中的内存代理 Unix socket。
- `UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON`：统一支付服务 Webhook 公钥集合，不是私钥。
- `UNIFIED_PAYMENT_RETURN_URL`：必须精确等于 Sub2 的 `/payment/result`，不能用通配符。

仓库只记录 Vault 引用和不含秘密的部署说明。不得把私钥写入 `.env` 模板、配置文件、镜像层、
日志或备份归档。

Sub2 请求签名私钥和支付服务 Webhook 签名私钥是两套独立 Ed25519 密钥。创建工具
`sub2api-payment-vault-request` 的 JSON 输出只能直接管道送入 `infra-vault create-item`；不得重定向
到文件或显示在终端。运行时由 SHA-256 固定的 `sub2api-payment-vault-activate` 一次性校验两组
公私钥关系，随后把私钥分别送入两端的内存代理，只把公钥登记到数据库和 Sub2 公共配置。

## 发布顺序与故障边界

1. 固定并预装同一 Sub2 commit 的 `linux/amd64` 镜像；先安装并校验蓝绿发布脚本、公共配置脚本和
   `sub2api-unified-payment-vault-container.sh`。
2. 启动无网络的 `sub2api-payment-vault`，此时它必须处于“等待注入”而非健康状态；共享卷只包含
   `public.sock`，私有注入 socket 位于容器专属 tmpfs。
3. 支付服务 Worker 的内存代理加入 Sub2 Webhook 私钥引用时，只停止 Worker；API、PostgreSQL 和
   公网支付宝回调入口继续运行。运行时、支付宝、Sub2 Webhook 三个字段全部重新注入后才允许
   Worker 恢复。
4. 激活器先注入两端内存私钥，再事务登记 Sub2 请求公钥、精确返回地址、Webhook 公钥和端点，
   最后写入 Sub2 的无秘密运行时块。任何一步失败均停止，不启动新 Sub2 容器。
5. 只有两端内存代理和支付 Worker 均健康后，才执行 Sub2 蓝绿发布。候选容器必须逐项比对所有
   公共配置、只读 socket 卷和镜像 revision，健康失败不得切换 Caddy。

激活不创建支付单。回滚优先恢复旧 Sub2 流量；不得删除支付数据库、Webhook inbox、审计记录或
Vault 项。任一内存代理重启都会主动清空密钥并 fail-closed，必须由 owner 重新运行固定消费者。

## 启用前检查

- 支付服务侧产品应用为 `app.sub2.sandbox`，环境为 `sandbox`。
- 支付服务登记 Sub2 请求签名公钥；Sub2 配置支付服务 Webhook 公钥。
- 支付服务的产品 Webhook 目标精确指向 Sub2 `/api/v1/payment/webhook/unified`。
- 支付服务允许的返回 URL 精确包含 Sub2 `/payment/result`。
- Sub2 后台“启用支付”已打开；不需要创建一个伪造的本地支付服务商实例。
- 先完成回调重复、乱序、延迟、签名错误、金额不符和超时未知状态演练，再切换生产环境。
