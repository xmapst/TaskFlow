/**
 * 工作目录浏览
 *
 * 工作目录在运行任务或首次上传文件时创建
 * （internal/worker/task.go 的 workspace 字段），
 * 未执行的任务访问会返回 code=1003（no data）。
 * 这属于正常情形而非错误，需与真实故障区分开来展示。
 */

import { useCallback, useEffect, useState } from 'react'
import type { FileRes } from '@/types/api'
import { listWorkspace, normalizePath, sortEntries } from '@/api/workspace'
import { ApiError, isCanceled, toErrorMessage } from '@/api/http'

export interface WorkspaceResult {
  entries: FileRes[]
  /** 当前所在相对路径，空串为根 */
  path: string
  loading: boolean
  error: string | null
  /** 工作目录尚未创建（任务未执行过） */
  absent: boolean
  /** 进入子目录 */
  open: (path: string) => void
  /** 返回上一级 */
  up: () => void
  refresh: () => void
}

export function useWorkspace(taskName: string | null): WorkspaceResult {
  const [entries, setEntries] = useState<FileRes[]>([])
  const [path, setPath] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [absent, setAbsent] = useState(false)
  /** 自增触发器，用于强制重新拉取（删除或上传后） */
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    if (!taskName) {
      setEntries([])
      setError(null)
      setAbsent(false)
      return
    }

    const controller = new AbortController()
    let disposed = false
    setLoading(true)
    setError(null)
    setAbsent(false)

    void (async () => {
      try {
        const result = await listWorkspace(taskName, path, controller.signal)
        if (disposed) return
        setEntries(sortEntries(result.files))
        setLoading(false)
      } catch (err) {
        if (disposed || isCanceled(err)) return
        setLoading(false)
        setEntries([])
        // no data 表示工作目录还不存在，不是故障
        if (err instanceof ApiError && err.isNotFound) {
          setAbsent(true)
          return
        }
        setError(toErrorMessage(err, '读取工作目录失败'))
      }
    })()

    return () => {
      disposed = true
      controller.abort()
    }
  }, [taskName, path, nonce])

  // 切换任务时回到根目录，避免带着上个任务的路径
  useEffect(() => {
    setPath('')
  }, [taskName])

  const open = useCallback((next: string) => {
    setPath(normalizePath(next))
  }, [])

  const up = useCallback(() => {
    setPath((current) => {
      const clean = normalizePath(current)
      if (!clean) return ''
      const index = clean.lastIndexOf('/')
      return index === -1 ? '' : clean.slice(0, index)
    })
  }, [])

  const refresh = useCallback(() => {
    setNonce((n) => n + 1)
  }, [])

  return { entries, path, loading, error, absent, open, up, refresh }
}
