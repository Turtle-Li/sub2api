import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import PaymentPromoBanner from '../PaymentPromoBanner.vue'

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  fallbackWarn: false,
  missingWarn: false,
  messages: { en: { payment: { viewOffer: 'View offer' } } },
})

describe('PaymentPromoBanner', () => {
  it('renders a safe clickable banner with the fixed presentation treatment', () => {
    const wrapper = mount(PaymentPromoBanner, {
      props: {
        banner: {
          enabled: true,
          title: 'Spring offer',
          description: 'Extra balance for new users',
          link_url: 'https://example.com/spring',
          button_text: 'View now',
        },
      },
      global: { plugins: [i18n], stubs: { Icon: true } },
    })

    const link = wrapper.get('a')
    expect(link.attributes('href')).toBe('https://example.com/spring')
    expect(link.attributes('target')).toBe('_blank')
    expect(wrapper.text()).toContain('Spring offer')
    expect(wrapper.text()).toContain('View now')
    expect(wrapper.classes()).toContain('mb-6')
  })

  it('does not render an unsafe destination', () => {
    const wrapper = mount(PaymentPromoBanner, {
      props: {
        banner: { enabled: true, title: 'Unsafe', link_url: 'javascript:alert(1)' },
      },
      global: { plugins: [i18n], stubs: { Icon: true } },
    })

    expect(wrapper.find('a').exists()).toBe(false)
    expect(wrapper.find('section').exists()).toBe(false)
  })

  it('renders raster data images but rejects inline SVG payloads', () => {
    const safe = mount(PaymentPromoBanner, {
      props: {
        banner: {
          enabled: true,
          title: 'Raster offer',
          link_url: '/offers/raster',
          image_url: 'data:image/png;base64,QUJD',
        },
      },
      global: { plugins: [i18n], stubs: { Icon: true } },
    })
    expect(safe.get('img').attributes('src')).toBe('data:image/png;base64,QUJD')

    const svg = mount(PaymentPromoBanner, {
      props: {
        banner: {
          enabled: true,
          title: 'SVG offer',
          link_url: '/offers/svg',
          image_url: 'data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=',
        },
      },
      global: { plugins: [i18n], stubs: { Icon: true } },
    })
    expect(svg.find('img').exists()).toBe(false)
    expect(svg.find('section').exists()).toBe(true)
  })

  it('rejects browser backslash escapes in relative destinations', () => {
    const wrapper = mount(PaymentPromoBanner, {
      props: {
        banner: { enabled: true, title: 'Escaped', link_url: '/\\\\attacker.example' },
      },
      global: { plugins: [i18n], stubs: { Icon: true } },
    })

    expect(wrapper.find('section').exists()).toBe(false)
  })
})
