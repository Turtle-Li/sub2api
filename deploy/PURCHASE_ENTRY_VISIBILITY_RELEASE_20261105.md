# 购买入口显示开关发布（2026-11-05）

已发布源码 `2a9e6ac9ed28766919816f883b0f3d90dad5d352`，版本 `0.2.21`。[规范工作流41998141760](https://github.com/Turtle-Li/sub2api/actions/runs/41998141760)于洛杉矶时间2026-11-05 01:06:45（UTC-08:00）成功完成。参数为 `main`、`build_only=false`、`deployment_target=aws-candidate`。后续记录提交仅更新文档，不改变已部署源码，也不另触发发布。

## 范围与操作

在**系统设置 → 功能开关 → 购买入口**分别配置“显示商城入口”和“显示充值/订阅入口”，通过原有保存按钮保存。商城使用 `shop_entry_enabled`，未配置时默认显示；充值/订阅复用 `payment_entry_enabled`，原值保留。

只改变入口显示；不新增名单、购买权限或直接访问拦截。原有订单、支付恢复、人工交付、使用指南及已拥有的订阅权益不增加限制。本次没有改变线上商品、价格、设置值、DNS、凭据或挂载，没有直接操作数据库或新增迁移。发布时两个入口均显示。

## 验证与云端执行

- 同一源码的前端7文件142项测试通过；后端5包最终通过。首次service编译被终止，降低并行度重试通过，保留首次失败记录。
- i18n3项、类型检查、生产构建及改动文件ESLint通过。云端类型检查首次默认约2GiB堆OOM，单进程4GiB重试通过；没有将首次运行记为全绿。
- 精确源码的Mac实际页面检查覆盖桌面/手机、浅色/深色、独立保存与入口隐藏。可选云浏览器未启动成功，不声称新增云端视觉验收通过。
- 正式发布由独立boxd CLI执行；启动连接断开后仍继续运行，并在自身工具环境验证GitHub账号 `Turtle-Li`。使用官方provider-managed个人Git连接，没有复制Mac认证、SSH私钥或Vault解锁。
- 云端安全fetch和完成时的fork/main均为已审查源码，工作区干净；该进程没有新增源码或文档提交。

## 发布身份与线上检查

- 规范维护锁下由blue切换至 `sub2api-green`，镜像 `sub2api:auto-20261105-010542-2a9e6ac9`，接收器报告healthy、background mode `activate`。
- Runner及加载镜像ID：`sha256:ddd2a27d84b96ef83ac3db9d649d98dff41a985f06082e4c62ffb82b1168de1a`。Docker归档config ID另记为 `sha256:8233c2478f91b6dcc0124e49676ab7845584061379e02aebdb8918b3912edf82`，不混为加载身份。
- 归档 `94152945` bytes；GitHub日志中的digest部分遮罩，未猜测完整值。接收器的归档digest、平台、source/revision/version检查成功。
- 发布日志 `/var/log/sub2api-release/gha-20261105-010542-2a9e6ac9-1766633`；审计 `app_5xx=0 / app_fatal=0 / caddy_5xx=0`。
- Caddy receiver确认相同源码、active_slot=`green`，投影后SHA256 `bb81db5a19648a277a0288c3f51abb31c0c81104bb94046e1fc31c979eae5baa`。
- 公共设置API与HTML注入的两个字段均为boolean、均为true且一致，原充值入口值保留。
- 19项线上JS/CSS与精确源码构建逐字节一致，包含SettingsView、index、商城、订单和付款资源。入口 `/assets/index-DA6gl7ju.js`，SHA256 `8be4a1f6ec624fa5057e8ae33e40a96de612314e69096181b9651eddcd9bffae`。
- 商城、商品、指南、付款返回、管理员设置、订单共6页HTTP/CSP检查通过；www/API health均200；4个匿名买家/管理员边界均401/no-store。首页和帮助中心内容与发布前一致。
- 主控制器收尾时重新读取线上公共设置、管理员设置页入口资源和www health，仍匹配上述源码资源且返回200。

云端没有生产操作员SSH身份；根属主配置与旧活动容器的安全检查由既有规范receiver/helper完成。没有声称独立SSH比较全部运行设置、挂载或旧容器最终排空状态。没有真实付款、提交Session、交付、重发邮件或切换生产开关来验收。

## 恢复与任务收尾

前一blue源码为 `2f43bfcf7ecfbe2387d9196ed27f4a7b073d9f37`，交给既有规范drain monitor管理。本次未独立查询其最终停止状态。恢复必须走维护锁、退款readiness和action-aware流程；保留已有交付动作、原收据、到期与撤销能力，不恢复旧DB或不认识shop actions的二进制。参见[上一轮发布](SHOP_ACCOUNT_COPY_RELEASE_20261105.md)及[自动交付边界](SHOP_PAYMENT_ACTIONS_RELEASE_20261105.md)。

控制器已取回18份选定非秘密报告和验收证据，并逐项核对SHA256一致；没有同步原始聊天、登录态或缓存。云端进程退出码0，tmux任务已结束，检查未发现其他工作负载后，控制器通过既有 `codex-cloud stop` 停机，并经provider API确认 `stopped`。

本次停机仍由主Mac控制器在回来收尾时触发。自动任务完成停机钩子尚未部署，不能声称Mac离线后也会自行停机。原认证超时、writer冲突及受限App会话的检查点均保留，不能将旧尝试退出码当作最终发布结果。

详细功能契约见[入口显示开关](../docs/operations/PURCHASE_ENTRY_VISIBILITY_20261105.md)。本地报告保存在控制器任务证据目录 `cloud-results-20261105/`；云端原始结果保留在持久磁盘。
