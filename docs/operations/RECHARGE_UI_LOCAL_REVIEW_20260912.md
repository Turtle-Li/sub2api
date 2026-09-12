# 充值卡片改版、审核与入口关闭发布 — 2026-09-12

状态：用户认可并授权的版本已于 2026-09-12 15:43:46 CST 发布到唯一在用源站，普通用户购买入口保持关闭。真实流程测试后再另行开放。生产版本为 `81e22dc1f413496a528e61e11a9f85b194e1b1f6`；本记录后续的文档提交不代表再次发布。

## 设计与实现

用户要求结合所给参考图，让按量充值卡片与现有订阅卡片保持同一视觉风格。Claude 额度不可用后，用户授权 Codex 自行设计审核；实际实现由官方 Kimi CLI 的 k3-256k/high 完成。不能将本方案署名为 Claude 设计。

- 保留订阅卡片既有的圆角、品牌青绿色、推荐标签和选中边框，改善价格字号、留白与内容层次。
- 所有档位都有统一的到账权益区。到账总额为主体，赠送以“已含赠送 +N 额度”在同一区域显示；无赠送档位不渲染赠送列，不为其保留空白面板。
- 并发权益沿用“可同时处理 N 个请求”，卡片没有“选择此档位 / 已选择”文案或额外购买按钮。
- 只去重已知自动文案：trim 并压缩空白后，完全等于该档实际赠送金额对应的“额外赠送 N 额度”。任何额外语句、标点或不同内容仍展示；不修改后台配置。
- 受限卡片金额与权益使用中性灰，保留完整门槛说明。即使此前已选择该档，也不再显示选中边框、勾或 aria-pressed=true；不自动改选其他档位。受限的后台推荐档位隐藏推荐外观，不把推荐转移给其他档位。

配置、充值金额、赠送、余额计算、订阅、重置卡与真实支付流程均保持原业务规则。本轮六档预览为：

| 实付 | 已含赠送 | 到账总额 | 并发权益 |
| --- | --- | --- | --- |
| 5 | 0 | 5 | 不调整 |
| 49 | 0 | 49 | 不调整 |
| 99 | 5 | 104 | 3 |
| 199 | 20 | 219 | 4 |
| 399 | 75 | 474 | 5 |
| 599 | 145 | 744 | 6 |

## 审核范围与证据

工作分支：`codex/recharge-claude-kimi`。基线：`ba290a4744ab8606951e012d8c1e40c58277992a`。仅以下四个生产源码文件变更：

| 文件 | SHA-256 |
| --- | --- |
| frontend/src/components/payment/AmountInput.vue | c095c02afefc2e7ef4073f2b2c67f2e38cfec887a50427203f54c3cb22873cf8 |
| frontend/src/components/payment/__tests__/AmountInput.spec.ts | 44a7a279eeb5f7c60d5d6228de45a00ea3f4c4aadc42458432548160a0a069a2 |
| frontend/src/i18n/locales/zh/misc.ts | 70fcf5ef0f70919902a2921ea221259d310922444463be10e5f28eb30d677cbc |
| frontend/src/i18n/locales/en/misc.ts | 20cc0743db00cbe6921cfcca83f3c05e413579f31dd9c47cd02afc30d44a2b83 |

冻结补丁 SHA-256：`6c0c8fd987c356be8a3de4b7635ed3d9ccc2bec04ec23f3f894fdd802a2b9856`。

- 独立 QA：21 个测试文件、168 个测试通过（支付组件目录 + PaymentView）。四文件 ESLint、git diff --check、冻结哈希核验通过。无成立的可行动缺陷。
- Codex：`pnpm run build` 通过，包含 i18n 校验、Vue/TypeScript 检查和 Vite 生产构建。现存包体积提示不是构建失败。
- 外部 Google Chrome：桌面浅色/深色，430px 英文，320px 中文、长标题/自定义说明、受限 599 档均已检查；窄屏赠送区能自然换行，门槛可读，无金额或内容截断。
- 桌面键盘：Tab 聚焦、Enter 选择 199 档后摘要显示 199 / 219；空格选择 399 档后摘要显示 399 / 474。最终保留普通中文浅色 599 档（599 / 744）。
- 本地组件预览没有执行真实下单、付款、重置或余额扣除。上述为发布前本地审核；用户随后认可本版并授权发布，生产验证范围见下文。

临时完整证据：`/tmp/recharge-claude-design-20260912/`，包括 `design.md`、`reviewed-change.patch`、`local-qa.md`、`final-build.log`、Kimi 可见实现/修整日志。

## 本地预览

地址：<http://127.0.0.1:3008/payment-style-preview.html>。外部 Chrome 已打开。服务器仅监听本机。

从仓库根目录启动：

```sh
pnpm --dir frontend exec vite --host 127.0.0.1 --port 3008 --strictPort
```

`frontend/payment-style-preview.html`、`.ts`、`.vue` 是本地检查用未跟踪文件，不属于生产改动。预览采用真实组件与脱敏配置样本，API adapter 禁用请求，付款按钮禁用。工具栏可切换订阅/充值、深浅色、中英文及边界状态；边界状态为演示门槛，不表示线上已经启用。

## 审核中另记的既有问题

以下来自基线代码检查，均不是此次样式改动引入，也未经本轮生产验证；保持记录，另行处理：

1. `PaymentView.vue` 的非 1 倍充值换算提示仍使用 locale `rechargeRatePreview` 中的 USD 文案。人民币钱包迁移后，启用非 1 倍充值时需要校对该提示。当前批准配置为 1，该提示隐藏；没有发现本轮金额计算变更。
2. `SubscriptionPlanCard.vue` 的订阅美元配额与人民币钱包余额均简写为“额度”，跨产品对比可能不够明确。应结合产品口径决定展示方式，不能据此断言现有扣费错误。
3. `ResetCardShop.vue` 的有效期单位匹配区分大小写，而后端重置逻辑会转小写。若通过高级配置存入大写 MONTH 等值，入口可能与后端判定不一致；当前套餐使用小写 month，没有证据显示当前目录受影响。

## 后续发布边界

用户随后已明确授权本版提交发布，要求入口保持关闭。发布前已同步 fork/main `451bbb9ec`，源码四文件哈希未改变，并读取人民币钱包迁移权威记录 `docs/operations/CNY_WALLET_CUTOVER_20260912.md`，在最终合并代码与制品上完成所需检查后执行已授权发布。不得回滚到不兼容人民币钱包的旧代码或旧数据。

## 最终提交、制品与发布结果

- [PR #17](https://github.com/Turtle-Li/sub2api/pull/17) 已合并。独立 QA / Review 通过的源码提交为 `00422b185099690a44cf53e4260d9bce866e75ed`；合并和生产提交为 `81e22dc1f413496a528e61e11a9f85b194e1b1f6`，二者完整 Git tree 均为 `caea2c8f6a8592992c66ad7652f416d06dd81032`。
- [CI 34679631845](https://github.com/Turtle-Li/sub2api/actions/runs/34679631845) 的 5 个任务和 [Security 34679633041](https://github.com/Turtle-Li/sub2api/actions/runs/34679633041) 的 2 个任务全部成功，均对应源码提交。此前本地 168 项聚焦测试、ESLint、i18n、类型检查及生产构建通过。
- [GitHub build-only 34680189310](https://github.com/Turtle-Li/sub2api/actions/runs/34680189310) 在合并提交上成功，产出 artifact `10294012963`。生产只校验和加载该镜像，没有在生产编译。

| 制品 | 身份 |
| --- | --- |
| GitHub ZIP | 83,848,860 bytes；SHA-256 `695737c4c3bc78fccf8d140af9ae4317c710147b0c7e0f2de49a7e191ce28313` |
| Docker tar.zst | 83,848,041 bytes；SHA-256 `de4c1d06aaf12eaa3258681a0486afb2f0c62dcb4684ec1cae0d986a58b0752c` |
| 平台与版本 | `linux/amd64`，`0.2.4`，source `https://github.com/Turtle-Li/sub2api` |
| 生产镜像 | `sub2api:auto-20260912-154259-81e22dc1`；`sha256:641795d7cb0f60f0e865b57dbee22f72e05fa8381d24102541b63433cc45ffb3` |

独立制品审核 `ARTIFACT_QA_PASS`：28 个 OCI blob 的内容哈希、13 个 layer 的描述符/大小/rootfs diff ID 均一致，实际应用二进制包含五个新版前端标记；1,770 个文件系统条目和二进制均未混入本地 `payment-style-preview`。网络传输改为目标机读取同一 GitHub 制品，ZIP 和压缩镜像仍按 GitHub / 构建元数据验证；未更改网络代理、安全验证或凭据配置。

唯一部署目标为 SSH alias `sub2api-candidate`。使用已安装的 root receiver、共享维护锁和蓝绿发布 helper：从兼容人民币钱包的 blue `ba290a474` 切换到 green `81e22dc1f`，Caddy upstream 于 15:43:45 CST 提交，15:43:46 CST 发布验证通过。候选容器的认证 API 探针 `/v1/models`、非流式 Responses 均返回 200；这不是支付流程测试。

服务端日志目录：`/var/log/sub2api-release/gha-20260912-154259-81e22dc1-1425090`。制品的受限暂存目录为 `/var/log/sub2api-release/recharge-refinement-20260912-artifact`。本地完整证据位于部署工作区 `evidence/recharge-refinement-release-20260912/`，包括源码审核、制品审核、GitHub 结果、发布日志、public/postflight 和健康探测记录。

## 入口关闭与线上复核

- 发布前通过外部 Google Chrome 的管理员支付设置关闭支付；发布后 public settings 仍为 `payment_enabled=false`、`purchase_subscription_enabled=false`。钱包结算仍为 CNY，美元换算率仍为 6.75，充值 1:1 的既定规则未更改。
- 外部 Chrome 全新访问 `/purchase` 被重定向离开购买页；当前管理员会话跳回 `/admin/dashboard`。侧栏不显示充值/订阅入口，`/orders` 历史订单页及刷新操作正常。
- 后端对新的充值/订阅订单和重置卡购买执行关闭检查。历史订单查询、发票处理及可信支付回调保留；已有重置购买的幂等重放仍保留。受限管理员 owner-test 路径留待后续获准的真实流程测试，本轮未使用。
- 新 green 容器 healthy、restart 0、无 OOM；节点 `traffic=accepting active_container=sub2api-green background=active`，node preflight 通过。Caddy 主机配置、容器启动配置和运行配置均仅指向 green，事务标记已清除。
- 发布 helper 的审计计数为 `app_5xx=0 / app_fatal=0 / caddy_5xx=0`。独立公网健康监视覆盖 15:35:39–15:50:36 CST，www/API 共 248 次、失败 0；其中发布后 15:43:51–15:50:36 的 116 次全部成功。
- 首页和帮助中心分别保持原 SHA-256 `c5b06aa5d590e978aeb883944ba8c40cd4755362cc573e66e8b6f6d13c42fe1a`、`943ee8b0d50f73a479df543ef887fa89d6981f737e7c64b47bf3b15425355f4a`。Caddy 与支付/飞书 Vault sidecar 没有重建或重启，后两者 healthy。
- 旧 blue 于 15:44:47 CST 正常退出，exit 0、无 OOM，应用记录 graceful shutdown。独立排空 unit 随后的连接计数与停止状态竞争，记录 `container is not running` 并退出；因此不将该 unit 记为排空成功。旧容器已停止、新容器与三份路由配置均正常，不为此重启应用或再次发布。
- Runtime-guard timer 延续此前人民币迁移记录中的 inactive 状态；本轮未启用，也不宣称定时自动恢复已经生效。保留兼容 CNY 的上一版镜像及 helper 生成的 Caddy 回滚备份；不得恢复旧 USD 数据库或失效源站。

本轮未新建真实订单、付款、退款、重置或扣除余额。普通用户购买继续关闭，待实际支付、到账和权益发放流程验收后另行开放。
