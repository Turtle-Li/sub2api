<template>
  <footer class="hairline-t">
    <div class="mx-auto max-w-[1180px] px-5 py-14 sm:px-8">
      <!-- 多页站点的页脚要承担导航，不能只放一行版权 -->
      <div class="grid gap-x-8 gap-y-10 sm:grid-cols-2 lg:grid-cols-4">
        <div v-for="group in footerGroups" :key="group.key">
          <p class="meta">{{ group.label }}</p>
          <ul class="mt-4 space-y-2.5">
            <li v-for="link in group.links" :key="link.key">
              <component
                :is="link.to ? 'router-link' : 'a'"
                :to="link.to"
                :href="link.href"
                :target="link.href ? '_blank' : undefined"
                :rel="link.href ? 'noopener noreferrer' : undefined"
                class="navlink"
              >
                {{ link.label }}
              </component>
            </li>
          </ul>
        </div>
      </div>

      <div class="hairline-t mt-14 flex flex-wrap items-end justify-between gap-6 pt-10">
        <div class="flex min-w-0 items-end gap-5">
          <img
            :src="siteLogo || '/turtleroute-mark.png'"
            alt=""
            class="h-12 w-12 shrink-0 object-contain"
          />
          <p class="wordmark min-w-0 [overflow-wrap:anywhere]">{{ siteName }}</p>
        </div>
        <p class="meta meta-cn">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
      </div>
    </div>
  </footer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useSite, GITHUB_URL } from './useSite'

interface FooterLink {
  key: string
  label: string
  to?: string
  href?: string
}

const {
  t,
  settings,
  siteName,
  siteLogo,
  docUrl,
  primaryDestination,
  statusDestination,
  showModelPlazaEntry,
} = useSite()

const currentYear = computed(() => new Date().getFullYear())

/** 后台配置的协议文档，经 /legal/:documentId 渲染。没配就整组不出现。 */
const legalLinks = computed<FooterLink[]>(() =>
  (settings.value?.login_agreement_documents ?? [])
    .filter((doc) => doc?.id && doc?.title)
    .map((doc) => ({ key: `legal-${doc.id}`, label: doc.title, to: `/legal/${doc.id}` })),
)

const footerGroups = computed(() => {
  const developer: FooterLink[] = [
    { key: 'docs', label: t('site.nav.docs'), to: '/docs' },
    { key: 'protocols', label: t('site.docs.sections.protocols'), to: '/docs/protocols' },
    { key: 'clients', label: t('site.docs.sections.clients'), to: '/docs/clients' },
    { key: 'github', label: 'GitHub', href: GITHUB_URL },
  ]
  if (docUrl.value) {
    developer.push({ key: 'extdocs', label: t('site.footer.links.externalDocs'), href: docUrl.value })
  }

  const product: FooterLink[] = [
    { key: 'models', label: t('site.nav.models'), to: '/models' },
    { key: 'platform', label: t('site.nav.platform'), to: '/platform' },
    { key: 'changelog', label: t('site.nav.changelog'), to: '/changelog' },
  ]
  if (showModelPlazaEntry.value) {
    product.push({ key: 'plaza', label: t('site.footer.links.plaza'), to: '/model-plaza' })
  }

  const groups = [
    { key: 'product', label: t('site.footer.groups.product'), links: product },
    { key: 'developer', label: t('site.footer.groups.developer'), links: developer },
    {
      key: 'service',
      label: t('site.footer.groups.service'),
      links: [
        { key: 'why', label: t('site.nav.why'), to: '/why' },
        { key: 'status', label: t('site.footer.links.status'), to: statusDestination.value },
        { key: 'console', label: t('site.footer.links.console'), to: primaryDestination.value },
        { key: 'keyusage', label: t('site.footer.links.keyUsage'), to: '/key-usage' },
      ] as FooterLink[],
    },
  ]

  if (legalLinks.value.length) {
    groups.push({ key: 'legal', label: t('site.footer.groups.legal'), links: legalLinks.value })
  }

  return groups
})
</script>
