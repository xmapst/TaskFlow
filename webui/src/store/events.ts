/**
 * 事件中心状态
 *
 * 走实时连接 /api/v1/ws 的 event 主题，帧里的 data 形如
 *   { "seq": 42, "text": "<task> <step> Execute" }
 * text 是一行纯文本，seq 是服务端的事件序号，用于断线后从断点续传：
 * 重连时带上 { since: 上一次看到的 seq }，服务端先把断线期间的事件补发过来。
 * 序号出现跳跃说明中间的事件已经过期或被丢弃。事件文本示例：
 *   "<task> Stop"                    —— 任务结束（worker/task.go）
 *   "<task> <step> PreExecution"     —— 步骤准备
 *   "<task> <step> Execute"          —— 步骤开始执行
 *   "<task> <step> Stop"             —— 步骤结束
 *   "<task> <step> error <err>"      —— 步骤报错
 *   "<task> <step> <message>"        —— 步骤执行结果消息
 *
 * 由于任务名与步骤名本身不含空格（后端用正则 [^a-zA-Z\p{Han}0-9\-_.~]
 * 过滤，空格会被剔除），可以安全地按空格切分前两段。
 *
 * 该 store 全局常驻：事件流是应用级信息，切换页面不应中断，
 * 否则会漏掉期间发生的事件。
 */

import { create } from 'zustand'
import { stream, type StreamSubscription } from '@/api/socket'

/** 事件语义分类，决定展示配色 */
export type EventLevel = 'info' | 'success' | 'error' | 'progress'

export interface AppEvent {
  /** 单调递增序号，作为列表 key */
  id: number
  /** 本地接收时间（毫秒） */
  at: number
  /** 涉及的任务名 */
  task: string
  /** 涉及的步骤名；任务级事件为空 */
  step: string
  /** 事件动作或消息主体 */
  action: string
  /** 原始文本，用于复制与排查 */
  raw: string
  level: EventLevel
}

/** 保留的事件条数上限，超出丢弃最旧的 */
const MAX_EVENTS = 500

/** 服务端事件帧 */
interface EventFrame {
  seq?: unknown
  text?: unknown
}

/** 从事件文本推断语义分类 */
function classify(action: string): EventLevel {
  const lower = action.toLowerCase()
  if (lower.startsWith('error') || lower.includes('failed')) return 'error'
  if (lower === 'stop' || lower.includes('success')) return 'success'
  if (lower === 'execute' || lower === 'preexecution' || lower === 'postexecution') return 'progress'
  return 'info'
}

let sequence = 0

/** 解析后端事件文本 */
export function parseEvent(raw: string): AppEvent {
  const text = raw.trim()
  const parts = text.split(/\s+/)
  const task = parts[0] ?? ''
  // 只有两段时是任务级事件（如 "<task> Stop"），第二段即动作
  const isTaskLevel = parts.length <= 2
  const step = isTaskLevel ? '' : (parts[1] ?? '')
  const action = isTaskLevel ? (parts[1] ?? '') : parts.slice(2).join(' ')

  sequence += 1
  return {
    id: sequence,
    at: Date.now(),
    task,
    step,
    action,
    raw: text,
    level: classify(action),
  }
}

interface EventState {
  events: AppEvent[]
  live: boolean
  /** 是否暂停接收（便于用户静态查看列表） */
  paused: boolean
  /** 未读计数，用于侧栏角标 */
  unread: number

  connect: () => void
  disconnect: () => void
  setPaused: (paused: boolean) => void
  clear: () => void
  markRead: () => void
}

/** 模块级持有订阅：store 只存可序列化状态，连接实例不进 state */
let subscription: StreamSubscription | null = null

/** 已收到的最大事件序号，重连时作为续传游标 */
let lastSeq = 0

/** 订阅参数：还没有游标时从新事件开始，有了就补齐断线期间的缺口 */
function cursor(): { since: number } | undefined {
  return lastSeq > 0 ? { since: lastSeq } : undefined
}

export const useEventStore = create<EventState>((set, get) => ({
  events: [],
  live: false,
  paused: false,
  unread: 0,

  connect: () => {
    if (subscription) return
    subscription = stream().subscribe('event', cursor(), {
      onOpen: () => set({ live: true }),
      onUpdate: (frame) => {
        const payload = frame.data as EventFrame | null | undefined
        // 游标先推进：暂停只影响是否入列表，不该让重连去补已经看过的事件
        if (typeof payload?.seq === 'number' && payload.seq > lastSeq) lastSeq = payload.seq
        if (get().paused) return
        const raw = typeof payload?.text === 'string' ? payload.text : ''
        if (!raw) return
        const event = parseEvent(raw)
        set((state) => {
          const next = [event, ...state.events]
          return {
            events: next.length > MAX_EVENTS ? next.slice(0, MAX_EVENTS) : next,
            unread: state.unread + 1,
          }
        })
      },
      // 事件流按设计会持续推送，中断只代表连接掉了：重连时 onOpen 会把它点亮。
      // 顺手把游标写回订阅参数，重连补发时才能从断点续上。
      onClose: () => {
        subscription?.update(cursor())
        set({ live: false })
      },
      onError: () => set({ live: false }),
    })
  },

  disconnect: () => {
    subscription?.close()
    subscription = null
    set({ live: false })
  },

  setPaused: (paused) => set({ paused }),
  clear: () => set({ events: [], unread: 0 }),
  markRead: () => set({ unread: 0 }),
}))
