import type { Theme } from 'vitepress'
import DefaultTheme from 'vitepress/theme'

import Layout from './Layout.vue'
import './jec.css'

// VitePress's own theme, in the colors of JEC's design tokens, with a few
// motions of its own.
export default {
  extends: DefaultTheme,
  Layout,
} satisfies Theme
