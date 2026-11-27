/**
 * 任务接口
 */

import type { ManageAction, TaskCreateRes, TaskListRes, TaskRes } from '@/types/api'
import { stream } from './socket'

/** 归一化后的任务列表，页码与数组均已补齐，免去各处判空 */
export interface TaskListPage {
  current: number
  size: number
  /** 总页数（后端语义），非总记录数 */
  totalPages: number
  tasks: TaskRes[]
}

/** 把可能为 null 的列表响应补齐成稳定结构 */
export function normalizeTaskList(
  data: TaskListRes | null,
  fallback: { page: number; size: number },
): TaskListPage {
  return {
    current: data?.page?.current ?? fallback.page,
    size: data?.page?.size ?? fallback.size,
    totalPages: data?.page?.total ?? 0,
    // 任务列表为空时，响应可能不含 data。
    tasks: data?.tasks ?? [],
  }
}

/**
 * 创建任务。
 *
 * 走实时连接的 task.create 调用：任务定义按原文传输，由服务端解析，
 * 前端不必再区分 JSON 与 YAML 的请求体形状。两者的时长字段均接受
 * Go 时长字符串（例如 "2m"）或整数纳秒。
 */
export function createTask(text: string, format: 'yaml' | 'json' = 'yaml'): Promise<TaskCreateRes> {
  return stream().call<TaskCreateRes>('task.create', { format, content: text })
}

/**
 * 管理任务：终止 / 挂起 / 恢复。
 *
 * 走实时连接的 task.manage 调用，与 create 走同一条连接。
 *
 * @param duration Go duration 字面量如 1m、30s。仅 pause 有意义：
 *        留空需手工恢复，传值则到期由后端自动恢复
 *        （worker 用 context.WithTimeout 实现）。
 */
export function manageTask(
  taskName: string,
  action: ManageAction,
  duration?: string,
): Promise<void> {
  return stream().call<void>('task.manage', {
    task: taskName,
    action,
    duration: duration ?? '',
  })
}

/** 删除任务及其数据库记录 */
export function deleteTask(taskName: string): Promise<void> {
  return stream().call<void>('task.delete', { task: taskName })
}

/**
 * 导出任务配置。
 * 后端把 STaskReq 序列化为 YAML 文本作为调用结果返回。
 */
export function dumpTask(taskName: string): Promise<string> {
  return stream().call<string>('task.dump', { task: taskName })
}
