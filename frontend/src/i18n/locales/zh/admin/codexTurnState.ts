export default { codexTurnState: {
  "title": "降智修复",
  "description": "监控与修复 OpenAI/Codex 账号降智，自动维持全血集群路由 Cookie 与 780 字节防降智票据。",
  "accounts": "账号管理",
  "unavailable": "面板操作失败，请检查降智修复守护服务是否启动、配置是否有效。",
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
