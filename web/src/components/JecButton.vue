<script setup lang="ts">
import { LoaderCircle } from '@lucide/vue'

withDefaults(
  defineProps<{
    variant?: 'default' | 'primary' | 'ghost' | 'danger'
    type?: 'button' | 'submit'
    size?: 'md' | 'sm'
    /** Shows a spinner and blocks clicks while an action runs. */
    busy?: boolean
    disabled?: boolean
  }>(),
  { variant: 'default', type: 'button', size: 'md' },
)

const variants = {
  default: 'border-line-strong bg-surface-raised text-ink hover:bg-surface-sunken',
  primary: 'border-brand bg-brand text-on-brand hover:border-brand-hover hover:bg-brand-hover',
  ghost: 'border-transparent bg-transparent text-ink hover:bg-surface-sunken',
  danger: 'border-danger-ink bg-danger-ink text-on-danger hover:brightness-110',
}
const sizes = { md: 'h-9 px-4', sm: 'h-[30px] px-3 text-small' }
</script>

<template>
  <button
    :type="type"
    :disabled="disabled || busy"
    :aria-busy="busy || undefined"
    :class="[
      'inline-flex items-center justify-center gap-1.5 rounded-md border font-semibold whitespace-nowrap',
      'transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-45',
      variants[variant],
      sizes[size],
    ]"
  >
    <LoaderCircle v-if="busy" class="size-4 animate-spin" aria-hidden="true" />
    <slot />
  </button>
</template>
