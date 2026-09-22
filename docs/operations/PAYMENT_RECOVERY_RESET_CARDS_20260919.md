# 支付恢复、订单到期与重置卡购买（2026-09-19）

状态：已于2026-09-19 20:09:47 +08发布v0.2.7，运行提交`ebf5e2e63cbfe381d920418864a606a0e02f4480`。

本轮 owner 明确授权修复后合并上游并发布。自动升级因历史付款及重置卡来源无法完整证明，按 owner 允许的管理员兜底处理；决策详见 `PAYMENT_ORDER_CATALOG_RESET_CARD_DESIGN_20260914.md`，不得用普通订阅新购模拟升级。

> 2026-09-22 用户取消契约变更已完成本地验证，尚未发布：见 `PAYMENT_LOCAL_CANCELLATION_20260922.md`。下文记录的是 September 19 已发布行为；新的本地取消与迟到付款退款必须成套发布，不能只提前改状态。

## 支付与订单契约

- 取消请求通过归属/状态校验后，先记录本地 `PAYMENT_CANCELLATION_REQUESTED` 意图，再请求支付服务；尚未可信关闭时保留 PENDING 并展示 `cancellation_pending`；不能把受理当成已取消，不释放已预占优惠券。
- `POST /payment/orders/:id/resume` 先验证登录用户及订单归属、状态、原截止时间和权威支付状态，再返回原订单已保存的 QR/URL。不得创建新单、重新计价、延长有效期或用缓存地址绕过确认。确认中、取消处理中、已支付和已关闭均不得恢复。恢复在外部查单后再次读取本地取消意图；取消意图先于外部关单写入，避免两个请求交错返回已经失效的链接。
- 前端倒计时到零请求权威核验；未知结果展示确认中，不将本机时间当作已取消依据。订单列表定向刷新正在处理的订单，同步可信关闭/付款结果。
- 历史 order #5 为旧创建失败分类留下的未绑定统一支付 reset-card 单。通过原 product_order_no 做签名范围查询；找到时验证币种/金额/类型/支付方式/产品范围并 CAS 补回原 UUID。未找到时仅在无付款、无 checkout、无优惠券、无活跃 dispatch、原范围匹配且超过两倍签名时钟窗口后 CAS 过期。不得因任意404或网络失败直接取消，更不得补建重复支付单。
- 已签名发出的支付宝收银台在原截止时间前不能仅凭 TRADE_NOT_EXIST 判定关闭。对应统一支付修复及迁移21见其 `docs/operations/ALIPAY_CANCEL_RECONCILIATION_20260919.md`。

## 重置卡购买目标

使用与订阅一致的选择卡片和右侧统一支付区。选中重置卡时隐藏优惠码，显示购买数量、有效期和可选“购买并使用”。数量及自动使用意图必须进入服务端不可变订单快照、幂等校验和微信签名恢复上下文。可信付款履约事务发放所购数量，勾选时仅使用新发批次一张；重复回调不得再用，不能由浏览器另发 use 请求完成此承诺。

## 验证与发布记录

开发阶段：订单恢复/取消/缺绑定恢复有针对性单元测试；前端本地实际组件样例用于验证倒计时、恢复二维码、取消确认和确认中状态，不调用真实支付。最终测试、独立审查、上游合并及线上镜像见下文实际发布记录。

### 发布前证据

- 支付恢复/缺绑定及数量/自动使用后端独立审查通过；真实 PostgreSQL 覆盖买3用1、旧卡不消耗、重复回调不重复发放/使用、暂停订阅保留所购卡并记跳过审计。
- 桌面1280与手机390本地实际组件检查：输入3后立即勾选保持3张与¥120，选中重置卡隐藏优惠码，手机二维码及原订单倒计时可见；样例全部使用本地模拟接口。
- JSAPI关闭支付窗口不再误报订单已取消；重置卡快照分别标记商品总价及单价。
- 合并前全后端 unit 唯一失败为既有附件测试清理竞态：编码slot释放早于后台cache落盘，测试仅等待slot就删除TempDir。独立分析后仅修测试等待flight落盘结束；隔离复现并修后50次通过。生产附件逻辑未改。go vet ./...通过。

### 上游 v0.2.7 合并

固定上游 `1a9d49e16f7a22c432b428fce4af8d731f1fa364`（包含v0.2.7版本同步），在本地支付修复 `c095e5fec` 后合入。9处冲突：

- `.gitignore` 合并双方文档例外；Go模块保留本地x/text直接依赖并运行tidy去除旧校验记录，接纳上游smithy直接依赖。
- `AmountInput.vue`及测试保留固定充值档位，不恢复上游任意金额输入；`AdminRefundDialog.vue`保留本地服务端审核报价/权限/审计路径，不引入旧balance不足前端判断。
- `BaseDialog.vue`保留共享唯一ID和嵌套body锁；`ChannelMonitorView.grok.spec.ts`保留动态PROVIDERS数量断言，覆盖上游新增provider。
- `UserOrdersView.vue`保留本地状态/履约/发票筛选及handleFilterChange重置页码，覆盖上游同目的修复并保留新恢复/取消交互。
- 自动合并的payment store接纳并发configPromise；统一支付gateway、webhook inbox、到期worker、退款恢复、每月重置卡发放及cleanup链经独立审查未丢失。上游没有新增Ent/schema迁移，未启用插件能力不在本次扩大配置范围。

前端独立审查最终153项通过；三项界面P2已修复且复审通过。合并后的全量测试及线上记录如下。

### 合并后验证

- `go test -p 4 -tags unit ./... -count=1`、`go test -p 2 -tags integration ./... -count=1`、`go vet ./...`、embed web/server测试均通过。
- 前端lint、生产构建（含vue-tsc/i18n）通过。全量334文件中333文件/2598项通过，新增上游退款余额测试旧契约的4项已适配为服务端review边界，相关退款套件48项全部通过；其他测试无失败。
- 合并的前端冲突区域独立定向检查37项通过；通用弹窗及本地退款审核契约14项通过。退款测试适配后typecheck再次通过。
- 发布前线上为95075af9；预发布备份 `/opt/sub2api-db-backups/sub2api-db-backup-20260919-192813.tar.gz` 已完成，PostgreSQL和Redis隔离还原验收通过（schema_count311）。

### 新快照的降级边界

旧版本会拒绝多张重置卡快照，但会忽略单张的`use_on_purchase`。因此本版扩展现有monitor-token-only `/internal/refund-rollback-readiness`，新增可选`unsettled_reset_card_purchase_count`；只要新格式reset-card订单未终结，返回503且`ready=false`，沿用canonical receiver的drain/readiness/保留候选流程，禁止旧版本接管。数据库读取失败同样拒绝回滚；不修改金融状态来清空门槛。

缺少schema_version字段按历史格式处理；显式null或未知版本视为不可降级。统计保守覆盖所有非v1快照（包括v2单张未勾选使用），而非猜测将来的字段兼容。COMPLETED、REFUNDED、PARTIALLY_REFUNDED为终态；CANCELLED/EXPIRED仅当paid_at为空时允许排除。PENDING、PAID、RECHARGING、FAILED及未知状态均阻止降级。常规发布和购买不受影响；计划降级须在同一维护锁内drain后，先由兼容版本完成履约/可信关单再检查。若有不可恢复异常，保留兼容版本前向修复，不跳过门槛、不删除审计或订单。

本门槛不改变旧字段与零值JSON；canonical脚本已拒绝任何非2xx/ready=false响应，无须修改服务器脚本。原实现的新增单测已实际失败（新订单存在仍Ready=true），修复后服务端/readiness路由和真实PostgreSQL状态矩阵必须通过才重建镜像。

门槛验证：service/repository/routes单测、embed路由和go vet通过；真实PostgreSQL与全部ResetCardExternalOrderPostgres合跑通过（7.020s）。状态矩阵包含v1/缺版本不拦、v2各未终态与已付款取消/过期拦、未来v3和未知状态拦，以及保守的v2单张不自动使用也拦。原候选956512264的CI35440405229、安全35440404805、镜像build-only35440405117均通过但未部署；最终采用包含本门槛的ebf5e2e63镜像，证据如下。

## 实际发布与线上验收

- 最终运行提交：`ebf5e2e63cbfe381d920418864a606a0e02f4480`，版本`0.2.7`。上游固定合并点`1a9d49e16f7a22c432b428fce4af8d731f1fa364`。
- 同提交GitHub CI `35441367103`、Security Scan `35441366302`、build-only `35441366334`均成功；独立后端/前端/合并/回滚门槛审查通过。
- GitHub artifact `10583289633`（84598213 bytes），外层SHA256 `9977fe6e2b75a0adfa290969881551c92a65dde487a8a47bc0566686ece2df5a`；内层Docker归档84597394 bytes，SHA256 `73d7fd42bcde05eae4b8264c849a12e2064562d66d131ff32992701b2d28bd42`。已逐字节验证外层/内层、成员白名单和来源/版本/平台，未在生产编译。
- 原canonical receiver SHA256 `8ccb62ae77776ff5f3298b3dc5ff0b3599ac5dc344db649f8f68f4a439f941d8`保持不变，使用其维护锁、镜像身份、健康、切换、回滚及排空流程。
- `sub2api-candidate` 当前绿色实例`sub2api-green`，镜像`sub2api:auto-20260919-200928-ebf5e2e6`，镜像ID `sha256:33c98eaf8176e6e97640aa88d3f7ef57a472a73d480a2ac2a3f7929f034a8015`；healthy、restart0，`traffic=accepting active_container=sub2api-green background=active`。旧蓝色由canonical drain monitor退出；支付和飞书Vault Agent实例ID保持不变。
- 发布日志：`/var/log/sub2api-release/gha-20260919-200928-ebf5e2e6-3030257`，app_5xx=0、app_fatal=0、caddy_5xx=0。www/API health、login、purchase均200，未登录`/api/v1/payment/orders/my`为401；公网入口资源`/assets/index-BGMMdFoz.js`。
- 备份：`/opt/sub2api-db-backups/sub2api-db-backup-20260919-192813.tar.gz`，SHA256 `45c987a58096e6b2a25a4538df600d67f6342b8814254a4832cd54327d1f14a2`；外层/内层校验、PostgreSQL实际恢复和Redis加载通过，schema_count311。本轮不增加数据库迁移。
- 线上只读确认：#5在20:10:41 +08由后台写入`RESET_CARD_UNIFIED_ORDER_ABSENT_EXPIRED`审计并转EXPIRED（paid_at仍为空）；#6保持CANCELLED。没有手工重写订单/金融状态，没有发起真实购买、支付、退款或伪造通知。
- 统一支付取消修复已先发布`20260919-alipay-cancel21`并自动关闭相关历史订单，详见该项目`docs/operations/ALIPAY_CANCEL_RECONCILIATION_20260919.md`。本轮线上验证不替代owner实际支付宝/微信付款验收。

## 后续 UI 修正（2026-09-19）

按 owner 后续要求，重置卡复用订阅 `payment-product-card` 外观，数量、到期日、购买并使用一张和选择按钮收进同一卡片，取消独立大选项面板。数量采用减号/数值/加号，禁止手填，保留服务端1–99边界。定位链接只滚动和聚焦，不再假装选中；已选卡仍能重新获取权威报价。优惠码保持原位置，以禁用输入和占位说明表达重置卡不可用，替代此前隐藏行为；不增加说明行导致支付面板高度变化。

我的订单列表和详情使用状态标签：待付/取消处理中琥珀、已付蓝、发放中青、完成绿、取消玫红、过期橙、失败红、退款紫；详情继续独立显示支付和履约事实，不改持久化状态。

本轮仅前端呈现与交互，没有支付、履约、定价、数据库或目录配置变化。本地专项96项通过；生产构建含类型/i18n校验通过。浏览器本地 mock 页面已确认整卡内容与未选中状态的可访问树；浏览器连接认证及系统截屏服务不可用，未取得本轮最终桌面/手机截图，不能声称已完成视觉验收。

### UI 修正实际发布

2026-09-19 23:12:33 +08 已发布 `b7fa7f4af7d876de513f082c5e058f9dd637a5ad`（版本仍0.2.7）。GitHub CI35450409266、安全35450411355、build-only35450413171全部成功；前端及状态色独立审查无P1/P2。Artifact10587260232，外层84614151 bytes/SHA256 `bd3af2f526216856a1013ee45533b7b9cb1d189acb2f4106b7c65b645cafe21b`；内层84613332 bytes/SHA256 `bfbc9798445933ce656744edd3a59baf1f0cebb9aeb490ab7dba1086e3cfa336`。

原canonical receiver未变，已按维护锁与blue-green门槛发布蓝色实例，镜像 `sub2api:auto-20260919-231215-b7fa7f4a`，ID `sha256:2459a5dd25809a8f94053799253974032e522ca6bdf229d3be74610295dcfdcd`；healthy/restart0，支付/飞书Vault Agent实例不变。旧绿色由canonical monitor排空，不手工终止连接。发布目录 `/var/log/sub2api-release/gha-20260919-231215-b7fa7f4a-3327260`，app5xx/fatal/caddy5xx均0。

公网curl检查www health/login/purchase200，未认证orders/my401，API health200；Python urllib被边缘403拒绝，不作为产品不可用结论。新入口 `/assets/index-DbxnZQVt.js`、PaymentView-B8zTOHZY.js含数量加减、UserOrdersView-e5YICLls.js引用新状态色组件，已核对公网实际字节。没有真实金融操作或数据库变更；回滚保留上一版ebf5e2e63及既有guard。视觉截图限制如上，仍需owner实际页面验收。
