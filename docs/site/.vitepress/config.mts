import { defineConfig, type DefaultTheme } from 'vitepress'

// The documentation site (PLAN.md 11): the README and the guides of docs/,
// as pages.mjs puts them together in src/, in English and in Chinese. It
// is published to GitHub Pages under the repository's path.

const repo = 'https://github.com/XMZO/JustEnoughCerts'

function sidebar(prefix: string, labels: Record<string, string>): DefaultTheme.SidebarItem[] {
  return [
    {
      items: [
        { text: labels.overview, link: `${prefix}/` },
        { text: labels.trying, link: `${prefix}/trying` },
        { text: labels.providers, link: `${prefix}/providers` },
        { text: labels.plugins, link: `${prefix}/plugins` },
        { text: labels.translating, link: `${prefix}/translating` },
      ],
    },
  ]
}

export default defineConfig({
  srcDir: 'src',
  base: '/JustEnoughCerts/',
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
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/JustEnoughCerts/jec-mark.svg' }],
    ['meta', { name: 'theme-color', content: '#2a7a2e' }],
  ],
  themeConfig: {
    logo: '/jec-mark.svg',
    socialLinks: [{ icon: 'github', link: repo }],
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
        nav: [
          { text: 'Guide', link: '/' },
          { text: 'Releases', link: `${repo}/releases` },
        ],
        sidebar: sidebar('', {
          overview: 'Overview',
          trying: 'Trying with real accounts',
          providers: 'Providers',
          plugins: 'Plugins',
          translating: 'Translating',
        }),
      },
    },
    'zh-CN': {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh-CN/',
      description: '刚好够用的证书，让网站一直是绿锁：自托管的 SSL/TLS 证书管理面板。',
      themeConfig: {
        nav: [
          { text: '指南', link: '/zh-CN/' },
          { text: '发布', link: `${repo}/releases` },
        ],
        sidebar: sidebar('/zh-CN', {
          overview: '概览',
          trying: '用真实账号试用',
          providers: '服务商',
          plugins: '插件',
          translating: '翻译',
        }),
        outline: { label: '本页内容' },
        docFooter: { prev: '上一页', next: '下一页' },
        darkModeSwitchLabel: '外观',
        lightModeSwitchTitle: '切换到浅色',
        darkModeSwitchTitle: '切换到深色',
        sidebarMenuLabel: '目录',
        returnToTopLabel: '回到顶部',
        langMenuLabel: '语言',
        notFound: { title: '找不到这个页面', quote: '链接可能有误，或者页面已经移走。', linkText: '回到首页' },
      },
    },
  },
})
