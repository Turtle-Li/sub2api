import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('AppSidebar collapsible groups', () => {
  it('lets the user collapse a group even while a child route is active', () => {
    // The expand state must come from the user's override first, falling back
    // to the active-route heuristic only when the user has not clicked yet.
    expect(componentSource).toContain('const groupExpandOverrides = ref<Map<string, boolean>>(new Map())')
    expect(componentSource).not.toContain('expandedGroups.value.has(item.path) || isGroupActive(item)')
  })
})

describe('AppSidebar admin order access', () => {
  it('keeps order management as a direct admin link when public purchasing is disabled', () => {
    expect(componentSource).toContain('...(adminSettingsStore.paymentEnabled')
    expect(componentSource).toMatch(/path: '\/admin\/orders',\s*label: t\('nav\.orderManagement'\),\s*icon: OrderIcon,\s*hideInSimpleMode: true,\s*expandOnly: true,\s*children:/)
    expect(componentSource).not.toContain('featureFlag: flagAdminPayment')
  })

  it('keeps payment dashboard and plans inside the enabled-payment branch only', () => {
    const paymentNavigation = componentSource.slice(componentSource.indexOf('...(adminSettingsStore.paymentEnabled'))
    expect(paymentNavigation).toContain("{ path: '/admin/orders/dashboard', label: t('nav.paymentDashboard'), icon: ChartIcon }")
    expect(paymentNavigation).toContain("{ path: '/admin/orders/plans', label: t('nav.paymentPlans'), icon: CreditCardIcon }")
  })
})

describe('AppSidebar purchase entry visibility', () => {
  it('gates the /purchase nav item on the entry-aware flag', () => {
    expect(componentSource).toContain('const flagPaymentEntry = () => isPaymentEntryVisible()')
    expect(componentSource).toContain("{ path: '/purchase', label: t('nav.buySubscription'), icon: RechargeSubscriptionIcon, hideInSimpleMode: true, featureFlag: flagPaymentEntry }")
    // The plain payment flag must not gate the purchase nav anymore.
    expect(componentSource).not.toContain("featureFlag: flagPayment,")
    expect(componentSource).not.toContain('featureFlag: flagPayment\n')
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })
})
