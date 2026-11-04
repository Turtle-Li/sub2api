# 商城实施契约

用户已确认：入口使用通用“商城”，未来支持 Gemini 等商品；名称、分类、价格、期限、详情由后台填写，期限默认1个月。按现有支付宝/微信、飞书和客服配置实施，不等待首批文案和价格。此契约冻结本次前后端接口；原设计的待确认状态由此替代。

## 商品

```ts
type ShopProduct = {
  id: number
  name: string
  category: string
  summary: string
  description: string // sanitized Markdown when rendered
  price: number // CNY, at most two decimal places; server-owned
  currency: 'CNY'
  duration_value: number // defaults to 1
  duration_unit: 'month' | 'day' | 'year'
  fulfillment_mode: 'session_topup' | 'account_delivery'
  input_format: 'session_json' | 'text'
  input_url: string // optional HTTPS URL; default ChatGPT session URL for session_json
  input_instructions: string
  guide_key: '' | 'chatgpt-plus-account'
  enabled: boolean
  sort_order: number
  revision: number
  created_at: string
  updated_at: string
}
```

POST商品payload除id/revision/timestamps/currency外同上；PUT含revision做CAS。草稿允许price=0但enabled=true要求price>0。没有默认上架商品或售价。分类自由文本，交付模式按行为命名，不能把ChatGPT或Gemini写死在入口/分类枚举中。

## 订单

持久支付类型 `manual_shop`；付款后保持PAID直至人工确认。人工状态：

`WAITING_PAYMENT | WAITING_INPUT | QUEUED | PROCESSING | NEEDS_INPUT | COMPLETED | CLOSED`

```ts
type ShopOrderDetail = {
  order_id: number
  out_trade_no: string
  product: ShopProduct // immutable purchase snapshot, never current catalog
  user_id: number
  user_email?: string // admin only
  pay_amount: number
  currency: 'CNY'
  payment_status: string // existing OrderStatus
  fulfillment_status: ShopFulfillmentStatus
  revision: number
  input_revision: number
  input_account: string
  input_check?: { account: string; plan_type: string; expires_at?: string; warnings: string[] } // single-order offline hints only
  input_submitted_at?: string
  created_at: string
  paid_at?: string
  completed_at?: string
  status_note: string
  delivery_content?: string // plaintext only on authenticated OWNER detail after delivery
  email_status: 'NOT_SENT' | 'PENDING' | 'SENDING' | 'SENT' | 'FAILED'
  admin_notification_status: 'NOT_SENT' | 'PENDING' | 'SENDING' | 'SENT' | 'FAILED'
  can_submit: boolean
  can_start: boolean
  can_complete: boolean
  can_reveal_input: boolean
}
```

所有响应沿用项目response.Success envelope；API client自动解包。

| Method / route（相对`/api/v1`） | 请求 | 响应 |
| --- | --- | --- |
| GET `/shop/products` | JWT | `ShopProduct[]`，只显示已上架；不因余额/订阅模式隐藏商城 |
| GET `/shop/products/:id` | JWT | `ShopProduct` |
| POST `/shop/orders` | JWT；header `Idempotency-Key`；body `{ product_id, product_revision, payment_type: 'alipay'|'wxpay' }` | 现有 `CreateOrderResponse`，复用付款展示；仅固定CNY商品价格，不给API余额 |
| GET `/shop/orders/:id` | JWT，必须本人 | `ShopOrderDetail` |
| POST `/shop/orders/:id/input/check` | JWT，本人已付款且允许提交；`{ content: string, revision: number }` | `{account,plan_type,expires_at?,warnings}`，本地初检；不存储、不排队、不回显令牌 |
| POST `/shop/orders/:id/input` | `{ content: string, revision: number }` | 最新 `ShopOrderDetail`，不能回显Session |
| GET `/admin/shop/products` | 管理员JWT | `ShopProduct[]`，含草稿 |
| POST `/admin/shop/products` | 商品payload | `ShopProduct` |
| PUT `/admin/shop/products/:id` | 商品payload + revision | `ShopProduct` |
| GET `/admin/shop/orders` | `page,page_size,status` | `{items:ShopOrderDetail[],total,page,page_size}`；不包含任何原始Session或发货密码 |
| GET `/admin/shop/orders/:id` | 管理员JWT | `ShopOrderDetail`，不含明文凭据 |
| POST `/admin/shop/orders/:id/input/reveal` | `{revision: number}`，适用step-up | `{content:string,input_revision:number}` |
| POST `/admin/shop/orders/:id/action` | `{action:'start'|'request_input'|'complete',revision:number,input_revision:number,note?:string,delivery_content?:string}`，适用step-up | 最新 `ShopOrderDetail`；发货正文只在account_delivery的complete提供 |
| POST `/admin/shop/orders/:id/notifications/retry` | `{channel:'email'|'feishu'}` | 最新 `ShopOrderDetail` |

通用用户/管理员订单仍显示manual_shop；前端点击跳转 `/shop/orders/:id` 或 `/admin/shop/orders/:id`。推荐UI路由 `/shop`、`/shop/products/:id`、`/shop/orders/:id`、`/shop/help/chatgpt-plus-account`、`/admin/shop/products`、`/admin/shop/orders`、`/admin/shop/orders/:id`。

通用付款弹窗和结果页对manual_shop必须能在PAID时引导到人工订单，不得等待COMPLETED才提示付款成功，也不得显示余额到账/订阅开通。继续使用现有支付verify/resume能力；商城初次使用原生二维码收银台，不引入不完整的微信OAuth商品恢复契约。

## 人工行为

- paid(session_topup) → WAITING_INPUT；paid(account_delivery) → QUEUED。
- submit(允许WAITING_INPUT/NEEDS_INPUT/QUEUED且未开始) → QUEUED，保存原始字节密文，revision和input_revision递增。
- start(QUEUED) → PROCESSING，记录管理员；session_topup必须有有效资料。
- request_input(QUEUED/PROCESSING) → NEEDS_INPUT，要求非空note，清除旧Session，revision/input_revision递增。
- complete(QUEUED/PROCESSING) → COMPLETED，代充核对当前input_revision，成品号要求非空delivery_content；同事务保存密文、资金订单COMPLETED、脱敏审计和邮件任务；清除Session。
- 已退款/退款中、未付款、取消、过期均拒绝操作；支付或退款复核栅栏阻止复制/履约。人工操作按 payment_order→shop_fulfillment 锁序执行。常规商城退款明确要求人工售后核对，不进入余额/订阅回收，不能扣API余额；取消后晚到付款沿用已有统一支付退款安全流程。
- 邮件任务、管理员新付款/新资料通知使用持久租约重试，不与实际发货耦合。飞书不带敏感资料。

## 前端验收

保留项目AppLayout/现有tokens和明暗主题；新商品页参考给定商店卡片结构。商品内容由owner配置。商品购买区只展示一次“5分钟–2小时操作完成，着急可联系客服”，不在订单各卡片重复；客服使用站点contact_info。成品号教程采用本站原生 HTML 和13张本地图片，仅保留操作所需外部网站；商品、订单和邮件提供入口，用户收货页在账号前突出“必看”。单独、整份复制账号信息；Session仅管理员私有接口一键复制，绝不localStorage。买家须先检查、核对账号与资料中的订阅类型，再确认提交；明确初检未在线验证签名或官方状态，后端提交时重新检查。

测试使用虚构商品和资料，无实盘付款、真实账号或外发消息。浏览器验证桌面1440/手机390、明暗主题和完整交付路径。未配置商品时清晰空状态，管理员直接到商品配置。
