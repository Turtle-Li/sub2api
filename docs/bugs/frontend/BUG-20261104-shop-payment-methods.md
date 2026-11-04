# BUG-20261104-shop-payment-methods

状态：RESOLVED；基线 `3b348d79e`（原生产业务代码 `76d6158e6`）；修复 `0df04a812a86d03e57c78efea470cb603c6ebf74` 已上线。

Bug: 商城商品详情隐藏所有可用支付方式。

现象: owner 在生产商品详情看到“暂无可用支付方式”，购买按钮禁用，无法进行 0.10 元流程测试。

复现条件: `/payment/checkout-info` 返回支付宝或微信 limits 对象，未返回 `available` 字段。这是正常服务端契约，统一支付及 provider-instance 路径都使用同一个 `MethodLimits` 结构。

调用链: `PaymentHandler.GetCheckoutInfo` → `GetAvailableMethodLimits` / `ApplyUnifiedPaymentPresentation` → JSON `methods` → `ShopProductView.load` → `getVisibleMethods` → `.filter(key => visible[key]?.available)`。

影响范围: 专用商城商品详情；已有充值页使用 `available !== false`，没有相同隐藏问题。资金、支付回调、人工履约及商品配置不受此次显示错误影响。

疑似根因: 已由静态契约直接确认。Go `MethodLimits` 只返回 currency/fee/limits，不包含 available；前端共享类型却将它声明为必填。商城要求该字段为真，导致正常响应被全部过滤。现有测试固定返回空 methods，未覆盖正常返回。

推荐修改方案: 将 wire 类型的 available 改为可选；仅从服务端实际返回的支付宝/微信键中选择可用方式，明确 false 时排除，缺失字段不当作禁用。复用既有支付选择器；金额限制和 CNY 由商品详情按实际商品金额判定，最终准入仍由服务端决定。增加实际未含 available 的 limits 回归，验证可选择并提交正确支付方式；保留空配置及明确禁用的关闭行为。

风险: 不得凭 available 缺失凭空添加未配置渠道，不得把后端禁用或金额限制变成可购买。此次不修改支付协议、密钥、生产配置、金额或资金状态。

证据: `backend/internal/service/payment_config_service.go:195`、`payment_service.go:373`、`frontend/src/views/shop/ShopProductView.vue:33`、`views/user/PaymentView.vue:1232`。固定上游 `Wei-Shaw/sub2api@bdb42e22f81fcb633ff0a060961211dd2bcb515b` 的 PaymentView 同样依据真实方法键构建列表；本地统一支付和人工履约差异继续保留，未改上游协议。

知识检索: 既有 `BUG-20260910-payment-fulfillment-recovery` 涉及履约租约，与本次前端 methods 字段无关；此前无匹配前端缺失字段案例。

Developer 修改目标: 修复可用方式被隐藏，并用 Gemini 4 优化商城/详情卡片。修改文件为 `ShopProductView.vue`、`ShopView.vue`、共享 payment wire 类型及商城测试。验证正常缺失字段、显式 false、无方法、金额边界、正确下单 payload；随后进行明暗主题/手机/桌面真实浏览器和独立 QA/Review。支付状态、资料校验、发货、教程及重复提醒约束保持不变。

## 修复与验收

- 已采用上述可选字段及实际渠道、币种、金额判断；下单按钮再次校验当前选中方式，失败信息显示在按钮旁。未修改后端、支付配置或商品价格。
- Gemini 4（请求及返回均为 `gemini-4-argon`）生成商城及详情卡片模板，整合既有主题、支付选择器和必看教程入口，并调整卡片对齐及重复信息。
- 独立 QA/Review 对同一六文件清单通过；最终商城回归 22 项通过。支付相关 27 文件、336 项回归通过；生产构建、类型及相关 ESLint 检查通过。
- 实际浏览器检查桌面、390 宽手机视口及明暗主题，支付宝/微信可见可选，购买按钮可用；键盘选择及焦点正常。浏览器未提交真实支付。
- 发布工作流 `41823520372` 于 `2026-11-04T11:56:41Z` 成功，健康 blue 容器运行精确修复 revision，流量接受、后台激活，重启/OOM 均为零。公开商城、详情及教程 HTTP 200，匿名商品接口 HTTP 401/no-store，新商城及详情脚本 HTTP 200。
- 生产只读确认两款商品仍为 10 分，支付宝/微信均启用且走统一支付，最低金额 0.10 元、手续费为 0。真实付款、手动交付及邮件/飞书送达由 owner 验收。
