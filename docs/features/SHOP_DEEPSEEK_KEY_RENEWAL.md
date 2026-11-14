# DeepSeek 商城多卡购买与续费

2026-11-15（北京时间）：商城与 New API v4 接收端已上线。沿用一个 Sub2 账号对应
一个 New API 账号的绑定；同一买家可持有多张独立商城卡。

## 买家操作

- 「续费已有卡」使用所选卡的原 Key；「另购一张卡」发放独立新 Key。没有卡时默认
  另购，只有一张时预选。两张或更多卡全部列出（包括正常过期的），普通商品入口
  不默认选卡；从已交付的多卡订单点「续费这张卡」只带入原卡。当前结账里切换周期、
  并发或购买方式，会保留仍可续费的选择。
- 卡片显示卡号、Key 尾号、状态、日卡/周卡图形标签和到期时间。使用中的卡按最近到期
  在前，待生效按最近生效在前，已过期按最近过期在前。待生效显示下一段起始时间；
  老卡的手工加时不改订单套餐标签。确实无原规格证据时显示「旧卡」。
- 提交前显示卡号和尾号或「新 Key」、规格、价格和预计起止时间。使用中的卡从最后
  已购权益结束时接续；过期卡从本次付款确认时恢复原 Key，另购从本次付款确认时起算。
  订单页显示实际到账时间段，统一使用北京时间。如退款产生空档，已购的后续权益保留，
  空档不填补。自动交付失败不换卡或重新发卡，订单按既有流程转客服复核。

## v4 契约与切换

- 商城 JWT 路由 `GET /shop/products/:id/daypass-cards` 从会话取买家 ID；返回脱敏元数据，
  禁止缓存。Checkout 仅接受 `daypass.action`（`renew` 或 `new`）和续费时的 `card_id`。
  服务端解析 SKU、核对卡归属后在订单快照保存意图和尾号；幂等重试不得改变对象。
  销售暂停时仍保留历史订单、完整 Key 的买家专属交付及退款记录。
- 私有付费动作快照版本 3 对应 New API `sku_version=4`。私有 `POST /api/daypass/automation/cards`
  走现有机器认证；完整 Key 只在私有 provision 回包与买家专属交付中出现。卡 ID 为原始
  发卡订单号，接收端再次核对卡、账号和快照并使用确切 token ID。历史收据只接受同买家、
  同商品且原生动作成功的证据，沿用下文的旧软删除限制；专用管理后台禁用或删除的卡
  不能续费。接收端给每张商城卡的实际付费窗口绑定原生订阅、并发和结算，退款撤销本单。
- Sub2 无数据库迁移；New API 增加 `daypass_shop_orders.explicit_card`（默认 false）。
  v4 另购才标记为 true，升级前已开的 v3 订单可以继续处理且不会隐式续到这些独立卡。
  旧快照和 v0/v2/v3 回执散列保持原样。
- 可复现接收端：[v4 补丁](../../deploy/patches/new-api-daypass-multicard-v4.patch)
  和 [v3 基线校验](../../deploy/patches/new-api-daypass-multicard-v4.base.sha256)。
  补丁 SHA256：`2e775c7e7bff7f28015b742a4016081e8a828d46030a9b4b3ae4b756240b3d5f`。在本地 guard.5.5
  候选基线（先应用下文 v3 补丁）中运行 `shasum -a 256 -c <base.sha256>` 和
  `git apply --check --unidiff-zero <v4.patch>`。已在基线副本正向应用并逐文件比较，
  在最终 New API 源码反向检查通过。
- 发布时先备份并切换单实例 New API 及完整 AGPL 对应源码，私有控制代理的精确路径
  白名单更新为 `/api/daypass/automation/(ready|cards|provision|revoke)`，公网仍拒绝
  整个前缀。确认 Ready 支持 `[2,3,4]`、卡片列表可用后，再切换商城和履约 worker。
  新 checkout 在接收端不支持 v4 时拒绝下单；旧 worker 不领新的快照。首个 v4 订单产生后，
  止损使用暂停购买和兼容版本继续处理已有订单及售后，保留回执和数据库。
- 商品简介和详情说明「续费沿用所选卡的 Key，另购发放新 Key；使用中续费到期接续，
  过期续费和另购从付款确认开始」。购物页已有的 SKU、价格、并发和上下架状态保持原值。

## 生产发布记录

- New API `v1.0.0-rc.47-daypass-guard.5.6` 先切换。SQLite 切换前备份通过完整性检查，
  同主机其余容器和公网控制隔离保持不变。详见
  [guard.5.6 对应源码](https://ds.turtleligpt.com/source/new-api-daypass-guard.5.6.tar.gz)
  （SHA256 `80dc701bd1bd0fae68ec0996147fd2bf3723d54d38d5506aa533cc299fbfd12b`）。
- 商城生产镜像对应提交 `87b64a955afa9b80d68d42531c3afbbb25193975`。
  [发布工作流](https://github.com/Turtle-Li/sub2api/actions/runs/43643109828)
  的镜像验证和应用切换通过。单独的 Caddy 模板同步步骤拒绝现有配置中的额外上游，
  在写入前结束。商城发布没有改模板；保留在线路由，应用仍在新版。
- 排空上个版本后，用同一个已验证镜像更新另一槽。Green 健康且持有后台任务，
  Blue 同镜像并自然排空；三份 Caddy 视图一致。两槽都兼容新订单，
  原有 Runtime Guard 定时器保持关闭；不要启动更早的无 v4 订单支持的镜像。
- 生产核验：WWW 和 API `/health` 正常，公网购买页加载新版选卡组件和客户端，
  未登录调用选卡接口返回 401 且禁止缓存。生产机器认证读取 Ready `[2,3,4]`；
  3 条已交付历史订单凭证均匹配到可续费卡，只返回脱敏元数据。公网控制路由
  全部返回 404。未执行真实购买或扣款。
- 商品 3 简介和详情按上线说明更新，版本 8→9；同一事务验证其余商品字段、
  四个 SKU、价格和上架状态没有变化。既有订单快照保持原样。

## v4 本地验收

- New API：SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 16.15 的竞态测试通过。
  覆盖同账号多卡、续费同一个 Key、旧卡隔离、两张都过期后只恢复一张、退款保留后续时段、
  只按该卡原生订阅结算、禁用/删除边界、旧卡加时以及旧回执迁移。原生 TokenAuth HTTP
  试验证明撤销一张不会停掉另一张。
- Sub2：商城和付款相关 Go 竞态测试、接收端协议测试、PostgreSQL 履约/退款集成测试
  通过。覆盖付款前验卡、幂等键绑定、旧订单证明隔离、付款后指定卡消失进入复核。
- 商城：46 个商城界面测试与前端构建通过。Chrome 桌面 1360px 亮色、移动 390px 暗色
  和 320px 亮色使用本地合成订单验证选卡、套餐切换、支付弹层、订单直达以及窄屏溢出。
  未调用外部支付或线上模型。

## v3 单卡候选（历史）

2026-11-14，本地实现与验收，未发布。商城改动在 `codex/deepseek-key-renewal`
worktree；New API 接收端改动在独立的 `new-api-daypass` 源码目录，商城仓库同时保存可复现补丁。

## 买家会看到什么

同一个 Sub2 账号购买同一 DeepSeek 商城商品，日卡或周卡都使用该账号的商城 Key。
例如，买家周一买了 24 小时日卡，周四再买一张：旧 Key 在两单之间不能调用，
周四这张从本次付款确认时间起算，交付后直接用客户端里原来的 Key。提前续购则
接在已有有效期之后；从 2 并发换到 3 并发，等新订单生效才切换。每笔订单显示本单
的起止时间和档位，时间使用北京时间。重复回调、刷新订单或交付重试不会多送时长。

首次购买没有可复用的商城 Key 时发放新 Key。已绑定的旧商城 Key 优先接续；绑定前
的 Key 只在商城有同买家、同商品且成功交付的历史订单，接收端也能核对原回执时接续。
同一账号下的手工发卡保持独立。管理员主动停用的 Key 不因续费解禁，需要客服复核；
管理员明确删除的 Key 不恢复，之后可发新 Key。

退款沿用原有人工售后流程：撤销的是该订单对应的权益，不会停掉共享的 Key 上其他
未撤销的权益，也不自动退款。如果中间一单被撤销，后续订单仍按已确认时间开始；
空档期间不可调用。

## 实现与兼容

- 已查看 `codex/deepseek-weekly-pass`（`606df1c459f0d5071154ef9468e136b22a71fb5d`）。
  该分支已包含在商城基线 `b427226151d13c34910bfff2d54b245b1c1c3058` 中。
  原交付快照未携带买家 ID；接收端即便有账号绑定，历史协议仍按单发 Key，普通到期后
  会清理旧 Key。
- Sub2 在可信付款和履约事务中写入私有买家快照。姓名仅用于后台展示；账户 ID 来自
  付款记录并与履约归属校验，旧订单提示只从成功交付的同买家、同商品订单读取。
  新原生动作使用快照请求版本 2，旧 worker 会拒绝该快照；普通 Webhook 和历史动作
  仍使用原协议，既有快照与散列不改写。
- New API 的私有交付请求 `sku_version=3` 绑定付款金额、订单、商品、时间、买家和
  显式 SKU。接收端用已有账号映射和商城回执定位 Key，不接受浏览器提交的 Key 或
  买家 ID。每单独立存储生效窗口、原生订阅和并发档位；重复交付复用该单回执。普通到期
  保留可续 Key，实际调用由当前订单和原生订阅共同授权。后台可以显式停用、删除或覆盖
  并发；已付款的订单窗口不能通过原手工延时按钮改写。
- 受控历史恢复只处理核对一致的过期软删除记录。专用后台的删除会标记撤销；比历史
  清理阈值更早的删除不会被当作清理。历史记录缺少删除原因，若用通用原生接口在
  到期清理阈值后删除，数据库无法仅凭旧时间戳区分；管理商城 Key 使用专用后台。
  多张旧单曾分别发过 Key 时，只接续一个有明确归属的商城 Key，不合并其他 Key。
- 固定参考：[Sub2 上游付款履约](https://github.com/Wei-Shaw/sub2api/blob/bdb42e22f81fcb633ff0a060961211dd2bcb515b/backend/internal/service/payment_fulfillment.go)
  及同目录测试；[New API 原生 Token](https://github.com/QuantumNous/new-api/blob/e835b1bfc43b565d625537830dbfb19328925134/model/token.go)、
  [原生订阅](https://github.com/QuantumNous/new-api/blob/e835b1bfc43b565d625537830dbfb19328925134/model/subscription.go)
  和 `model/token_migration_test.go`、`model/subscription_reset_test.go`。保留上游原生账户、
  订阅、Token、计费和资金事务；订单接续是本地扩展。保留 Sub2 LGPL-3.0 与 New API
  AGPL-3.0 许可和原有署名。
- 安全验证参考 [OWASP ASVS 5.0.0](https://github.com/OWASP/ASVS/releases/tag/v5.0.0)
  8.3.1、14.2.4、16.2.5，以及 [认证](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、
  [会话管理](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 指南。
  本次验证服务端归属、到期、撤销、重复请求及旧协议边界。私有交付继续使用现有机器
  认证和禁止缓存响应；日志、付款快照和补丁不包含真实 Key。

## 配套源码与切换顺序

补丁：[new-api-daypass-renewal-v3.patch](../../deploy/patches/new-api-daypass-renewal-v3.patch)；
基线：[受影响文件的 SHA256](../../deploy/patches/new-api-daypass-renewal-v3.base.sha256)。
补丁 SHA256：`fa22297c51840f6bee1df7bbb187aaa77c13bb5242e2b7452cecf27c508f326d`。
基线为本地 guard.5.4（含用户绑定与双栈），不是早期 guard.5.3 的公开归档。
在匹配的干净 New API 源码目录用 `shasum -a 256 -c <base.sha256>` 核对，再用
`git apply --check --unidiff-zero <patch>` 检查并应用。已用基线副本正向应用、在
候选源码反向检查，并逐文件比较结果。

未来发布先构建并备份专用 New API 服务，保留现有 DB 和全部历史回执；部署兼容 0/2/3
的接收端及完整 AGPL 对应源码，确认私有 Ready 返回版本 3。再切换商城及其后台交付
worker，旧 worker 先停止领取动作，避免把新快照误标为待复核。新商城在接收端未支持
版本 3 时不会放行新购。第一笔 v3 交付后，如需止损，暂停新购并保留支持 v3 的
接收端和清理/售后能力；不要回滚到不识别续费回执的版本或用旧库覆盖交易。

## 本地验证

- New API：`python3 ops/test-daypass-databases.py`，使用 SQLite 3.50.4、MySQL
  8.4.13、PostgreSQL 16.15 的实际实例，覆盖现有回执两次迁移、重复付款、历史回执、
  同账号复用、退款和多档位；临时数据库容器已删除。
- New API：`go test -race ./model ./controller ./middleware ./router -run '^(TestDaypass|TestAccountGuard)' -shuffle=on -count=1`。
  本地私有 HTTP 交付与真实 TokenAuth 验证了「旧 Key 到期被拒 → 同一 Key 续购后通过
  → 本单权益撤销后拒绝」。没有访问线上 DeepSeek 或使用真实买家 Key。
- Sub2：`GOWORK=off go test -race -tags unit ./internal/service ./internal/handler -run 'Test(Shop|PaymentShop)' -shuffle=on -count=1`；
  `internal/shopaction` 单测与本地 PostgreSQL `TestShopPostgresAtomicDeliveryAndRefundFences`
  通过，覆盖旧快照、跨买家、付款与退款边界。
- 商城：70 个页面/交互测试与 `vue-tsc --noEmit` 通过。New API 管理台：7 个相关
  交互测试，受影响文件 lint、TypeScript 5.9.3 检查和 Rsbuild 通过。
