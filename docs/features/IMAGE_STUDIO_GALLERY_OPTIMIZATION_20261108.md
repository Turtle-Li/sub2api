# Image Studio catalogue and gallery refinement

Follow-up on 2026-11-08: the safe key directory publishes separate generation
and historical-task eligibility. Catalogue/gallery discovery now skips unrelated
keys and runs at most six independent reads at once. Preview reads use three
slots, independent of the original-download queue; bounded 429/503 retries keep
cold backfill admission from losing a cover. Price calculation reuses group,
user-rate and candidate-account reads within one catalogue request only.
Gemini image-output token prices are converted to a whole-image quote using
verified resolution-specific output token counts, before existing multipliers.

Baseline: `06b619a2d7b1bd611b518c217d8f72b5349c3fc9`.

Owner request: organize model choice by platform/model with visible prices,
reduce gallery chrome, and load works quickly with thumbnails, following VSO.
The Kimi creation rail, host theme, server task authority and single ledger remain.

## Analysis and implementation plan

Bug: STUDIO-GALLERY-20261108
现象: History appears late; small cards download full images serially.
复现条件: Multiple owned keys, twelve batches per key, large generated images.
调用链: Workspace → workspaceAPI.discover → each key's tasks → batch list →
sequential item lists → Promise.all barrier → TaskCard → full item content.
影响范围: Initial history, catalogue readiness, scrolling and download bandwidth.
疑似根因: Direct source evidence in api.ts/workspaceAPI.ts/TaskCard.tsx: N+1
HTTP metadata reads, catalogue/history barrier, no thumbnail variant. Production
latency is not measured; object storage/provider latency may contribute too.
推荐修改方案: Owner/key-bound paged item+job metadata read, independent catalogue
render, cached 480px thumbnails and explicit original reads.
风险: Preserve authorization on cache hits, opaque cursor ownership, loaded-item
polling, uncertain-submit routing, native status projection and original downloads.

| Task | Dependency | Owner / allowed writer | Output and acceptance | Validation / rollback |
| --- | --- | --- | --- | --- |
| G1 metadata | — | Codex, backend service/repository/Studio adapter | One joined query per key/page, 24 items; stable cursor, server search/status; no object reads | SQL/service/adapter tests; previous image restores old APIs |
| G2 thumbnails | — | Codex, download service/handler/card | 480px JPEG, bounded cache, same owner admission, originals only on explicit use | Dimensions/bytes/cache/ownership tests; no schema or original changes |
| G3 model UI | — | Codex, public catalogue/InputBar/host component | Platform families, searchable exact models, server-derived size prices, keyboard selection | Pricing and mounted interaction tests; submit ledger unchanged |
| G4 integrate | G1–G3 | Codex, Studio/CSS/docs | Compact tools, independent first paint, host themes/phone | Targeted tests/build + browser screenshots; bounded visual fix/confirmation |

No production actions or paid generation are part of this local change. The branch
preflight reported CANONICAL_NOT_READY (no configured trunk); the task uses a new
managed worktree from freshly fetched fork/main, preserving existing worktrees.

## References and intentional differences

VSO `4edef85ab3716fd43989b4a49561a148ba500d93` (owner project):
`backend/src/main/resources/static/workspace/workspace.js` model family/price
rendering and paged gallery; `userflow/read/GalleryCardQueryService.java`,
`userflow/gallery/GalleryCardAssetService.java`, `service/ImagePreviewService.java`
and corresponding `GalleryCardQueryServiceTest`, `GalleryCardAssetServiceTest`,
`CloudGalleryUiIntegrationTest`. Adopt the interaction and delivery separation,
including 480px/quality80 preview. No Java/JS source is transplanted. Sub2 keeps
existing job/items instead of introducing VSO's projection tables, and retains
generic credit units instead of VSO's CNY wallet or membership points.

88lin `5523a592af054bfe4cbee76c2ef8d866ce5cd81e` (MIT): inspected pinned
`src/components/{InputBar,TaskCard,TaskGrid}.tsx`, `src/lib/virtualGrid.test.ts`.
Retain measured virtualization, viewport gating and viewer lifecycle. Host-owned
catalogue, metadata and thumbnail adapters extend the differences in UPSTREAM.md;
retain the upstream MIT license. Never copy local browser storage as task authority.

## 实现结果

- 模型入口显示平台、完整模型名及当前分辨率单价。点击后按平台分组，
  可搜索平台/模型，键盘选择和 Escape 关闭均可用；手机使用同一原生弹层。
  预计消耗随分辨率和批量图片数变化，缺价显示“价格待配置”。报价复用
  现有计费快照，包含用户/分组/图片/账号倍率及供应商折扣，实际按任务结算。
- 标题、数量、状态、搜索和刷新合并为单行工具栏。桌面实测36px；手机
  用紧凑状态下拉，搜索按需展开。右侧空间优先留给作品。
- 旧列表：获取可用 key 后，每个 key 先读12个批次，再逐个读取最多100个
  图片条目；等模型目录与这些读取全部完成后显示，卡片按可见区域串行拉原图。
  大批次的后续条目需要另外翻页，原先也没有一次获取整个历史。
- 新列表：历史与模型目录独立加载；每个 key 每页一条 HTTP 请求和一次
  owner/key 限定的 job/item 联表查询，最多24个条目，使用行号组合游标。
  搜索、状态和续改根任务由服务端筛选。继续加载保留各 key 的独立位置。
  轮询只读已加载的 custom_id，千图批次不会反复从头读到当前页。
- 浏览器列表按可见区域获取480px、质量80 JPEG缩略图；详情、放大、下载、
  引用使用原图接口。按后续用户要求，缩略图现已在结果处理时保存至现有
  私有云存储，冷启动直接读 JPEG。历史缺失图受控按批次补齐一次，读取原始
  结果后保存整批缺失封面；共享租约防止多实例并读同批源文件。内存只加速
  已存缩略图，服务端32MiB/256条、前端16MiB/64条。历史源读取仍服从原有
  用户下载并发限制。迁移270支持分页；271只增加任务的缩略图生命周期字段。
  不标记用户已下载，不新增生成或扣费操作。详细契约与独立冷读证据见
  `IMAGE_STUDIO_PERSISTENT_THUMBNAILS_20261108.md`。

## 固定参照补充

VSO 的 Java 源码位于 `backend/src/main/java/com/vso/controlplane/`，相关
测试位于 `backend/src/test/java/com/vso/controlplane/`，具体相对路径为：

| 源码 | 测试/采用契约 |
| --- | --- |
| `userflow/read/GalleryCardQueryService.java` | 同目录 `GalleryCardQueryServiceTest.java`：owner 过滤、分页边界和游标；本项目保留实时状态查询，不采用其冻结快照签名游标 |
| `userflow/gallery/GalleryCardAssetService.java` | 同目录 `GalleryCardAssetServiceTest.java`：越权先拒绝、预览与原图分离、variant 回填前检查原始制品 |
| `service/ImagePreviewService.java` / `ImageVariantStorageService.java` | thumbnail long-edge480/quality80；采用预存和历史一次补齐契约，绑定本项目已有任务和私有对象存储 |
| `backend/src/main/resources/static/workspace/workspace.js` | 平台/模型族、版本和价格选择；用 `git show 4edef85a:…` 复核固定源码，未采用工作目录内的额外未提交修改 |

采用交互及契约思路；未移植 VSO 源码，无新增第三方许可证代码。88lin 的
MIT 文件仍保留，虚拟网格、可见区域加载与查看器生命周期维持其固定契约。

## 验证与证据

本地使用真实应用及显式标注的合成 API，无生产数据、真实付费请求或部署。
证据目录：[gallery-optimization](../../.omo/evidence/gallery-optimization/)。

| 检查 | 结果 |
| --- | --- |
| 前端 Vitest | 发布复核9个测试文件、62项通过；包含平台选择/价格、独立历史加载、归属路由、续改、参数、查看器和虚拟网格；最新证据见 `docs/operations/IMAGE_STUDIO_GALLERY_RELEASE_20261108.md` |
| 类型与构建 | `vue-tsc -b`、`pnpm build`通过；沿用现有 bundle 大小提示，无新增构建阻断 |
| ESLint / 设计检测 | 修改的可执行 UI 文件通过；impeccable detector结果为空数组 |
| 后端测试 | service/repository/handler/routes/migrations 中 BatchImage/ImageStudio 测试通过；新增图库SQL、授权、价格倍率/区间、缩略图、缓存上限及加载条目轮询验证 |
| race | ImageStudio service/repository/routes 通过；覆盖缓存、报价、游标及入口权限 |
| 浏览器桌面 | 1272×716，浅色/深色；平台切换、搜索、4K价格、原图放大及 Escape 层级通过 |
| 浏览器手机 | 390×844，浅色/深色；画廊工具栏、创作面板、平台搜索和长模型名通过 |
| 低高度 | 390×500，模型面板上/下边界34px/466px；无横向溢出，内部列表可滚动；未声称真实手机键盘测试 |
| 作品行为 | 提示词搜索可检索初始页之外作品；空搜索与失败重试状态通过；分页42→66项；两轮改图均进入同一根任务，历史包含两个新结果 |
| 慢模型目录实验 | 合成 API 的模型接口延迟8002ms时，42项历史先显示；观察窗口内浏览器发出2次图库分页、6次缩略图、0次原图接口请求。该实验没有真实对象存储，不能证明后端源对象读取为0。轮询请求单独存在，不计入初始化元数据请求 |
| 缩略图样本 | 单元测试2048×1024合成 PNG 194440字节 →480×240 JPEG 29651字节，缩小约84.8%；这不是线上耗时或所有图片格式的压缩率保证 |

具体命令和输出：`vitest.log`、`eslint.log`、`build.log`、`backend-unit.log`、
`go-race.log`、`migration-check.log`；请求证据为`requests.jsonl`。
`browser-console.json`记录合成管理员环境下既有 BaseDialog 的 show=undefined
警告；本次 React 生图界面没有执行异常。该警告不作为生产控制台验收结论。

![桌面模型与报价](../../.omo/evidence/gallery-optimization/models-light.jpg)
![桌面画廊与紧凑工具栏](../../.omo/evidence/gallery-optimization/desktop-light.jpg)
![手机画廊](../../.omo/evidence/gallery-optimization/phone-light.jpg)
![手机模型](../../.omo/evidence/gallery-optimization/phone-models-dark.jpg)

其余截图：`desktop-dark.jpg`、`models-dark.jpg`、`phone-dark.jpg`、
`phone-models-light.jpg`、`phone-short-models.jpg`、`history-before-catalogue.jpg`、
`search-results.jpg`、`empty-search.jpg`、`original-detail.jpg`、
`continuation-two-rounds.jpg`、`history-read-error.jpg`。

## 边界、复核与回滚

先前内存版首次读取原始结果、重启变冷的问题已在本地改为持久化派生对象。
新结果预生成；历史缺失封面在一次受控批次扫描后保存。失败、损坏或过期的
源不能承诺自动恢复；不会重跑生成。这里未执行线上性能测量、数据库
EXPLAIN、真实供应商或生产 COS 验证。

2026-11-08追问核对：历史来自任务表，截图数据是本地合成记录；内存淘汰、
进程重启和不同实例均可能再次读取源图，不是全局“只读一次”。上一轮浏览器
请求数不足以验证完整取图性能。持久化缺口的静态证据、固定 VSO 参考及应有
验收见 `../bugs/backend/BUG-20261108-studio-thumbnail-cold-read.md`；后续修复
结果与冷启动读源计数见 `IMAGE_STUDIO_PERSISTENT_THUMBNAILS_20261108.md`。

复核：新模块职责分别为元数据分页、报价投影、缩略图缓存及平台选择；输入
边界限制长度、枚举和正数游标，SQL值绑定；缓存命中仍检查 owner/key/结果
生命周期，HTTP缓存按身份和 key 变化。无凭据输出、自动重放 POST 或跨 key
续改。价格与任务状态仍来自服务器，列表渲染保持虚拟化。

新源码模块均小于200行有效代码。SIZE_OK：既有 `Studio.tsx` 从249到277行，
保留页面状态编排与原有查看器生命周期，避免本次视觉/加载修复扩成状态重构；
模型选择已独立为71行模块。下一次增加页面编排时应先提取历史加载或详情。
既有较大的批次服务/仓储文件仅增加接口字段及接线；新查询、报价和缓存均
放在独立文件。TaskCard为241行，保留固定上游实现，本次只切换预览适配方法。

当前变更位于独立 managed worktree，未提交、推送或部署。后续发布前须按
项目原有流程确认迁移270/271及真实环境 smoke。回滚保留新增列和私有缩略图，
使用保留派生图清理能力的兼容镜像；不回滚任务数据、钱包或供应商任务。
