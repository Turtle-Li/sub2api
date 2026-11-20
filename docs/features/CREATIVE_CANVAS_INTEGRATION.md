# 创作画布接入与本机验收

原型与产品边界见[原提案](CREATIVE_CANVAS_20261121.md)；选用的固定源码、测试和授权见[开源选型](CREATIVE_CANVAS_OPEN_SOURCE_20261121.md)。该文档记录实际代码的接口和开启方式，不是生产发布授权。

## 本地试用

- 标准工作区、已登录账号；创作画布有独立开关 `gateway.creative_canvas_enabled`（环境变量 `GATEWAY_CREATIVE_CANVAS_ENABLED`），**默认关闭**。只在本地或明确授权的环境打开，正式用户入口为 `/creative-canvas`。既有视频生成能力另由 `gateway.video_studio_enabled` 控制；开启画布不会开启生成。钱包沿用已有的渠道分组配置，不需用户创建 Key。
- 先从画廊把本人原图加入画布，从原图建两个视频草稿，各写不同运镜。保存后重新打开；应看见同一张原图、两条边、各自的文字和原视口。点击「去视频工作台」不是提交；在那里确认模型、价格并手动生成才会产生任务。不能把即时内存预览当作账号存档。
- 视频还没成片、图片过期、原分组授权被撤销时，都不能用另一个作品顶替。删除画布或节点不是删除、取消生成或退款。作品封面来自私有工作台媒体端点；没有实测视频时长就不显示秒数。

## 接口与所有权

所有端点使用既有登录 JWT、账号状态检查与面板限流；关闭开关时不提供画布数据。成功使用项目通用 JSON 响应。错误 404 不区分「不存在」和「不属于此用户」。写入仅接受受限 DTO，响应 `private, no-store`。通用审计不记录画布正文。

| 操作 | 端点及语义 |
| --- | --- |
| 列出 / 新建 | `GET /api/v1/creative-canvases` 只列本人的；`POST` 只接受 `title`，由服务端分配 UUID 和初始 revision。 |
| 打开 / 保存 | `GET /:id` 返回 summary、document、当前可用作品及尝试；`PUT /:id` 只接受 `{revision,title,document}`。revision 对不上返回 409，不能默默覆盖另一标签页的编辑。 |
| 删除 | `DELETE /:id?revision=N`。只删除该用户画布和关联；工作台任务及流水不受影响。 |
| 绑定一次显式生成 | `POST /:id/nodes/:node/attempts` 输入 `{request_id,revision}`；必须在确认后的工作台生成 POST **之前**成功绑定。绑定本身不创建工作台任务、不调用渠道、不冻结或扣款。同一任务号不能换草稿/画布，已存在的工作台任务不能被倒绑。旧 revision 返回冲突，不允许在另一个标签页已更换来源的草稿上提交。 |

关系表使用原来的 `(user_id, request_id)` 查工作台本地回执，不轮询上游，同时完成、乱序到达和请求结果不明确都不自动再发 POST。同一有效 revision、同一分支和回执的重复绑定幂等；此前绑过的请求号也不能绕过旧 revision 的冲突检查。业务保存保留原图的 `(route,batchId,itemId,index)` 或视频工作台 UUID，不保存媒体、URL、Key 值、分组凭据或支付信息。钱包的 `route=0` 是原工作台的所有权命名空间；旧作品不切换到新 Key。入板和解析均重查归属、过期及原授权；归档文件不能冒充可直接读取的原图。缩略图、原图与成片仍走各自现有受权端点。

`288_creative_canvases.sql` 只增加画布/尝试关系表，按用户过滤；画布限 32 个、每板 160 节点 / 320 边，单板 256 次绑定、单草稿 48 次。节点种类和边方向受限；允许一张图派生多个草稿，但一个视频草稿只有一张来源图。版号和行锁保护并发写入，已绑定的来源不能移花接木。回退只需关闭开关，不删除作品、画布、历史或流水。

## 验证命令（本地，不发真实生成）

```sh
# 从 frontend/ 运行。直接用所发模块和宿主类型适配跑 BeatDesign 固定版本的 12 项原始测试。
node verification/beatdesign/verify.mjs

# 从 backend/ 运行；integration harness 启动临时本地 PostgreSQL/Redis，测试内仅用离线 Muse 传输。
go test -tags=integration ./internal/repository -run '^Test(CreativeCanvas|VideoStudio)' -count=1 -v
go test -tags=unit ./internal/service ./internal/handler ./internal/server/routes ./internal/server/middleware -run 'Canvas|VideoStudio|ImageStudio' -count=1
```

关键用例：`TestCreativeCanvasJWTPostgresJourney` 验证零 Key、跨用户拒绝、原图路由/序号、保留失效位置、冲突、零扣费；`TestCreativeCanvasSimultaneousReceiptsStayOnTheirBranches` 验证两个分支三次回执倒序、手动固定旧版本和删除不影响原任务；`TestVideoStudioZeroKeyJWTPostgresJourney` 在既有工作台手动提交的整个模拟链路上验证画布显示回执、撤权和只扣一次。测试 H3 参数/本地 fixture 不代表向 H3 渠道产生调用。

## 已验证的结果（2026-11-21，未发布）

| 场景 | 本机实测结果 |
| --- | --- |
| 从两个工作台加入 | 在生图详情的弹窗内点「加入画布」→「新建并加入画布」，打开后核对钱包路由 `0`、原作条目及第 `0` 张图均原样保留。再从视频画廊「加入画布」选同一块板，保存的正是点击的成片任务 ID；两次导入都没有发起生成或绑定。 |
| 画布编辑与手势 | 桌面和手机视口均显示原图、两张私有视频封面和两条来源连线，浅色/深色均检查。桌面实拖到空白处建分支、移动节点、滚轮缩放，手机模拟双指捏合；保存刷新保留视口和分支。撤销/重做同时恢复节点和边。编辑过程没有生成请求，也没有下载完整视频。 |
| 工作台往返 | 从分支进工作台、刷新，原图和提示词仍然一致。手动确认后**先绑定当前版本，再发一次模拟 Muse 请求**，不传请求时长；成片只回原草稿。可并排比较两条分支，固定旧成片后刷新不跳回新版。 |
| 拒绝路径 | 刷新时原图撤权：零绑定、零付费请求；提交前画布版本变化：一次绑定返回 `409`、零付费请求。后端独立覆盖跨账号访问、过期、原图归档、并发保存、乱序回执与幂等结算。 |

前端按仓库 CI 的 Node 24 运行：Vitest **4,461/4,461**，类型检查、前端生产构建及嵌入该前端的 Go 服务构建均通过；BeatDesign 的两份算法及三份测试与固定源码逐字相同，原版 **12/12** 通过。Go 相关单元用例 **108/108**，隔离 PostgreSQL/Redis 上的仓储与会话集成用例 **8/8**，均无跳过。浏览器验收使用实际构建产物和本机模拟作品、回执，不是线上试单；未调用真实 Muse 或 H3，也未开启生产开关。
