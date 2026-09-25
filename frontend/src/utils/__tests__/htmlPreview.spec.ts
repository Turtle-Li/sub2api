import { describe, expect, it } from 'vitest'

import { extractHtmlDocument, sandboxedHtmlPage } from '../htmlPreview'

describe('htmlPreview', () => {
  it('extracts fenced or bare HTML documents', () => {
    expect(extractHtmlDocument('see\n```html\n<div>a</div>\n```\nend')).toBe('<div>a</div>')
    expect(extractHtmlDocument('x <!DOCTYPE html><html><body>b</body></html> tail')).toBe('<!DOCTYPE html><html><body>b</body></html>')
    expect(extractHtmlDocument('```html\n<html>cut')).toBe('<html>cut')
    expect(extractHtmlDocument('plain answer with <b>bold</b>')).toBeNull()
    expect(extractHtmlDocument('')).toBeNull()
  })

  it('wraps HTML in a sandboxed iframe with escaped srcdoc', () => {
    const page = sandboxedHtmlPage('<p class="x">&</p>', 'a"b')
    expect(page).toContain('sandbox="allow-scripts allow-forms allow-modals"')
    expect(page).toContain('srcdoc="&lt;p class=&quot;x&quot;&gt;&amp;&lt;/p&gt;"')
    expect(page).toContain('<title>a&quot;b</title>')
    expect(page).not.toContain('allow-same-origin')
  })
})
