/**
 * 版本与服务健康
 *
 * 版本信息只需取一次；健康检查按固定间隔轮询，用于顶栏的连通指示。
 */

import { useEffect, useState } from 'react'
import type { VersionRes } from '@/types/api'
import { fetchHealthyz, fetchVersion } from '@/api/system'

/** 健康检查间隔。30 秒足以反映服务异常，又不会造成额外压力 */
const HEALTH_INTERVAL = 30_000

export interface VersionResult {
  version: VersionRes | null
  /** 服务是否可达 */
  healthy: boolean
}

export function useVersion(): VersionResult {
  const [version, setVersion] = useState<VersionRes | null>(null)
  const [healthy, setHealthy] = useState(true)

  useEffect(() => {
    let disposed = false

    void (async () => {
      try {
        const result = await fetchVersion()
        if (!disposed) setVersion(result)
      } catch {
        // 版本获取失败不影响主流程，页脚留空即可
      }
    })()

    const check = async (): Promise<void> => {
      try {
        await fetchHealthyz()
        if (!disposed) setHealthy(true)
      } catch {
        if (!disposed) setHealthy(false)
      }
    }

    void check()
    const timer = window.setInterval(() => void check(), HEALTH_INTERVAL)

    return () => {
      disposed = true
      window.clearInterval(timer)
    }
  }, [])

  return { version, healthy }
}
