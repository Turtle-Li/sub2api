// Local verification fixture: real components and synthetic account data only.
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import zh from '../../src/i18n/locales/zh'
import Preview from './Preview.vue'
import '../../src/style.css'

createApp(Preview).use(createPinia()).use(createI18n({
  legacy: false, locale: 'zh', messages: { zh }
})).mount('#app')
