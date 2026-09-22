# 支付与订阅修复清单 — 2026-09-20

Task ID: PAYMENT-FIX-20260920
类型：用户反馈 Bug + 已确认交互/业务规则调整。
基线：0f892ce34（运行版 b7fa7f4af）。目标：逐项修复、针对性验证、独立复核后发布。

状态规则：待分析 → 已定位 → 已实现 → 已验证；只有有验证证据才勾选。每项补充根因、文件、测试和发布记录；不把构建通过当作视觉或真实支付验收。

当前发布状态：P01–P19已随 `f8c209764c46ccd7b791b90efd6f51b5543ed09b` 上线，自动权益退款已启用。详细制品、门禁、备份和激活证据见 `PAYMENT_BENEFIT_REFUND_RELEASE_20260920.md`。以下进度日志按时间保留，旧“未发布/待确认”记录不代表当前状态；真实商户二维码视觉尚待用户验收。

## 2026-09-22 用户后续修正（本地完成，尚未发布）

本节替代 P17 的嵌入呈现方向，历史发布日志不改写。详细契约、固定来源及验证见 `PAYMENT_LOCAL_CANCELLATION_20260922.md`。

- [x] 支付宝恢复独立收银台，移除 iframe；已有待付单展示订单明细弹窗和继续支付/取消操作。
- [x] 取消先本地提交并解除待付限制，持久后台关单；取消后到账不发权益，按原交易稳定幂等退款。
- [x] 两端锁、重放、重启、丢失响应、迟到资金及任务超时回归通过；前端 139 项、Sub2 全 service unit、真实 PostgreSQL 与两端独立 QA/Review 通过。

上述为本地实现状态，未提交、推送或部署；中央迁移 23–24 与 Sub2 256 需要配套发布。

## 逐项验收

- [x] P01 （已实现/本地验证，独立复核通过）优惠码输入：初次输入不显示“当前订单需要重新使用优惠码”；不在输入区和支付区重复提示。订单条件变化仍应使旧报价失效，提交不得绕过服务端校验。
- [x] P02 （已实现/本地验证，独立复核通过）支付成功后清空优惠码及已用报价，无需刷新充值页面；重复成功事件安全。
- [x] P03 （已实现/本地验证，独立复核通过）管理优惠码：每用户最大使用次数填2时编辑弹窗不消失；定位输入/重渲染/关闭事件根因，保留其他字段。
- [x] P04 （已实现/本地验证，独立复核通过）管理审计详情改成人能读懂的操作、字段和前后变化；技术审计原始数据保留，不泄露内部敏感信息。
- [x] P05 （已实现/本地验证，独立复核通过）优惠码使用记录显示用户名；无用户名历史记录有合理兜底。
- [x] P06 （已实现/本地验证，独立复核通过）优惠码支持复制；列表默认不明文展示优惠码，明确操作后查看/复制（沿用权限控制）。
- [x] P07 （已实现/本地验证，独立复核通过）我的订阅显示过期订阅：对照固定上游源码确认是否上游逻辑；若是则保留列表，并给过期订阅增加续费跳转。
- [x] P08 （已实现/本地验证，独立复核通过）套餐推荐标识：调查季付/年付Plus未配置推荐却显示的原因；展示服从后台配置，不擅自自动推荐。
- [x] P09 （已实现/本地验证，独立复核通过）用户订单详情重排信息层级：购买内容、金额与优惠、支付/履约状态、时间和操作清晰分组，长订单号不破版，手机可读。
- [x] P10 （已实现/本地验证，独立复核通过）弹窗点击外层关闭：审计本次相关弹窗和公共弹窗默认行为，详情支持外层关闭；避免鼠标从内容拖到外层误关闭，保留业务确需防重复提交的处理中保护。
- [x] P11 （已实现/本地数据库验证，独立复核通过）开票提示：申请/开具流程明确提示“开票成功后订单无法退款”。
- [x] P12 （已实现/本地数据库验证，独立复核通过）服务端开票退款互斥：已成功开票订单禁止退款，驳回允许；覆盖用户申请、管理员执行和并发状态变化，不只做前端拦截。
- [x] P13 （已实现/本地验证，独立复核通过）重置卡选择流畅：查明切换加载来源，减少阻塞视觉/重复请求；权威报价、资格、幂等和过期校验不能取消。
- [x] P14 （已实现/本地数据库验证，独立复核通过）购买重置卡有效期改为15天：服务端发放、报价显示与不可变订单快照一致；用户已确认：保留完整15天，订阅有效时才可使用，续费后可继续使用；保留历史已购快照，不修改赠卡配置。
- [x] P15 （已实现/本地验证，独立复核通过）支付成功布局：长订单编号可换行/复制，不挤压标签或撑破弹窗；核对截图中金额美元/实付人民币是否错误标记，若不一致修正实际支付币种。
- [x] P16 （已实现/本地验证，独立复核通过）“购买并使用一张”复选框样式优化，统一现有控件风格，键盘/选中/禁用状态清晰。

- [x] P17 （已实现/本地验证，源码独立复核通过；待实际浏览器验收）支付宝站内二维码：对齐微信弹窗；核查可信支付返回类型与二维码内容，保留已存在订单恢复、原截止时间及移动端兼容，不把HTML/错误链接冒充原生二维码。
- [x] P18 （已实现/本地验证，独立复核通过）支付渠道商品说明中文化：Plus订阅、Pro订阅、订阅重置卡、余额充值；不出现Sub2API等内部英文标识。沿实际发给支付宝/微信/统一支付的边界确认，保留渠道必需技术ID，不篡改旧交易。
- [x] P19 （源码/数据库验证/独立复核通过；Sub2已部署并启用）历史订阅/余额无法退款：逐笔只读核查审核阻断原因、paid/gift来源及权益发放/使用证据；已用重置卡不可退规则保留。可证明的未用权益应自动判断回收，历史无法证明不可盲目退；解释需要人工撤销的具体权益与系统能处理的边界，优先实现可靠自动回收。不得手工改金融状态或发起未经单独确认的实际退款。

## 执行分组与所有权

1. 优惠码管理与审计：独立定位P03–P06后开发。
2. 订单、公共弹窗、成功页：定位P09/P10/P15，按现有组件修复。
3. Root：P01/P02/P07/P08/P11–P14/P16的业务边界、上游对照与集成；后续按独立文件边界委派开发。
4. 冻结后独立QA/Review；发布沿用项目canonical镜像/蓝绿流程和当前授权，不改支付凭据、手工金融状态或历史订单。

## 证据与进度

- 已记录用户截图：支付成功弹窗长订单号导致标签被挤成逐字竖排、内容溢出；目前仍需对照代码确认根因。
- 本文是本轮连续性入口，后续每完成一项立即更新，不依赖聊天上下文。

### Root诊断与改动计划（P01/P02/P07/P08）

ANALYSIS_READY。直接源码证据：PaymentView couponStatus和railNotice均在有输入但无报价时返回reapply，故初次输入双重提示；onPaymentSuccess没有removeCoupon，造成用后残留。featuredPlanId会自动取最大折扣，造成未设置recommended仍展示。固定上游1a9d49e16的SubscriptionsView调用getMySubscriptions并渲染expired状态，当前保持同样路径。

开发边界：PaymentView及测试删除自动初次reapply/重复rail错误提示（不取消couponReady与服务端报价校验），成功事件清空优惠码；推荐只读显式配置。SubscriptionsView只给active/expired且支付入口开放的订阅提供续费，暂停不增加入口。验证初次输入/报价失效/支付完成/重复事件/后台推荐及过期跳转。

### P14实施边界

新订单采用明确的paid_duration/15天权益快照，可信paid_at起算，到期订阅不缩短卡片期限；使用卡片仍校验订阅有效。旧订单保持原subscription到期策略，不重新计算旧权益。报价新增validity_days用于展示“付款后15天”，预计expires_at不参与新策略的客户端幂等指纹（避免随时钟变化创建重复订单）。服务端原订阅最小付款窗口及订单到期不变。针对15天、短/长订阅、延迟履约、重复回调、旧快照及非法策略做验证。

进度：P01/P02/P07/P08 的 PaymentView 与订阅入口测试75/75通过。P13/P16已实现，ResetCardShop/paymentFlow/PaymentView合计117项通过；P14新增服务端权益策略及幂等适配在验证中。

### 并行修复与新增问题进度

P03–P06：真实number输入被当string调用trim导致render异常；已统一规范化，13项前端测试、优惠码服务测试、类型/ESLint通过。列表默认隐藏、复制和显式查看，审计白名单翻译、使用记录真实LEFT JOIN用户名（保留已删除用户历史）。
P09/P10/P15：订单详情四区分组、长订单号换行复制；公共遮罩pointerdown保护和顶层Escape，支付shell仅隐藏保留恢复。104项定向测试和typecheck/ESLint通过。强制管理员合规确认仍保持必须确认/退出，不改安全门槛。
P13/P16：预取复用、缓存失效丢弃旧响应、预取失败后点击重试测试13/13；复选框样式统一。P14正在跑真实PG集成以验证到期保留/续费可用。
P18：新渠道subject统一Plus订阅/Pro订阅/订阅重置卡/余额充值；其他套餐泛称订阅，内部前后缀不再进入渠道标题，不改变技术订单号和旧交易。TestBuildPaymentSubjectCustomerDescriptions通过。
P17：官方page.pay支持qr_pay_mode4的嵌入二维码；统一支付显式metadata选择，新增独立checkout_frame_url，维持原渠道/到期/通知与未请求模式的其他产品。两端适配开发中。
P19只读生产证据：部分已完成余额订单包含并发提升与赠额，当前并发高于该订单授予目标；部分已完成订阅订单的赠卡尚未使用且存在可信期限凭据；另有单独购买的重置卡已经使用。现有不可回收权益一刀切导致前两类阻断。没有执行实际退款或修改历史权益。未用赠卡收回/已用禁止的细则已询问，待答复。此文不保存个人订单金额或余额。

P14：CI=true go test -tags integration ./internal/repository -run TestResetCardExternalOrderPostgres -count=1 实际Docker PostgreSQL/Redis集成通过（10.707s），包含订阅过期后发卡、付款+15天断言、过期拒用/续费后可用。未跳过数据库。

P19额外阻断：只读统一支付live绑定确认支付宝/微信daily_order_limit=0、daily_amount_limit_fen=0，但daily_refund_limit_fen仍为20（每方式每天0.20元测试限额）。已询问正式退款限额，未修改资金或配置。

### 中断恢复记录

本轮尚未提交、推送或发布。P11/P12全service unit通过204.548s；真实PG开票/退款抢锁测试通过4.633s（非跳过）。已开票只阻止新退款；已有退款attempt恢复/查单不被阻断。前端定向45项通过。补全REFUND_INVOICED_ORDER中英文错误与审核说明，取消过时“冲红后退款”表述。
恢复后i18n key完整性3/3通过。支付宝中央服务go test ./...已通过；runtime候选totools-pay-live:20260920-embedded-qr已构建，未上传/发布。Docker Hub不可达，采用已存在固定前版runtime与本地Go1.26.6交叉构建linux/amd64程序的既有打包路径，完整制品身份与独立复核仍待完成。浏览器验证受Codex auth token unavailable阻塞，未声称截图验收通过。

恢复后全量前端回归：335文件/2662测试通过（177.12s），生产build通过（Vite39.73s），全量lint:check退出0。中央P17独立QA与双轴Review通过，报告/tmp/sub2-sep20-central-review.md；Sub2边界独立复核尚在进行。

P17收敛：Sub2 backend adapter/service/middleware定向测试通过，包含响应/恢复的订单与组织产品应用范围拒绝、官方URL严格校验、旧pending兼容、终态/过期排除和精确frame-src；frontend最后补充223项支付用例+旧QR路由1项、typecheck/i18n通过。站内两处支付弹窗显式启用嵌入，旧全屏路由保持原流程；iframe不自动新开窗口，6秒/加载失败提供用户主动备用入口，状态只认服务端。
P15补充：确认/成功金额不再把潜在USD套餐原价按CNY标记，使用可信实付及其结算币种；长号换行复制保留。
P19待产品决定，不标完成：推荐未用赠卡自动回收、已用赠卡禁止自动退款；正式退款每日限额仍待回复。余额当前并发高于订单授予目标说明不能只凭订单含concurrency字段要求人工撤销，后续应基于可信来源及当前状态做回收或无变更判定，在退款预占锁内复核；不能不经凭据检查直接解除全局权益保护。

### 独立审查修复收尾

中央P17独立QA/Review PASS。Sub2后端独立审查发现管理员测试订单仍有英文subject，已改余额充值，并断言真实中央POST内容；TestOwnerTestOrder/TestBuildPaymentSubject通过2.958s，后端最终Review PASS。
前端P17集成发现请求前仍预开空白窗，已取消支付宝预开，旧hosted订单留本站shell使用显式备用入口；159项支付定向测试与类型检查通过。
前端独立审查FR-001：开票提交/重发期间可关闭窗口。已在AdminInvoiceDialog和实际AdminInvoiceRequestsView双层加入busy关闭/切换保护；11项组件+父级在途更新/邮件/飞书回归通过。最终delta独立复核进行中。
当前仍未发布，P19退款自动回收与正式退款限额等待产品规则回复，不做默认同意处理。

### 源码验证结论

2026-09-20：P01–P18源码与定向测试门禁PASS。中央QA/Review、Sub2后端QA/Review、前端QA/Review均通过；FR-001已复核关闭。前端最终冻结37文件manifest SHA256：49d40e8e113cf9e7d2b6a6847523b725311ebbf02fbfc01d6ba76222d5d57231。最终开票关闭保护11项测试、typecheck通过。浏览器认证/真实商户视觉验收仍未完成；不能把测试通过当成线上已生效。

保存已验证修复提交；不发布生产。P19仍未实现，需用户确认赠卡使用后的退款规则及正式每日退款限额后继续自动回收开发和发布。

## P19 已确认开发与发布（2026-09-20 后续）

用户明确取消每日退款限额，并要求系统记录赠卡ID及发放前历史并发，退款自动检查和回收，无需常规人工撤销。沿既有未用赠卡回收、已用赠卡拒绝退款边界实现，不新增扣卡价格规则；重复请求/未知资金结果继续既有幂等与预占恢复。

执行DAG：T19-A中央0=不限额与相关数据库/准入校验（独立并行）→中央单元/PG验收；T19-B Sub2真实发放快照、管理员覆盖版本、赠卡来源ID（后端单一写入责任）→T19-C接入既有review/reserve/capture/release并补用卡竞态/失败恢复→T19-D展示自动回收效果、历史证据可证明时自动处理→独立QA与双轴Review→同源CI/安全检查/制品与备份恢复验证→canonical中央/Sub2发布。

架构责任root；中央写入refund_unlimited_central；Sub2后端责任待架构边界冻结后分配；root负责UI/文档/集成/最终发布。正常新订单必须有完整快照；历史缺失数据不能伪造，先利用既有订单、赠卡来源和更高并发证据自动判断。支付总额上限、单笔预占、开票互斥、单次成功退款和账务审计保持。

复用现有任务分支；preflight因项目无.agent-worktree.toml trunk返回CANONICAL_NOT_READY，不创建重复分支、不修改全局框架配置。当前a025553c4与fork/main一致，已有用户开发/提交/发布授权继续生效。

### P19 开发与发布准备证据

前端自动回收明细与busy关闭保护45项测试通过，i18n3项与typecheck通过；独立UI QA/Review无P0-P2，已把“赠卡”文案明确为赠送重置卡。实际回收数量、卡ID及并发变化仅展示服务端审核结果，不在浏览器推算。

中央migration22/0=unlimited候选已完成开发者真实PG17.11测试与制品构建；archive、scope config SQL、rollout script已上传项目release目录并核对SHA，仍为暂存未运行。中央独立release review进行中。

Sub2现网blue健康，旧green已正常退出。root-owned receiver SHA256保持8ccb62ae77776ff5f3298b3dc5ff0b3599ac5dc344db649f8f68f4a439f941d8，配置仍Turtle-Li/sub2api/main。
新canonical备份：/opt/sub2api-db-backups/sub2api-db-backup-20260920-035430.tar.gz，288402827字节，SHA256 42f13644bf78ec373d5cf4f251887d97257b059c239e7b5f39f112b86bbf5127。备份service success/exit0；installed隔离restore-smoke通过outer/inner校验、PostgreSQL恢复/amcheck和Redis载入，schema_count311、schema_hash d718ec8409f7a53578b5927f1081cdbe。未恢复生产数据。

中央R1独立发布检查NO：旧默认0不能被直接解释成无限，否则可能放宽非Sub2项目；旧回滚镜像必须校验不可变ID，不能仅tag。R1未执行，暂存脚本已隔离为REJECTED。正在修R2：明确unlimited opt-in、旧0仍禁止退款，仅授权的Sub2两个binding设置0+opt-in；增加旧imageID前置/回滚核验。R2须重新通过独立检查后才运行。

### P19 独立审查返修（未发布Sub2）

中央R2通过独立检查后实际执行停在前置备份verify：backup create成功，verify 5分钟timeout。API/Worker/PG仍cancel21、4代理healthy、尚未dockerload/Compose变更/迁移/SQL配置。根因已定位verify的GPG stdout→pg_restore --list管道：目录读取提前结束，父进程不drain剩余解密流，较大备份阻塞到timeout。R3修复读完并验证解密/MDC状态，不能跳过校验；新只读oneshot helper先验证旧备份，旧PG21helper继续隔离恢复验收，之后才允许发布。

Sub2独立缓存审查返修：wire_gen过时；提交后的新cap必须立即限制旧AuthSubject，不能等freshsnapshot；0无限/tombstone必须合法。作者正在补tokenized mutation barrier+提交/回滚投影同步、revisionCAS/reconcile及server实际构建。
Sub2财务审查返修：balance已有walletledger回收赠额时应允许与并发一起自动退；旧balance来源兼容遗漏；旧writer rawUPDATE必须UNATTRIBUTED屏障而非可信SET；审计0不能视为缺省；补真实service→PG refundreserve/capture/release而非仅手动改state的测试。

root新增restrict-only forwardpause hostguard及PGadvisory CAS SQL，正常HTTP/WS不drain；hermetic守卫/EOF/错误ACK/pending拒绝和真实PG CAS/重复/并发预占竞争均通过，待独立审查。该guard仅暂停新reviewedrefund，旧writer数据桥接仍依赖上述UNATTRIBUTED边界，不能凭gate关闭宣称安全。

P19前端最终定向46项通过，生产build通过（17.32s），ESLint无错误；退款并发0按现有语义显示“无限制”。Root另补P17手机兼容：仅桌面新Alipay请求embedded_qr，手机保留原hosted表现；统一支付Alipay/Wechat相关测试通过0.707s，已交cache审查范围。
forwardpause guard独立检查PASS，仅限hostguard/PGCAS；包括hermetic、ShellCheck、真实PG竞争及应用advisory-lock测试。自动回收整体仍待cache/financial返修复核，不因gate通过而提前激活。
中央R3加入GPG流drain/MDC校验，真实>1MiB旧超时/新成功与坏口令/截断失败测试通过，source/artifact重新构建并上传校验，待独立R3delta复核。R2失败backupverify未改变线上服务/schema/配置。

中央R3真实尝试再次在服务切换前停止，ERR定位为一次性helper挂载文本比较（Docker模板额外换行排序后多空行）。root创建未启动inspect-onlyhelper读取结构确认2个ro bind/private、UID999、caps/security/network全部符合；该helper已按精确ID删除。R3b仅把挂载检查改为JSON exactlen2/固定dest/固定source/Typebind/RWfalse/Propagationrprivate，7类正常/恶意fixture通过，scriptSHA7ea17e12f094d6e9aaf64efc2c843325183b1383883a6bb71f73403c121f3e3a；R3archive/SQL/backupbinary不变，待独立delta复核。生产仍cancel21，未迁移22/未改退款限额。

中央服务已实际发布成功（2026-09-20 06:59:16+08）：R3b script c6eddf5aeec014b0d32b4af1d80795f2a70377c3000334b2df3c26a82af47cf3，unit totools-pay-refund22-r3b-20260920.service exit0。migration22、Sub2支付宝/微信每日退款0+opt-in=true，收款0/0保持。API/Worker/PG及4代理healthy/restart0，代理ID未变。pre/post encrypted backups20260919T225857Z/225914Z均verify+isolatedrestore通过。中央P17embedded支持已包含；Sub2UI/自动权益回收尚未发布。全证据在中央docs/operations/SUB2_UNLIMITED_DAILY_REFUND_R3_20260919.md及privateRegistry项目记录；未执行真实订单退款。

forwardpause helper c2edfe266affde6ee7b9192f79d848b35ae4871645e197ca76db20cd842a8043已按只读比对确认原已安装版本与HEAD基线相同后安装；原helper SHA111037b559d4aea9b1bb9cdc8a2929c55602f4282f3151458144502d0de141c1已保留before-p19副本。仅更新控制工具，未执行pause、未改变refundgate或普通请求状态。
Cache作者新的真实PG测试验证外层Ent事务8→3提交后旧Auth8受到regular/Live约束、回滚1→0不留下错误；server wire/serverbuild已通过，缓存slice独立复核中。财务worker真实service→PG balancebonus+并发review/reserve/capture与audit首例通过，继续最小release/zero/legacy/unknown屏障用例。

### P19 返修冻结，等待最终独立结论

Cache marker ownership已改为captured token/revision CAS；old completion A/reconcile-A不得清writer B，regular/Live保持failclosed，0无限保留。真实PGoutertx提交/rollback及Rootrepo/race/PG/UserRepo/service/servercompile/build链全部通过。
财务返修已冻结：balancebonus+concurrency可自动处理、legacybalancecurrent>target保守不降、UNATTRIBUTED屏障、新source后可回放、0audit不改写；source migration255 SHAfe22f4f5f7252af380740d54bdb6ecaf4e4274757792e11f04b8624e018e4e22，helper0ded90c1d2f559a6527a1cf1e4330dbd0153e1e43d98e70eabfa3954739f4fc9。unit新3例和既有outerReview/Prepare/terminal4例通过；真实PGservice2例7.790s、repository4例5.776s通过。独立financial/cache最后delta复核中；root正在较大unit回归。任何未过gate仍不激活Sub2。

最终Sub2 independent financial/cache复核均PASS，无P0-P2；financial PG service7.367s、repo/migration5.682s/0.593s验证了5项修复；marker ownership竞态normal/race和regular/Live保护均独立通过。
Root较大unit回归：service197.325s与migration1.780s通过；repository唯一4个失败已定位为本地httptest被固定Tea SDK字面host:port NO_PROXY匹配误送代理（并非真实Aliyun外部测试）。仅测试helper临时追加对应loopback endpoint，继承其他proxy值不变、生产代码不变；原4断言无删减，重跑通过2.062s，repository全量重跑中。该测试卫生delta独立复核中。

P19源码本地门禁收敛：service全量unit197.325s、repository全量unit4.680s、migrationunit1.780s通过；独立financial/cache/forwardguard/UI均PASS；captcha测试卫生delta也独立PASS（固定SDKliteralhost:port，仅本地mockendpoint绕代理，未削弱断言或改生产配置）。当前保存同一source提交进入CI/security/build-only，Sub2生产仍旧b7fa，尚未启用权益自动退款。

CI818e72240修复：五项Go lint最小修正；旧integration报价测试仍要求订阅到期时间，改为调用前后时间包围的15天报价断言，历史walletpurchase到期与durablereplay断言保持。真实PG目标测试6.870s通过，lintdelta独立PASS。新候选f8c209764c46ccd7b791b90efd6f51b5543ed09b；CI35478690004、安全35478692208、build-only35478694051已启动，旧818镜像不用于此次发布。

最终发布：Sub2 08:52:52+08切换新green，旧blue自然退出；08:55:26+08 canonical CAS启用自动退款。最终内部/外部健康通过，Redisfence ready1/reconcile0，退款预占0，代理未变。P01–P19源码及发布完成；不声称执行过真实客户退款或浏览器视觉验收。
