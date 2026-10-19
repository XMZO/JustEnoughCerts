import { nextTick } from 'vue'

// Changes that repaint the whole page, as of the language or the theme,
// cross-fade from the page before to the page after (the View Transitions
// API), where the browser has it and motion is welcome; elsewhere they are
// at once. A change while one is under way takes over from it: the one
// before ends where it is. While one runs, the page's own transitions,
// such as of a button's colors, keep still, so that the page after shows
// as it will stay.

let running: ViewTransition | undefined

/** crossFade runs update, which changes the page, as a cross-fade. */
export async function crossFade(update: () => void | Promise<void>): Promise<void> {
  const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  if (typeof document.startViewTransition !== 'function' || reduced) {
    await update()
    return
  }
  running?.skipTransition()
  const root = document.documentElement
  root.classList.add('jec-switching')
  const transition = document.startViewTransition(async () => {
    await update()
    await nextTick() // Vue has drawn the page after
  })
  running = transition
  try {
    await transition.finished
  } catch {
    // skipped by the next change, which goes on from here
  } finally {
    if (running === transition) {
      running = undefined
      root.classList.remove('jec-switching')
    }
  }
}
