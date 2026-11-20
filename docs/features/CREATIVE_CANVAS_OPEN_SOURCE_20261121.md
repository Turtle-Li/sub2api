# 生图 / 生视频无限画布：开源选型

2026-11-21 · **选型记录。** 首版使用下列固定版本；实际接入范围、权限与本地验证方式见[创作画布接入说明](CREATIVE_CANVAS_INTEGRATION.md)。 [产品提案与已确认的离线样机](./CREATIVE_CANVAS_20261121.md)

**建议用 Vue Flow 2 做底座，不自己写拖拽、缩放和连线，也不把别人的 AI 工作台整个搬进来。** 用户关心的是：从自己的同一张图建出「推镜头」「固定机位」两条视频草稿，原图不动；两个成片回来后可以并排比较。围绕这条路径，下面三个项目分别解决不同的问题。

| 项目 | 用在我们哪里 | 不拿什么 |
| --- | --- | --- |
| [Vue Flow 2（xyflow）](https://github.com/xyflow/xyflow/tree/29ab9e001c2ba320c9d84b00c729c745132d9145/packages/vue) · MIT | **直接依赖的首选**。平移、触控缩放、坐标换算、拖动与框选、连接端点、小地图；图片和视频是可交互的自定义节点。 | 它不是生成工作流，也不提供我们的账号存档、素材权限和扣费。 |
| [AIGCCanvasFlow](https://github.com/MaLunan/AIGCCanvasFlow/tree/c88c00c58c6fcf613d58c6674c46b4a11c1d9911) · MIT | **最贴近已有原型的 Vue 产品参考**。图片卡旁点「＋」建视频节点并连回原图；也能从端点拖到空白处再选类型。触屏用户不用瞄准细线。 | 不移植基于 Vue Flow 1.x 的整套组件、节点内生成表单、登录或任务服务。 |
| [BeatDesign v0.2.5](https://github.com/BeatAPI/BeatDesign/releases/tag/v0.2.5) · Apache-2.0 | **最值得复用的两小块逻辑**：把新卡放到原图附近、避开已有卡；多个成片版本只在本草稿范围内比较，记住用户选中的版本。 | 不引入它的 React 编辑器、BeatAPI Key、MCP、时间线或素材服务器。 |

想先看手感：[拖出一条线，在落点新建节点（可操作）](https://vueflow.dev/examples/nodes/add-node-on-edge-drop) · [移动节点后保存并恢复（可操作）](https://vueflow.dev/examples/interaction/save-and-restore) · [AIGCCanvasFlow 界面预览](https://github.com/MaLunan/AIGCCanvasFlow/tree/c88c00c58c6fcf613d58c6674c46b4a11c1d9911/img)。前两个是引擎示例，不是我们的成片界面。

## 哪些可以直接交给开源项目

### 1. 画布手势、连线和视口：Vue Flow 2

- **版本不要混用。** 原提案看的 [`@vue-flow/core 1.48.1`](https://github.com/bcakmakoglu/vue-flow/blob/87b2d2c4e8b8e1e321a9cb0795987672b13de913/packages/core/package.json) 是 1.x 源码基线（`87b2d2c4e8b8e1e321a9cb0795987672b13de913`），不是当前推荐包。官方已[并入 xyflow](https://xyflow.com/blog/vue-flow-joins-xyflow)，2.x 改名；核对 npm 发布记录和 GitHub [正式发布](https://github.com/xyflow/xyflow/releases/tag/%40xyflow%2Fvue%402.0.2)：首个 2.0.0 于 2026-11-16 发布，**首轮试接锁 `@xyflow/vue@2.0.2`**（11-17 发布；`29ab9e001c2ba320c9d84b00c729c745132d9145`），不是 `next` 预览。1.x 包的 npm `latest` 是 1.48.2，不是新版的同名入口。两代的导入、事件和视口 API 不要拼用；[迁移差异](https://vueflow.dev/learn/troubleshooting/migrate-to-v2)。
- **为什么优先 Vue，而不是 React Flow？** 本项目的路由、视频工作台是 Vue；生图页面是在 Vue 路由内单独挂载 React，并不要求画布跟着用 React。宿主锁定 Vue / `@vue/reactivity` 3.5.43、Vite 5.4.21；满足[新版 Vue peer 要求](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/packages/vue/package.json)。但库要 VueUse 14，宿主锁的是 10.11.1；允许隔离共存，试接时实测依赖与分包，而不是为画布全站升版。2.x 刚发，不据此承诺兼容性或帧率；如果宿主构建或触控不过，回到上面的已审 1.x 对照，不悄悄混装，也不回头手写交互引擎。
- [XYDrag](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/packages/system/src/xydrag/XYDrag.ts)、[XYPanZoom](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/packages/system/src/xypanzoom/XYPanZoom.ts)、[XYHandle](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/packages/system/src/xyhandle/XYHandle.ts) 处理底层输入。用公开的[自定义卡片](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/examples/vue/src/CustomNode/CustomNode.vue)及[拖入坐标示例](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/examples/vue/src/DragNDrop/DnD.vue)，让现有封面、播放和详情仍归工作台。上游有 [Vue Playwright 入口](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/tests/playwright/playwright.vue.config.ts)及 [节点拖动/多选/键盘](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/tests/playwright/e2e/nodes.spec.ts)、[点按连线](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/tests/playwright/e2e/click-connect.spec.ts)、[视口中断](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/tests/playwright/e2e/viewport.spec.ts) 测试；本轮核对了用例，没有运行它们的浏览器套件。
- **存档原语 ≠ 账号存档。** 2.x [存取动作](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/packages/vue/src/store/actions.ts)及[恢复示例](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/examples/vue/src/SaveRestore/Controls.vue)用 `setNodes / setEdges / setViewport`；[视口实现](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/packages/vue/src/composables/useViewportHelper.ts)保留合法的零坐标。1.48.1 的 `fromObject` 则可能跳过轴上的视口。我们的服务端只存允许的字段，不能照抄示例把 `toObject()` 整份写入浏览器或数据库。
- **撤销/重做不必另买或从零写。** Vue Flow 的[官方撤销示例](https://vueflow.dev/examples/interaction/undo-redo)是 Pro，不属核心 MIT。宿主已装的 [VueUse `useManualRefHistory`](https://github.com/vueuse/vueuse/blob/dce876a23ccec3335c2fbf7600244ea8756889a5/packages/core/useManualRefHistory/index.ts)有[独立测试](https://github.com/vueuse/vueuse/blob/dce876a23ccec3335c2fbf7600244ea8756889a5/packages/core/useManualRefHistory/index.test.ts)（10.11.1，`dce876a23ccec3335c2fbf7600244ea8756889a5`）。建议只在松开拖拽或完成「建草稿＋连线」时记一次，对可编辑布局与草稿作有限深快照，不纳入素材内容、回执和流水。**本机用宿主已装版本验证**：撤销第二条草稿会同时去掉它的线，第一条不变；重做能一起恢复。未修改业务代码。

### 2. 从同一张图建两条视频草稿：AIGCCanvasFlow

固定 `c88c00c58c6fcf613d58c6674c46b4a11c1d9911`。图片组件挂载[节点旁的加号](https://github.com/MaLunan/AIGCCanvasFlow/blob/c88c00c58c6fcf613d58c6674c46b4a11c1d9911/h5/src/components/nodes/ImageNode.vue) → [NodeAddButton](https://github.com/MaLunan/AIGCCanvasFlow/blob/c88c00c58c6fcf613d58c6674c46b4a11c1d9911/h5/src/components/nodes/NodeAddButton.vue) 测原图位置、在右侧找空位、增加新卡和来源边，不替换原图。另一入口在 [FlowCanvas 的 createFromEdgeDrop](https://github.com/MaLunan/AIGCCanvasFlow/blob/c88c00c58c6fcf613d58c6674c46b4a11c1d9911/h5/src/components/FlowCanvas.vue)。**我们采用两种入口，不照搬它的菜单和固定高度**；默认动作就是「建视频草稿」，新分支按真实封面尺寸避让。卡片视觉延续已确认样机。

为什么不整套 fork：[前端清单](https://github.com/MaLunan/AIGCCanvasFlow/blob/c88c00c58c6fcf613d58c6674c46b4a11c1d9911/h5/package.json)仍是 `@vue-flow/core` 1.x；[视频节点](https://github.com/MaLunan/AIGCCanvasFlow/blob/c88c00c58c6fcf613d58c6674c46b4a11c1d9911/h5/src/components/nodes/VideoNode.vue)直接提交并轮询；[项目存档](https://github.com/MaLunan/AIGCCanvasFlow/blob/c88c00c58c6fcf613d58c6674c46b4a11c1d9911/h5/src/stores/projectStore.js)只送节点和边，加载后重新全览，不是恢复用户上次所看的镜头。固定树及前端脚本中未见覆盖该交互的自动化测试；用它学动作，不把它当回归保障，也不复制其媒体存档与生成服务。

### 3. 分支避让、成片比较：BeatDesign

固定 v0.2.5 / `796d89f0f451b436877836f91392152f43c3e270`。选中素材 → [以来源卡 ID 创建视频草稿](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/src/components/beatcanvas/use-beatcanvas-draft-actions.ts) → [按来源位置与占用矩形排新卡](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/src/core/beatcanvas/upload-layout.ts)。后者是不依赖 React 的 TypeScript 函数，适合连同测试复用；它按有限次数找位，不保证挤满的画布一定无重叠，保留拥挤退路和实测尺寸用例。

它把「同一原图的不同草稿」和「同一草稿的多次成片」区分开来：[版本选择](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/src/core/beatcanvas/generation-history.ts)先按草稿 ID 找结果，保留用户明确选中的可用版本，否则优先最近的成功版本。[浏览器用例](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/tests/e2e/workspace.spec.ts)覆盖手动选回旧版本、刷新不跳回新版。**我们只借这个规则**：结果以原工作台回执和已授权作品 ID 绑定；无成片就显示草稿/进行中/失败，没有授权或已过期就显示占位，不拿 URL 当作品身份，也不把同一原图下另一条草稿的结果顶过来。

**本轮实际验证：** 在本机 Node 26.10.0 对固定版本的 [画布放置](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/src/core/beatcanvas/canvas-placement.test.ts)、[重叠避让](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/src/core/beatcanvas/upload-layout.test.ts)、[草稿版本](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/src/core/beatcanvas/generation-history.test.ts) 三个上游测试文件，原样用内存模块加载、仅剥离 TS 类型执行，**12 项通过，0 失败，0 跳过**。没有启动 BeatDesign，没有运行它的浏览器套件，也没有向任何生成渠道发请求。

## 为什么不选另外两条路线

- [火宝无限画布](https://github.com/chatfire-AI/huobao-canvas/blob/9b7f7389693dcc3ab02afe67890ff76b90d89761/README.md) 产品形态和 Vue 技术栈都很接近，但[该版本许可是 CC BY-NC-SA 4.0](https://github.com/chatfire-AI/huobao-canvas/blob/9b7f7389693dcc3ab02afe67890ff76b90d89761/LICENSE)，作者明确商业服务须另获书面许可。我们的分组付费工作台不纳入它的代码、样式、素材或示例；也不引入其浏览器自带 Key 和独立运行队列。
- [Excalidraw](https://github.com/excalidraw/excalidraw) 的优势是自由手绘白板，不是把私有图片与视频草稿按类型连接；[tldraw SDK](https://tldraw.dev/community/license) 则对商业生产另有许可。首版要做镜头分支而不是多人手绘，两者都不是最短路径。

**许可交付：** 若正式采用，附带 [xyflow MIT](https://github.com/xyflow/xyflow/blob/29ab9e001c2ba320c9d84b00c729c745132d9145/LICENSE)、[VueUse MIT](https://github.com/vueuse/vueuse/blob/dce876a23ccec3335c2fbf7600244ea8756889a5/LICENSE) 的完整声明；若复制 AIGCCanvasFlow 的实质代码，保留 [MaLunan MIT](https://github.com/MaLunan/AIGCCanvasFlow/blob/c88c00c58c6fcf613d58c6674c46b4a11c1d9911/LICENSE)。若移植 BeatDesign 的函数或测试，随分发提供 [Apache-2.0](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/LICENSE)，保留适用版权及归属声明、标明修改，并检查[第三方声明](https://github.com/BeatAPI/BeatDesign/tree/796d89f0f451b436877836f91392152f43c3e270/third_party)与[商标限制](https://github.com/BeatAPI/BeatDesign/blob/796d89f0f451b436877836f91392152f43c3e270/docs/TRADEMARKS.md)。引用这些工程不代表获得其品牌、演示媒体、上游模型或 Pro 示例的授权。

## 原选型的验收条件（已在本地完成）

下面是选型时定义的试接路径；[实测结果与未发布边界](CREATIVE_CANVAS_INTEGRATION.md#已验证的结果2026-11-21未发布)记录在接入说明中。

1. **前端由 Gemini 4 试接**：在隔离路由里，把现有样机换成 Vue Flow 2，用本地素材模拟「一图 → 两条视频草稿」。在桌面拖线、手机点「＋」、两指缩放；撤销/重做、刷新后保留两条独立分支与视口（含 x=0、y=0）。验证新卡不盖原图，不因进入视口加载整条视频，能以真实封面并排比对。比较加入前后主包与懒加载路由的体积，检查打包是否引入重复 Vue、是否影响原工作台。
2. **后端负责账号存档、素材权限和结果关联，不另做生成服务**：[已有的图→视频交接](../../frontend/src/views/user/ImageStudioView.vue)和[原图读取](../../frontend/src/features/video-studio/source.ts)保留。画布存本人作品引用、布局、草稿、版本号；读取重查归属与时效，跨账号拒绝、过期留位置。分支 A/B 的模拟回执故意乱序返回，按原 `request_id` 与任务号各自回挂；刷新不重发，也不跨分支猜「最近作品」。双标签保存冲突不能静默覆盖。
3. **放行条件**：零个人 Key；打开、连线、保存、恢复、删除节点都不生成、不扣费、不轮询上游。真正付费只能在原工作台按渠道分组报价后手动确认；删除节点不影响原作品。Muse 不出现请求时长项，成片只显示实测时长。技术试接用 mock，不访问真实 H3。任一边界没过就不开正式入口。
