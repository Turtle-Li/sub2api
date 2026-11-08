# 统一商城商品与 SKU 配置

状态：RELEASE_AUTHORIZED（2026-11-08）；基线 `2a7267d2ed422c5320e495d9a1df798ad48221c0`。用户要求普通商品配置、订阅/按量/商城一致的 tab、统一 SKU 配置，并明确代充和成品号人工交付，DeepSeek 调用独立服务交付。本地实现与独立审查通过后，用户已明确授权提交、合并和发布；线上商品和价格保持现值。

## 问题分析（ANALYSIS_READY）

Bug: SHOP-20261108-price-save

现象: DeepSeek 修改价格不能保存；日卡/周卡和并发价格不能在商品配置中编辑；商城入口和订阅、按量的样式与结构不同。

复现条件: 原生 `newapi.daypass.v1` 商品设置非 6 元基础价格，调用 SaveProduct；商品编辑器选择 DeepSeek 指南也会重写价格、周期与交付设置。

调用链: ShopProductEditor → shopAPI.saveProduct → ShopService.SaveProduct → normalizePaymentActions；购买走 selectShopSKU → shopDaypassAdapter → 独立 New API receiver。

影响范围: 商品编辑、公开目录、规格选择、订单计价/幂等/不可变快照、自动交付协议和接收方。

疑似根因: 直接源码证据：`shop_action_bindings.go` 拒绝非 6 元基础价；`shop_daypass_sku.go` 固定 600/800/3000/4000 分；receiver `model/daypass_shop.go` 又以金额推断规格；编辑器 guide watch 有隐式写入；保存异常被概括为通用提示。用户遇到的具体请求未采集，因此不能排除另有版本冲突或必填错误。

推荐修改方案: 持久化通用 SKU（稳定 ID、属性、价格、时长、上架与默认项），所有新配置使用同一编辑器；订单只提交 SKU ID，由服务端锁定目录重新计价并冻结所购 SKU；DeepSeek 新协议明确携带周期/并发，不再按金额猜测；保留原协议处理历史订单。

风险: 新旧接收方兼容、历史回放、并发改价、停用 SKU、自动交付与人工资料边界。既有付款/退款/通知/加密/原始 paid_at 语义保持。

## 实施 DAG（PLAN_READY）

| 任务 | 依赖 | 唯一写入者/范围 | 交付与验收 |
|---|---|---|---|
| T1 通用 SKU 数据与计价 | 无 | Codex；Ent schema/migration、Shop 服务/handler | 普通及 DeepSeek SKU 保存、锁行重新定价、稳定 ID 幂等、历史订单兼容；服务与真实 DB 测试 |
| T2 明确自动交付规格 | T1 契约 | Codex；Shop adapter/client、独立 receiver model/controller | 新协议显式周期/并发/金额；旧协议摘要不变；ready 能力门禁；回放/撤销测试 |
| T3 统一商品配置 | T1 契约 | Codex；商品管理 route/panel、Shop editor/SKU editor/types | 三个同款 tab、统一商品表单与规格表格、人工/自动明确选择、准确保存错误 |
| T4 买家展示与选择 | T1/T3 | Codex；Shop 列表/详情/订单摘要 | 属性选择决定 SKU 和实际价格；不存在或停用组合不可购买；订单展示购买时规格 |
| T5 验证与审查 | T1–T4 | Codex 验证；独立审查只读 | 聚焦 Go/前端测试、类型/build、实际桌面/手机明暗截图与保存/重载/选择流程、独立代码与画面审查 |

用户已验收本地实现并授权提交、合并、发布。发布顺序为独立接收方先行，再发布 Sub2；保留现有商品价格与人工/自动交付方式，不创建真实付款。迁移只增加字段；回滚保留新增数据，发生新版 SKU 购买后只能回退到兼容订单快照和新版接收协议的程序。禁止通过恢复旧 DB 回滚。

## 界面决策

Operate 模式，沿用当前订阅/按量后台的青绿色、system 中文字体、明暗主题、DataTable、btn、BaseDialog。用户已指定常规电商商品/SKU 与统一 tab 方向，直接落实这一既有视觉系统。商品管理统一为“订阅商品 / 按量商品 / 商城商品”；商城列表以商品、规格/价格、交付方式、状态和操作组织。编辑器内使用“基础信息 / 规格与价格 / 交付设置”同款 tab，操作在固定 footer，错误留在表单并展示服务端原因。

单规格和多规格共用 SKU 记录。多规格按属性及属性值生成组合，逐 SKU 配置人民币价、有效期、启用和默认项。DeepSeek 提供日/周与 2/3 并发初始模板；其配置一旦存储，后续由数据库决定，模板不覆盖手工价格。使用指南只负责内容。

## 固定参考与许可

- Wei-Shaw/sub2api `bdb42e22f81fcb633ff0a060961211dd2bcb515b`：已检查 `backend/internal/service/payment_fulfillment.go`、`payment_fulfillment_test.go`；保留可信付款、重复通知与原事务资金栅栏。人工履约、SKU 快照与自动交付为本地扩展。LGPL-3.0 保留。
- QuantumNous/new-api `e835b1bfc43b565d625537830dbfb19328925134`：参考本地固定 vendor 的 `model/subscription.go`、`model/token.go` 及原生测试；继续复用同一事务的原生 token/subscription。自有 guard.5.2 的 `model/daypass_shop.go`、`controller/daypass_shop.go` 为当前协议基线，历史摘要兼容。AGPL-3.0 与完整对应源码发布义务保留；本次尚未部署。

knowledge_candidate: no；这是当前项目的持久业务契约，写回本项目，不提升为全局模式。

## 已实现的配置与接口

- 商品管理入口 `/admin/products`，三个同款 Tab。`?tab=shop` 可直接进入商城；旧 `/admin/shop/products` 重定向，旧 `/admin/orders/plans` 作为别名保留。商城管理不依赖充值功能开关。
- 编辑器“基本信息 / 规格与价格 / 交付设置”。单规格、多规格共用 SKU 行；最多 3 个属性、100 个组合，重复属性组合（包括 nil/空属性）拒绝。生成相同组合保留 ID 和原价格，新组合价格为 0，须由管理员填写。DeepSeek 模板补全缺失组合时统一属性名称，同时保留已有规格 ID/价格/上架状态。默认 SKU、每行上架与整件上架分别配置。
- 通用 SKU：`id`、`attributes`、`price`、`duration_value`、`duration_unit`、`enabled`；原生服务另有 `concurrency`。价格最多两位小数，上架须大于 0；通用有效期支持天/月/年，DeepSeek 仅接受服务支持的 1/7 天、2/3 并发。每行独立定价，不按周价倍率生成新业务配置。
- 迁移 `274_shop_sku_config.sql` 增加 nullable JSONB `shop_products.sku_config`，保存 `{skus,default_sku_id}`。默认 SKU 派生旧顶层 price/duration 以维持兼容。旧目录打开时读取已有服务端价格并呈现在同一表格，管理员保存才转换为持久 SKU；历史价格映射仅为旧目录/旧订单保留。合并时因主分支新增监控迁移273而顺延编号。
- 新配置下单必须提交 `sku_id`，不接收客户端价格。后端解析目录，事务中锁商品并复核 revision/选中 SKU，订单只冻结所购 SKU 的属性、金额、期限和并发。相同幂等键改选 SKU 或省略 ID 会冲突；后续改价/停用不改变原订单。
- 指南不再修改价格、期限或交付设置。人工代充收集买家资料，成品号由管理员交付账号；自动交付只选择已登记的集成，商品编辑器不接收任意服务 URL/凭据。
- 保存失败展示服务端安全业务原因，编辑器保留修改。修复的是已复现的 6 元约束；实际线上请求没有采集，仍可能出现正常的版本冲突或必填校验。

## 配套自动交付与恢复契约

新原生请求在原绑定末尾增加可省略字段：`sku_version:2`、`duration_seconds`、`concurrency`。
`price_fen` 保留，但版本 2 不再以它推断权益。Ready 必须返回 `sku_versions:[2]` 才可
创建可配置 SKU 的收银台；旧接收端拒绝准入。旧版本省略新字段，历史 JSON 顺序和
摘要保持一致；带新字段却不声明版本 2、未知版本、无效金额或权益均拒绝。

通用 HTTP 动作可通过可省略的 `order.sku` 获得所购 ID/属性/有效期/并发，金额仍使用
`order.amount_fen`。历史订单不补入字段，不改变其快照散列、付款时刻、动作 ID、
加密交付、重试/撤销或资金栅栏。`weekly_enabled`、`max_concurrency` 只限制新销售，
不撤销旧订单权益。

配套源码为独立 `new-api-daypass` 的 guard.5.2 扩展，已在本地修改并验证；本仓库保存
可复现补丁 [`new-api-daypass-configurable-sku-v2.patch`](../../deploy/patches/new-api-daypass-configurable-sku-v2.patch)，
SHA256 `9115702c5bb54c05f9a4bd073c5b900d26be6514ef25c08b8a8ebfb14e1fceae`。
补丁使用零行上下文，应用时使用 `git apply --unidiff-zero`；内容与已测源码完全一致。
补丁基于其完整对应源码 archive SHA256
`42261afa4ad0cbae22c5ebf86dd77c273496280f3112c647f46df00ebca51a1a`。
源码/测试路径与许可见上文及该项目 `docs/upstream.md`。

后续获准发布时，先按各自项目部署记录定位目标和边界：兼容接收端 → 验证私有 Ready
版本 2 → Sub2 migration274 与兼容程序 → 管理员保存 SKU 并验收新购。版本 2 订单出现
后不得回滚到不识别该绑定的程序；保留全部交易/回执数据，不能恢复旧 DB 抹去订单。
接收端部署必须同时提供完整 AGPL 对应源码。这里记录发布前提，没有执行发布。

## 验证与独立复核

| 范围 | 实际结果 |
|---|---|
| 已复现价格 Bug | 同一数据库保存非 6 元原生商品的测试修改前红灯、修改后通过；重载保持 9.9 / 49.9 |
| Go 订单/交付回归 | `go test -race -tags unit ./internal/service ./internal/handler ./internal/shopaction -run 'TestShop\|TestPaymentShop' -count=1` 通过 |
| 最后 SKU 边界补充 | `go test -race -tags unit ./internal/service -run TestShopConfiguredSKU -count=1` 通过，含 nil/空属性重复拒绝 |
| Sub2 PostgreSQL | `go test -race -tags integration ./internal/service -run '^TestShopPostgresAtomicDeliveryAndRefundFences$' -count=1 -v` 通过；真实迁移274重复执行、JSONB重载、选中快照与 revision，以及原人工/退款/竞态测试 |
| 接收端 | `python3 ops/test-daypass-databases.py` 通过；真实 SQLite/MySQL8.4/PostgreSQL16，含同价不同权益、回放、冲突、过期、撤销、历史摘要；临时容器已删除 |
| 前端 | Shop 全套及商品管理测试 72 项通过，含部分 DeepSeek 模板补全；i18n 3 项、类型检查、生产构建通过；改动文件 ESLint 通过 |
| 浏览器 | 真实组件＋本地合成 API；4个 SKU 分别改价9.9/29.9/12.9/49.9，保存并重开 revision2 保留；买家周卡+3路=49.9；390px无页面横向溢出，明暗桌面/手机截图 |
| 视觉复核 | 唯一 material fix 为深色 SKU 辅助字对比度；改用 gray-400 后重拍，复核 resolved，最终 `ship` |
| 正确性复核 | 独立审查 SKU、计价、锁行、幂等、快照、新旧原生协议和 HTTP 元数据，`CLEAR / APPROVE`，无阻塞项；最后 nil 属性归一化和模板属性统一亦复核通过 |

普通应用扩展未另做视觉候选稿；一次检测器输出为空。因专用 impeccable reviewer/documenter
profile 不可用，使用独立通用代码审查员及文档执行者替代，未继承实现会话。
设计记录 [`DESIGN.md`](../../frontend/src/components/shop/DESIGN.md) 与其局部 sidecar
保存真实组件样式。审查通过仅覆盖上述本地验证范围，不含线上真实收款或生产发布。

截图存于 [`artifacts/shop-sku`](../../artifacts/shop-sku/)，其中
`sku-desktop-light.jpg` 为完整价格表，`sku-desktop-dark-after.jpg` / `sku-mobile-dark-after.jpg`
为对比度修正后结果；其余包含目录、交付设置与买家明暗手机/桌面。所有商品、账号与
金额均为合成数据。预览入口：运行前端 Vite 后访问
`/previews/shop-sku/index.html#/admin/products?tab=shop`，不连接真实付款或客户资料。
