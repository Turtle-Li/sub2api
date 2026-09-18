export default { codexTurnState: {
  "title": "Codex 续期监控",
  "description": "沿用现有探针工具，管理账号、模型、代理来源与定时续期。",
  "accounts": "账号管理",
  "unavailable": "面板操作失败，请检查本项目探针服务是否启动、配置是否有效。",
  "accountPicker": {
    "title": "选择 OpenAI OAuth 兼容账号",
    "label": "账号",
    "placeholder": "搜索已启用的 OpenAI OAuth 兼容账号",
    "hint": "OAuth 与 setup-token 账号均可选择；这里只显示账号名称，凭据继续保留在现有账号存储中。",
    "empty": "没有可选的已启用 OpenAI OAuth 兼容账号。",
    "loadError": "无法加载可选账号，请重新搜索或关闭后重试。",
    "incompatible": "名称不受支持",
    "incompatibleHint": "名称含控制字符或超过 128 个字符的账号无法使用，请重命名后再选择。",
    "confirm": "使用此账号"
  }
} }
