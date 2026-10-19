import { defineConfig, type DefaultTheme } from 'vitepress'

import { sections, source, titleOf } from '../pages.mjs'

// The documentation site (PLAN.md 11): the guides of docs/, as pages.mjs
// puts them together in src/, in English and in Chinese. It is published
// to GitHub Pages at https://getjec.dev/.

const repo = 'https://github.com/XMZO/JustEnoughCerts'

// The path the site is published under, as the workflow learns it from
// GitHub Pages (.github/workflows/docs.yml): "/" on the custom domain and
// when built here.
const base = process.env.DOCS_BASE || '/'

type Lang = 'en' | 'zh-CN'

// The names of the sections, as the sidebar's groups.
const groups: Record<Lang, Record<string, string>> = {
  en: { guide: 'Getting started', usage: 'Usage', operations: 'Operations', reference: 'Reference', development: 'Development' },
  'zh-CN': { guide: '开始', usage: '使用', operations: '运维', reference: '参考', development: '开发' },
}

function prefix(lang: Lang): string {
  return lang === 'en' ? '' : '/zh-CN'
}

/** sidebar lists every page, by section, under the titles of the pages. */
function sidebar(lang: Lang): DefaultTheme.SidebarItem[] {
  return sections.map((s) => ({
    text: groups[lang][s.dir],
    items: s.pages.map((name) => ({
      text: titleOf.get(source(s.dir, name, lang)),
      link: `${prefix(lang)}/${s.dir}/${name}`,
    })),
  }))
}

/** editLink links a page to its file on GitHub: the page of the site at
 * zh-CN/guide/x.md is docs/guide/x.zh-CN.md in the repository. The pattern
 * runs in the browser, so it refers to nothing around it. */
function editLink(text: string): DefaultTheme.EditLink {
  return {
    text,
    pattern: ({ filePath }) => {
      const zh = filePath.startsWith('zh-CN/')
      const path = zh ? filePath.slice('zh-CN/'.length).replace(/\.md$/, '.zh-CN.md') : filePath
      return `https://github.com/XMZO/JustEnoughCerts/edit/main/docs/${path}`
    },
  }
}

/** nav leads to the first page of the guide, the reference and development. */
function nav(lang: Lang, text: { guide: string; reference: string; development: string; releases: string }): DefaultTheme.NavItem[] {
  const at = prefix(lang)
  return [
    { text: text.guide, link: `${at}/guide/introduction`, activeMatch: `^${at}/(guide|usage|operations)/` },
    { text: text.reference, link: `${at}/reference/providers`, activeMatch: `^${at}/reference/` },
    { text: text.development, link: `${at}/development/contributing`, activeMatch: `^${at}/development/` },
    { text: text.releases, link: `${repo}/releases` },
  ]
}

export default defineConfig({
  srcDir: 'src',
  base,
  title: 'Just Enough Certs',
  cleanUrls: true,
  markdown: {
    // Inline code shows templates, such as {{.domains}}, as they are, as
    // fenced code does: Vue does not read them.
    config(md) {
      const code = md.renderer.rules.code_inline!
      md.renderer.rules.code_inline = (...args) => code(...args).replace(/^<code/, '<code v-pre')
    },
  },
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: `${base}jec-mark.svg` }],
    ['meta', { name: 'theme-color', content: '#2a7a2e' }],
  ],
  themeConfig: {
    logo: '/jec-mark.svg',
    socialLinks: [{ icon: 'github', link: repo }],
    externalLinkIcon: true,
    outline: { level: [2, 3] },
    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © 2026 XMZO',
    },
    search: {
      provider: 'local',
      options: {
        locales: {
          'zh-CN': {
            translations: {
              button: { buttonText: '搜索', buttonAriaLabel: '搜索文档' },
              modal: {
                displayDetails: '显示详细列表',
                resetButtonTitle: '清除',
                backButtonTitle: '关闭搜索',
                noResultsText: '没有找到',
                footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' },
              },
            },
          },
        },
      },
    },
  },
  locales: {
    root: {
      label: 'English',
      lang: 'en-US',
      description: 'Just enough certs to keep your site green: a self-hosted SSL/TLS certificate manager.',
      themeConfig: {
        nav: nav('en', { guide: 'Guide', reference: 'Reference', development: 'Development', releases: 'Releases' }),
        sidebar: sidebar('en'),
        editLink: editLink('Edit this page on GitHub'),
        notFound: {
          title: 'PAGE NOT FOUND',
          quote: 'A creeper blew this page up. Or the link is wrong, or the page has moved.',
          linkText: 'Take me home',
        },
      },
    },
    'zh-CN': {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh-CN/',
      description: '自托管的 SSL/TLS 证书管理面板：自动签发、部署与续期，告别证书过期。',
      themeConfig: {
        nav: nav('zh-CN', { guide: '指南', reference: '参考', development: '开发', releases: '发布' }),
        sidebar: sidebar('zh-CN'),
        editLink: editLink('在 GitHub 上编辑此页'),
        footer: { message: '基于 MIT 许可发布', copyright: '版权所有 © 2026 XMZO' },
        outline: { level: [2, 3], label: '本页内容' },
        docFooter: { prev: '上一页', next: '下一页' },
        darkModeSwitchLabel: '外观',
        lightModeSwitchTitle: '切换到浅色',
        darkModeSwitchTitle: '切换到深色',
        sidebarMenuLabel: '目录',
        returnToTopLabel: '回到顶部',
        langMenuLabel: '语言',
        notFound: { title: '找不到这个页面', quote: '这一页被苦力怕炸没了。也可能是链接有误，或者页面已经移走。', linkText: '回到首页' },
      },
    },
  },
})
