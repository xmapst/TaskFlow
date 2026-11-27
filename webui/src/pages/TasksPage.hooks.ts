/**
 * 任务列表页的 store 绑定
 *
 * zustand 的每个 selector 都是一次独立订阅，把它们集中在此处而不是
 * 散落在组件里，一是避免组件顶部堆十几行 useTaskListStore(...)，
 * 二是连接的建立与释放只有一处，不会漏掉 disconnect。
 */

import { useEffect } from 'react'
import { useTaskListStore, computeTaskStats, type ConnectionState } from '@/store/taskList'
import type { TaskRes } from '@/types/api'

export { computeTaskStats }

export interface TaskListBinding {
  tasks: TaskRes[]
  page: number
  size: number
  totalPages: number
  prefix: string
  loading: boolean
  error: string | null
  connection: ConnectionState
  setPage: (page: number) => void
  setSize: (size: number) => void
  setPrefix: (prefix: string) => void
  refetch: () => void
}

export function useTaskListStoreBinding(): TaskListBinding {
  const tasks = useTaskListStore((state) => state.tasks)
  const page = useTaskListStore((state) => state.page)
  const size = useTaskListStore((state) => state.size)
  const totalPages = useTaskListStore((state) => state.totalPages)
  const prefix = useTaskListStore((state) => state.prefix)
  const loading = useTaskListStore((state) => state.loading)
  const error = useTaskListStore((state) => state.error)
  const connection = useTaskListStore((state) => state.connection)

  const connect = useTaskListStore((state) => state.connect)
  const disconnect = useTaskListStore((state) => state.disconnect)
  const setPage = useTaskListStore((state) => state.setPage)
  const setSize = useTaskListStore((state) => state.setSize)
  const setPrefix = useTaskListStore((state) => state.setPrefix)
  const refetch = useTaskListStore((state) => state.refetch)

  // 进入页面即建连，离开即断开：列表连接会持续推送，
  // 停留在其他页面时保持它只会白耗后端 goroutine
  useEffect(() => {
    connect()
    return () => disconnect()
  }, [connect, disconnect])

  return {
    tasks,
    page,
    size,
    totalPages,
    prefix,
    loading,
    error,
    connection,
    setPage,
    setSize,
    setPrefix,
    refetch,
  }
}
