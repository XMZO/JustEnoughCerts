<script setup lang="ts">
import { X } from '@lucide/vue'
import { nextTick, ref, watch } from 'vue'

import { useT } from '../i18n'

// A modal dialog (docs/design/system/components/Dialog.md) on the native
// <dialog>: it keeps the focus inside, closes on Esc and gives the focus
// back to whatever opened it. On phones it fills the screen.
const open = defineModel<boolean>('open', { required: true })
withDefaults(defineProps<{ title: string; small?: boolean }>(), { small: false })

const { t } = useT()
const dialog = ref<HTMLDialogElement>()

watch(
  open,
  async (now) => {
    await nextTick()
    const el = dialog.value
    if (!el) return
    if (now && !el.open) {
      el.showModal()
      el.querySelector<HTMLElement>('input, textarea, select')?.focus()
    } else if (!now && el.open) {
      el.close()
    }
  },
  { immediate: true },
)
</script>

<template>
  <dialog
    ref="dialog"
    :class="[
      'm-auto flex-col overflow-hidden border-line bg-surface-raised p-0 text-ink shadow-popover open:flex',
      'max-sm:h-dvh max-sm:max-h-none max-sm:w-full max-sm:max-w-none max-sm:border-0',
      'sm:max-h-[calc(100dvh-48px)] sm:rounded-lg sm:border',
      small ? 'sm:w-[min(480px,calc(100vw-48px))]' : 'sm:w-[min(720px,calc(100vw-48px))]',
    ]"
    @close="open = false"
  >
    <div class="flex items-center justify-between gap-3 border-b border-line py-3 pr-3 pl-5">
      <h2 class="m-0 min-w-0 text-section-title font-semibold [overflow-wrap:anywhere]">{{ title }}</h2>
      <button
        type="button"
        :aria-label="t('kernel.action.close')"
        class="grid size-11 flex-none cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink hover:bg-surface-sunken sm:size-9"
        @click="open = false"
      >
        <X class="size-4" aria-hidden="true" />
      </button>
    </div>
    <div class="min-h-0 flex-1 overflow-auto px-5 py-4">
      <slot />
    </div>
    <div class="flex flex-wrap items-center justify-end gap-2 border-t border-line px-5 py-3">
      <slot name="footer" />
    </div>
  </dialog>
</template>

<style scoped>
dialog::backdrop {
  background: color-mix(in srgb, var(--ink) 45%, transparent);
}
</style>
