# Custom 分支功能记录

本文仅记录 `custom` 分支相对上游 `main` 新增的功能点，供后续同步上游代码时快速检查是否已有同类实现。

## 允许使用返利邀请码注册

- 新增设置 `affiliate_code_registration_enabled`，默认关闭。
- 开启邀请码注册时，允许返利邀请码代替一次性邀请码作为注册凭证。
- 手工输入支持一次性邀请码和返利邀请码，一次性邀请码优先。
- 没有手工输入时，支持使用邀请链接保存的 `aff_code` 完成注册。
- 手工输入的返利邀请码会正常绑定邀请关系，并覆盖 URL 中的返利码。
- 使用一次性邀请码注册时，URL 中的返利码仍可正常绑定邀请关系。
- 返利码作为注册凭证时，账号创建与邀请关系绑定保持原子性，绑定失败不创建账号。
- 已有账号登录或 OAuth 绑定不会重新绑定邀请人。
- 邮箱注册及 GitHub、Google、Email OAuth、LinuxDo、OIDC、微信、钉钉和 pending OAuth 新账号路径均支持该功能。
- 邀请码预校验接口支持区分手工输入和 URL 返利码，并返回邀请码类型。
- 管理后台新增“允许使用返利邀请码注册”开关及中英文文案。
- 公开设置、管理设置、前端类型和设置审计增加对应字段。
- 增加邀请码优先级、返利绑定、错误兼容、并发使用和事务回滚测试。

## 发布流程不回写默认分支

- 保留现有构建、发布和通知流程。
- 移除发布成功后的 `sync-version-file` Job。
- 发布标签或手工触发 Release 后，不再自动修改默认分支的 `backend/cmd/server/VERSION`。
- 避免 custom 分支发版时由 GitHub Actions 向上游同步分支或默认分支产生额外提交。

## 精简公开首页展示

- 移除首页“订阅转 API”功能标签，保留会话保持和按量计费标签。
- 移除首页“已支持的 AI 模型”展示区及 Claude、GPT、Gemini、Antigravity 等静态卡片。
- 同步删除中英文 `home.tags.subscriptionToApi` 和 `home.providers` 文案，避免遗留无引用翻译。
- 不影响后台平台配置、模型能力、API 路由及实际转发功能，仅调整公开首页展示。

## 站点域名访问守卫

- 新增设置 `site_domain`，用于配置面板和前端页面的规范域名，留空时不启用守卫。
- 管理后台提供“站点域名”配置项；只接受不带协议、路径、查询参数、Fragment 和用户信息的域名，可包含端口。
- 保存时去除首尾空白和末尾 `/`，并统一转为小写；非法值返回 `INVALID_SITE_DOMAIN`。
- 非规范域名访问前端页面或静态资源时，以 `307 Temporary Redirect` 跳转到规范域名，并保留原路径和查询参数。
- 非规范域名访问 `/api/v1` 面板 API 时返回 `403`，防止从非规范 Host 使用站内接口。
- AI 网关路由不受域名限制，包括 Claude、OpenAI、Gemini、Antigravity、图片、视频、语音和其他兼容入口。
- 路由判断使用 Gin 已匹配路由结果，避免依赖易遗漏的 API 路径白名单，也避免 SPA 中间件误接管后端路由。
- 设置更新后会刷新运行中的域名守卫，不需要重启服务。
- 管理设置 DTO、审计、API Contract、中英文文案及前后端类型均增加对应字段。
- 增加域名规范化、页面跳转、面板 API 拒绝、AI 路由放行和嵌入式前端路由测试。

## CC-Switch 导入增强

- CC-Switch 的 `ccswitch://v1/import` 协议使用单个 `endpoint` 参数承载逗号分隔的多个端点；首项为主端点，其余项导入为自定义端点。
- 导入时以 `api_base_url` 为主端点，并追加公开设置中的全部 `custom_endpoints`。
- 导入前过滤空端点并去重，保持主端点在第一位。
- 每个端点都会应用对应平台的路径规则，例如 Grok 补齐 `/v1`、Antigravity 补齐 `/antigravity`。
- `homepage` 不再固定复用 API 地址，按 `site_domain`、`api_base_url`、当前页面 `window.location.origin` 的顺序回退。
- `site_domain` 作为非敏感站点配置加入公共设置响应及嵌入式前端设置注入，前端按当前页面协议补成完整站点 URL。
- Provider 名称继续使用站点名称，API Key、用量查询脚本和平台默认模型等既有导入行为保持不变。
- 增加多端点去重、独立官网地址和 Grok 多端点规范化测试。

## OIDC SSO 直连免行为验证码

- 新增设置 `oidc_connect_skip_action_captcha`，对应配置项 `oidc_connect.skip_action_captcha`，默认关闭。
- 开启后可直接访问 `GET /api/v1/auth/oauth/oidc/start?redirect=/dashboard` 发起 OIDC SSO。
- 该开关仅跳过腾讯天御和阿里云行为验证码；仅启用 Cloudflare Turnstile 时，OAuth start 按既有逻辑本就直接放行。
- 登录页和注册页不增加 OIDC 专用绕过逻辑，免行为验证码入口由上述直达地址提供。
- 其他 OAuth Provider 的验证码行为不受影响。
- OIDC 配置校验、state、浏览器会话 Cookie、PKCE、nonce、ID Token 和 callback 校验保持不变。
- 配置文件、数据库设置、管理设置 DTO、审计、API Contract、中英文文案及前后端类型均增加对应字段。

## 上游更新时检索

### 返利邀请码注册

```text
affiliate_code_registration_enabled
ValidateRegistrationInvitation
registrationInvitation
validate-invitation-code
invitation_code
aff_code
BindInviterByCode
```

### 发布流程

```text
sync-version-file
backend/cmd/server/VERSION
github.event.repository.default_branch
```

### 首页展示

```text
home.tags.subscriptionToApi
home.providers
Supported Providers
subscriptionToApi
```

### 站点域名守卫

```text
site_domain
SettingKeySiteDomain
SiteDomainGuard
normalizeSiteDomain
RequireHost
RedirectPage
SetSiteDomainGuard
INVALID_SITE_DOMAIN
```

### CC-Switch 导入

```text
ccswitch://v1/import
buildCcSwitchImportDeeplink
additionalEndpoints
custom_endpoints
homepage
endpoint
site_domain
```

### OIDC SSO 直连

```text
oidc_connect.skip_action_captcha
oidc_connect_skip_action_captcha
SkipActionCaptcha
OIDCOAuthStart
requireActionCaptchaForOAuthLoginStart
VerifyActionCaptchaIfEnabled
/api/v1/auth/oauth/oidc/start
```
