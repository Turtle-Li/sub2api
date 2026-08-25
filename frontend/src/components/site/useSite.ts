/**
 * 公开站点的共享状态：站点身份、账号去向、导航模型。
 *
 * masthead、页脚和各个页面都要用同一份，避免每个页面各写一遍
 * siteName / dashboardPath 的 fallback 逻辑而慢慢走样。
 */

import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

export const GITHUB_URL = 'https://github.com/Wei-Shaw/sub2api'

export interface SiteNavItem {
  key: string
  label: string
  to: string
}

export function useSite() {
  const { t } = useI18n()
  const authStore = useAuthStore()
  const appStore = useAppStore()

  const settings = computed(() => appStore.cachedPublicSettings)

  // 站点名沿用首页原有规则：管理员配了就用配置值，否则回落到品牌名。
  const siteName = computed(() => {
    const configured = settings.value?.site_name?.trim()
    if (configured && configured !== 'Sub2API') return configured
    return appStore.siteName && appStore.siteName !== 'Sub2API' ? appStore.siteName : 'TurtleRoute'
  })

  // site_logo / doc_url 都是管理员可写的字段，必须过 sanitizeUrl。
  // 这里刻意和 AppSidebar / AppHeader / KeyUsageView 写成同一个表达式：
  // 有一组守卫测试按源码文本比对，保证这几处不会有人各写各的。
  const siteLogo = computed(() =>
    sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
      allowRelative: true,
      allowDataUrl: true,
    }),
  )

  const siteSubtitle = computed(() => settings.value?.site_subtitle || 'AI API Gateway Platform')

  /** 管理员配置的外部文档站；站内 /docs 会在页尾链过去，不占导航位。 */
  const docUrl = computed(() =>
    sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''),
  )

  const isAuthenticated = computed(() => authStore.isAuthenticated)
  const isAdmin = computed(() => authStore.isAdmin)
  const dashboardPath = computed(() => (isAdmin.value ? '/admin/dashboard' : '/dashboard'))
  const primaryDestination = computed(() => (isAuthenticated.value ? dashboardPath.value : '/login'))

  const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
  const modelPlazaRequiresAuth = computed(() => settings.value?.model_plaza_require_auth === true)
  const showModelPlazaEntry = computed(
    () => modelPlazaEnabled.value && (isAuthenticated.value || !modelPlazaRequiresAuth.value),
  )

  /** 渠道状态页需要登录，未登录时先去登录页。 */
  const statusDestination = computed(() => (isAuthenticated.value ? '/monitor' : '/login'))

  /** masthead 主导航。顺序即信息优先级。 */
  const navItems = computed<SiteNavItem[]>(() => [
    { key: 'models', label: t('site.nav.models'), to: '/models' },
    { key: 'platform', label: t('site.nav.platform'), to: '/platform' },
    { key: 'docs', label: t('site.nav.docs'), to: '/docs' },
    { key: 'why', label: t('site.nav.why'), to: '/why' },
    { key: 'changelog', label: t('site.nav.changelog'), to: '/changelog' },
  ])

  return {
    t,
    settings,
    siteName,
    siteLogo,
    siteSubtitle,
    docUrl,
    isAuthenticated,
    isAdmin,
    dashboardPath,
    primaryDestination,
    statusDestination,
    showModelPlazaEntry,
    navItems,
  }
}
