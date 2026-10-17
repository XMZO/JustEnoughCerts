import { createRouter, createWebHistory } from 'vue-router'

import { where } from './api/account'
import { i18n, type MessageKey } from './i18n'

declare module 'vue-router' {
  interface RouteMeta {
    /** The page title, also used for document.title. */
    title: MessageKey
    /** Who the page is for: visitors before setup, signed-out ones, or accounts. */
    for: 'setup' | 'login' | 'app'
  }
}

export const router = createRouter({
  // The server's <base href> carries the base path.
  history: createWebHistory(new URL('.', document.baseURI).pathname),
  routes: [
    { path: '/', name: 'home', component: () => import('./pages/HomePage.vue'), meta: { title: 'kernel.home.title', for: 'app' } },
    { path: '/login', name: 'login', component: () => import('./pages/LoginPage.vue'), meta: { title: 'kernel.login.title', for: 'login' } },
    { path: '/setup', name: 'setup', component: () => import('./pages/SetupPage.vue'), meta: { title: 'kernel.setup.title', for: 'setup' } },
    { path: '/:rest(.*)*', redirect: '/' },
  ],
})

const homeOf = { setup: { name: 'setup' }, login: { name: 'login' }, app: { name: 'home' } } as const

router.beforeEach(async (to) => {
  let state: 'setup' | 'login' | 'app'
  try {
    state = await where()
  } catch {
    state = 'login' // the sign-in page reports a server that does not answer
  }
  if (to.meta.for === state) return true
  if (state === 'login' && to.meta.for === 'app' && to.fullPath !== '/') {
    return { name: 'login', query: { next: to.fullPath } }
  }
  return homeOf[state]
})

/** updateTitle names the browser tab after the current page, in the current language. */
export function updateTitle(): void {
  const { t } = i18n.global
  const page = router.currentRoute.value.meta.title
  document.title = page ? `${t(page)} · ${t('kernel.app.name')}` : t('kernel.app.name')
}

router.afterEach(updateTitle)
