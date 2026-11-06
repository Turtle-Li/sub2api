# 多平台生图工作台整合

## 用户决定与边界

2026-11-07 用户要求整合已有项目设计，核心逻辑代码复用，不重新实现；经真实截图选择 A：画廊为主、聊天改图为辅。充值、余额、套餐、任务执行和结算继续由 Sub2 持有。生成界面采用 `88lin/gpt-image-studio` 固定版本的实际源代码，不把截图当成页面。

本轮是功能开发，不包含提交、push、部署或付费生产生成。源码基线为自有 `fork/main` 的 `ce5f17142704f0fbd269b76b7b9d2a266a4a6f20`，已 fetch 核对。旧首页 checkout 存在他人商城改动，使用独立 app-managed worktree。通用 preflight 返回 CANONICAL_NOT_READY（缺少 trunk 配置），因此使用已验证自有 ref 创建 app-managed worktree，不改全局/原 checkout 配置。

## 固定参考与复用策略

- 前端：`https://github.com/88lin/gpt-image-studio`，revision `5523a592af054bfe4cbee76c2ef8d866ce5cd81e`，MIT；本地洁净参考在 `/Users/lijiayu/auto_work/sub2 api/reference-projects/image-studio-88lin-5523a592`。实际采用 React TaskGrid、TaskCard、InputBar、Lightbox，以及虚拟列表、视口和键盘交互等原代码，保留 LICENSE。身份、持久化、图片读取和参数选择由小型适配层对接 Sub2；不迁写为 Vue。未搬入原项目自带的 LLM AgentWorkspace、独立钱包、IndexedDB 任务真源或本轮无后端合同的蒙版编辑。连续改图使用 Sub2 parent_batch_id 与真实图片引用，不宣称 LLM Agent 编排。
- Sub2：直接复用 batch public service、provider registry、SQL job/item/event、Redis queue、对象存储、reserve/capture/release 和 settlement。Studio 不 HTTP 回调自身公开生图 handler，不新增钱包或重复扣款。
- 一键生图：`/Users/lijiayu/auto_work/一键生图/video-skill-orchestrator`，HEAD `4edef85ab3716fd43989b4a49561a148ba500d93`；Wan/Seedream 核心文件未列为 dirty，但 registry/phase/profile 等存在并发修改。只读参考，不能覆盖这些改动。优先抽取原 Python 请求/解析函数及原测试，使用中立薄桥；不翻写为 Go，也不搬 VSO 调度和账本。
- Muse：复用本地 `muse2api` 的原生 `/v1/images/tasks` 异步合同；固定 `5542a479ec6ba036a9717e8776380ff5aacf3fe7` 的 `app.py` 与已核查 `b84ecba0e3e09bd77175ea49151a590c2b8d2c63` 中该文件一致。MIT。只增加 Sub2 任务适配器，不复制浏览器控制、账号池或生成服务。详细合同与测试见 `MUSE_BATCH_PROVIDER_20261107.md`。
- 混元：用户指向 WorkBuddy，已核实 `misakano7545/workbuddy2api-panel` revision `a19c78fc143dce790041071c18e1c7705a5a4f1e` 的真实图片接口（MIT）。现有网关提供 `/v1/images/generations`、`/v1/images/edits`，底层 `internal/upstream/images.go` 调用 `/v2/images/generations`、`/v2/images/edits`。中国区生成模型 `cn:hunyuan-image-alpha`，编辑模型须按上游区域合同选择，不能用 Hunyuan 文本模型冒充。优先调用现有网关以复用账号池，不在 Sub2 另建账号池。

### WorkBuddy 固定源码核对

主参考：`https://github.com/misakano7545/workbuddy2api-panel/tree/a19c78fc143dce790041071c18e1c7705a5a4f1e`。相关路径：`internal/server/handler.go`、`internal/server/images.go`、`internal/upstream/images.go`；测试 `internal/server/images_test.go`、`images_openai_spec_test.go`、`imagetool_pipeline_test.go`、`internal/upstream/images_test.go`。MIT 版权方包括 Sliverkiss、linguo2625469、misakano7545；如抽取代码，保留完整 LICENSE。

对照：`dingminhua/dsh-connect-workbuddy` revision `e39dba5c1f7ee90daa6d08857f2496538fdb7c37` 的 `src/image-gen.ts`、`src/image-tool.ts`、`tests/image-gen.spec.ts`，MIT / Copyright 2026 LaoDing。仅做协议对照，不引入第二套 TS 网关。

采用合同：逐图 `n=1`、真实模型/地区匹配、参考图编辑、图片结果落到 Sub2 对象存储后交付；任务和商业结算仍唯一归 Sub2。OpenAI 返回默认 URL，要求 `b64_json` 也可能在网关下载失败时降级为 URL，必须按可信下载策略处理，不把格式偏好当保证。

有意差异/开放阻碍：主参考 `internal/server/images.go` 的 `imageAttempts=3` 在部分传输错误后换号重发，未区分未知执行结果；Registry 已记录的部署 revision `dab1b08c8cb5aa3e0bf6f9ab10d3b1728ffa4ffe` 亦存在该行为。Sub2 自身禁止重发不能消除网关内部重发，正式启用批量前需收敛该策略。尚未对生产图片端点进行实测，本轮不修改网关部署/自动更新策略，不将源码能力表述为生产验收。

## 关键事实

当前 `/v1/images/batches*` 和 `/v1/images/*/async` 只接受 API Key。JWT 门面必须复用完整准入规则，并在服务器验证 key ID 归属/状态、组权限、模型白名单、用户状态、余额/套餐额度与安全审计；客户端只提供选择 ID，不传用户 ID 或获得 key 内容。现有实时 task 同时按 UserID/APIKeyID 隔离，不能放宽。

OpenAI batch executor 直接调用 `Gateway.ForwardImages`，batch settlement 独占用户扣费。`isOpenAIImageBatchExecution` 仅控制 worker deadline/取消，不能用它作为免扣费标记。

## 实施 DAG（程序性计划）

1. S1 前端来源与产品契约：固定参考版本/许可证，记录用户选择与可复用组件；先检查原有源码、测试和设计 token。完成后允许 S2。
2. S2 真实纵向切片：同源 Studio、现有登录、服务端校验所选 key ID、复用 batch service，贯通 Gemini/OpenAI 提交→任务→图片→下载。单一实现者负责业务变更。
3. S3 图库与连续改图：服务端任务为真源，复用原界面，关联原图和明确选择的参考图；不伪造 Agent 多模型能力，不把 Redis TTL 当作永久资产期限。保留原批量入口与历史兼容。
4. S4 更多提供方：在来源与执行合同确认后复用 Wan/Seedream 原 Python 核心及 Muse 原生协议；无独立钱包、轮询调度器或自动重放。WorkBuddy 图片能力确认前保持不可用。
5. S5 独立 QA 与 Review：冻结变更后验证用户隔离、失效登录、模型/组拒绝、部分失败、重复请求/回调、未知提交不重放、单次结算、页面刷新与下载；真实桌面/手机截图验证。

S2→S3→S4 有依赖；源代码/协议核查可以并行。没有外部调用条件的部分必须明确未验收，不能称上线可用。

## 验证与停止条件

前端构建与相关现有测试；后端 handler 权限及服务契约测试；原 batch/settlement/恢复回归。选择端到端浏览器夹具验证交互，不使用假数据宣称真实生成成功。只有必要胶水新增测试，不为仅复制第三方文件逐行写镜像测试。

保持旧批量路由和数据不变；新入口可撤回，回滚不删除历史或财务记录。若发现唯一扣费或 owner 隔离无法从原服务保证，先修复边界再接 UI。任何未证实 provider 不进入可选目录。

## 交付状态

- S1–S3：JWT 会话门面、原前端源码接入、画廊、下载、任务详情、批量输入、取消与连续改图已实现。旧批量入口保留。服务器任务与 item 状态为唯一真源，用户/密钥切换取消旧请求并清除界面引用。连续编辑在成功后自动采用新图片，手动切换受版本和取消信号保护。
- S4：Muse、万相/Seedream 桥接及 WorkBuddy 薄适配器均已实现、通过独立审查并合入集成工作树。万相/Seedream 原 Python HTTP 与尺寸策略保留逐文件来源证明；配置默认关闭，安装可选 bridge 并显式配置后才提供模型。已有 OpenAI/Gemini 执行和账本仍复用。WorkBuddy 精确区分生成和编辑模型，显式单次提交验证标记默认关闭；网关修复补丁已在本地完成及审查，生产网关和自动更新策略未修改。
- S5：当前测试证据分别在 `.omo/evidence/image-studio/session-api/`、`source-port/`、`edit-continuation/`、`terminal-status/`、`model-parameters/`、`token-cleanup/`，提供方审查和测试在 `.omo/evidence/` 顶层。界面实际截图与手工操作报告在 `/Users/lijiayu/auto_work/sub2 api/artifacts/image-studio-qa-20261107/QA.md`；图像明确标注为参考仓库截图裁片，未冒充真实生成。

已核验的范围是本地代码、契约/回归测试及本地 HTTP 夹具上的真实浏览器交互。尚未核验生产 DB/Redis、Docker 扩展构建、真实提供方生成和生产价格。不得将本地验收表述为部署或上线完成。

最终集成检查：后端278项顶层测试通过（race、零跳过），Python56项通过；server/repository组装编译通过。前端最终44项功能测试、类型检查与生产构建通过，来源/设计复核为 APPROVE。完整交付说明见 `IMAGE_STUDIO_DELIVERY_20261107.md`。
