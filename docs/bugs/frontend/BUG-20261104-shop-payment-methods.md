# BUG-20261104-shop-payment-methods

状态：ANALYSIS_READY；基线 `3b348d79e`（生产业务代码 `76d6158e6`）。

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
