# 多平台生图工作台交付记录

用户选择：A，画廊为主，连续对话式改图为辅。开发基线 `ce5f17142704f0fbd269b76b7b9d2a266a4a6f20`。本轮交付本地代码与验证证据，不包含提交、推送、部署或真实付费生图。

## 用户流程

登录 Sub2 后进入「生图工作台」，选择自己的生图分组和模型，输入一条或以独立 `---` 行分隔的多条提示词。服务端创建原有批量任务，画廊显示每张图的状态、错误和结果。可查看详情、放大、下载、取消未完成批次，或把结果作为参考图继续修改。新改图成功后自动成为下一轮参考图。

画廊、连续改图和原「批量生图」共用服务端任务、对象存储、身份权限和同一套预留/结算账本。刷新页面从服务端恢复；切换密钥时清理旧画面与未完成读取。前端只发送选中的密钥 ID，不读取或发送用户 API Key 内容。

## 复用来源与平台

| 平台/界面 | 采用方式 | 启用条件与实际限制 |
|---|---|---|
| 画廊与编辑界面 | [88lin/gpt-image-studio](https://github.com/88lin/gpt-image-studio/tree/5523a592af054bfe4cbee76c2ef8d866ce5cd81e) 的真实 React 卡片、虚拟列表、输入栏、Lightbox、视口/键盘交互；保留 MIT | Vue 保留站点外壳，React 组件挂载于新 `/image-studio`；任务和鉴权替换为 Sub2 适配层 |
| Gemini / Image | 原 Sub2 batch provider、执行器与结算 | 现有账号、分组、模型与价格配置继续生效 |
| Muse | 原 Muse `/v1/images/tasks` 异步合同，持久化 task_id 后仅查询恢复 | 精确 `muse-image` 映射及正价格；最多一张参考图；不重写浏览器生成或账号池 |
| 万相 | 一键生图 VSO `4edef85…` 的原 Wan HTTP/解析/尺寸策略，通过 Python 模块调用 | 可选 Python runtime 配置默认关闭；Wan2.7 两模型，1K/2K、9种比例、最多9张参考图 |
| Seedream | 同一 VSO 固定版本原 Seedream HTTP/解析/尺寸策略 | Lite：2K/4K、最多14张参考；Pro：1K/2K、最多10张；使用原策略给出的精确像素映射 |
| WorkBuddy 混元 | [WorkBuddy 网关](https://github.com/misakano7545/workbuddy2api-panel/tree/a19c78fc143dce790041071c18e1c7705a5a4f1e) 的图片 HTTP 接口，复用其账号池 | 精确区分 `cn:hunyuan-image-alpha`、`cn:hunyuan-image-alpha-edit`、`global:hy-image-3.5`；编辑模型恰好一张参考图；显式验证标记缺省关闭 |

混元网关原来的失败重试可能重复提交付费请求。本轮提供单次提交修复补丁和 MIT 声明，已用本地 HTTP 场景验证。正式启用须先部署经过验证的网关版本，并固定或协调自动更新策略，再设置账号的单次提交验证标记。Sub2 不把这个标记伪装成远程版本检测。

万相/Seedream 可选运行时安装见 `image-provider-bridge/README.md`。账号仍通过现有 OpenAI API-key 账号与明确模型映射接入，复用 Sub2 账号代理和并发；不支持的代理类型明确拒绝。未引入第二个调度器、钱包或计费服务。

## 源码与验收入口

- `frontend/src/features/image-studio/UPSTREAM.md`：实际采用模块、来源哈希、MIT 与有意差异。
- `image-provider-bridge/SOURCE_MANIFEST.json`：原 Python 核心逐文件/片段来源、固定 revision、差异和所有权说明。
- `docs/features/IMAGE_STUDIO_SESSION_API_20261107.md`：JWT 门面、所有权与接口合同。
- `docs/features/MUSE_BATCH_PROVIDER_20261107.md`、`WORKBUDDY_IMAGE_PROVIDER_20261107.md`：提供方合同、限制与启用条件。
- `.omo/evidence/image-studio/root-acceptance.md`：根任务手工验收、上下文审查与组合检查。
- `.omo/evidence/`：独立后端、Python 核心、提供方和界面来源审查报告及运行日志。
- `/Users/lijiayu/auto_work/sub2 api/artifacts/image-studio-qa-20261107/QA.md`：真实浏览器操作记录和桌面/手机截图；图片为明确标注的参考截图裁片。

## 验证边界

本地界面验证覆盖登录跳转、提交/轮询、失败、取消、详情/下载、连续改图、切换密钥、刷新恢复、模型参数与手机布局。后端测试覆盖归属/分组/余额准入、未知提交不重发、进程恢复、共享并发/代理、受限图片下载、预留前拒绝和单次结算。

最终组合检查通过：后端278项（race、零跳过）、Python56项；前端最终44项功能测试、类型检查及生产构建通过。独立后端/提供方审查及界面来源复核通过。

生产 DB/Redis、真实提供方生图质量、线上价格配置和 Docker 扩展构建未验收。当前未部署，WorkBuddy 网关与自动更新策略也未修改。详细组合检查见 root-acceptance.md。

集成工作树已重新附加为当前任务工作目录。两个辅助工作树的代码与证据已复制；应用归档接口因「仍被另一活动任务引用」拒绝归档，因此保留原样，未强制删除。
