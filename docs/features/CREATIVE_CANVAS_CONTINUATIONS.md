# 从一张分镜延伸到下一镜

**状态：前后端已实现并完成离线验收，未部署。** 旧画布仍见[接入说明](CREATIVE_CANVAS_INTEGRATION.md)。本次改动不涉及部署、数据库迁移或计费接口。

## 用户反馈与交互验收

旧版桌面和手机路径中，只有创作表单里有「返回画布」，手机关闭创作栏后不再有可见的返回。原图输出按钮使用文本 `+`，不与圆共用几何中心；视频草稿没有输出端口。Go 原先也只允许原图给草稿提供素材。

| 操作意图 | 验收标准 |
| --- | --- |
| 看一片 | 在画布内点成片封面或「查看成片」，复用视频工作台的详情弹窗。展示当前选中的版本、实际时长和已有参数；未成片则看草稿、提示词和来源。关闭详情回当前节点，不跳工作台、不自动拉整段视频。 |
| 确认生成 | 这是不同的次级操作，不和看详情合并。来自画布的工作台首屏始终有「返回画布」；手机收起创作栏、刷新直达、素材过期及版本冲突都能回原画布/原节点，保留视口，不用浏览器后退。直接进入工作台不显示假返回，不接受外部 return URL。 |
| 再接一镜 | 图片、草稿、成片都可以继续；点击加号与拖端口到空白共用建分支命令。圆心与 SVG 加号中心一致，细线端口和触屏按钮不互相截走点击；键盘可达。明确标明「沿用此镜素材」或「用这版成片尾帧续镜」。建分支、切换和删除不生成。 |
| 在大画布上找回镜头 | 先提供节点查找、定位原节点/上一级、全览以及已有撤销/重做；以原引擎的视口和框选能力实现。下阶段再验收多选、复制分支（不复制历史与扣款）、分组/折叠。**顺着线做下一镜**和**把一组节点收进一个容器**是不同功能，前者不要求进入另一个工作区。多人协作、模板发布和一键执行整图不属于这次交付。 |

## 固定参考及差异

- Runway 的[尾帧节点](https://help.runwayml.com/hc/en-us/articles/47184761711379-Using-Utility-Nodes-in-Workflows)和 Magnific / Freepik [Spaces 的端口、连线和节点搜索](https://www.freepik.com/ai/docs/introduction-to-spaces)是产品行为参考，不复制它们的界面、素材或执行器。我们不沿线自动执行付费节点。
- 沿用[已固定的 Vue Flow、AIGCCanvasFlow、BeatDesign 放置与版本规则](CREATIVE_CANVAS_OPEN_SOURCE_20261121.md)。AIGCCanvasFlow 的 `NodeAddButton.vue` 有新节点定位和聚焦，Vue Flow 2 提供端口/坐标/视口；不另造手势引擎、不移植 Vue Flow 1 的组件。
- BeatDesign v0.2.5，`796d89f0f451b436877836f91392152f43c3e270`：[浏览器尾帧实现](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/src/core/media/video-frame.ts)及[时间定位测试](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/src/core/media/video-frame.test.ts)原样移植，归属记入 `frontend/public/licenses/creative-canvas/NOTICE.txt`。项目的 `video-studio/source.ts` 通过既有私有 `content` 接口读媒体，限制大小、像素和解码时限，处理撤权/退出并释放 URL；不拿封面顶替、不接收第三方 URL、不把二进制塞进画布。Chromium 使用前红后蓝的测试片段，验证取得蓝色尾帧而非红色首帧。
- ComfyUI_frontend v1.27.7 `1a6ef576433346875073b3f78460c63d3fd94c6b`：[面包屑](https://github.com/Comfy-Org/ComfyUI_frontend/blob/1a6ef576433346875073b3f78460c63d3fd94c6b/src/components/breadcrumb/SubgraphBreadcrumb.vue)、[导航存储](https://github.com/Comfy-Org/ComfyUI_frontend/blob/1a6ef576433346875073b3f78460c63d3fd94c6b/src/stores/subgraphNavigationStore.ts)、[分层视口测试](https://github.com/Comfy-Org/ComfyUI_frontend/blob/1a6ef576433346875073b3f78460c63d3fd94c6b/tests-ui/tests/store/subgraphNavigationStore.viewport.test.ts)。只参考根视图/上级/当前节点可返回的行为；不移植其 GPL-3.0 前端、测试、执行队列或子图库。

## 后端契约与旧客户端

- 维持每板 160 节点/320 边。注释边与素材输入独立。每个草稿最多一条素材来源边，可有多个子镜头；不允许环、重复来源或悬空引用。节点顺序不决定结果。
- `image → video-draft` 与旧版相同。`video → video-draft` 指定一个已授权、已完成的工作台作品；`video-draft → video-draft` 不带 `take_id` 时只继承父镜头的**输入**（原图、祖先选定成片或纯文字），不会跟随父节点的最新输出。指定 `take_id` 时它必须是来源草稿自己的有效回执；成片晚到、父节点另选预览版本，都不会替换这条边的来源。
- `GET /creative-canvases/:id` 对续镜草稿额外返回按节点 ID 索引的 `inputs`：`{image:{route,batchId,itemId,index},available}`、`{video_id,available}` 或纯文字 `{available:true}`。已撤权的来源保留引用，`available:false`；不得降级成无素材的付费提交。`video_id` 不是 URL、不是向提供方传递的参数。所有类型仍经原工作台归属及可用性检查。
- 新客户端在原 `PUT` 和手动确认后的 `POST .../attempts` 中传 `graph_version:2`；原 `revision` 仍是编辑冲突检查。v1 的单层板不必升级才能保存/绑定。不认识多级图的旧页面若回写有续镜的画布（即使把所有未知边都过滤掉），或从续镜分支绑定生成，返回 `409`，不覆盖、不生成。已绑定后不能修改或借远端祖先替换其实际输入。
- 只存输入身份：旧原图绑定的 JSON 不变；续镜可存工作台视频 ID。没有 SQL 迁移，不持久化 URL、媒体、Key 或新的生成订单。无论打开、保存、加边、读取详情或解析来源均不派发付费任务。只有工作台显式确认才沿原分组报价和提交。

## 本机验证范围（2026-11-21 至 2026-11-23）

- `go test -tags=unit ./internal/service ./internal/handler ./internal/server/routes ./internal/server/middleware -run 'Canvas|VideoStudio|ImageStudio' -count=1`：116 项通过。
- `go test -tags=integration ./internal/repository -run '^Test(CreativeCanvas|VideoStudio)' -count=1`：9 项通过，使用隔离 PostgreSQL/Redis。新用例覆盖三层续镜、错分支和跨用户成片、撤销原分组、过期、子镜固定结果、旧编辑器拒写/拒绑；未向真实 Muse/H3 请求、未修改实际钱包。嵌入已有界面的后端构建通过。
- `node verification/beatdesign/verify.mjs`：固定版本 14 项测试通过（含两个尾帧时间用例）。
- 合并候选工作区使用与 CI 一致的 Node 24：`pnpm run test:run` 497 个文件、4,517 项全部通过；本轮相关 27 个文件、332 项通过。
- 本地 Mock 验证画布内审片、跨页返回、多级分支及固定尾帧，并确认未经明确提交不绑定、不生成。最后一轮 Chromium 检查覆盖生图/视频/画布 × 320/390/1440 px × 浅色/深色共 18 个组合；验证触屏详情、键盘焦点恢复、画布改名自动保存以及仅缩放不触发保存。未验证真实渠道出片，未发布。
