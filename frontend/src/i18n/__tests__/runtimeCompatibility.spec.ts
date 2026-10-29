import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { describe, expect, it } from 'vitest'
import { useI18n } from 'vue-i18n'

import { i18n, loadLocaleMessages } from '../index'

describe('application i18n runtime compatibility', () => {
  it('renders lazy locale messages and reacts to locale changes with interpolation', async () => {
    await loadLocaleMessages('zh')
    await loadLocaleMessages('en')
    const previousLocale = i18n.global.locale.value
    i18n.global.locale.value = 'zh'

    const wrapper = mount(defineComponent({
      setup() {
        const { t } = useI18n()
        return { t }
      },
      template: '<button>{{ t("common.save") }}</button><span>{{ t("common.selectedCount", { count: 3 }) }}</span>'
    }), { global: { plugins: [i18n] } })

    try {
      expect(wrapper.get('button').text()).toBe('保存')
      expect(wrapper.get('span').text()).toBe('（已选 3 个）')

      i18n.global.locale.value = 'en'
      await nextTick()

      expect(wrapper.get('button').text()).toBe('Save')
      expect(wrapper.get('span').text()).toBe('(3 selected)')
    } finally {
      wrapper.unmount()
      i18n.global.locale.value = previousLocale
    }
  })
})
