/**
 * 任务详情实时订阅
 *
 * 走实时连接 /api/v1/ws 的 task 主题：后端每秒比对一次，仅在变化时推送，
 * 任务进入终态后结束订阅（done）。
 *
 * 「根据步骤状态猜测任务状态是否变化」的启发式（smartUpdateTaskInfo）
 * 来决定何时重新请求，既复杂又会漏更新。既然后端原生支持推送，
 * 就没有理由在前端重建一套猜测逻辑。
 */

import { useEffect, useState } from 'react'
import type { TaskRes } from '@/types/api'
import { stream } from '@/api/socket'

export interface TaskDetailResult {
  task: TaskRes | null
  /** 后端返回的状态码，反映当前执行态 */
  code: number | null
  /** 后端聚合消息，含状态计数汇总 */
  message: string
  loading: boolean
  error: string | null
  /** 实时通道是否已连通 */
  live: boolean
}

/**
 * 订阅单个任务的详情。
 * @param taskName 任务名；传 null 表示不订阅（如弹窗未打开）
 */
export function useTaskDetail(taskName: string | null): TaskDetailResult {
  const [task, setTask] = useState<TaskRes | null>(null)
  const [code, setCode] = useState<number | null>(null)
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [live, setLive] = useState(false)

  useEffect(() => {
    if (!taskName) {
      setTask(null)
      setCode(null)
      setMessage('')
      setError(null)
      setLive(false)
      return
    }

    let disposed = false
    // 订阅后服务端立刻推首帧快照，不必先走 HTTP 取一次
    setTask(null)
    setCode(null)
    setMessage('')
    setLoading(true)
    setError(null)

    const subscription = stream().subscribe('task', { task: taskName }, {
      onOpen: () => {
        if (!disposed) setLive(true)
      },
      onUpdate: (frame) => {
        if (disposed) return
        if (frame.data) setTask(frame.data as TaskRes)
        setCode(frame.code ?? null)
        setMessage(frame.message ?? '')
        setLoading(false)
        setError(null)
      },
      onDone: () => {
        // 任务进入终态，不会再有新帧
        if (!disposed) setLive(false)
      },
      onError: (message) => {
        if (!disposed) setError(message)
      },
      onClose: () => {
        if (!disposed) setLive(false)
      },
    })

    return () => {
      disposed = true
      subscription.close()
      setLive(false)
    }
  }, [taskName])

  return { task, code, message, loading, error, live }
}
