import { describe, expect, it, vi } from 'vitest'

const authStore = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  isAuthenticated: false,
  isAdmin: false,
  isSimpleMode: false,
}))

const appStore = vi.hoisted(() => ({
  siteName: 'Sub2API',
  backendModeEnabled: false,
  cachedPublicSettings: null as null | Record<string, unknown>,
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({
    customMenuItems: [],
  }),
}))

vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
}))

vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn(),
  }),
}))

/**
 * 公开站点的一级页面。这些页面是给未登录访客看的，一旦哪条被误标成
 * 需要登录，访客点导航就会被弹到登录页——所以这里逐条钉住。
 */
const SITE_ROUTES: Array<[string, string]> = [
  ['SiteModels', '/models'],
  ['SitePlatform', '/platform'],
  ['SiteDocs', '/docs'],
  ['SiteDocsSection', '/docs/:section'],
  ['SiteWhy', '/why'],
  ['SiteChangelog', '/changelog'],
]

describe('public site routes', () => {
  it.each(SITE_ROUTES)('registers %s as a public route at %s', async (name, path) => {
    const { default: router } = await import('@/router')
    const route = router.getRoutes().find((record) => record.name === name)

    expect(route?.path).toBe(path)
    expect(route?.meta.requiresAuth).toBe(false)
  })

  it('resolves each docs section to the docs view rather than 404', async () => {
    const { default: router } = await import('@/router')

    for (const section of ['protocols', 'clients', 'media', 'errors']) {
      const resolved = router.resolve(`/docs/${section}`)
      expect(resolved.name).toBe('SiteDocsSection')
      expect(resolved.params.section).toBe(section)
    }
  })

  it('keeps the docs index on its own route so /docs is linkable', async () => {
    const { default: router } = await import('@/router')

    expect(router.resolve('/docs').name).toBe('SiteDocs')
  })
})
