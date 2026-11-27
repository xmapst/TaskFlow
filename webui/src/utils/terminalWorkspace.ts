export const maxPanes = 4

export type SplitDirection = 'side-by-side' | 'stacked'
interface Workspace {
  panes: number[]
  active: number | undefined
  direction: SplitDirection
  next: number
}

type Action =
  | { type: 'open-pane' }
  | { type: 'select-pane' | 'close-pane'; id: number }
  | { type: 'split'; direction: SplitDirection }

export const initialWorkspace: Workspace = { panes: [1], active: 1, direction: 'side-by-side', next: 2 }

export function workspaceAction(state: Workspace, action: Action): Workspace {
  const { panes, active, next } = state
  if (action.type === 'open-pane') {
    if (panes.length !== 0) return state
    return { ...initialWorkspace, panes: [next], active: next, next: next + 1 }
  }
  if (action.type === 'split') {
    if (panes.length === 0 || panes.length >= maxPanes) return state
    return { panes: [...panes, next], active: next, direction: action.direction, next: next + 1 }
  }
  const index = panes.indexOf(action.id)
  if (index === -1 || (action.type === 'select-pane' && active === action.id)) return state
  if (action.type === 'select-pane') return { ...state, active: action.id }
  return {
    ...state,
    panes: panes.filter((id) => id !== action.id),
    active: active === action.id ? (panes[index + 1] ?? panes[index - 1]) : active,
  }
}

// Use a conservative literal subset common to the supported interactive shells.
// Paths outside this subset can still be sent with a manually quoted sz command.
export function transferDownloadCommand(paths: string, windowed = false): string | undefined {
  const names = paths.split(/\r?\n/).map((path) => path.trim()).filter(Boolean)
  if (names.length === 0 || names.length > 100 || names.some((path) => !/^[\p{L}\p{N}\p{M}_.:/ -]+$/u.test(path) || path.endsWith('/'))) return
  return `sz${windowed ? ' -w 262144' : ''} -- ${names.map((path) => `"${path}"`).join(' ')}`
}
