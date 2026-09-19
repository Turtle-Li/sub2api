# 充值与订阅卡片预览

从 frontend 目录运行：

```sh
pnpm exec vite --config previews/recharge/vite.config.ts
```

打开 http://127.0.0.1:5199/previews/recharge/index.html 。

打开 `http://127.0.0.1:5199/previews/recharge/index.html?view=coupons` 可查看正式组件“订单管理 → 支付优惠码”（生产路由 `/admin/orders/coupons`）。原注册赠送码仍位于 `/admin/promo-codes`。该视图只提供本地列表、使用记录和审计记录示例，所有写操作仍会被拒绝。

使用实际 PaymentView、AmountInput、SubscriptionPlanCard 与订单摘要组件；外层 AppLayout 替换为本地预览容器。适配器拦截全部 API 请求，拒绝写操作，不创建真实订单。该 HTML 不是生产构建入口。

`catalog.json` 是可直接编辑的示例展示配置，ID、文案、额度、价格只用于预览，不能作为线上覆盖导入文件。月付不赠送；季付一次赠送 2 张；年付每月赠送 1 张，共 12 期。数量、发放节奏、有效期均来自现有 entitlement 字段，组件没有写死周期赠送规则。

支付优惠码输入 `SAVE2026` 可查看本地 20% 折扣报价。预览适配器只模拟 `POST /payment/coupon-quote`；任何创建订单或支付写请求仍会被拒绝。

管理后台的订阅编辑框已支持这些字段：

| 周期 | 每次张数 | 发放方式 | 示例卡有效期 |
| --- | --- | --- | --- |
| 月付 | 0 | immediate | 无赠送 |
| 季付 | 2 | immediate | 3 个月 |
| 年付 | 1 | monthly | 1 个月 |

年付订阅有效期设为 1 年或 12 个月。后台从有效期派生 12 期，保存时无需手工传 `reset_card_issue_count`；预览 JSON 的该字段模拟服务端响应。

调整真实目录时仅合并上述权益字段，保留余额赠送、并发数、购买资格、重置卡等级、重置卡价格等既有配置；不要用示例 entitlement 对象覆盖整个线上对象。删除历史 features 中的“重置卡不延长订阅有效期”，并按实际产品调整 description/features；历史发布快照不修改。

`?view=confirmation` 单独展示真实支付确认组件的优惠明细与二维码布局。
二维码只编码本地预览说明，不是支付链接；查询返回本地待支付样例，任何取消或订单写入仍被拦截。
`?view=coupons` 中的样例订单 #202601 可跳到真实管理订单详情组件，读取同一个本地优惠快照。

后续规则：`catalog.json.groups` 是 Plus/5X Pro 两组额度和倍率的唯一预览来源，套餐只关联 group_id；修改后各周期共用新数据。真实接口每次读取分组数据库，页面在重新聚焦、回到可见状态及切回订阅时刷新。`features` 示例包含“同步官方赠送重置卡”，可在商品配置中调整文案。

优惠码只用于余额与订阅，可在后台选择具体订阅套餐；重置卡不参与。抵扣后的实付按整元向上取整，例如 99 元减免 20% 后付 80 元，优惠明细显示实际减免 19 元。预览仍不保存管理配置或创建真实订单。
