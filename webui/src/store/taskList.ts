/**
 * 任务列表状态
 *
 * 数据来源是实时连接 /api/v1/ws 的 tasks 主题：后端每 300ms 比对一次，
 * 变化时才下发完整列表；无变化不发帧，连接由 30s 的 Ping 保活。
 *
 * 翻页与刷新都通过 subscription.update 换参数：沿用同一个 id，服务端替换
 * 会话并立刻推一份新快照，连接不中断。
 * params 的字段名与后端 SPageReq 一致：page/size/prefix。
 */

import { create } from 'zustand'
import type { TaskListRes, TaskRes } from '@/types/api'
import { stream, type StreamSubscription } from '@/api/socket'
import { normalizeTaskList } from '@/api/task'

export type ConnectionState = 'idle' | 'connecting' | 'live' | 'retrying' | 'offline'

interface TaskListState {
  tasks: TaskRes[]
  /** 当前页码 */
  page: number
  /** 每页条数 */
  size: number
  /** 总页数（后端语义） */
  totalPages: number
  /** 名称前缀过滤 */
  prefix: string
  loading: boolean
  error: string | null
  connection: ConnectionState

  connect: () => void
  disconnect: () => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  setPrefix: (prefix: string) => void
  /** 重新取一次快照；实时连接已放弃时重建订阅并重新开始退避 */
  refetch: () => void
}

/** 模块级持有订阅：store 只存可序列化状态，连接实例不进 state */
let subscription: StreamSubscription | null = null

export const useTaskListStore = create<TaskListState>((set, get) => {
  /**
   * 按当前分页条件建立或刷新订阅。
   * 首次建立，之后走 update 换参数：连接不复用也不中断，只是服务端换了会话。
   */
  const resubscribe = (): void => {
    const { page, size, prefix } = get()
    if (subscription) {
      subscription.update({ page, size, prefix })
      return
    }
    subscription = stream().subscribe('tasks', { page, size, prefix }, {
      onOpen: () => set({ connection: 'live', error: null }),
      onUpdate: (frame) => {
        const current = get()
        const list = (frame.data as TaskListRes | undefined) ?? null
        const data = normalizeTaskList(list, { page: current.page, size: current.size })
        // 翻页后，先前页码的推送可能仍在途中
        if (data.current !== current.page || data.size !== current.size) return
        set({
          tasks: data.tasks,
          totalPages: data.totalPages,
          loading: false,
          error: null,
          connection: 'live',
        })
      },
      onError: (message) => set({ connection: 'offline', loading: false, error: message }),
      onClose: () => set({ connection: 'retrying' }),
    })
  }

  /** 翻页 / 改过滤条件后重新订阅；尚未 connect 时不该由翻页动作去建连接。 */
  const pushQuery = (): void => {
    if (!subscription) return
    resubscribe()
  }

  return {
    tasks: [],
    page: 1,
    size: 20,
    totalPages: 0,
    prefix: '',
    loading: true,
    error: null,
    connection: 'idle',

    connect: () => {
      if (subscription) return
      set({ connection: 'connecting', loading: true })
      resubscribe()
    },

    disconnect: () => {
      subscription?.close()
      subscription = null
      set({ connection: 'idle' })
    },

    setPage: (page) => {
      set({ page })
      pushQuery()
    },

    setSize: (size) => {
      // 改变每页条数后回到首页，避免停留在不存在的页码
      set({ size, page: 1 })
      pushQuery()
    },

    setPrefix: (prefix) => {
      set({ prefix, page: 1 })
      pushQuery()
    },

    refetch: () => {
      set({ loading: true, error: null })
      // 退避耗尽后订阅已放弃，重建一个才能重新开始重连
      if (get().connection === 'offline') {
        subscription?.close()
        subscription = null
      }
      resubscribe()
    },
  }
})

/**
 * 派生统计。
 * 只统计当前页：后端未提供全局状态计数接口，跨页统计会误导用户，
 * 因此展示时需明确标注「当前页」。
 */
export interface TaskStats {
  total: number
  running: number
  pending: number
  paused: number
  failed: number
  stopped: number
  skipped: number
}

export function computeTaskStats(tasks: readonly TaskRes[]): TaskStats {
  const stats: TaskStats = {
    total: tasks.length,
    running: 0,
    pending: 0,
    paused: 0,
    failed: 0,
    stopped: 0,
    skipped: 0,
  }
  for (const task of tasks) {
    switch (task.state) {
      case 'running':
        stats.running += 1
        break
      case 'pending':
        stats.pending += 1
        break
      case 'paused':
        stats.paused += 1
        break
      case 'failed':
        stats.failed += 1
        break
      case 'stopped':
        stats.stopped += 1
        break
      case 'skipped':
        stats.skipped += 1
        break
      default:
        break
    }
  }
  return stats
}
