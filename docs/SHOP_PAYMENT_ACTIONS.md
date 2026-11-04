# 商城付款后操作

本功能是 manual_shop 的可选自动交付。管理员为商品选择已登记的集成和业务参数；
付款确认后，商城持久化一个任务，后台调用集成并将最终结果加密交给买家。
用户无需 New API 账号；日卡用户只领取独立 Key 和使用说明。生成请求直接调用
独立 API 出口，不经过 Sub2 的模型中转。

## 运行约定

- 每单一个决定履约结果的动作。付款事实、动作及通知在同一事务提交；配置异常
  保留已确认的付款，进入待复核；数据库写失败则整体回滚。
- 下单固定商品/集成版本和非秘密参数。商品后续改价、下架、换集成不改变旧订单。
- 执行至少一次，接收端必须持久去重。同一 action_id 始终代表同一业务，超时重试
  必须恢复原结果，不延长原授权时间。网络层不承诺“恰好一次”。
- 任务独占 claim_revision 租约；后台20秒扫描，每轮最多3个任务。单次 HTTP15秒，
  自动执行最多16次、原付款后24小时；有界退避并尊重不超过1小时的 Retry-After。
  这24小时是交付恢复窗口。其他商品的服务有效期由参数和接收端决定。
- 只有 account_delivery 支持一期动作。需要用户补充资料的 session_topup 继续人工。
- 通知重试与交付独立。自动单待复核时仍禁止人工重复发货；仅提供重试原任务、
  原生日卡精确撤销和人工售后核对。
- 撤销意图先持久化为 COMPENSATING；只能继续撤销，不会因资金状态重新开放而发货。
  暂停自动发货仍可清理旧授权。存在未撤销订单时必须保留原集成版本与原 SKU ID。
- API Key 只写入现有买家专属加密交付。后台、任务、审计和飞书没有交付原文。
  退款仍需人工售后，原生授权未确认撤销时，退款审核页提示先处理准确订单的授权。

## 配置与权限

`shop_actions.enabled` 默认 false。集成由部署 config.yaml 登记，商品页面不能填写
任意 URL、请求头、脚本或 Vault ref。配置通过既有 CONFIG_FILE / DATA_DIR 规则载入。
以下全为示意，不能直接作为生产启用配置：

```yaml
shop_actions:
  enabled: false
  integrations:
    - id: license
      version: 1
      adapter: http.webhook.v1
      endpoint: https://hooks.example.com/fulfill
      ready_endpoint: https://hooks.example.com/ready
      credential_alias: license
      enabled: false
    - id: newapi-domestic
      version: 1
      adapter: newapi.daypass.v1
      endpoint: http://100.64.0.1:17867 # 仅演示，需填项目批准的 Tailnet 地址
      enabled: false
shop_daypass:
  enabled: false
  product_id: 0 # 启用日卡前填实际已创建的商品 ID；暂停后保留该 ID
```

`id@version` 固定 adapter、目的端、Ready 地址与凭据别名。修改这些字段需发新版本，
保留旧版本供旧订单恢复；enabled 可切换。不要删除仍有未撤销订单的原生版本。
日卡处理器要求6元人民币、1天，默认2并发；GuideKey 只控制教程。

商品绑定示例（后台使用）：

```json
{"binding_key":"primary-delivery","adapter":"http.webhook.v1","integration_ref":"license","integration_version":1,"params":{"sku":"pro-year"}}
```

后端将批准的集成配置写入内部快照。公开商品和买家订单裁剪 payment_actions，
只暴露 automatic_fulfillment、安全状态与结果类型。商品管理沿用管理员 JWT 权限；
任务重试/撤销额外走现有 step-up、claim_revision 和审计。管理员 API Key 不可管理商城。

HTTP 签名秘密只在 Vault，由独立 agent 提供
`/run/sub2api-shop-actions-vault/public.sock`；别名只能映射
`vault://secret/data/ops/shop-actions/<alias>#signing_secret`。不得复用支付、飞书、媒体
或日卡 agent。当前生产挂载守护尚未批准该新 socket；启用 HTTP 集成前须单独完成
精确 ref、隔离 agent 和只读 socket 挂载的部署审查及验收，不能把广泛挂载加入白名单。
仅配置而未接通该 agent 时，结账 Ready 检查会拒绝自动商品。

Ready 可省略；省略时只检查本地配置和凭据，不证明接收端在线。配置 Ready 时只发
签名 GET 到同主机的独立只读端点，必须不产生发货副作用。

## HTTP 契约 v1

只向部署批准的公网 HTTPS 地址 POST。拒绝用户凭据、查询参数、fragment、重定向、
未批准私网及 metadata 地址。每次解析并验证全部 DNS 结果，固定所连 IP；TLS 仍验证
原始主机。原生日卡的私有 HTTP 是独立处理器例外，不能被普通 HTTP 集成继承。

```json
{
  "version":1,
  "event":"shop.order.paid",
  "action_id":"act_example",
  "source":"sub2api-production",
  "order":{"id":42,"product_id":8,"amount_fen":300,"currency":"CNY","paid_at":"2026-11-05T03:00:00Z"},
  "params":{"sku":"pro-year"}
}
```

同一动作的 body 固定，不包含尝试次数、买家邮箱、Session 或其他 Key。
Standard Webhooks 的三个头：webhook-id=action_id；webhook-timestamp=本次秒级时间；
webhook-signature=`v1,<base64(HMAC-SHA256(key, id.timestamp.raw-body))>`。
Idempotency-Key 也为 action_id。Vault 值格式为 `whsec_` 加 Base64；推荐32字节随机
签名 key，兼容最小24字节。签名采用原始请求字节，不得重新序列化后验证。

接收端须验证签名（恒定时间比较）和前后5分钟时间窗，以 `(source,action_id)`
持久去重，并确认重复请求的业务内容一致。业务去重期限必须覆盖人工恢复，不能只
依赖时间窗。接收端必须原子记录副作用和回执，重放返回同一最终结果。

完成必须 HTTP200 + 不超过32KiB的单个 JSON，严格匹配动作与订单：

```json
{"version":1,"action_id":"act_example","order_id":42,"status":"completed","receipt_id":"receipt_example","delivery":{"kind":"text","content":"应加密保存的交付文本"}}
```

receipt_id 是不含秘密的 ASCII 标识（1–128字符，字母、数字及 `._:-`）。无凭据商品
用 `"delivery":{"kind":"none"}`，content 为空或省略；商城显示服务完成，不展示空凭据面板。
纯文本交付默认遮挡，不执行返回的 HTML。空200、错误绑定、额外 JSON、未知字段、
202 或其他协议错误均待复核；202 不代表完成，也不继续盲发。429、5xx、网络错误
在上述幂等前提下重试；3xx/401/403 等不会跟随或尝试其他目的端。HTTP 一期没有自动
撤销协议，远端状态不明时需联系服务方核对，不能把人工重试当成新的发货。

## 上线与恢复边界

迁移266只增加字段/表/通知事件，不创建或上架商品。先发布兼容代码并排空旧颜色，
再登记并启用集成，最后启用商品。不能让不识别动作的旧二进制继续付款或管理自动单。
暂停/停售不等于可以回滚到旧二进制：待付款单和旧任务仍在。恢复必须保留兼容任务
执行器和原始配置；不要删除表、重建动作或修改 paid_at 来清队列。

修复扫描只使用原订单快照与可信原 paid_at，补建同一 action_id；不使用当前商品来
补发历史人工单。已人工完成/关闭的单不执行。新增 HTTP 接收端需分别验收伪造/
过期签名拒绝、并发去重、响应丢失、原回执恢复及其自身业务副作用；本仓库发送端的
互操作测试不能代替接收端验收。发布与真实购买仍按项目既有授权和门禁执行。

## 固定参考

- 签名：standard-webhooks/standard-webhooks@ab6844147ce9cdb0d2dbf2e60d1ea85001fd2554，
  已比较 `spec/standard-webhooks.md`、`libraries/go/webhook.go`、`libraries/go/webhook_test.go`
  与 README；采用三头、原始字节 HMAC 与公开互操作向量。根 LICENSE 为 Apache-2.0，
  Go 子目录无独立许可证；未引入 SDK 依赖。若复制 SDK 代码需保留适用许可证、通知和修改说明。
  [固定规范](https://github.com/standard-webhooks/standard-webhooks/blob/ab6844147ce9cdb0d2dbf2e60d1ea85001fd2554/spec/standard-webhooks.md)。
- 支付：Wei-Shaw/sub2api@bdb42e22f81fcb633ff0a060961211dd2bcb515b，已比较
  `backend/internal/service/payment_fulfillment.go` 的可信付款/重复回调及对应测试；保留
  资金权威，本地 manual_shop 和动作 outbox 为有意扩展，仍遵循项目 LGPL-3.0。
- New API：QuantumNous/new-api@e835b1bfc43b565d625537830dbfb19328925134，原生私有
  provision/revoke 是本项目配套 receipt 扩展，保留 AGPL-3.0 完整对应源码提供义务。
  它不替代通用 HTTP 契约，源站并发/IP与日卡有效期由 New API 执行。
- [AWS 事务 outbox](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html)、
  [Stripe webhook 实践](https://docs.stripe.com/webhooks#best-practices-for-using-webhooks)
  用于架构比较；本实现复用数据库与现有后台，不增加云队列或工作流平台。
