/** 工作区列表、下载与删除接口。目录返回 JSON，文件以附件响应直接下载。 */

import type { ApiResponse, FileListRes, FileRes } from '@/types/api'
import { API } from '@/config/endpoints'
import { appendQuery, request } from './http'

/** 归一化路径：去掉首尾斜杠。空串表示任务工作目录根 */
export function normalizePath(path: string | undefined): string {
  return (path ?? '').replace(/^\/+|\/+$/g, '')
}

export interface WorkspaceListing {
  total: number
  files: FileRes[]
}

/**
 * 列出目录内容。
 *
 * 任务工作目录在运行任务或首次上传文件时创建
 * （见 internal/worker/task.go 的 workspace 字段），
 * 未执行的任务访问会得到 code=1003（no data），
 * 调用方应视为「暂无工作目录」而非错误。
 */
export async function listWorkspace(
  taskName: string,
  path = '',
  signal?: AbortSignal,
): Promise<WorkspaceListing> {
  const payload = await request<FileListRes | null>(API.workspace(taskName), {
    query: { path: normalizePath(path) },
    ...(signal ? { signal } : {}),
  })
  return {
    total: payload.data?.total ?? 0,
    // 空目录时后端 files 为 null
    files: payload.data?.files ?? [],
  }
}

/**
 * 构造文件下载地址。
 * 不走 fetch + blob：直接交给浏览器可保留后端设置的
 * Content-Disposition 文件名，并支持大文件流式下载。
 */
export function buildDownloadUrl(taskName: string, path: string): string {
  return appendQuery(API.workspace(taskName), { path: normalizePath(path) })
}

/** 触发浏览器下载 */
export function downloadWorkspaceFile(taskName: string, path: string): void {
  const link = document.createElement('a')
  link.href = buildDownloadUrl(taskName, path)
  // 不设 download 属性，让后端的 Content-Disposition 决定文件名
  link.rel = 'noopener'
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
}

/**
 * 批量删除文件或目录（后端用 os.RemoveAll，对目录是递归删除）。
 *
 * 路径以重复的 path 参数发送。后端会先整体校验再逐项删除：
 * 路径不合法时一个都不删，删除过程中个别失败不会中断其余项，
 * 失败项汇总在错误消息里返回。
 */
export function deleteWorkspacePaths(
  taskName: string,
  paths: readonly string[],
): Promise<ApiResponse<unknown>> {
  return request(API.workspace(taskName), {
    method: 'DELETE',
    query: { path: paths.map(normalizePath) },
  })
}

/** 删除单个文件或目录，是批量接口的一元用法。 */
export function deleteWorkspacePath(taskName: string, path: string): Promise<ApiResponse<unknown>> {
  return deleteWorkspacePaths(taskName, [path])
}

/** 构造 multipart 工作区上传请求；当前服务端路由仅支持 GET 和 DELETE。 */
export function uploadWorkspaceFiles(
  taskName: string,
  files: readonly File[],
  path = '',
  signal?: AbortSignal,
): Promise<ApiResponse<unknown>> {
  const form = new FormData()
  for (const file of files) {
    // webkitRelativePath 在目录选择时带相对路径，用它保留目录结构
    const relative = (file as File & { webkitRelativePath?: string }).webkitRelativePath
    form.append('files', file, relative || file.name)
  }
  return request(API.workspace(taskName), {
    method: 'POST',
    query: { path: normalizePath(path) },
    form,
    // 上传耗时不可预期，关闭超时
    timeout: 0,
    ...(signal ? { signal } : {}),
  })
}

/** 目录优先、同类按名称排序，贴近文件管理器习惯 */
export function sortEntries(files: readonly FileRes[]): FileRes[] {
  return [...files].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    return a.name.localeCompare(b.name, 'zh-CN', { numeric: true })
  })
}
