import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const sidebarSource = readFileSync(resolve(dir, '../AppSidebar.vue'), 'utf8')
// 公开站点（首页及其子页）的 site_logo 现在统一在 useSite 里取，
// HomeView 不再自己读这个字段。
const siteSource = readFileSync(resolve(dir, '../../site/useSite.ts'), 'utf8')
const keyUsageViewSource = readFileSync(resolve(dir, '../../../views/KeyUsageView.vue'), 'utf8')

describe('site_logo sanitization', () => {
  it('AppSidebar imports sanitizeUrl and applies it to siteLogo', () => {
    expect(sidebarSource).toContain("import { sanitizeUrl } from '@/utils/url'")
    expect(sidebarSource).toContain('sanitizeUrl(appStore.siteLogo')
  })

  it('useSite applies sanitizeUrl to siteLogo', () => {
    expect(siteSource).toContain('sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo')
  })

  it('KeyUsageView applies sanitizeUrl to siteLogo', () => {
    expect(keyUsageViewSource).toContain('sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo')
  })

  it('all three pass allowRelative and allowDataUrl options', () => {
    for (const src of [sidebarSource, siteSource, keyUsageViewSource]) {
      expect(src).toContain('allowRelative: true')
      expect(src).toContain('allowDataUrl: true')
    }
  })
})
