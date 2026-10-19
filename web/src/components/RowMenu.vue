<script setup lang="ts">
import { Ellipsis } from '@lucide/vue'
import { ref, useId, type Component } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

// The menu of a row (docs/design/boards/Certs): a button that opens the
// row's actions in a popover on the top layer, where no table clips it,
// under the button, or above it when there is no room below. The arrow
// keys, Home and End move between the items; Escape or a click elsewhere
// closes it, and the focus goes back to the button. An item easy to take
// for another says under its label what it does.
export interface MenuItem {
  key: string
  label: string
  hint?: string
  icon?: Component
  danger?: boolean
  to?: RouteLocationRaw
  run?: () => void
}
defineProps<{ label: string; items: MenuItem[] }>()

const id = useId()
const button = ref<HTMLButtonElement>()
const menu = ref<HTMLElement>()
const open = ref(false)
const gap = 4

// Before it shows, under the button and flush with its right edge, which
// needs no size; once it shows, above the button if it does not fit.
function beforeToggle(e: Event) {
  const m = menu.value
  const b = button.value?.getBoundingClientRect()
  if ((e as ToggleEvent).newState !== 'open' || !m || !b) return
  m.style.top = `${b.bottom + gap}px`
  m.style.right = `${document.documentElement.clientWidth - b.right}px`
}

function toggled(e: Event) {
  open.value = (e as ToggleEvent).newState === 'open'
  const m = menu.value
  const b = button.value?.getBoundingClientRect()
  if (!open.value || !m || !b) return
  if (b.bottom + gap + m.offsetHeight > document.documentElement.clientHeight) {
    m.style.top = `${Math.max(gap, b.top - gap - m.offsetHeight)}px`
  }
  entries()[0]?.focus()
}

const entries = () => [...(menu.value?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]

function move(e: KeyboardEvent) {
  const list = entries()
  const at = list.indexOf(document.activeElement as HTMLElement)
  const to = ({ ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: list.length - 1 } as Record<string, number>)[e.key]
  if (to === undefined || !list.length) return
  e.preventDefault()
  list[(to + list.length) % list.length]?.focus()
}

function choose(item: MenuItem) {
  menu.value?.hidePopover()
  item.run?.()
}

const itemClass = (item: MenuItem) => [
  'flex min-h-9 w-full cursor-pointer gap-2 rounded-sm border-0 bg-transparent px-2.5 text-left text-body whitespace-nowrap no-underline',
  item.hint ? 'items-start py-1.5' : 'items-center',
  'hover:bg-surface-sunken focus-visible:bg-surface-sunken focus-visible:outline-none',
  item.danger ? 'text-danger-ink hover:text-danger-ink' : 'text-ink hover:text-ink',
]
</script>

<template>
  <button
    ref="button"
    type="button"
    :aria-label="label"
    aria-haspopup="menu"
    :aria-expanded="open ? 'true' : 'false'"
    :aria-controls="id"
    :popovertarget="id"
    class="grid size-8 cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink hover:bg-surface-sunken"
  >
    <Ellipsis class="size-4" aria-hidden="true" />
  </button>
  <div
    :id="id"
    ref="menu"
    popover="auto"
    role="menu"
    :aria-label="label"
    class="fixed inset-auto m-0 max-w-72 min-w-44 flex-col gap-0.5 rounded-md border border-line bg-surface-raised p-1 text-ink shadow-popover [&:popover-open]:flex"
    @beforetoggle="beforeToggle"
    @toggle="toggled"
    @keydown="move"
  >
    <template v-for="item in items" :key="item.key">
      <RouterLink v-if="item.to" :to="item.to" role="menuitem" tabindex="-1" :class="itemClass(item)" @click="menu?.hidePopover()">
        <component :is="item.icon" v-if="item.icon" class="size-4 flex-none" aria-hidden="true" />{{ item.label }}
      </RouterLink>
      <button v-else type="button" role="menuitem" tabindex="-1" :class="itemClass(item)" @click="choose(item)">
        <component :is="item.icon" v-if="item.icon" :class="['size-4 flex-none', item.hint && 'mt-[3px]']" aria-hidden="true" />
        <span v-if="item.hint" class="grid">
          {{ item.label }}<span class="text-small whitespace-normal text-ink-muted">{{ item.hint }}</span>
        </span>
        <template v-else>{{ item.label }}</template>
      </button>
    </template>
  </div>
</template>
