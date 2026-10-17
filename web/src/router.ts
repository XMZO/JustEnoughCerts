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
    {
      path: '/',
      component: () => import('./components/AppShell.vue'),
      children: [
        { path: '', name: 'overview', component: () => import('./pages/OverviewPage.vue'), meta: { title: 'kernel.overview.title', for: 'app' } },
        { path: 'certificates', name: 'certificates', component: () => import('./pages/CertsPage.vue'), meta: { title: 'certs.list.title', for: 'app' } },
        {
          path: 'certificates/new',
          name: 'add-certificate',
          component: () => import('./pages/AddCertPage.vue'),
          meta: { title: 'certs.action.add', for: 'app' },
        },
        {
          path: String.raw`certificates/:id(\d+)`,
          name: 'certificate',
          component: () => import('./pages/CertDetailPage.vue'),
          props: (route) => ({ id: Number(route.params.id) }),
          meta: { title: 'certs.detail.title', for: 'app' },
        },
        {
          path: String.raw`certificates/:id(\d+)/issuances/:issuance(\d+)`,
          name: 'issuance',
          component: () => import('./pages/IssuancePage.vue'),
          props: (route) => ({ id: Number(route.params.id), issuance: Number(route.params.issuance) }),
          meta: { title: 'certs.issuance.title', for: 'app' },
        },
        {
          path: String.raw`certificates/:id(\d+)/deployments/:deployment(\d+)`,
          name: 'deployment',
          component: () => import('./pages/DeploymentPage.vue'),
          props: (route) => ({ id: Number(route.params.id), deployment: Number(route.params.deployment) }),
          meta: { title: 'certs.deployment.title', for: 'app' },
        },
        {
          path: 'history',
          name: 'history',
          component: () => import('./pages/HistoryPage.vue'),
          meta: { title: 'certs.runs.title', for: 'app' },
        },
        {
          path: 'sources',
          name: 'sources',
          component: () => import('./pages/ProvidersPage.vue'),
          props: { kind: 'source' },
          meta: { title: 'kernel.providers.source.title', for: 'app' },
        },
        {
          path: 'targets',
          name: 'targets',
          component: () => import('./pages/ProvidersPage.vue'),
          props: { kind: 'target' },
          meta: { title: 'kernel.providers.target.title', for: 'app' },
        },
        {
          path: 'notify',
          name: 'notify',
          component: () => import('./pages/ProvidersPage.vue'),
          props: { kind: 'notifier' },
          meta: { title: 'kernel.providers.notifier.title', for: 'app' },
        },
        {
          path: 'dns',
          name: 'dns',
          component: () => import('./pages/ProvidersPage.vue'),
          props: { kind: 'solver' },
          meta: { title: 'kernel.providers.solver.title', for: 'app' },
        },
        { path: 'modules', name: 'modules', component: () => import('./pages/ModulesPage.vue'), meta: { title: 'kernel.modules.title', for: 'app' } },
        { path: 'more', name: 'more', component: () => import('./pages/MorePage.vue'), meta: { title: 'kernel.shell.more', for: 'app' } },
      ],
    },
    { path: '/login', name: 'login', component: () => import('./pages/LoginPage.vue'), meta: { title: 'kernel.login.title', for: 'login' } },
    { path: '/setup', name: 'setup', component: () => import('./pages/SetupPage.vue'), meta: { title: 'kernel.setup.title', for: 'setup' } },
    { path: '/:rest(.*)*', redirect: '/' },
  ],
})

const homeOf = { setup: { name: 'setup' }, login: { name: 'login' }, app: { name: 'overview' } } as const

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
