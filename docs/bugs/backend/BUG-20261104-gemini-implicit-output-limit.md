# Gemini 兼容入口隐式注入8192输出上限

Bug: BUG-20261104-gemini-implicit-output-limit

现象: 小说消费者经SillyTavern Custom调用gemini-4-argon，两次均返回finish_reason=length、终止事件到达、正文为空。第二次精简任务仍失败。调用者保持HIGH，未发送max_tokens或max_completion_tokens。不能把压缩提示或降低思考作为修复。

复现条件: Gemini平台Chat Completions入口，请求省略输出上限，经Responses→Anthropic→Gemini转换。Antigravity兼容Chat/Responses映射到Gemini时存在同一机制。真实Anthropic接口要求max_tokens，行为须保留。

调用链: 小说scripts/sillytavern_novel_pipeline.mjs → SillyTavern 1.19.0/06bde93 Custom生成 → Sub2API ForwardAsChatCompletions → chatCompletionsToResponses（保持nil）→ apicompat.ResponsesToAnthropicRequest（未指定时补8192）→ convertClaudeGenerationConfig（写入maxOutputTokens=8192）→ Gemini。

影响范围: Gemini兼容入口的未设上限请求，尤其HIGH思考加长正文。不是模型能力结论；思考与正文共用输出预算会导致正文前耗尽。原生v1beta路径不经此默认注入。不得全局删除Anthropic默认值，也不修改组、账号、计费、模型或思考档位。

疑似根因: ANALYSIS_READY。在本地fork/main 9bb4085339b6e3dedb7550b66bb05ff0af9e0678可直接追踪8192的生成与传递，固定上游Wei-Shaw/sub2api e60cefeaf78f2fbfa95aa076029e28b40d0a5527也有相同链路。原始线上失败确定，但尚未读取线上最终Gemini请求体，不能把本地源码身份当作生产部署证明。需真实转发stub回归及同原任务线上验证补齐。

推荐修改方案: 在Gemini专属转换边界保留“未指定上限”。Antigravity仅在最终解析为Gemini时消除兼容中间态补入的上限。保留显式上限的优先级与现有clamp、真实Claude默认值。小说消费者增加流式usage请求和思考delta计数（不记录思考内容），用于诊断。原始完整任务+HIGH验证恢复后再继续审稿润色。

风险: 中间Anthropic结构被多个提供者共用，禁止全局改默认。Gemini省略上限仍受模型服务自身限制，不能宣称无限输出。必须测试stream/nonstream、两种显式上限及优先级、Gemini映射别名、Claude回归。无数据库迁移，无账号或额度改动。

证据: 消费方保留原始完整任务及精简任务的两次失败记录，均无正文且length结束。消费者原SHA256 77b07636f64870c089a62fdb1e7161f6d1deeb25af26ef1719870c26041fbf5d。SillyTavern固定源码src/endpoints/backends/chat-completions.js确认Custom排除上限后不重建默认。

上游契约: 对照e60cefeaf78f2fbfa95aa076029e28b40d0a5527的backend/internal/pkg/apicompat/{chatcompletions_to_responses.go,responses_to_anthropic_request.go}、backend/internal/service/gemini_chat_completions_compat_service.go及相关测试；采用其协议与思考转换，刻意修正Gemini复用Anthropic中间结构时的人工上限。保留项目LGPL许可和版权声明。

状态: IMPLEMENTATION_READY。本地修复、回归及独立QA/代码审查完成；生产尚未发布，原完整任务＋HIGH线上验证未完成。

## 本地修复与验证

本地验证完成时尚未提交或部署；随后作者已明确授权提交main并按既有蓝绿流程发布。生产恢复须以后续实际发布和完整任务验证为准。

- Gemini原生账号的Chat转换：中间Responses未设上限时清除Anthropic临时默认值，Gemini序列化因此省略maxOutputTokens；HIGH照常转换。
- Antigravity兼容Chat/Responses：保留输入是否省略的状态，仅在最终映射为Gemini时删除临时上限。真实Claude继续保留默认值及原有思考预算调整；显式上限、优先级与clamp不变。
- 小说消费者：draft使用custom_include_body发送stream_options.include_usage；统计思考delta字符数但不留内容；成功与失败均保存传输观察。不得把字符数当Token数。

红灯：真实转发stub复现6个Gemini场景被注入8192。首次测试的2个Claude夹具未计入既有HIGH调整（实际11240），在业务修改前已核实机制，修正夹具预期；没有改变Claude业务规则。

绿灯：新增14场景及既有显式限额回归通过；相关Gemini/AntigravityCompat/ResponsesToAnthropic三个包回归通过（service 26.464s、apicompat 0.524s、antigravity 1.063s）；go build ./...通过；git diff --check通过。

独立QA与代码审查：advisory Level 1 QA_PASS；针对性race+shuffle通过（service 4.721s），同一冻结Go源码未发现剩余可行动缺陷。不是正式发布Gate或线上恢复证明。

另一个观测缺陷已修复：SillyTavern 06bde93不转发顶层stream_options。独立复核发现后移到custom_include_body；真实酒馆+无凭据临时回环mock验证HTTP200、最终HIGH、include_usage=true、无max_tokens/max_completion_tokens，usage及DONE均转发。mock已关闭，不增加常驻服务。

线上只读核对：sub2api-aws-candidate的健康sub2api-green镜像revision为9bb4085339b6e3dedb7550b66bb05ff0af9e0678，与分析基线一致。未修改生产配置、账号或数据库。日志定向查询没有找到相应模型事件，不将此当作无调用证明。

冻结源码SHA256：
- gemini_chat_completions_compat_service.go：17f78c905aaeacef558ddc0be0ce66eb196b28c84deefed120f6ba8f7fc774b2
- antigravity_gateway_compat.go：838d2ea367c48bd90a2681ea38047253e80830bfbee0b68d7e8b6ac4c1017222
- gemini_compat_output_limit_test.go：04829b9457f1d79b74c6fbfa656d8c7a37c4086520faf5bbcd6686a02203bc70
- 小说消费者：3fc1cd9a4d0722a5eaa41abbbafd50395329572167094a5b0ef4d67e69d1ad96

## 生效与恢复写作

根因位于共享Sub2API服务，不能只更新消费者就宣称恢复。作者已授权提交并发布：安全整合最新fork/main，只提交本次改动和故障记录，运行所需检查，再按deploy/README.md的手动GitHub构建与AWS蓝绿流程发布。保留既有镜像和自动回退；无迁移、不改分组或额度。Sub2文档被现有docs/*忽略，提交时必须显式纳入，不能只提交代码。

生产生效后，使用消费方保存的原完整输入、同gemini-4-argon、HIGH、temperature=1且不传输出上限，要求正常STOP、非空完整正文、usage/思考统计与模型标签、酒馆聊天逐字读回。未通过这一项前，故障状态保持“本地修复待线上验证”，不将技术修复等同内容交付。

## 发布前CI资源阻碍

首次CI 41708888531的frontend任务在Build embedded frontend失败：Vue编译阶段V8堆约2044MB时退出134，明确报JavaScript heap out of memory。前置typecheck与critical vitest通过，frontend/Dockerfile/workflow在Gemini修复提交前后无变动，属于现有CI资源不足。仅在该编译步骤设置NODE_OPTIONS=--max-old-space-size=4096；保留同一pnpm build、所有检查与生产配置。新CI实际结果另行记录，不能把资源配置修改本身当作通过。
