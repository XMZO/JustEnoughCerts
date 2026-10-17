<script setup lang="ts">
import { LoaderCircle } from '@lucide/vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

withDefaults(
  defineProps<{
    variant?: 'default' | 'primary' | 'ghost' | 'danger'
    type?: 'button' | 'submit'
    size?: 'md' | 'sm'
    /** Shows a spinner and blocks clicks while an action runs. */
    busy?: boolean
    disabled?: boolean
    /** Makes the button a link to this route, for actions that open a page. */
    to?: RouteLocationRaw
  }>(),
  { variant: 'default', type: 'button', size: 'md', to: undefined },
)

// A label stays on one line while there is room. A button that alone is
// wider than its row, as with a long translation, wraps its label rather
// than spill out.
const base = [
  'inline-flex max-w-full items-center justify-center gap-1.5 rounded-md border text-center font-semibold no-underline',
  'transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-45',
]
const variants = {
  default: 'border-line-strong bg-surface-raised text-ink hover:bg-surface-sunken hover:text-ink',
  primary: 'border-brand bg-brand text-on-brand hover:border-brand-hover hover:bg-brand-hover hover:text-on-brand',
  ghost: 'border-transparent bg-transparent text-ink hover:bg-surface-sunken hover:text-ink',
  danger: 'border-danger-ink bg-danger-ink text-on-danger hover:text-on-danger hover:brightness-110',
}
// Phones get the 44px touch target.
const sizes = { md: 'min-h-9 px-4 py-1.5 max-sm:min-h-11', sm: 'min-h-[30px] px-3 py-1 text-small max-sm:min-h-11' }
</script>

<template>
  <RouterLink v-if="to" :to="to" :class="[base, variants[variant], sizes[size]]">
    <slot />
  </RouterLink>
  <button
    v-else
    :type="type"
    :disabled="disabled || busy"
    :aria-busy="busy || undefined"
    :class="[base, 'cursor-pointer', variants[variant], sizes[size]]"
  >
    <LoaderCircle v-if="busy" class="size-4 animate-spin" aria-hidden="true" />
    <slot />
  </button>
</template>
