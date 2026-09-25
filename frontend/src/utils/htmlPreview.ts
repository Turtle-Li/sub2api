/**
 * 从模型回答中提取可预览的 HTML 文档：优先取 ```html 代码块，其次取裸露的
 * <!doctype html> / <html> 文档。没有可预览内容时返回 null。
 */
export function extractHtmlDocument(content: string | null | undefined): string | null {
  if (!content) return null
  const fenced = content.match(/```html[^\n]*\n([\s\S]*?)(?:```|$)/i)
  if (fenced && /<[a-z!][\s\S]*>/i.test(fenced[1])) return fenced[1].trim()
  const start = content.search(/<!doctype html|<html[\s>]/i)
  if (start < 0) return null
  const tail = content.slice(start)
  const end = tail.search(/<\/html>/i)
  return (end >= 0 ? tail.slice(0, end + '</html>'.length) : tail).trim()
}

function escapeAttribute(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/**
 * 包一层沙箱 iframe（允许脚本但不同源），避免模型生成的页面读取后台登录态。
 */
export function sandboxedHtmlPage(html: string, title = 'HTML preview'): string {
  return `<!doctype html><html><head><meta charset="utf-8"><title>${escapeAttribute(title)}</title>`
    + '<style>html,body{margin:0;height:100%}iframe{border:0;width:100%;height:100%}</style></head>'
    + `<body><iframe sandbox="allow-scripts allow-forms allow-modals" srcdoc="${escapeAttribute(html)}"></iframe></body></html>`
}

/** 在新窗口打开沙箱预览；被浏览器拦截弹窗时返回 false。 */
export function openHtmlPreviewWindow(html: string, title?: string): boolean {
  const url = URL.createObjectURL(new Blob([sandboxedHtmlPage(html, title)], { type: 'text/html' }))
  // 不传 noopener：它会让 window.open 恒返回 null，无法判断是否被拦截。
  const opened = window.open(url, '_blank')
  if (opened) opened.opener = null
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
  return opened !== null
}
