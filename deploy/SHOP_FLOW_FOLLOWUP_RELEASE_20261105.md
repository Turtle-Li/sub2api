# 商城第二轮反馈发布记录（工作项日期 2026-11-05）

发布完成：规范工作流 [41922974101](https://github.com/Turtle-Li/sub2api/actions/runs/41922974101) 成功，源码 `0234aefc8a0d2677366a4d13a83e8429c05ffab8`，版本 `0.2.21`。流水线于 `2026-11-04T22:20:42Z` 完成应用切换，随后 Caddy 配置接收成功。后续仅记录文档的提交不改变已部署源码，不触发另一轮发布。

## 来源与运行状态

- 活动 green：`sub2api:auto-20261104-142010-0234aefc`，OCI source `https://github.com/Turtle-Li/sub2api`，revision 与发布源码一致；healthy/running，restart=0、OOM=false。
- 节点：traffic=accepting，active_container=sub2api-green，background=active。
- 压缩归档 94771127 字节，SHA256 `0b53a3e15473518830e0a6c50fe428e8b4dbca432da3a6e44fb432d2cad753ad`。
- 加载镜像 ID `sha256:186ce344993302723b900442c9a4c93ab0d29d053ad08809957ced9f96a6df0e`；传输身份使用归档 digest 与 OCI 标签，不能仅凭 daemon 本地 ID。
- Caddy receiver：相同源码，active_slot=green，请求 digest `sha256:8469457ee548ed85c3cb88f0a61359d32ee2902a6ef3919ce87acf2752857257`，最终配置 SHA256 `bb81db5a19648a277a0288c3f51abb31c0c81104bb94046e1fc31c979eae5baa`。
- 生产仓库/分支仍为 owner `Turtle-Li/sub2api` main；只使用 AWS 规范受限 receiver，无生产源码检出、安装依赖或应用编译。

## 修改与验收

详见 [逐项反馈清单](../docs/operations/SHOP_FLOW_FOLLOWUP_20261105.md)。支付成功事件携带服务端确认的订单身份，各购买入口直接进入本单；固定返回页在 HTML head 提前隐藏。Session 操作说明统一为先登录 ChatGPT、同浏览器打开 Session、复制全部内容并提交。指南固定目录、收紧顶部、加宽正文并提供返回入口。单条账号----密码在后台预览、买家分别复制，原交付内容和版本校验保持不变。

前端冻结24文件 SHA256 `2198f1043023627791a70b1bba31dfea3e06249b43e65b5d47581ecb587b23ba`，可执行基线 f03，另安全整合只含部署记录的8b89b809a。开发13文件290测试、ESLint、i18n、vue-tsc及生产构建通过。独立 QA_PASS 与 REVIEW_PASS 使用相同最终冻结版本；此前发现的 URL/缓存类型导航和多标签全局恢复错单问题均修正，失败报告保留。独立视觉 PASS 限实际展示状态。

本地实际浏览器验证深浅色、桌面/手机、指南长滚动与重复切换、返回订单、账号/密码准确复制、后台分栏预览和支付成功自动进入操作页。交付原文及 revision/input_revision 另经真实本地 HTTP 接收端核验。所有资料与付款状态均合成，未真实付款或在生产发货。

## 线上检查

- www/API 两个公开 health 均200、返回 JSON。
- 商城、商品1、成品号指南、付款返回四个页面均200，入口为 `/assets/index-CQeUJRW2.js`。
- 17个受影响 JS/CSS 资源逐字节 SHA256 与最终本地生产构建相同，入口 SHA256 `0b52078fda279f6fcece5c00c1a43a2d5ac1fe69e936a12bcb4a397dba502023`。
- 四页的提前隐藏脚本位于 body 之前；占位 nonce 已替换并被相同响应 CSP 授权。
- 买家订单、管理员订单/商品/集成四个匿名接口均401、no-store。
- 实际浏览器打开商城进入预期的登录页；没有绕过登录检查生产订单或读取真实账号资料。
- 代充与成品号仍启用、各10分/1个月、revision=1；既有商品3保持600分/1天、revision=3。发布前后完整公开配置元数据一致。
- 现有 shop_actions.enabled=true、SHOP_DAYPASS_ENABLED=true 保持；payment/Feishu/daypass agents 均健康且零重启，既有只读凭据挂载保留。未新增权限、商品或集成。

真实支付宝提供方中间页仍须买家付款验收。本次证明本站返回页首帧提前隐藏，不能据此宣称跨域提供方全链路零闪屏。邮件由用户确认正常，本轮未重发或修改邮件逻辑。

## 备份与兼容恢复

既有备份 `/opt/sub2api-db-backups/sub2api-db-backup-20261105-031632.tar.gz`，338082718字节，SHA256 `2a3f1908e62ec7acd738c99043189b698e80c5f71a8cab50f8fe0c417167298b`；备份与 NAS 同步服务成功。本轮没有新增数据库迁移；已上线迁移266的结构在发布前确认存在。

前一兼容 blue 镜像 `sub2api:auto-20261104-134213-f03ed1a3`，源码 `f03ed1a3854aa13c9a21cf1d8547a3f39243e897`、ID `sha256:3c4379334a4185329090df8561d4b3ff8daee3f1fd64c0250bf4df2479df4eb9` 已规范排空并停止，零重启、非 OOM。其退出后的 health 标志不代表正在服务的故障。恢复只能通过规范维护锁与 action-aware 版本；保持现有集成、收据、原到期时间及撤销能力，不能恢复启用自动交付前的旧数据库或不识别订单动作的旧二进制。参照 [已上线自动交付边界](SHOP_PAYMENT_ACTIONS_RELEASE_20261105.md)。

买家测试入口：[商城](https://www.turtleligpt.com/shop)；人工交付入口：[后台商城订单](https://www.turtleligpt.com/admin/shop/orders)。
