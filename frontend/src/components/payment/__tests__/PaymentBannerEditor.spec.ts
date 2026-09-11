import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PaymentBannerEditor from '../PaymentBannerEditor.vue'
import ImageUpload from '@/components/common/ImageUpload.vue'
import Toggle from '@/components/common/Toggle.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('PaymentBannerEditor', () => {
  it('preserves content when disabled and restores the fields when enabled', async () => {
    const banner = { enabled: true, title: 'Offer', link_url: '/offers', image_url: '/offer.png' }
    const wrapper = mount(PaymentBannerEditor, {
      props: { modelValue: banner },
      global: { stubs: { ImageUpload: true } },
    })
    wrapper.getComponent(Toggle).vm.$emit('update:modelValue', false)
    const disabled = wrapper.emitted('update:modelValue')![0][0]
    expect(disabled).toMatchObject({ ...banner, enabled: false })
    await wrapper.setProps({ modelValue: disabled as typeof banner })
    expect(wrapper.find('input').exists()).toBe(false)
    await wrapper.setProps({ modelValue: banner })
    expect(wrapper.get('input').element.value).toBe('Offer')
  })

  it('rejects SVG from the uploader without replacing the saved image, then accepts raster images', async () => {
    const wrapper = mount(PaymentBannerEditor, {
      props: { modelValue: { enabled: true, title: 'Offer', link_url: '/offers', image_url: '/old.png' } },
      global: { stubs: { ImageUpload: true } },
    })
    wrapper.getComponent(ImageUpload).vm.$emit('update:modelValue', 'data:image/svg+xml;base64,PHN2Zy8+')
    await wrapper.vm.$nextTick()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('bannerImageFormatError')
    wrapper.getComponent(ImageUpload).vm.$emit('update:modelValue', 'data:image/png;base64,QUJD')
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')![0][0]).toMatchObject({ image_url: 'data:image/png;base64,QUJD' })
  })

  it('updates title without losing the configured destination or image', async () => {
    const banner = { enabled: true, title: 'Offer', link_url: '/offers', image_url: '/offer.png' }
    const wrapper = mount(PaymentBannerEditor, {
      props: { modelValue: banner },
      global: { stubs: { ImageUpload: true } },
    })
    await wrapper.get('input').setValue('New offer')
    expect(wrapper.emitted('update:modelValue')![0][0]).toMatchObject({ ...banner, title: 'New offer' })
    expect(banner.title).toBe('Offer')
  })
})
