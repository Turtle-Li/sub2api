# Codex 按量 0.25 — 2026-09-12

已于 16:52:34 CST 生效。用户确认：原有 Codex 按量分组从 0.03 改为
0.25，模型保持原来的美元报价，直接乘倍率扣人民币；¥500 对应 $2000
参考用量。不增加设置、字段或价卡开关，不再次转换已有余额。

## 生效范围

- 仅 standard + OpenAI 分组的 CNY 钱包结算去掉额外 ×6.75。沿用既有分组、
  用户、独立媒体倍率及 Free Fast 规则。实际分组为 6 和测试分组 16。
- 线上仅修改 group6 的倍率和名称：`0.0300 → 0.2500`、`按量-0.25倍率`。
  测试分组 16 的 0.01、Gemini 7 的 1.1、Kimi 18 的 0.8 均未修改。
- 订阅分组及其钱包回退、其他平台维持原规则。全局 CNY/6.75 设置和未来
  1:1 充值规则保持。发布后 payment_enabled 和 purchase_subscription_enabled
  仍为 false。
- group6 无分组价卡、渠道或用户单独倍率，Free Fast/profit-control 均关闭。
  模型目录未改写；发布前后 SHA-256 均为
  `28a7152aa49b3f094cfe01977a5fa9d5941004d7f317186b60636c1309d73fbf`。

## 代码、制品与发布

- [PR19](https://github.com/Turtle-Li/sub2api/pull/19)，源码
  `617c150321ace14db0c03c4fe7241c0fc4640c0c`，合并/运行版本
  `3f61330af2a11580e74191d1cf1cd7f74357886b`，完整 tree 相同。
- 独立 QA/Review、针对性回归通过；CI 34683008881 五项及 Security
  34682998220 两项全部通过。已尝试 Claude，但其 provider 返回无可用账号
  503，没有取得新的 Claude 审批。
- GitHub build-only 34683547469，artifact 10295380803。ZIP 83,865,752 bytes，
  SHA-256 `62723b9066125a1484acaf72e4c1b3558f5c7618b9c369b00b346e03475f00b3`。
  镜像压缩包 83,864,933 bytes，SHA-256
  `9d33f51f17aa020867b6596273a3a148fe65dbe7bf518df97e5a851df4e604da`。
  已验证 28 个 OCI blob、13 个 layer、linux/amd64 及 source/revision/version。
- 只在 `sub2api-candidate` 用已安装 canonical receiver 发布；无生产编译。
  blue 于 16:48:18 验证通过，旧 FX green 于 16:49:19 正常退出；再以同一
  镜像更新 green，于 16:51:55 验证通过。两次 app_5xx/app_fatal/caddy_5xx
  均为 0。随后在同一 origin 维护锁 FD8 下，由继承锁的 psql 提交 group6
  CAS，刷新 10 个 Key 的 auth 缓存并等待既有倍率缓存 TTL。
- 最终 active green，accepting/background active；blue 已正常停止，两槽
  均为上述兼容版本。Docker image 为
  `sha256:01a83f80dc91437c657e69662bf427e445ab9458f6bd316971441d7af5ad2578`。
  restart=0/noOOM。Caddy 配置最终哈希与发布前相同，站点路由和模型目录保持。

## 真实扣款与观察

专用 release Key 暂绑 group6，经本机 loopback443 发出一次实际 Responses
请求，同时验证 api.turtleligpt.com 的 SNI/TLS；结束后恢复原测试分组并刷新
缓存。HTTP 200，模型 gpt-5.6-sol，usage 623912：

`560 × $0.000005 + 230 × $0.00003 + 3840 × $0.0000005 = $0.01162000`

`$0.01162000 × 0.25 = ¥0.00290500`

独立按原目录 token 单价计算、usage 行、实际钱包差额完全一致；累计充值
未变化。独立证据复核通过。切换瞬间的一条在途请求仍按旧 0.03 少扣，符合
用户允许短时少扣的要求；不补扣。后续请求使用 0.25。

16:55:19 被动复核迁移后 219 条余额、1486 条订阅请求：29 个钱包均落在
日志/命令金额精度推导的精确区间内；币种、倍率公式、组成项、去重及账户
参考基数均无差异。活跃订阅窗口增加量匹配，钱包未因订阅消费扣款。监控
明确记录混合版本窗口 usage 623872–623896，之后要求 OpenAI standard
直率结算；历史记录仍按当时规则验证，不回写历史。

## 备份、证据与回退

- 数据机 `sub2api-db` 保留 root0600 配置备份：
  `/opt/sub2api-migration/codex-rate-20260912/groups-before-20260912-162409.dump`，
  4,386 bytes，SHA-256
  `5f516fd894e444bc6b8f6ecc55479a6b03ae55aecd3751c2c542823a58cb77a3`，
  pg_restore list 校验通过。配置 CAS/测试/对账证据存于同目录的 acceptance。
  初次 CNY 迁移的完整数据备份及 manifest 仍按原记录保留。
- 应用制品和发布日志：
  `/var/log/sub2api-release/direct-rate-20260912-3f61330a/`；两次 receiver
  原日志分别为 `gha-20260912-164740-3f61330a-1466828` 和
  `gha-20260912-165110-3f61330a-1472178`。
- 回退优先使用已保留的同版兼容槽。不得把旧 FX 代码与 group6=0.25 配对；
  如确需旧代码，必须先仅将该组 CAS 回 0.03 并刷新缓存。禁止恢复旧 USD
  数据库或重跑余额迁移。Runtime-guard timer 延续 inactive 状态，没有在
  本任务中启用；不宣称定时自动恢复已生效。

## 18:04 消费显示修正

用户随后给出实际消费明细截图，确认仅统一消费显示的 `$` 参考符号，保持
原始数值，不按标签再换算。总消费恢复已有粗略汇总。模型价卡、实际 CNY
钱包/扣费、持久化 currency、导出、倍率和支付配置都不修改。

- PR22 源码 `df5101c691f1196217b1038645bb22d1e0e6f31e`，运行合并版本
  `f9f9615a92c4fffb619a2a5955dba24bb6b64a42`。仅前端/文档；后端及 deploy
  与支付版本 `7ffd65d66` 完全相同。独立 Review、50 项聚焦测试、typecheck、
  ESLint、完整前端 build、实际组件浏览器检查通过；CI34686230132 全部通过。
- build-only34687016892，归档83,897,308bytes，SHA-256
  `8da0c816da9a7ff30aa090323966f271d51c4baf805a07b506eed55e86e28fa7`，
  ZIP及28blob/13layer与版本标记验证通过。canonical receiver 于18:04:10
  验证成功，active green，镜像
  `sha256:9173b00a903a31002e056b7174e6a506cfc5549333f9fba7eea77a6f45f14ada`，
  healthy/accepting/background active，restart0/noOOM，三类发布错误计数均0。
- 回退保留支付兼容的 blue `7ffd65d66`，由独立 drain monitor 等待现有连接
  自然结束。18:06读取时仍在正常 drain，不强停连接。不可用旧 `3f61330af`
  回退而保持支付打开，因为它不认识隐藏支付入口设置。
- 浏览器实际组件验证与截图相同数字：输入/输出$5/$30，原始$0.102952，
  0.25倍后$0.025738。线上消费/仪表盘/金额工具资源与本地已验证 build
  逐字节一致。Chrome在线会话自动化超时，未把本地浏览器截图宣称为线上截图。
- 18:06只读核对 group6=.25，pay=true/entry=false/purchase_subscription=false
  与发布前一致，用户2受限支付目录由原支付任务维护。最新 usage624349：
  0.093346×.25=0.0233365，CNY持久化不变。未手动发起额外付费测试或余额写入。
- 受保护归档/receiver日志分别在
  `/var/log/sub2api-release/usage-display-20260912-f9f9615a/` 和
  `/var/log/sub2api-release/gha-20260912-180339-f9f9615a-1521940/`。
  本地验收证据 `evidence/native-pricing/`。只需刷新页面获取新资源，无数据迁移、
  无计费缓存清理，钱包余额继续显示人民币。
