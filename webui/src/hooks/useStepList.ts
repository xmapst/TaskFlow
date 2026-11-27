/**
 * 步骤列表实时订阅
 *
 * 走实时连接 /api/v1/ws 的 steps 主题：后端每 500ms 比对一次，变化才推。
 *
 * 任务进入 success / failed / skipped 终态后，服务端推完最后一帧就结束
 * 订阅（done）。这是「正常结束」，连接层不会重连，也不会残留服务端 goroutine。
 */

import { useEffect, useState } from 'react'
import type { StepRes } from '@/types/api'
import { stream } from '@/api/socket'
import { getStateOrder } from '@/constants/state'

export interface StepListResult {
  steps: StepRes[]
  /** 所属任务的状态码 */
  code: number | null
  /** 后端聚合消息，含各状态计数 */
  message: string
  loading: boolean
  error: string | null
  live: boolean
  /** 服务端已推完（任务进入终态） */
  finished: boolean
}

export function useStepList(taskName: string | null): StepListResult {
  const [steps, setSteps] = useState<StepRes[]>([])
  const [code, setCode] = useState<number | null>(null)
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [live, setLive] = useState(false)
  const [finished, setFinished] = useState(false)

  useEffect(() => {
    if (!taskName) {
      setSteps([])
      setCode(null)
      setMessage('')
      setError(null)
      setLive(false)
      setFinished(false)
      return
    }

    let disposed = false
    // 订阅后服务端立刻推首帧快照，DAG 区域不必先走 HTTP
    setSteps([])
    setCode(null)
    setMessage('')
    setLoading(true)
    setError(null)
    setFinished(false)

    const subscription = stream().subscribe('steps', { task: taskName }, {
      onOpen: () => {
        if (!disposed) setLive(true)
      },
      onUpdate: (frame) => {
        if (disposed) return
        setSteps((frame.data as StepRes[] | undefined) ?? [])
        setCode(frame.code ?? null)
        setMessage(frame.message ?? '')
        setLoading(false)
        setError(null)
      },
      onDone: () => {
        if (disposed) return
        setLive(false)
        // 任务已进入终态，服务端推完即止
        setFinished(true)
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

  return { steps, code, message, loading, error, live, finished }
}

/**
 * 步骤排序。
 *
 * 后端 sortTasksByDepth 已按依赖深度做过拓扑排序，遍历 map 的顺序
 * 在 Go 里是随机的，因此同层步骤的相对次序每次推送都可能不同 ——
 * 直接渲染会导致列表抖动。这里在保留后端拓扑序的基础上做稳定化：
 * 以后端给出的次序为主序，仅对相邻的同依赖层项按状态与名称稳定排序。
 */
export function sortStepsStable(steps: readonly StepRes[]): StepRes[] {
  // 计算每个步骤的依赖深度，作为主排序键
  const depthOf = new Map<string, number>()
  const byName = new Map(steps.map((step) => [step.name, step]))

  const resolveDepth = (name: string, guard: Set<string>): number => {
    const cached = depthOf.get(name)
    if (cached !== undefined) return cached
    // 后端已做环检测，这里的 guard 仅防御异常数据导致的栈溢出
    if (guard.has(name)) return 0
    guard.add(name)

    const step = byName.get(name)
    const depends = (step?.routing?.edges ?? []).map((edge) => edge.step)
    let depth = 0
    for (const dep of depends) {
      if (!byName.has(dep)) continue
      depth = Math.max(depth, resolveDepth(dep, guard) + 1)
    }
    depthOf.set(name, depth)
    return depth
  }

  for (const step of steps) resolveDepth(step.name, new Set())

  return [...steps].sort((a, b) => {
    const depthDiff = (depthOf.get(a.name) ?? 0) - (depthOf.get(b.name) ?? 0)
    if (depthDiff !== 0) return depthDiff
    // 同层内：执行中与失败的优先，便于一眼定位问题
    const orderDiff = getStateOrder(a.state) - getStateOrder(b.state)
    if (orderDiff !== 0) return orderDiff
    return a.name.localeCompare(b.name, 'zh-CN', { numeric: true })
  })
}

export interface StepStats {
  total: number
  running: number
  pending: number
  paused: number
  failed: number
  stopped: number
  skipped: number
  /** 已进入终态的步骤占比，0-100 */
  progress: number
}

export function computeStepStats(steps: readonly StepRes[]): StepStats {
  const stats: StepStats = {
    total: steps.length,
    running: 0,
    pending: 0,
    paused: 0,
    failed: 0,
    stopped: 0,
    skipped: 0,
    progress: 0,
  }
  for (const step of steps) {
    switch (step.state) {
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
  const settled = stats.stopped + stats.failed + stats.skipped
  stats.progress = stats.total > 0 ? Math.round((settled / stats.total) * 100) : 0
  return stats
}
