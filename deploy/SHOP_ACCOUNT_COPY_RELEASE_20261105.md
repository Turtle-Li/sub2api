# 成品号复制入口精简发布（2026-11-05）

已发布源码 `2f43bfcf7ecfbe2387d9196ed27f4a7b073d9f37`，版本0.2.21，[规范工作流41976158807](https://github.com/Turtle-Li/sub2api/actions/runs/41976158807)成功。后续文档记录提交不改变该已部署源码，不另触发发布。

## 范围与验证

已识别为单条账号----密码的成品号，只保留复制账号、复制密码；不再重复提供完整原文复制。通用未拆分文本与API Key仍可完整复制。仅修改ShopDelivery的一处显示条件，解析、原交付内容、接口、邮件和订单逻辑保持现有契约。

更新行为测试先红后绿：ShopDelivery与shop视图共39项通过，ESLint、i18n3/3、vue-tsc及生产构建通过。实际深色浏览器合成订单中两个字段复制按钮可见，重复入口消失。按单组件小修流程完成开发者回归与视觉检查；早先独立QA/Review记录对应上一轮流程改版。本次没有真实付款、生产交付或邮件重发。

两个前端文件冻结SHA256 `e916dd0d4ae196ac4229b26284d0c197cc9e20a7f483410675fcc06986e7a234`，基线74ad657bd。

## 线上身份与检查

- 活动sub2api-blue：`sub2api:auto-20261104-215604-2f43bfcf`，source Turtle-Li/sub2api、revision匹配；healthy/running，零重启、非OOM，traffic accepting、background active。
- 归档SHA256 `sha256:d810812a023f1b62c3c9ab60c326f2b565a6ecf979a4a4cc8783a37ccde0ded1`；加载镜像ID `sha256:7731f4a4cc3b6ec27e9e59dc14be7f81d34c16b09b157b36d1018906379e9131`。归档大小在GitHub日志被遮罩，未猜测填值；接收端归档digest/OCI身份校验成功。
- Caddy receiver相同源码、active_slot=blue，请求digest `sha256:8469457ee548ed85c3cb88f0a61359d32ee2902a6ef3919ce87acf2752857257`，最终SHA256 `8469457ee548ed85c3cb88f0a61359d32ee2902a6ef3919ce87acf2752857257`。
- 17个线上JS/CSS与最终本地构建逐字节相同；入口 `/assets/index-BPJrTS86.js`，SHA256 `78bd7d88056720fd26ba6da96b4949637a3c02ea6e7f36247d74b66608cf33b4`。
- 商城/商品/指南/付款返回4页200，既有head脚本与CSP nonce一致；2个health200；4个买家/管理员匿名边界401/no-store。
- 发布前后运行开关及10个挂载元数据相同，payment/Feishu/daypass凭据agents均健康、零重启。没有修改商品、集成、权限、挂载或配置。

## 兼容恢复

前一green `sub2api:auto-20261104-142010-0234aefc`，源码 `0234aefc8a0d2677366a4d13a83e8429c05ffab8`，镜像ID `sha256:186ce344993302723b900442c9a4c93ab0d29d053ad08809957ced9f96a6df0e` 已规范排空停止。本轮无后端或迁移变更。只能走现有维护锁和action-aware恢复流程，保留集成、原收据、到期与撤销能力，不恢复启用自动交付前的旧DB或不认识动作的二进制。沿用[上一轮发布及备份记录](SHOP_FLOW_FOLLOWUP_RELEASE_20261105.md)与[已启用自动交付边界](SHOP_PAYMENT_ACTIONS_RELEASE_20261105.md)。

操作说明：[商城与人工交付](../docs/SHOP_MANUAL_FULFILLMENT.md)；[反馈清单S08](../docs/operations/SHOP_FLOW_FOLLOWUP_20261105.md)。
