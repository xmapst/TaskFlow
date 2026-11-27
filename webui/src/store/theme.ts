/**
 * 主题状态
 *
 * 深浅双主题的切换要同时作用于三处，缺一处就会出现割裂：
 *  1. document 根节点的 data-theme：驱动本项目的 CSS 变量（tokens.scss）；
 *  2. Arco Design 要求在 body 上加 arco-theme="dark" 才切换其内部变量；
 *  3. CodeMirror 需要在编辑器实例上重新装配主题扩展（见 CodeEditor 组件）。
 */

import { create } from 'zustand'

export type ThemeMode = 'dark' | 'light'

const STORAGE_KEY = 'aef-theme'

/** 读取偏好：本地存储优先，其次跟随系统 */
function resolveInitialTheme(): ThemeMode {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (stored === 'dark' || stored === 'light') return stored
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

/** 把主题写入 DOM。在 store 之外也可调用，供首屏防闪烁使用 */
export function applyTheme(mode: ThemeMode): void {
  document.documentElement.setAttribute('data-theme', mode)
  // Arco 只认 body 上的这个属性
  if (mode === 'dark') {
    document.body.setAttribute('arco-theme', 'dark')
  } else {
    document.body.removeAttribute('arco-theme')
  }
}

interface ThemeState {
  mode: ThemeMode
  setMode: (mode: ThemeMode) => void
  toggle: () => void
}

export const useThemeStore = create<ThemeState>((set, get) => ({
  mode: resolveInitialTheme(),

  setMode: (mode) => {
    applyTheme(mode)
    localStorage.setItem(STORAGE_KEY, mode)
    set({ mode })
  },

  toggle: () => {
    get().setMode(get().mode === 'dark' ? 'light' : 'dark')
  },
}))

/** 在应用挂载前同步一次，避免首屏闪烁 */
export function initTheme(): void {
  applyTheme(useThemeStore.getState().mode)
}
