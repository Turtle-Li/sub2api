import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Router, RouterOptions } from 'vue-router'

type NavigationGuard = (
  to: Record<string, any>,
  from: Record<string, any>,
  next: ReturnType<typeof vi.fn>
) => Promise<void>

const routerHarness = vi.hoisted(() => ({
  guard: null as NavigationGuard | null,
  router: null as Router | null,
}))

const authStore = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  isAuthenticated: true,
  isAdmin: false,
  isSimpleMode: false,
  hasPendingAuthSession: false,
}))

const appStore = vi.hoisted(() => ({
  siteName: 'Sub2API',
  backendModeEnabled: false,
  publicSettingsLoaded: false,
  cachedPublicSettings: null as null | {
    payment_enabled?: boolean
    payment_entry_enabled?: boolean
    risk_control_enabled?: boolean
    subscription_enabled?: boolean
    custom_menu_items?: []
  },
  fetchPublicSettings: vi.fn(),
}))

vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>()
  return {
    ...actual,
    createWebHistory: vi.fn(() => actual.createMemoryHistory()),
    createRouter: vi.fn((options: RouterOptions) => {
      const router = actual.createRouter(options)
      routerHarness.router = router
      vi.spyOn(router, 'beforeEach').mockImplementation((guard) => {
        routerHarness.guard = guard as NavigationGuard
        return vi.fn()
      })
      return router
    }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] }),
}))

vi.mock('@/stores/adminCompliance', () => ({
  useAdminComplianceStore: () => ({
    initialized: true,
    fetchStatus: vi.fn(),
    requireAcknowledgement: vi.fn(),
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

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function runGuard(meta: Record<string, unknown>, path: string) {
  if (!routerHarness.guard) {
    throw new Error('router guard was not registered')
  }

  const next = vi.fn()
  const navigation = routerHarness.guard(
    {
      path,
      fullPath: path,
      name: 'FeatureRoute',
      params: {},
      meta: { requiresAuth: true, ...meta },
    },
    {},
    next
  )
  return { navigation, next }
}

function routeFor(path: string) {
  const route = routerHarness.router?.resolve(path)
  if (!route?.matched.length) throw new Error(`route not found: ${path}`)
  return route
}

describe('feature route guard', () => {
  beforeAll(async () => {
    await import('@/router')
  })

  beforeEach(() => {
    authStore.isAuthenticated = true
    authStore.isAdmin = false
    authStore.isSimpleMode = false
    appStore.publicSettingsLoaded = false
    appStore.cachedPublicSettings = null
    appStore.fetchPublicSettings.mockReset()
  })

  it('waits for the first public-settings request before deciding payment access', async () => {
    const deferred = createDeferred<{ payment_enabled: boolean }>()
    appStore.fetchPublicSettings.mockImplementation(async () => {
      const settings = await deferred.promise
      appStore.cachedPublicSettings = settings
      appStore.publicSettingsLoaded = true
      return settings
    })

    const { navigation, next } = runGuard({ requiresPayment: true }, '/purchase')

    await vi.waitFor(() => expect(appStore.fetchPublicSettings).toHaveBeenCalledTimes(1))
    expect(next).not.toHaveBeenCalled()

    deferred.resolve({ payment_enabled: true })
    await navigation
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each([
    ['payment', { requiresPayment: true }, '/purchase'],
    ['risk control', { requiresRiskControl: true }, '/admin/risk-control'],
    ['subscription', { requiresSubscription: true }, '/subscriptions'],
  ])('does not treat a failed %s settings load as explicitly disabled', async (_name, meta, path) => {
    authStore.isAdmin = meta.requiresRiskControl === true
    appStore.fetchPublicSettings.mockResolvedValue(null)

    const { navigation, next } = runGuard(meta, path)
    await navigation

    expect(appStore.publicSettingsLoaded).toBe(false)
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each([
    ['payment', { requiresPayment: true }, { payment_enabled: false }, '/dashboard'],
    [
      'risk control',
      { requiresRiskControl: true },
      { risk_control_enabled: false },
      '/admin/settings',
    ],
    ['subscription', { requiresSubscription: true }, { subscription_enabled: false }, '/dashboard'],
  ])('redirects when loaded settings explicitly disable %s', async (_name, meta, settings, target) => {
    authStore.isAdmin = meta.requiresRiskControl === true
    appStore.cachedPublicSettings = settings
    appStore.publicSettingsLoaded = true

    const { navigation, next } = runGuard(meta, '/feature')
    await navigation

    expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith(target)
  })

  it('allows an administrator to reach order management while public purchasing is disabled', async () => {
    authStore.isAdmin = true
    appStore.cachedPublicSettings = { payment_enabled: false }
    appStore.publicSettingsLoaded = true
    const ordersRoute = routeFor('/admin/orders')
    expect(routeFor('/admin/orders/coupons').meta).toMatchObject({ requiresAuth: true, requiresAdmin: true, requiresPayment: false })

    expect(ordersRoute.meta).toMatchObject({ requiresAuth: true, requiresAdmin: true })
    expect(ordersRoute.meta?.requiresPayment).toBeUndefined()
    expect(routeFor('/admin/orders/dashboard').meta?.requiresPayment).toBe(true)

    const { navigation, next } = runGuard(ordersRoute.meta, ordersRoute.path)
    await navigation

    expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it('retains the former product URL as an alias of the unified catalog', () => {
    const catalog = routeFor('/admin/products')
    const legacy = routeFor('/admin/orders/plans')

    expect(legacy.matched[legacy.matched.length - 1]?.aliasOf)
      .toBe(catalog.matched[catalog.matched.length - 1])
  })

  it.each(['/admin/products', '/admin/orders/plans'])(
    'allows an administrator to reach %s while public purchasing is disabled',
    async (path) => {
      authStore.isAdmin = true
      appStore.cachedPublicSettings = { payment_enabled: false }
      appStore.publicSettingsLoaded = true
      const route = routeFor(path)

      expect(route.meta).toMatchObject({ requiresAuth: true, requiresAdmin: true, requiresPayment: false })
      const { navigation, next } = runGuard(route.meta, route.path)
      await navigation

      expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
      expect(next).toHaveBeenCalledOnce()
      expect(next).toHaveBeenCalledWith()
    }
  )

  it.each(['/admin/products', '/admin/orders/plans'])(
    'requires sign-in for %s',
    async (path) => {
      authStore.isAuthenticated = false
      const route = routeFor(path)
      const { navigation, next } = runGuard(route.meta, route.path)
      await navigation

      expect(next).toHaveBeenCalledOnce()
      expect(next).toHaveBeenCalledWith({ path: '/login', query: { redirect: path } })
    }
  )

  it.each(['/admin/products', '/admin/orders/plans'])(
    'blocks non-admin users from %s',
    async (path) => {
      const route = routeFor(path)
      const { navigation, next } = runGuard(route.meta, route.path)
      await navigation

      expect(next).toHaveBeenCalledOnce()
      expect(next).toHaveBeenCalledWith('/dashboard')
    }
  )

  it('keeps the customer purchase route blocked while public purchasing is disabled', async () => {
    appStore.cachedPublicSettings = { payment_enabled: false }
    appStore.publicSettingsLoaded = true
    const purchaseRoute = routeFor('/purchase')

    expect(purchaseRoute.meta?.requiresPayment).toBe(true)
    const { navigation, next } = runGuard(purchaseRoute.meta, purchaseRoute.path)
    await navigation

    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith('/dashboard')
  })

  it('still allows a direct purchase visit when only the entry switch is off', async () => {
    appStore.cachedPublicSettings = { payment_enabled: true, payment_entry_enabled: false }
    appStore.publicSettingsLoaded = true
    const purchaseRoute = routeFor('/purchase')

    // The presentation-only entry switch must not tighten the route guard.
    expect(purchaseRoute.meta?.requiresPayment).toBe(true)
    const { navigation, next } = runGuard(purchaseRoute.meta, purchaseRoute.path)
    await navigation

    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it('keeps order history reachable while the purchase entry is hidden', async () => {
    appStore.cachedPublicSettings = { payment_enabled: true, payment_entry_enabled: false }
    appStore.publicSettingsLoaded = true
    const ordersRoute = routeFor('/orders')

    expect(ordersRoute.meta?.requiresPayment).toBe(false)
    const { navigation, next } = runGuard(ordersRoute.meta, ordersRoute.path)
    await navigation

    expect(next).toHaveBeenCalledWith()
  })
})

describe('subscription route guard (opt-out flag)', () => {
  beforeEach(() => {
    authStore.isAdmin = false
    authStore.isSimpleMode = false
    appStore.publicSettingsLoaded = true
    appStore.fetchPublicSettings.mockReset()
  })

  it.each([
    ['missing key', {}],
    ['explicit true', { subscription_enabled: true }],
  ])('lets /subscriptions through when the flag is %s', async (_name, settings) => {
    appStore.cachedPublicSettings = settings

    const { navigation, next } = runGuard({ requiresSubscription: true }, '/subscriptions')
    await navigation

    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it('sends admins to the admin dashboard when subscriptions are disabled', async () => {
    authStore.isAdmin = true
    appStore.cachedPublicSettings = { subscription_enabled: false }

    const { navigation, next } = runGuard({ requiresSubscription: true }, '/subscriptions')
    await navigation

    expect(next).toHaveBeenCalledWith('/admin/dashboard')
  })
})
