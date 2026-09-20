# 支付修复与自动权益退款发布 — 2026-09-20

状态：已发布。Sub2于08:52:52+08完成蓝绿切换，08:55:26+08启用自动权益退款；中央每日退款不限额已生效。用户已授权测试通过后发布。

## 范围与来源

- P01–P18：优惠码输入/清空/审计、订阅推荐与续费、订单详情与弹窗、开票退款互斥、重置卡15天与交互、支付宝站内二维码、中文支付商品说明。
- P19：履约快照保存实际赠卡ID与历史并发；退款自动审核、预占、成功回收、失败释放；已用赠卡拒绝自动退款；并发按来源重放，保留后续合法调整。
- 运行时源代码：`f8c209764c46ccd7b791b90efd6f51b5543ed09b`，版本 `0.2.7`。不使用先前818e72240构建。
- 同源门禁：CI `35478690004`，Security `35478692208`，build-only `35478694051`。三个运行全部PASS。
- 源码独立financial/cache/UI/forward-guard review通过；最终lint/test delta复核通过。全量service/repository/migration unit、真实PG退款与用卡/并发缓存边界通过。前端全量2662测试与后续定向回归通过。

## 备份与边界

- 发布前canonical备份：`/opt/sub2api-db-backups/sub2api-db-backup-20260920-035430.tar.gz`，288402827字节，SHA256 `42f13644bf78ec373d5cf4f251887d97257b059c239e7b5f39f112b86bbf5127`；隔离PG/amcheck/Redis恢复验证通过，schema_count311。
- 只发布当前 `sub2api-candidate`；不使用已过期sub2api-new，不改变DNS、账户配置或Vault代理。
- 旧运行时b7fa7f4a在blue；canonical receiver负责验包、候选健康、真实请求门禁、Caddy切流和自然drain。新自动退款只在旧writer全部退出后启用。
- migration255和财务/来源记录必须保留；回滚必须走既有drain/readiness/gate流程，不删除新表或伪造历史快照，不强关WebSocket。

## 中央服务

统一支付R3b于06:59:16+08发布成功，migration22已执行。仅Sub2 live支付宝/微信绑定启用每日退款不限额（0加显式opt-in），其他产品默认0语义保持。七服务healthy/restart0，四Vault代理身份保持；发布前后加密备份验证及隔离恢复通过。详见中央项目 `docs/operations/SUB2_UNLIMITED_DAILY_REFUND_R3_20260919.md`。

## 验收范围

本轮不执行真实用户扣款或退款。浏览器工具认证不可用，未声称真实商户二维码视觉验收通过；官方SDK契约、嵌入/手机兼容、fallback及服务端支付状态有自动化验证。

## 实际发布结果

- canonical receiver退出0；日志 `/var/log/sub2api-release/gha-20260920-085234-f8c20976-68987`。
- 活跃容器 `sub2api-green`，ID `32f03b21e65615fe49f9a0ffac4fe2403754dbcda9c38f4b54f5ce56c0dacbb9`；image `sha256:bcd5a00ff9b6874866286140672657e4b5dbf427738a9437d6b90e43ffb4a68b`，tag `sub2api:auto-20260920-085234-f8c20976`。
- traffic=accepting/background=active，healthy/restart0/noOOM；旧blue由drain流程自然退出exit0，未强制断开用户连接。
- forwardpause在旧镜像/唯一writer/零预占验证后CAStrue→false，普通请求保持accepting；最终canonical helper确认新唯一writer并CASfalse→true成功。
- migration255已记录，runner按TrimSpace内容计算的checksum `271a58466c368a44e400d067a0517b52e4fdd0f1e2aa1187c3c1a2fb20ada318` 与本地一致；原文件SHA256 `fe22f4f5f7252af380740d54bdb6ecaf4e4274757792e11f04b8624e018e4e22`。现有34用户并发基线就绪，未凭空补造历史发放快照。
- 激活前后Redis ready=1/reconcile不存在；最终refund_gate=true，reserved_benefits=0，reviewed_reservations=0。内部readyz=true，refund readiness=true/0。
- canonical发布audit app5xx/fatal/Caddy5xx均0；API和WWW公网health均200。curl读取login及新入口 `/assets/index-CP08wpUl.js` 成功（194460字节）。本机Python默认UA读取login遇403，curl正常；不将该自动客户端拒绝误报为应用故障。
- 支付/飞书Vault代理ID分别保持 `0da3d88c27a369a879148c9f2d494dc33243cf4702296f7e569b5a08b18f238a` / `cf264bc94e12dee43beb20adf56ca7c7ee5de11a342434eb7e84094716164d98`，healthy/restart0。
- 无真实客户扣款/退款操作；商户二维码视觉仍待用户实测。

### 激活前只读检查

独立缓存审查确认：除准确新镜像、唯一writer、Caddy三视图和退款readiness外，需要直接在应用相同Redis DB读取 `concurrency:authorization_ceiling:ready` 为 `1`，`EXISTS concurrency:authorization_ceiling:reconcile` 为 `0`。公共readyz只验证流量状态和DB/Redis连接，不能替代此检查。mutation:users集合可以非空（进行中操作对相应用户保持保护），不能通过删除marker来获得通过。激活前数据库flag必须为false，再由canonical helper执行CAS。所有探测使用现有保护注入，不打印监控令牌或Redis密码。

### 固定制品

GitHub artifact10594729546（run35478694051），zip84630013字节，SHA256 `a2e171b1d6a1d3dc8d5aabb5a16cce93ad8183dde3d440562dc89d89bdbdd451`；内层Docker archive84629194字节，SHA256 `82bb8ac2417172af58acfb516ab82478204713a13ad156722f31fa6533491a47`。下载在候选主机完成，外层和内层均核验；metadata精确匹配commit/version/source/platform/run。发布包装只调用SHA256 `8ccb62ae77776ff5f3298b3dc5ff0b3599ac5dc344db649f8f68f4a439f941d8` 的已安装canonical receiver；独立编排检查无阻断。制品内容独立QA PASS：28内容哈希、13层、OCI/config/diff_ids绑定、实际ELF与嵌入前端验证通过；app二进制SHA256 `7fd7407201507752e6b5dd68dc7b6f1987bf269879288601a645e5cc5cfbd74c`。
