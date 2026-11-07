# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Product Purpose

TurtleRoute / Sub2 向现有用户提供多平台生图，复用已有充值、内部余额/套餐和计费。此次范围是生图工作台，不改变站点其他业务。

## Capabilities and Constraints

- 用户明确要求复用既有核心逻辑和成熟前端代码，避免重新实现。
- 保留 Gemini/OpenAI 批量能力；万相、Seedream 复用一键生图的原 Python 核心，Muse 复用原生任务接口。混元沿用官方更新和已记录的单次验证或原生重试接受模式。
- 生成执行、持久化任务、授权与计费由 Sub2 服务端控制，浏览器记录不是唯一真源。
- 不修改他人工作树。当前任务已有提交、部署和受控真实调用授权，具体发布仍遵循项目运维门禁。

## Brand Commitments

用户已选择画廊为主、连续改图为辅，复用 `88lin/gpt-image-studio` 的任务、网格、查看器和手势交互。后续明确选择 Kimi 的左侧创作栏、右侧画廊，Gemini 4 提供对照，Claude 审查补充。使用宿主明暗主题和唯一页头，用户直接选择模型，不显示分组或密钥选择。每个模型使用管理员配置的价格，内部复用原分组计费。详见 `docs/features/IMAGE_STUDIO_REDESIGN_20261108.md`。

## Evidence on Hand

详见 `docs/features/IMAGE_STUDIO_INTEGRATION_20261107.md`；公开参考 revision 已固定，本地 Sub2 基线已核对。整合界面的桌面和手机交互使用本地接口夹具验收；提供方和计费使用契约测试及独立代码审查。真实提供方、生产数据库及部署仍需独立验收。

## Product Principles

- 一次生成只由一个结算责任方收费。
- 不把未知提交自动重放为新收费生成。
- 只有经能力校验的模型才呈现在可用选项中。
- 批量创作、历史与连续改图统一到工作台，旧批量页面重定向，后端批量接口保留；身份和归属边界一致。
