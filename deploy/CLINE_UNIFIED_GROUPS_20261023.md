# ClinePass 统一账号与国产模型分组

2026-10-23 应用配置检查点；本次未部署或重启。项目连接与生产操作边界继续以 `deploy/README.md` 为准。

## 最终账号与分组

原来共享同一密钥的 221 / 238 / 239 合并为 221「Cline Pass 国产多模型」。238、239 停用解绑，保留历史。不同密钥的 218、原生 Kimi 17、WorkBuddy 232 不变。

| 分组 ID | 名称 / 平台 | 倍率 | 221 提供的模型 |
|---:|---|---:|---|
| 17 | DeepSeek / deepseek | 0.5 | deepseek-v4-pro、deepseek-v4.1-flash、deepseek-v4.1-flash-1017 |
| 18 | KIMI / kimi | 0.3 | kimi-k3 |
| 35 | GLM / zhipu | 0.3 | glm-5.3、glm-5.3-flash |
| 36 | MiniMax / minimax | 0.3 | minimax-m3、minimax-m3.1-flash |

四组均启用公开模型名白名单。账号 221 将上述名字映射到同名 `cline-pass/` SKU，另有 `deepseek-flash → cline-pass/deepseek-v4.1-flash`、`k3/k3-256k → cline-pass/kimi-k3`。共 11 个公开映射、8 个不同 Pass 模型；账号并发 10，由四组共享。

分组 17 还保留既有 WorkBuddy 别名 `deepseek-v4.1-flash-sg`；分组 18 还保留原生账号 17 供应的 `kimi-for-coding`、`kimi-for-coding-highspeed`。不要把单个账号的映射当作整组供应能力。

用户明确只采用 ClinePass，撤下已经从 Pass 下线的 K2.7 Code，不接普通 Cline 积分模型；最终不配置 Qwen、MiMo。也不把其他 Kimi 产品型号冒充 K2.7 Code。

## 渠道和价格

现有渠道 2/3/4 分别绑定 17/18/35；新增渠道 12 绑定 36。GLM 仅列 5.3 和 5.3 Flash，Kimi 仅公开 K3。四组在公开模型广场共 9 张卡（含 DeepSeek 一个兼容名）。

价格为官方国内人民币参考价，沿用请求模型计费：MiniMax M3 标准输入/输出/缓存读取为 ¥2.1/8.4/0.42 每百万 token，输入超过 512000 后整档 2 倍；M3.1 Flash 为 ¥1/4/0.1。新 MiniMax 组启用长上下文阶梯、倍率 0.3，与既有 GLM/Kimi 组一致。其他三组倍率不变。

DeepSeek 按用户要求采用全天固定价：渠道 2 已清空 `time_pricing`，保留输入/输出/缓存读取 ¥1/4/0.02 每百万 token，分组 17 倍率仍为 0.5。分组 31「DeepSeek反代」原本无自定义价，会落到包含隐式峰时 2 倍的默认目录；现已明确配置其原默认基础价 USD 0.15/0.60/0.003 每百万 token，倍率仍为 0.1，从而避开默认目录峰谷倍率。两组 `peak_rate_enabled` 均为 false。

DeepSeek 官方当前 Pro 兼容名仍按路由公告使用 Flash 参考价；Cline Pro 的实际供应映射独立保留。

模型广场展示调整：国产平台注册表 `cn_provider` 为真的 DeepSeek、Kimi、GLM、MiniMax，其官方参考价显示人民币 `¥`；实际价继续使用 `$` 内部参考标记，数值算法和充值 1:1 口径不变。后端官方价格保持 USD/token 契约，前端使用与 CNY 价卡归一化同源的公开汇率还原人民币，独立于分组实付展示汇率；缓存、阶梯和混合分组表头同步显示。该前端改动已本地验证，用户随后授权提交发布，并要求先合并上游 v0.2.18 一起上线；最终版本和验收另记发布记录。

## 维护与验证

以后更换此份密钥或调整上游只维护账号 221；新增另一份密钥只建一个 Cline 账号，绑定相同四组。新增模型要同时加入账号映射、对应分组白名单和渠道价卡，均通过 Admin API 操作，以触发缓存和调度更新。

8 款模型真实账号测试全部成功；DeepSeek/Kimi 既有密钥的模型列表和正向网关调用成功，6 次跨组三协议请求均返回 404。MiniMax 绑定、候选项、渠道和公开广场阶梯回读通过；相关现有单元测试及独立只读复核通过。GLM/MiniMax 没有既有 owner 网关密钥，未为测试创建新密钥。

后续固定价修正经线上配置与公开广场回读确认；现有 DeepSeek 自定义分组价不受峰谷缩放、无时段配置返回倍率 1 的单元测试通过。前端新增回归测试先出现预期失败，再修复通过：相关 59 项测试、3 项 i18n 完整性测试、类型检查、构建和目标文件 ESLint 全部通过。原生 Chrome 已打开本地前端并使用线上公开 API，核对四家人民币官方价及 DeepSeek 四条固定价行。

对比未修改凭据的旧重复账号，221 除模型映射外的凭据完全一致（仅记录比较布尔值）；秘密未写入证据。完整非秘密快照与恢复边界保留在本次工作区 `review-artifacts/cline-unified-models-20261023/`。

官方依据：[Cline 实时 Pass 目录](https://api.cline.bot/api/v1/ai/cline/recommended-models)、[固定 revision 的 Pass 下线说明](https://github.com/cline/cline/blob/1619168bdeb570aa233d0f0a9af1d332d83a2816/docs/getting-started/clinepass.mdx#L204-L237)、[MiniMax 国内价格](https://platform.minimax.cn/docs/pricing/overview)、[Kimi 国内价格](https://platform.kimi.com/docs/pricing/chat)、[智谱价格](https://docs.bigmodel.cn/cn/guide/start/pricing)、[DeepSeek 价格](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)及[路由公告](https://api-docs.deepseek.com/zh-cn/news/news260910/)。
