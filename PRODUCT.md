# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Product Purpose

TurtleRoute / Sub2 向现有用户提供多平台生图，复用已有充值、内部余额/套餐和计费。此次范围是生图工作台，不改变站点其他业务。

## Capabilities and Constraints

- 用户明确要求复用既有核心逻辑和成熟前端代码，避免重新实现。
- 保留 Gemini/OpenAI 批量能力；万相、Seedream 复用一键生图的原 Python 核心，Muse 复用原生任务接口。混元图片协议已在 WorkBuddy 固定源码中确认，启用前必须满足网关单次提交条件。
- 生成执行、持久化任务、授权与计费由 Sub2 服务端控制，浏览器记录不是唯一真源。
- 不修改他人工作树，不默认部署或付费调用。

## Brand Commitments

用户已选择 A：`88lin/gpt-image-studio` 画廊为主，聊天改图为辅。沿用其可复用组件和交互，接入 TurtleRoute 的登录和任务服务。

## Evidence on Hand

详见 `docs/features/IMAGE_STUDIO_INTEGRATION_20261107.md`；公开参考 revision 已固定，本地 Sub2 基线已核对。整合界面的桌面和手机交互使用本地接口夹具验收；提供方和计费使用契约测试及独立代码审查。真实提供方、生产数据库及部署仍需独立验收。

## Product Principles

- 一次生成只由一个结算责任方收费。
- 不把未知提交自动重放为新收费生成。
- 只有经能力校验的模型才呈现在可用选项中。
- 同一任务可从画廊、批量页或聊天查看，身份和归属边界一致。
