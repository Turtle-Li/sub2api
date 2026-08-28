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

## 上游更新时检索

重点检索以下关键词，确认上游是否已实现或修改相关逻辑：

```text
affiliate_code_registration_enabled
ValidateRegistrationInvitation
registrationInvitation
validate-invitation-code
invitation_code
aff_code
BindInviterByCode
```
