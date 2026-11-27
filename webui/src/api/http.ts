/** HTTP 客户端：先检查 HTTP 状态，再从统一响应包络中读取业务状态 `code` 和消息 `msg`。 */

import type { ApiResponse, ResponseCode } from '@/types/api'
import { CODE, isSuccessCode } from '@/constants/state'

/** 业务错误：HTTP 通了但 code 非 0 */
export class ApiError extends Error {
  readonly code: number
  readonly url: string
  readonly payload: unknown

  constructor(message: string, options: { code: number; url: string; payload?: unknown }) {
    super(message)
    this.name = 'ApiError'
    this.code = options.code
    this.url = options.url
    this.payload = options.payload
  }

  /** 数据不存在：任务/步骤已被删除，或工作目录尚未创建 */
  get isNotFound(): boolean {
    return this.code === CODE.NO_DATA
  }
}

/** 传输层错误：网络中断、超时、响应体非法 */
export class NetworkError extends Error {
  readonly url: string

  constructor(message: string, options: { url: string; cause?: unknown }) {
    super(message)
    this.name = 'NetworkError'
    this.url = options.url
    if (options.cause !== undefined) this.cause = options.cause
  }
}

/** 请求被主动取消，通常发生在组件卸载或参数变更时，不应提示用户 */
export class CanceledError extends Error {
  constructor() {
    super('请求已取消')
    this.name = 'CanceledError'
  }
}

const DEFAULT_TIMEOUT = 30_000

/**
 * 查询参数值。数组按重复键发送（`?path=a&path=b`），
 * 用于一次操作多个条目的接口。
 */
export type QueryValue =
  | string
  | number
  | boolean
  | undefined
  | null
  | readonly (string | number | boolean)[]

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  /** 查询参数，值为 undefined/null/'' 的键会被丢弃 */
  query?: Record<string, QueryValue>
  /** JSON 请求体 */
  json?: unknown
  /** 已序列化的 JSON，保留编辑器原文。 */
  jsonText?: string
  /** YAML 请求体，后端 Send 支持内容协商 */
  yaml?: string
  /** FormData 请求体，用于 multipart 上传 */
  form?: FormData
  /** 超时毫秒，0 表示不限制（上传等场景） */
  timeout?: number
  signal?: AbortSignal
  /** 额外视为成功的 code，用于详情类接口的中间态 */
  acceptCodes?: readonly ResponseCode[]
  headers?: Record<string, string>
}

/** 拼接查询串，键名按接口约定原样发送。 */
export function appendQuery(url: string, query: Record<string, QueryValue>): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      for (const item of value) params.append(key, String(item))
      continue
    }
    params.append(key, String(value))
  }
  const qs = params.toString()
  if (!qs) return url
  return url.includes('?') ? `${url}&${qs}` : `${url}?${qs}`
}

async function requestResponse(url: string, options: RequestOptions = {}): Promise<Response> {
  const {
    method = 'GET',
    query,
    json,
    jsonText,
    yaml,
    form,
    timeout = DEFAULT_TIMEOUT,
    signal,
    headers: extraHeaders,
  } = options

  const target = query ? appendQuery(url, query) : url
  const headers: Record<string, string> = { Accept: 'application/json', ...extraHeaders }
  let body: BodyInit | undefined

  if (form) {
    // 不设 Content-Type，交由浏览器生成含 boundary 的头
    body = form
  } else if (yaml !== undefined) {
    headers['Content-Type'] = 'application/yaml'
    body = yaml
  } else if (jsonText !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = jsonText
  } else if (json !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(json)
  }

  // 合并外部取消与超时：任一触发即中止
  const controller = new AbortController()
  const timer = timeout > 0 ? window.setTimeout(() => controller.abort(), timeout) : null
  const onExternalAbort = (): void => controller.abort()
  signal?.addEventListener('abort', onExternalAbort, { once: true })

  let response: Response
  try {
    // body 显式给 null 而非 undefined：exactOptionalPropertyTypes 下
    // RequestInit.body 的类型是 BodyInit | null，不接受 undefined
    response = await fetch(target, {
      method,
      headers,
      body: body ?? null,
      signal: controller.signal,
    })
  } catch (error) {
    // 外部主动取消不算异常，转为专用错误类型供调用方静默处理
    if (signal?.aborted) throw new CanceledError()
    const reason = controller.signal.aborted ? `请求超时（${timeout / 1000}s）` : '网络连接失败'
    throw new NetworkError(reason, { url: target, cause: error })
  } finally {
    if (timer !== null) window.clearTimeout(timer)
    signal?.removeEventListener('abort', onExternalAbort)
  }

  if (!response.ok) {
    throw new NetworkError(`服务异常（HTTP ${response.status}）`, { url: target })
  }

  return response
}

/** 发起请求并解包统一响应。 */
export async function request<T>(url: string, options: RequestOptions = {}): Promise<ApiResponse<T>> {
  const response = await requestResponse(url, options)
  const { acceptCodes = [] } = options
  let payload: ApiResponse<T>
  try {
    payload = (await response.json()) as ApiResponse<T>
  } catch (error) {
    throw new NetworkError('响应格式异常，无法解析', { url: response.url || url, cause: error })
  }

  const code = payload?.code
  if (!isSuccessCode(code) && !acceptCodes.includes(code)) {
    throw new ApiError(payload?.msg || '请求失败', { code, url: response.url || url, payload })
  }

  return payload
}

/** 仅检查 HTTP 状态，用于成功时没有响应体的接口。 */
export async function requestEmpty(url: string, options?: RequestOptions): Promise<void> {
  await requestResponse(url, options)
}

/** 只取 data 的便捷形式 */
export async function requestData<T>(url: string, options?: RequestOptions): Promise<T> {
  const payload = await request<T>(url, options)
  return payload.data
}

/** 把任意异常转为可直接展示的中文文案 */
export function toErrorMessage(error: unknown, fallback = '操作失败'): string {
  if (error instanceof CanceledError) return error.message
  if (error instanceof ApiError || error instanceof NetworkError) return error.message
  if (error instanceof Error) return error.message || fallback
  return fallback
}

/** 判断是否为「请求被取消」，此类错误不应向用户提示 */
export function isCanceled(error: unknown): boolean {
  return error instanceof CanceledError || (error instanceof Error && error.name === 'AbortError')
}
