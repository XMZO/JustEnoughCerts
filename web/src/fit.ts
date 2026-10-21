import { nextTick, onBeforeUnmount, onMounted, ref, watch, type Ref, type WatchSource } from 'vue'

export interface FitOptions {
  /** The height of a row until one shows. */
  row: () => number
  /** The room taken above the first row when none shows yet, as by the head of a table. */
  head?: () => number
  /** The room kept under the rows, for the pager and the page's margin. */
  below: () => number
  /** Fewer rows than this, on a short window, and the page scrolls. */
  min?: number
  /** A change that lays the rows out anew, as a page loaded; watched from the mount on. */
  again?: WatchSource
}

/**
 * useFitRows is how many rows of a list fit between where the list starts,
 * at el, and the bottom of the window, as the user asked that a page of a
 * long list show whole without scrolling. The rows are as tall as the
 * first one shown, marked data-row; 0 until el is in the page.
 */
export function useFitRows(el: Ref<HTMLElement | undefined>, opts: FitOptions): Ref<number> {
  const size = ref(0)
  function measure() {
    const box = el.value
    if (!box) return
    const first = [...box.querySelectorAll<HTMLElement>('[data-row]')].find((r) => r.offsetParent !== null)
    const top = first ? first.getBoundingClientRect().top : box.getBoundingClientRect().top + (opts.head?.() ?? 0)
    const row = first?.getBoundingClientRect().height || opts.row()
    const room = window.innerHeight - (top + window.scrollY) - opts.below()
    // A pixel or two to spare, for the rounding of fractional heights.
    size.value = Math.max(opts.min ?? 5, Math.floor((room - 2) / row))
  }
  let timer: ReturnType<typeof setTimeout> | undefined
  const later = () => {
    clearTimeout(timer)
    timer = setTimeout(measure, 150)
  }
  let stop: (() => void) | undefined
  onMounted(() => {
    measure()
    // The bundled fonts make the rows a little taller once they load.
    void document.fonts?.ready.then(measure)
    window.addEventListener('resize', later)
    if (opts.again) stop = watch(opts.again, () => void nextTick(measure))
  })
  onBeforeUnmount(() => {
    clearTimeout(timer)
    stop?.()
    window.removeEventListener('resize', later)
  })
  return size
}

/** useWindowHeight is the height of the window, kept up as it changes. */
export function useWindowHeight(): Ref<number> {
  const height = ref(window.innerHeight)
  const update = () => (height.value = window.innerHeight)
  onMounted(() => window.addEventListener('resize', update))
  onBeforeUnmount(() => window.removeEventListener('resize', update))
  return height
}

/**
 * keepPlace is the page that holds the first row of page, of size rows,
 * once a page holds size2: the same rows stay in sight.
 */
export function keepPlace(page: number, size: number, size2: number): number {
  return Math.floor(((page - 1) * size) / size2) + 1
}
