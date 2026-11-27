/**
 * 执行状态与响应码 —— 前端唯一真源
 *
 * 状态取值严格对应后端 internal/storage/models/base.go 的 StateMap：
 *   stopped / running / failed / unknown / pending / paused / skipped
 * 后端不存在 success、timeout、canceled、killed 等状态，切勿再引入。
 *
 * 关于 stopped 的语义：后端 StateStopped = iota 首项，Go 侧注释为「成功」，
 * 后端将其映射为 CodeSuccess。因此 stopped 表示
 * 「执行完成且成功」，而非字面意义的「被停止」，展示文案须为「已完成」。
 */

import type { ExecState, ManageAction, ResponseCode } from '@/types/api'

export const STATE = {
  STOPPED: 'stopped',
  RUNNING: 'running',
  FAILED: 'failed',
  UNKNOWN: 'unknown',
  PENDING: 'pending',
  PAUSED: 'paused',
  SKIPPED: 'skipped',
} as const satisfies Record<string, ExecState>

/** 后端 api.Code，见 api/response.go。 */
export const CODE = {
  SUCCESS: 0,
  RUNNING: 1001,
  FAILED: 1002,
  NO_DATA: 1003,
  PENDING: 1004,
  PAUSED: 1005,
  SKIPPED: 1006,
} as const satisfies Record<string, ResponseCode>

/** 仅 code 0 代表业务成功 */
export const isSuccessCode = (code: number): boolean => code === CODE.SUCCESS

/**
 * 详情类接口会把执行状态编码进 code 返回，同时 data 完整。
 * 这类 code 属正常业务态，请求层须放行，否则运行中或失败的任务无法查看。
 */
export const STATE_CODES: readonly ResponseCode[] = [
  CODE.RUNNING,
  CODE.FAILED,
  CODE.PENDING,
  CODE.PAUSED,
  CODE.SKIPPED,
]

/** Arco Tag / Badge 的色彩枚举 */
export type ArcoStatus = 'success' | 'processing' | 'warning' | 'error' | 'default'

export interface StateMeta {
  /** 中文展示文案 */
  label: string
  /** Arco 组件的语义色 */
  status: ArcoStatus
  /** CSS 变量后缀，对应 tokens.scss 的 --c-state-<token> */
  token: string
  /** 列表默认排序权重，越小越靠前 */
  order: number
  /** 是否施加呼吸动效（仅执行中） */
  pulse: boolean
}

/**
 * 状态元数据单一真源：文案、色彩、排序集中于此。
 * 新增状态只需补一行，全站展示自动跟随。
 */
export const STATE_META: Record<ExecState, StateMeta> = {
  running: { label: '执行中', status: 'processing', token: 'running', order: 1, pulse: true },
  pending: { label: '等待中', status: 'warning', token: 'pending', order: 2, pulse: false },
  paused: { label: '已挂起', status: 'warning', token: 'paused', order: 3, pulse: false },
  failed: { label: '已失败', status: 'error', token: 'failed', order: 4, pulse: false },
  stopped: { label: '已完成', status: 'success', token: 'stopped', order: 5, pulse: false },
  skipped: { label: '已跳过', status: 'default', token: 'skipped', order: 6, pulse: false },
  unknown: { label: '未知', status: 'default', token: 'unknown', order: 7, pulse: false },
}

const UNKNOWN_META = STATE_META.unknown

/** 取状态元数据。后端理论上不会返回表外取值，兜底到 unknown 避免渲染空白 */
export function getStateMeta(state: string | undefined): StateMeta {
  if (!state) return UNKNOWN_META
  return STATE_META[state as ExecState] ?? UNKNOWN_META
}

export const getStateLabel = (state: string | undefined): string => getStateMeta(state).label
export const getStateToken = (state: string | undefined): string => getStateMeta(state).token
export const getStateOrder = (state: string | undefined): number => getStateMeta(state).order

/** 仍在调度中，可执行管理操作 */
export const ACTIVE_STATES: readonly ExecState[] = ['running', 'pending', 'paused']
/** 已进入终态，不再接受管理操作 */
export const TERMINAL_STATES: readonly ExecState[] = ['stopped', 'failed', 'skipped']

export const isActiveState = (state: string | undefined): boolean =>
  ACTIVE_STATES.includes(state as ExecState)
export const isTerminalState = (state: string | undefined): boolean =>
  TERMINAL_STATES.includes(state as ExecState)
export const isRunningState = (state: string | undefined): boolean => state === STATE.RUNNING
export const isFailedState = (state: string | undefined): boolean => state === STATE.FAILED
export const isSuccessState = (state: string | undefined): boolean => state === STATE.STOPPED

export interface ActionMeta {
  label: string
  /** 允许执行该操作的状态 */
  allowedStates: readonly ExecState[]
  /** 是否为破坏性操作，影响按钮样式 */
  danger: boolean
  /** 需二次确认时的提示文案，null 表示直接执行 */
  confirm: string | null
}

/**
 * 管理操作的前置条件，与后端 worker 判断保持一致：
 *  - kill：任务或步骤仍在调度中即可（worker 会 Stop 并置为 failed）；
 *  - pause：后端在 *state == StateRunning 时直接返回错误
 *    「step is running」，故仅 pending 可挂起；
 *  - resume：仅 paused 可恢复（CAS 从 1 到 0）。
 */
export const ACTION_META: Record<ManageAction, ActionMeta> = {
  kill: {
    label: '强制终止',
    allowedStates: ACTIVE_STATES,
    danger: true,
    confirm: '终止后将标记为失败且无法恢复，确认继续？',
  },
  pause: {
    label: '挂起',
    allowedStates: ['pending'],
    danger: false,
    confirm: null,
  },
  resume: {
    label: '恢复执行',
    allowedStates: ['paused'],
    danger: false,
    confirm: null,
  },
}

export const MANAGE_ACTIONS: readonly ManageAction[] = ['kill', 'pause', 'resume']

/** 判断某状态下能否执行指定操作 */
export function canRunAction(state: string | undefined, action: ManageAction): boolean {
  return ACTION_META[action].allowedStates.includes(state as ExecState)
}

/** 列出某状态下全部可用操作，用于渲染操作菜单 */
export function availableActions(state: string | undefined): ManageAction[] {
  return MANAGE_ACTIONS.filter((action) => canRunAction(state, action))
}

/**
 * 退出码语义。后端 api.ExecCode：0 成功，非 0 失败
 * （负值为内部约定，如 ExecCodeKilled）。
 */
export function getExitCodeLevel(code: number | undefined): 'success' | 'danger' | 'muted' {
  if (code === undefined || code === null) return 'muted'
  return code === 0 ? 'success' : 'danger'
}

/** 步骤类型 → CodeMirror 语言标识。解释器来自步骤 options.shell。 */
export function scriptLanguage(kind: string | undefined, shell?: string): 'python' | 'shell' | 'powershell' | 'cmd' | 'javascript' | 'yaml' | 'text' {
  const type = (kind ?? '').trim().toLowerCase()
  if (type === 'approval' || type === 'mkdir' || type === 'touch') return 'yaml'
  const explicit = (shell ?? '').trim().toLowerCase()
  // container 同样使用 options.shell，缺省为 sh。
  const key = type === 'container' ? explicit || 'sh' : explicit || type
  if (/^(?:python|py)[23]?$/.test(key)) return 'python'
  if (['sh', 'shell', 'bash', 'zsh', 'dash', 'ksh'].includes(key)) return 'shell'
  if (['powershell', 'pwsh', 'ps', 'ps1'].includes(key)) return 'powershell'
  if (key === 'cmd' || key === 'batch') return 'cmd'
  if (['js', 'javascript', 'node'].includes(key)) return 'javascript'
  return 'text'
}
