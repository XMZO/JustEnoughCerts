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

const base = [
  'inline-flex items-center justify-center gap-1.5 rounded-md border font-semibold whitespace-nowrap no-underline',
  'transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-45',
]
const variants = {
  default: 'border-line-strong bg-surface-raised text-ink hover:bg-surface-sunken hover:text-ink',
  primary: 'border-brand bg-brand text-on-brand hover:border-brand-hover hover:bg-brand-hover hover:text-on-brand',
  ghost: 'border-transparent bg-transparent text-ink hover:bg-surface-sunken hover:text-ink',
  danger: 'border-danger-ink bg-danger-ink text-on-danger hover:text-on-danger hover:brightness-110',
}
// Phones get the 44px touch target.
const sizes = { md: 'h-9 px-4 max-sm:min-h-11', sm: 'h-[30px] px-3 text-small max-sm:min-h-11' }
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
