/**
 * 步骤日志实时订阅
 *
 * 走实时连接 /api/v1/ws 的 step-log 主题。后端行为要点：
 *  - running 状态下按 latestLine 增量推送，每 300ms 一轮，无新行不发帧；
 *  - pending / paused / unknown 各只推一次占位说明；
 *  - 步骤进入终态后推完最后一批即结束订阅（done）；
 *  - 日志行自带 line 行号，可据此去重 —— 重连后订阅会重建、后端从头推，
 *    不去重会出现整段重复。
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import type { StepLogRes } from '@/types/api'
import { stream } from '@/api/socket'

/** 单个步骤保留的最大行数。超出后丢弃最旧的行，防止长任务耗尽内存 */
const MAX_LINES = 20_000

export interface StepLogResult {
  lines: StepLogRes[]
  live: boolean
  /** 服务端已推完 */
  finished: boolean
  /** 是否被截断（达到行数上限） */
  truncated: boolean
  clear: () => void
}

export function useStepLog(taskName: string | null, stepName: string | null): StepLogResult {
  const [lines, setLines] = useState<StepLogRes[]>([])
  const [live, setLive] = useState(false)
  const [finished, setFinished] = useState(false)
  const [truncated, setTruncated] = useState(false)

  /** 已收录的行号，用于跨帧与重连去重 */
  const seenLines = useRef<Set<number>>(new Set())
  /** 批量缓冲：后端每 300ms 一帧，React 逐帧 setState 会造成大量重渲染 */
  const buffer = useRef<StepLogRes[]>([])
  const flushTimer = useRef<number | null>(null)

  const flush = useCallback(() => {
    flushTimer.current = null
    if (buffer.current.length === 0) return
    const incoming = buffer.current
    buffer.current = []

    setLines((previous) => {
      const merged = previous.concat(incoming)
      if (merged.length <= MAX_LINES) return merged
      setTruncated(true)
      return merged.slice(merged.length - MAX_LINES)
    })
  }, [])

  const scheduleFlush = useCallback(() => {
    if (flushTimer.current !== null) return
    // 120ms 合批：既保证观感上的实时，又把重渲染次数压到每秒 8 次左右
    flushTimer.current = window.setTimeout(flush, 120)
  }, [flush])

  const clear = useCallback(() => {
    buffer.current = []
    seenLines.current.clear()
    setLines([])
    setTruncated(false)
  }, [])

  useEffect(() => {
    if (!taskName || !stepName) {
      setLines([])
      setLive(false)
      setFinished(false)
      return
    }

    // 切换步骤时重置全部累积状态
    buffer.current = []
    seenLines.current = new Set()
    setLines([])
    setTruncated(false)
    setFinished(false)

    let disposed = false

    const subscription = stream().subscribe(
      'step-log',
      { task: taskName, step: stepName },
      {
        onOpen: () => {
          if (!disposed) setLive(true)
        },
        onUpdate: (frame) => {
          if (disposed) return
          const incoming = frame.data as StepLogRes[] | undefined
          if (!incoming || incoming.length === 0) return

          for (const line of incoming) {
            // 行号去重：重连后订阅重建、后端从头推送，此处过滤已收录的行
            if (seenLines.current.has(line.line)) continue
            seenLines.current.add(line.line)
            buffer.current.push(line)
          }
          scheduleFlush()
        },
        onDone: () => {
          if (disposed) return
          setLive(false)
          // 先冲刷缓冲，确保最后一批日志不丢
          flush()
          setFinished(true)
        },
        onError: () => {
          // 交由重连机制处理，行号去重保证重连后不重复
        },
        onClose: () => {
          if (!disposed) setLive(false)
        },
      },
    )

    return () => {
      disposed = true
      if (flushTimer.current !== null) {
        window.clearTimeout(flushTimer.current)
        flushTimer.current = null
      }
      subscription.close()
      setLive(false)
    }
  }, [taskName, stepName, flush, scheduleFlush])

  return { lines, live, finished, truncated, clear }
}

/** 把日志行拼成纯文本，用于复制与导出 */
export function linesToText(lines: readonly StepLogRes[]): string {
  return lines.map((line) => line.content).join('\n')
}
