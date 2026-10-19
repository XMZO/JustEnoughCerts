import { ref } from 'vue'

import type { MessageKey } from './i18n'

/**
 * fromMore is the way back of the pages the phone's More tab leads to, which
 * the sidebar lists on wider screens: a back button on phones only.
 */
export const fromMore: { to: string; label: MessageKey } = { to: '/more', label: 'kernel.shell.more' }

/** jumpOpen opens the quick jump of the shell, as Ctrl+K does: from the phone's More tab too. */
export const jumpOpen = ref(false)
