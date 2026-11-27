import { API } from '@/config/endpoints'
import type { HealthyzRes, VersionRes } from '@/types/api'
import { requestData } from './http'
import { stream } from './socket'

export function fetchVersion(): Promise<VersionRes> {
  return requestData<VersionRes>(API.version())
}

export function fetchHealthyz(): Promise<HealthyzRes> {
  return requestData<HealthyzRes>(API.healthyz())
}

export interface SystemDisk {
  device: string
  mountpoint: string
  fs_type: string
  total_bytes: number | null
  used_bytes: number | null
  used_percent: number | null
}

export interface SystemInfo {
  pool_size: number
  cpu_cores: number | null
  cpu_usage_percent: number | null
  memory_total_bytes: number | null
  memory_used_percent: number | null
  disks: SystemDisk[] | null
}

/**
 * 订阅本机资源指标。
 *
 * 走实时连接 /api/v1/ws 的 system 主题，后端每 3 秒采样一次，变化才推。
 * 采样失败时帧内没有 data 而带 message，用它提示用户即可。
 */
export function subscribeSystemInfo(
  onInfo: (info: SystemInfo) => void,
  onUnavailable: (message: string) => void,
): () => void {
  const subscription = stream().subscribe('system', undefined, {
    onUpdate: (frame) => {
      if (!frame.data) {
        onUnavailable(frame.message || '暂时无法读取系统状态')
        return
      }
      onInfo(frame.data as SystemInfo)
    },
    onError: (message) => onUnavailable(message),
    onClose: () => onUnavailable('系统信息推送已断开，正在重连…'),
  })
  return () => subscription.close()
}

/**
 * 调整工作池大小。
 *
 * 走实时连接的 system.update 调用；缩容需要等待退役 worker 完成当前任务，
 * 可能远超默认的 30 秒，因此这里不设调用超时。
 */
export function updateWorkerPool(size: number): Promise<void> {
  return stream().call<void>('system.update', { size }, 0)
}
