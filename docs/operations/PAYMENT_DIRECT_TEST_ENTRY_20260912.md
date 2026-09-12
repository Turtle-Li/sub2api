# 直达链接支付测试 — 2026-09-12

任务 PAY-DIRECT-TEST-20260912，功能与受控配置。用户要求其指定普通账号通过直达链接走正常支付：Plus 保留正常月付权益，售价 0.10 CNY；余额支付 0.10 CNY，到账 100 CNY。普通用户入口不展示、普通商品不开放。用户自行撤销旧订阅和付款，执行者不修改已有订阅或代付。

## 接受标准与架构

- T1：后台独立配置 `payment_entry_enabled`。只控制导航与购买引导，缺失时保持旧版导航行为；`payment_enabled` 仍为正常支付与直接路由的总开关，不新增绕过总开关的下单路径。
- T2：仅指定账号可见、可购买两项测试商品；隐藏受众不得从配置、计划、checkout-info 及订单快照泄漏。其他账号直接提交测试商品、原商品或任意余额金额均拒绝。
- T3：继续复用原 `/purchase`、订单、支付渠道、签名回调、幂等履约、余额/订阅发放、历史订单与发票。支付金额按整数分 10，余额使用现有精确赠送计算（0.1 + 99.9 = 100），净实付累计只计 0.1。
- T4：普通六项计划暂时 `for_sale=false`，六档充值暂时 `enabled=false`，其他配置保留。独立添加有明确账号受众的测试 Plus 月计划和唯一 enabled 的充值档位。Plus 组、周期、权益和原重置价覆盖原样保留，不从测试低价派生新的重置价格。
- T5：总开关最后开启。关闭测试必须先关闭 `payment_enabled`，再移除/停用测试项或恢复普通目录。不能在总开关打开时禁用最后一个充值档位，否则现有规则会回到自定义充值模式。

只读架构咨询选择复用现有商品授权与目录配置，而不增加 test-only API 或总开关例外。非指定账号的重置报价/购买也受唯一在售月计划及其受众限制；指定账号原有重置功能不由本任务关闭，本轮不执行。

初始基线 `d756ab3ea7c88ea3ab0a42db154cfe776afcecff`；最终代码纳入 `eb6fe30e0bfdeb1081360307c688fb54a8122674`（包含 `3f61330af` 计费代码及其发布记录）。正式支付契约继续采用 `docs/UNIFIED_PAYMENT_INTEGRATION.md` 的固定 SDK/types blob `9a4829f36b202a9904cfc38b752d31c7f1c63063` 与 OpenAPI blob `0bcfa34ee58bcd8f346a4bac86db9e2d89829c48`（整数分最小 1）；未修改第三方签名、回调、退款或凭据契约。

## 执行与验证计划

1. Root：维护需求/配置 manifest、后端设置持久化与 public/admin/HTML 注入投影；不修改下单授权核心。后端源码写入 `codex/payment-test-entry` 独立工作树。
2. Kimi：在 `codex/payment-test-entry-ui` 独立工作树处理 frontend 的设置开关、类型、导航显示与对应聚焦测试；沿用现有 UI，直接路由/恢复仍依赖支付总开关。与步骤 1 并行，文件所有权不重叠。
3. Root：整合；验证设置缺省兼容、false 单独保存、HTML/API 一致、隐藏导航仍可直达、目录受众/金额与不可购买边界。独立 QA 与 Review 针对同一冻结提交。
4. Root：在原支付仍关闭时发布精确 GitHub 制品，再做 expected-old-value 配置事务。保存相关开关、原计划、原档位的 before/after 和测试新增 ID；确认订阅收款换算关闭、余额 multiplier=1、fee=0、唯一受限固定档位存在后，最后启用支付。单一源站 `sub2api-candidate`，保留 CNY 与正常背景所有权。
5. Root：线上 readback、非指定会话拒绝/不可见、外部 Chrome 直达与隐藏导航检查。实际支付由用户完成；不把本地测试或配置验收宣称为资金到账成功。

## 发布协调与配置事务约束

同一时段人民币计费任务已将 `3f61330af2a11580e74191d1cf1cd7f74357886b` 合并到 fork/main，正在发布并激活标准 OpenAI 按量倍率 0.25。本任务等待其发布与兼容回退槽准备完成后才推进 main 或操作生产；整合时保留这版计费规则，不启动旧 FX 代码与新倍率的组合。

目录 staging 使用短事务（锁等待 3 秒、语句 15 秒），先锁定 Plus 组及计划/设置表，再比较完整计划和选定设置前值。设置表锁覆盖尚不存在的导航设置键。所有执行使用 `psql -X -qAt -v ON_ERROR_STOP=1`，由源站同一执行链持有 canonical maintenance fd8 至 psql 结束；源站既有运行时仅在短期子进程内提供 DB 凭据。中途失败/回执未知必须先只读核对，禁止盲重试。

staging、独立 readback、enable、close 与 restore 均显式使用 `Asia/Shanghai` 会话时区，保证完整 JSON 前值比较不会被时间戳格式差异误判。最终 enable 只改 `payment_enabled=false→true`，要求读回唯一受限月计划/固定充值档位、实际收款 0.10、余额赠送 99.90、无额外汇率或费用，并确认入口为 false。运维 Python 明确拒绝优化模式，防止前置校验被禁用；enable/restore 只接受成功的独立 readback 回执。关闭和还原是两个独立步骤：close 只将支付/入口置 false；从新的关闭读回生成 restore，计划/设置如有变化则中止还原。保留测试计划为停售，以保存历史订单引用；不删除财务记录。

生产前状态、source/artifact 身份、发布与配置读回结果见下方完成记录。回滚使用总开关优先关闭、配置前值校验和兼容 CNY 的旧镜像；不覆盖历史订单、余额、订阅或退款。

knowledge_candidate: no — 本项目测试目录与支付导航控制，不是跨项目通用结论。

## 已完成发布与受控启用

- 已审源码 `93f3f20c0a524a42faf969fa2547bd24505a0529`，PR20 合并/运行版本
  `7ffd65d66a961db6a0fc26b048e064fe7b6ce124`，完整 tree
  `e38f1e107f1f76ba79ab5be40159fc04680ad87c` 相同。独立源码 QA/Review、
  配置 SQL 与完整 stage/readback/enable/close/readback/restore 本地 PG18
  排练、release wrapper 与 artifact QA 均通过（advisory）。
- CI `34684992822`、Security `34684994713` 全部成功；GitHub build-only
  `34685534229` 成功。归档 83,848,257 bytes，SHA-256
  `022591ebeb5bd128570feab1ee6499e726abfb14b6d68daa4da05b98ddfbfea3`；ZIP
  83,849,076 bytes，SHA-256
  `2e7531e2e9f895a4310fb562b85bbe7968f6f767d0d96e9859746768537ed24b`。
  28 个 blob、13 个 layer、Docker/OCI 身份及实际 binary 开关标记均验证。
- canonical receiver 于 17:45:17 CST 完成。active `sub2api-blue`，运行镜像
  `sha256:7b348f9c98fef18b23de127485e45b5e4b1dd2258cdff9ae8a46d117ec5510b5`，
  healthy/accepting/background active。app_5xx/app_fatal/caddy_5xx 均 0。
  旧 green `3f61330af` 于 17:46:17 正常退出，exit0；未强停在途连接。
  Caddy 仅 upstream color 变化；www 首页/帮助页 SHA、TLS 与健康检查保持。
- 在旧槽停止后，持 canonical maintenance lock 先 stage，独立 readback，再
  单独启用支付并再次独立读回。最终 `payment_enabled=true`、
  `payment_entry_enabled=false`、legacy `purchase_subscription_enabled=false`。
  public API 与 HTML 注入一致；CNY/6.75 及刚上线的 Codex .25 规则保留。
- 实际新增 **plan7**（Plus 月付·支付测试），price0.10/original120 CNY，
  group4/month1/正常 Plus 权益，reset override40，受众仅 regular user2。
  原 plans1–6 仅 `for_sale=false`；原六档充值仅 `enabled=false`。
  唯一启用充值档位 amount0.10/bonus99.90，到账100 CNY，受众同 user2。
  最小充值0.10、multiplier1、fee0、subscription conversion0，无并发赠送。
- Chrome 已登录的非受众管理员会话直达充值页时无可用档位、确认支付禁用，
  侧栏无购买入口。未伪造 user2 会话、撤销其订阅、代付或执行资金发放。
  真实支付、回调和到账验收由用户后续操作，不能把配置验收当作支付成功。

直达链接：
- `https://www.turtleligpt.com/purchase?tab=subscription&plan_id=7`
- `https://www.turtleligpt.com/purchase?tab=recharge&amount=0.1`

受保护制品：`/var/log/sub2api-release/payment-test-entry-20260912-artifact/`；
receiver 日志：`/var/log/sub2api-release/gha-20260912-174452-7ffd65d6-1506838/`。
任务证据目录名 `evidence/payment-test-entry-20260912` 保存原目录、逐次 SQL
hash/回执、独立读回与关闭脚本；不保存运行时凭据。

关闭脚本 SHA-256 `4d9823125494ce5b078a8387fb104bf610ac67c27fb80f4a257ec1e6b3008ebc`，
已准备但未执行。关闭测试和回退旧应用都必须先关闭支付；旧兼容计费版本
`3f61330af` 尚不识别独立入口开关，不能在支付打开时回退并声称导航仍隐藏。
还原目录前重新独立读取并校验前值，保留 test plan7 为停售，不删除订单引用。
指定账号的原重置功能没有因本任务禁用，不应宣称全站重置 API 已关闭。
后续前端发布应保留上述专属测试配置；不应沿用旧 `payment_enabled=false`
作为线上状态断言，或在支付仍开时移除唯一受限充值档位。
