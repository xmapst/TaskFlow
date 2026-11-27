/** 管理 tus 上传队列的并发、暂停和恢复。上传前须指定目标任务。 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { UploadTask, type UploadTaskState, type UploaderConfig } from '@/api/upload'

export interface UploadQueueConfig {
  chunkSizeMB: number
  parallelUploads: number
  /** 同时上传的文件数 */
  concurrency: number
}

export const DEFAULT_UPLOAD_CONFIG: UploadQueueConfig = {
  chunkSizeMB: 8,
  parallelUploads: 4,
  concurrency: 3,
}

export interface UploadQueueResult {
  items: UploadTaskState[]
  config: UploadQueueConfig
  setConfig: (patch: Partial<UploadQueueConfig>) => void
  add: (files: readonly File[]) => void
  start: (id: string) => void
  pause: (id: string) => void
  resume: (id: string) => void
  remove: (id: string) => void
  startAll: () => void
  pauseAll: () => void
  /** 清空队列，同时终止进行中的上传 */
  clear: () => void
  /** 移除已完成项 */
  clearFinished: () => void
  stats: { total: number; uploading: number; waiting: number; paused: number; error: number; success: number }
}

let idSeed = 0

export function useUploadQueue(
  taskName: string | null,
  onSuccess?: (state: UploadTaskState) => void,
  onError?: (state: UploadTaskState, error: Error) => void,
): UploadQueueResult {
  const [items, setItems] = useState<UploadTaskState[]>([])
  const [config, setConfigState] = useState<UploadQueueConfig>(DEFAULT_UPLOAD_CONFIG)

  /** 控制器实例不进 state（不可序列化），按 id 索引 */
  const tasks = useRef<Map<string, UploadTask>>(new Map())
  /** 回调放 ref，避免因回调标识变化触发无谓的 effect 重建 */
  const successRef = useRef(onSuccess)
  successRef.current = onSuccess
  const errorRef = useRef(onError)
  errorRef.current = onError
  const configRef = useRef(config)
  configRef.current = config

  const syncState = useCallback((state: UploadTaskState) => {
    setItems((previous) => previous.map((item) => (item.id === state.id ? state : item)))
  }, [])

  /** 在并发额度内启动等待中的任务 */
  const pump = useCallback(() => {
    const running = Array.from(tasks.current.values()).filter((t) => t.status === 'uploading').length
    let slots = configRef.current.concurrency - running
    if (slots <= 0) return

    for (const task of tasks.current.values()) {
      if (slots <= 0) break
      if (task.status !== 'waiting') continue
      slots -= 1
      void task.start()
    }
  }, [])

  const add = useCallback(
    (files: readonly File[]) => {
      if (!taskName) return
      const uploaderConfig: UploaderConfig = {
        taskName,
        chunkSizeMB: configRef.current.chunkSizeMB,
        parallelUploads: configRef.current.parallelUploads,
      }

      const created: UploadTaskState[] = []
      for (const file of files) {
        idSeed += 1
        const id = `u${idSeed}`
        const task = new UploadTask(id, file, uploaderConfig, {
          onStateChange: syncState,
          onSuccess: (state) => {
            syncState(state)
            successRef.current?.(state)
            // 腾出并发额度后立即拉起下一个
            pump()
          },
          onError: (state, error) => {
            syncState(state)
            errorRef.current?.(state, error)
            pump()
          },
        })
        tasks.current.set(id, task)
        created.push({ ...task.state })
      }

      setItems((previous) => previous.concat(created))
      // 入队即自动开始，符合「拖入就上传」的预期
      pump()
    },
    [taskName, syncState, pump],
  )

  const start = useCallback(
    (id: string) => {
      void tasks.current.get(id)?.start()
    },
    [],
  )

  const pause = useCallback((id: string) => {
    tasks.current.get(id)?.pause()
    pump()
  }, [pump])

  const resume = useCallback((id: string) => {
    tasks.current.get(id)?.resume()
  }, [])

  const remove = useCallback(
    (id: string) => {
      const task = tasks.current.get(id)
      if (task) {
        task.abort()
        tasks.current.delete(id)
      }
      setItems((previous) => previous.filter((item) => item.id !== id))
      pump()
    },
    [pump],
  )

  const startAll = useCallback(() => {
    for (const task of tasks.current.values()) {
      if (task.status === 'paused' || task.status === 'error') task.resume()
    }
    pump()
  }, [pump])

  const pauseAll = useCallback(() => {
    for (const task of tasks.current.values()) task.pause()
  }, [])

  const clear = useCallback(() => {
    for (const task of tasks.current.values()) task.abort()
    tasks.current.clear()
    setItems([])
  }, [])

  const clearFinished = useCallback(() => {
    for (const [id, task] of tasks.current) {
      if (task.status === 'success') tasks.current.delete(id)
    }
    setItems((previous) => previous.filter((item) => item.status !== 'success'))
  }, [])

  const setConfig = useCallback((patch: Partial<UploadQueueConfig>) => {
    setConfigState((previous) => ({ ...previous, ...patch }))
  }, [])

  // 组件卸载时终止全部上传，避免后台继续占用带宽与后端临时空间
  useEffect(
    () => () => {
      for (const task of tasks.current.values()) task.abort()
      tasks.current.clear()
    },
    [],
  )

  const stats = {
    total: items.length,
    uploading: items.filter((i) => i.status === 'uploading').length,
    waiting: items.filter((i) => i.status === 'waiting').length,
    paused: items.filter((i) => i.status === 'paused').length,
    error: items.filter((i) => i.status === 'error').length,
    success: items.filter((i) => i.status === 'success').length,
  }

  return {
    items,
    config,
    setConfig,
    add,
    start,
    pause,
    resume,
    remove,
    startAll,
    pauseAll,
    clear,
    clearFinished,
    stats,
  }
}
