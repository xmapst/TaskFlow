/**
 * 步骤详情实时订阅
 *
 * 走实时连接 /api/v1/ws 的 step 主题，与 task 主题同构：每秒比对、
 * 变化才推，步骤进入终态后结束订阅（done）。
 *
 * 详情比列表项多出 kind / content / env / timeout / retryPolicy，是步骤抽屉里「配置」区域的唯一数据来源。
 */

import { useEffect, useState } from 'react'
import type { StepRes } from '@/types/api'
import { stream } from '@/api/socket'

export interface StepDetailResult {
  step: StepRes | null
  code: number | null
  message: string
  loading: boolean
  error: string | null
  live: boolean
}

export function useStepDetail(
  taskName: string | null,
  stepName: string | null,
): StepDetailResult {
  const [step, setStep] = useState<StepRes | null>(null)
  const [code, setCode] = useState<number | null>(null)
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [live, setLive] = useState(false)

  useEffect(() => {
    if (!taskName || !stepName) {
      setStep(null)
      setCode(null)
      setMessage('')
      setError(null)
      setLive(false)
      return
    }

    let disposed = false
    // 订阅后服务端立刻推首帧快照，不必先走 HTTP 取一次
    setStep(null)
    setCode(null)
    setMessage('')
    setLoading(true)
    setError(null)

    const subscription = stream().subscribe('step', { task: taskName, step: stepName }, {
      onOpen: () => {
        if (!disposed) setLive(true)
      },
      onUpdate: (frame) => {
        if (disposed) return
        if (frame.data) setStep(frame.data as StepRes)
        setCode(frame.code ?? null)
        setMessage(frame.message ?? '')
        setLoading(false)
        setError(null)
      },
      onDone: () => {
        // 步骤进入终态，不会再有新帧
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
  }, [taskName, stepName])

  return { step, code, message, loading, error, live }
}
