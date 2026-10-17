import './styles/main.css'

import { VueQueryPlugin } from '@tanstack/vue-query'
import { createApp } from 'vue'

import { queryClient } from './api/query'
import App from './App.vue'
import { i18n, initialLocale, setLocale } from './i18n'
import { router } from './router'
import { followSystemTheme } from './theme'

followSystemTheme()
await setLocale(initialLocale(), false)

createApp(App).use(i18n).use(VueQueryPlugin, { queryClient }).use(router).mount('#app')
