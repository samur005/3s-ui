import { ref, watch } from 'vue'

export type ViewMode = 'cards' | 'list'

export function useViewMode(page: string, defaultMode: ViewMode = 'cards') {
  const key = `3sui-view-${page}`
  const stored = (typeof localStorage !== 'undefined' && localStorage.getItem(key)) as ViewMode | null
  const mode = ref<ViewMode>(stored === 'list' || stored === 'cards' ? stored : defaultMode)

  watch(mode, (v) => {
    try { localStorage.setItem(key, v) } catch { /* ignore */ }
  })

  return { mode }
}
