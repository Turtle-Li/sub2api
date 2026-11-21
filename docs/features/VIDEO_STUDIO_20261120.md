# 视频工作台一期：后端说明与前端交接

**当前契约**：Gemini 4 现在直接负责视频前端实现与复审；以下原 Kimi 分工只作历史记录。Muse 工作台不开放时长设置。新建视频已切换到[登录会话分组钱包与渠道参数](VIDEO_STUDIO_WALLET_CONTROLS.md)。下文基于用户已有 Key 的新建路径及旧详情交互仅为历史说明。保留原作品画廊和旧任务安全边界。

**展示契约已更新：** 所有者不采纳下面的旧列表方案。当前界面以作品封面和生成参数为主，请先看[作品画廊说明](VIDEO_STUDIO_GALLERY_20261121.md)。以下设计与评审记录仅保留历史，旧验收不能作为此次画廊的验收。

**历史列表版状态（不代表新需求验收）：隔离工作树当时已完成后端、本地前端实现和 Gemini 对实际修订界面的视觉复核。开关默认关闭，未提交或上线。** 原有 Codex 界面稿不是设计基线或可交付终稿。前端按下列次序交接，Codex 只写后端、传递设计材料和做客观验证，不补写前端。

| 阶段 | 负责人 | 下一阶段可以开始的条件 |
|---|---|---|
| 1 · 页面设计 | Gemini 4，Antigravity CLI，原生 Google 登录 | 基于真实生图工作台参考，交付可渲染的桌面／手机方案，写明推荐方向、关键状态和素材交互。仅列出模型或发出请求，不算有稿。 |
| 2 · 页面实现 | Kimi Code 官方 CLI，所有者已配置的中转 | 拿到上一步原稿才改 Vue；按冻结契约实现，不改后台或用生产生成作演示。 |
| 3 · 对照复审 | Gemini 4，同一原生客户端 | Codex 对 Kimi **实际页面**构建和运行夹具，提供桌面／手机 × 明／暗截图、交互记录；Gemini 指明差异、影响和修订优先级。设计原型或旧稿截图不能替代。 |
| 4 · 优化与复验 | Kimi 修改，Codex 验证 | 对应复审意见逐项修订；复跑用例并重拍同场景。仍有影响使用的问题，连同新截图交还 Gemini；未验证的状态明确列出。 |

任一客户端不可用时停在相应阶段。不更改全局分工，不切账号或改用通用模型 API。恢复 Kimi 只需要已启用的非秘密中转配置名，不需要在文档或聊天里提供密钥。

**账号与路由复核：** 前三次 Argon 设计调用的原生 OAuth 成功日志都与 Antigravity 桌面设置里的当前账号一致。桌面核对时 Argon 剩余 100%，Gemini 模型组的五小时额度剩余 100%、周额度剩余 97%；当时 Argon 的 HTTP 503 是服务端报告没有容量，不是账号额度耗尽。Kimi 实际使用 **CC Switch** 下的自有中转 `T`；官方 Kimi CLI 按进程选 `--model turtle/kimi-k3.1-pro`，已成功完成一次只读接口交接核对，不改用户默认项，不需要 TT Switch。这次核对不是前端实现或视觉复审。

**已被取代的列表原稿：** 同一账号的 Gemini 4 Neon Medium 已交付两种可渲染方案，推荐 A：保留左侧创作栏，右侧用任务索引代替无封面卡片，明确点击才加载审片区。手机先看历史，展开后创作。已对两方案分别跑桌面、手机与明暗渲染。原型不替代 Kimi 实现、后续截图复审及实际操作验证。

**列表版当时的验证与限制：** Kimi 经 CC Switch 的 `turtle/kimi-k3.1-pro` 实现并修订页面；Gemini 4 Neon 对真实桌面／手机的明暗截图完成复核，任务号可辨识性、状态区分和手机分隔点三项全部通过。生产模式构建、相关 19 个文件的 257 项测试通过。合成视频可解码、下载哈希一致；未发起线上视频。桌面 Chromium 的原生视频控件有时不把 Esc 送给页面，使用“收起审片”（键盘 Shift+Tab 后回车）能关闭并回焦。全量套件的商店支付反馈用例在未修改基线也失败；零外部请求企图门禁仍捕捉到已被拦截的宿主收银 SDK 请求，不能宣称上线门禁全绿。

## 目标、范围和归属

独立登录页 `/video-studio` 与 `/image-studio` 相邻；仍用同一套产品工作台，图片详情的结果动作只创建草稿，不生成视频。已获授权的用户无需粘贴 Key。首期仅 Muse、H3 和 H3 Fast，不接 SD，不新增无 Key 视频钱包，不动既有公网 `/v1/videos` 兼容协议。

服务端只在本人有效 Key 中选择：检查用户、分组、IP ACL、精确模型身份、可调度的映射及有效价格。Muse 需专属 Muse 组；H3 仅走配置批准的钱包组。排在首位的合格授权作为报价范围；不因已有任务或提交失败切换 Key、渠道或账号重发。配置 `gateway.video_studio_enabled`（或 `GATEWAY_VIDEO_STUDIO_ENABLED`）只控制目录和新建；停新单时已有任务的查询、取片和原 H3 结算仍保留。公共设置仅暴露布尔开关，不是准入依据。

## 登录会话契约

所有接口使用 `/api/v1/video-studio` 前缀、现有登录认证；请求不需要也不能用 `X-API-Key-ID` 指定视频授权。除媒体正文外均使用项目既有 JSON envelope。

| 方法和路径 | 行为 |
|---|---|
| `GET /models` | 返回 `{enabled, items}`。每项含精确模型 ID、显示名、提供方、时长范围/默认值、画幅、是否支持首尾帧及 seed、参考图上限、按秒数索引的 `quotes`、`currency` 和 `quote_id`。授权、单位、金额变化需重新确认。没有有效报价不应提交。 |
| `POST /jobs` | JSON：`request_id`（客户端创建的非零规范 UUID）、`model`、`quote_id`、`prompt`、`seconds`，可选 `size`、`seed`、`first_frame`、`last_frame`、`references`。第一次返回 `202` 和本地任务；相同意图重复则 `200` 返回已有任务，同 ID 不同内容 `409`。不提交 URL、真实 Key 或账本价格。 |
| `GET /jobs?cursor=…` | 返回 `{items,next_cursor}`，每页至多 24 条，仅工作台个人记录。只读本地索引，不检查渠道、不取视频正文，不因浏览列表触发 Muse 计费。 |
| `GET /jobs/:id` | 按本人＋UUID 查原 Key/组，并用原归属查询需要跟踪的任务。不要批量轮询全页。状态：`submitting`、`queued`、`running`、`completed`、`failed`、`uncertain`、`expired`。H3 的 `billing_review` 可与 `completed` 共存。 |
| `GET /jobs/:id/poster` | 原授权内只读已缓存的真实成片 JPEG，无缓存不取上游；`has_poster` 和可选 `parameters` 见画廊说明。 |
| `GET /jobs/:id/content` | 鉴权后从绑定的原任务读取二进制；不返回可分享的上游 URL。原授权有效但余额/配额耗尽不阻止取旧片；失效、撤权、换用户或移组不可取。H3 原回执窗口为 24h；Muse 不承诺文件永久可取。 |

Muse 接受 5–15 秒、最多两张有序参考，不接受首尾帧锁定；H3 接受 4–15 秒、首帧＋可选尾帧，或独立的一组参考图；Fast 接受 5–14 秒和横/竖两种画幅、可选 seed。H3 的**整个上游 JSON** 不得超过 8 MiB；工作台总请求最多 24 MiB。每张上传须为可解码的 PNG/JPEG/WebP，不超过 8 MiB 和 4096² 总像素；在创建任务之前核验并限制进程内的请求/解码内存。站内原作只能在生成前用本人原图授权读取，不用过期链接、缩略图或浏览器缓存代替。

## 资金与恢复

- 迁移 `282_video_studio_jobs.sql` 只增加私有任务索引。`(user_id,id)` 唯一，先落占位再发起渠道调用；索引不保存原提示、原图、上游媒体地址或第二份资金流水。硬删除用户会连带移除该用户的工作台索引。
- 创建时按原 Key/组验证金额与钱包币种；H3 走既有冻结、后台计量结算/释放及超价停单逻辑；Muse 在首次明确观察到完成时沿用既有幂等计费。单任务查询和取片都不能变成第二次生成。公共面板审计不记录提交正文。
- 没拿到可核验回执的超时/5xx 不等于失败：界面应保留原 `request_id`，只查 `GET /jobs/:id`，不要刷新后重发。回执落库失败时占位仍在；超过五分钟仍未完成占位的，对外显示 `uncertain`。可明确判断在发送前被价格/准入拒绝的，则不是“渠道可能还在生成”。
- 出现越权、重发或账单解释不清，先关新单；留存历史与原渠道对账路径，不回退数据库，也不影响公共 API。

## 实现与验证路径

路由见 `backend/internal/server/routes/video_studio.go`；会话适配、素材校验、能力目录、持久化分别在 `backend/internal/handler/video_studio*.go`、`backend/internal/service/video_studio.go`、`backend/internal/repository/video_studio.go`。渠道契约以本项目 `docs/features/MUSE_PUBLIC_VIDEO.md` 与 `docs/goodput-h3-video.md` 为准；图片工作台和既有参考/许可见 `docs/features/IMAGE_STUDIO_SESSION_API_20261107.md`、`frontend/src/features/image-studio/UPSTREAM.md`。本次未复制新的外部产品源码。

已在本地运行：

```sh
# 从 backend/ 执行，均不使用生产账号或真实模型
 go test -count=1 -tags=unit ./internal/handler ./internal/service ./internal/config ./internal/server/middleware ./internal/server/routes -run 'VideoStudio|GoodPut|Muse|ImageStudio|PublicSettings|RegisterUser|GrokMediaVideoCompletionStillClaimsBillingOnce'
 go test -count=1 -tags=integration ./internal/repository -run 'TestVideoStudioReceipt|TestGoodPutVideoJobsReserveSettleAndRefund|TestGoodPutVideoReconcilesLegacyReviewAtActualUsage'
```

Muse 使用实际会话处理器和假渠道，断言两张参考图顺序、列表零观察、三次完成查询及两次取片仅一条计费指令；H3 覆盖余额耗尽后取片、撤权、过价零提交、不确定结果及回执写入失败；临时 PostgreSQL 验证唯一占位（12 路并发）、恢复、用户隔离、游标分页和不可倒退的已核验状态。**这些结果不代表上游实单成片、线上扣费或前端视觉验收。** 前端已由指定客户端另行实现和复核，验证范围及已知限制见上。真实生成、提交、推送及部署未授权。
