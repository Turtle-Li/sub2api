import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import zh from '../../src/i18n/locales/zh'
import Preview from './Preview.vue'
import '../../src/style.css'

document.documentElement.classList.toggle('dark', new URLSearchParams(location.search).get('theme') === 'dark')
createApp(Preview).use(createPinia()).use(createI18n({
  legacy: false, locale: 'zh', messages: { zh },
})).mount('#app')
