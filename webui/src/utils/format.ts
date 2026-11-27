/**
 * 格式化工具
 *
 * 后端各类时间字段的单位互不相同，务必对号入座：
 *  - task.timeout / step.timeout / retryPolicy.interval：Go time.Duration，纳秒整数
 *  - file.modTime / stepLog.timestamp：UnixNano 时间戳
 *  - task.time.start / end：后端 STimeStr()/ETimeStr() 已格式化的字符串
 */

import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import duration from 'dayjs/plugin/duration'
import 'dayjs/locale/zh-cn'

dayjs.extend(relativeTime)
dayjs.extend(duration)
dayjs.locale('zh-cn')

export const EMPTY = '—'

const NS_PER_MS = 1e6
const NS_PER_SECOND = 1e9

/** 文件体积，入参为字节 */
export function formatBytes(bytes: number | undefined | null): string {
  if (bytes === undefined || bytes === null) return EMPTY
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const index = Math.min(Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** index
  const unit = units[index] ?? 'B'
  return `${value >= 100 || index === 0 ? Math.round(value) : value.toFixed(1)} ${unit}`
}

/**
 * Go time.Duration（纳秒）转人类可读文案。
 * 例：120000000000 → 2分钟；172800000000000 → 2天
 */
export function formatDuration(nanoseconds: number | undefined | null): string {
  if (!nanoseconds || nanoseconds <= 0) return EMPTY

  const totalMs = nanoseconds / NS_PER_MS
  if (totalMs < 1) return '不足 1 毫秒'
  if (totalMs < 1000) return `${Math.round(totalMs)}毫秒`

  const d = dayjs.duration(totalMs)
  const parts: string[] = []
  const days = Math.floor(d.asDays())
  if (days > 0) parts.push(`${days}天`)
  if (d.hours() > 0) parts.push(`${d.hours()}小时`)
  if (d.minutes() > 0) parts.push(`${d.minutes()}分钟`)
  if (d.seconds() > 0) parts.push(`${d.seconds()}秒`)

  // 最多两个量级，避免 "1天2小时3分钟4秒" 这类过长文案
  return parts.slice(0, 2).join(' ') || `${Math.round(totalMs)}毫秒`
}

/**
 * Go time.Duration 转 Go 风格简写，用于 duration 查询参数。
 * 例：90000000000 → 1m30s
 */
export function formatGoDuration(nanoseconds: number): string {
  if (!nanoseconds || nanoseconds <= 0) return ''
  const totalSeconds = Math.floor(nanoseconds / NS_PER_SECOND)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  return [hours && `${hours}h`, minutes && `${minutes}m`, seconds && `${seconds}s`]
    .filter(Boolean)
    .join('')
}

/** 把秒数转为 Go duration 字面量，用于挂起时长选择 */
export function secondsToGoDuration(seconds: number): string {
  return formatGoDuration(seconds * NS_PER_SECOND) || '0s'
}

/** UnixNano 时间戳格式化为 YYYY-MM-DD HH:mm:ss */
export function formatNanoTime(nanoseconds: number | undefined | null): string {
  if (!nanoseconds) return EMPTY
  return dayjs(nanoseconds / NS_PER_MS).format('YYYY-MM-DD HH:mm:ss')
}

/** UnixNano 时间戳只取时分秒，日志行前缀用 */
export function formatNanoClock(nanoseconds: number | undefined | null): string {
  if (!nanoseconds) return ''
  return dayjs(nanoseconds / NS_PER_MS).format('HH:mm:ss')
}

/** UnixNano 相对时间，如「3 分钟前」 */
export function formatNanoFromNow(nanoseconds: number | undefined | null): string {
  if (!nanoseconds) return EMPTY
  return dayjs(nanoseconds / NS_PER_MS).fromNow()
}

/**
 * 后端时间字符串原样展示。
 * task.time.start/end 已是可读格式，不再二次解析以免时区偏移。
 */
export function displayTime(value: string | undefined | null): string {
  return value || EMPTY
}

/**
 * 两个后端时间字符串之间的耗时。
 * end 缺省表示仍在进行中，按当前时间计算。
 * 解析失败返回空串，由调用方决定是否留白。
 */
export function elapsedBetween(
  start: string | undefined,
  end: string | undefined,
): string {
  if (!start) return ''
  const from = dayjs(start)
  if (!from.isValid()) return ''
  const to = end ? dayjs(end) : dayjs()
  if (!to.isValid() || to.isBefore(from)) return ''
  return formatElapsedMs(to.diff(from))
}

/** 毫秒差值转 mm:ss 或 h:mm:ss */
export function formatElapsedMs(milliseconds: number): string {
  const totalSeconds = Math.max(0, Math.floor(milliseconds / 1000))
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  const pad = (n: number): string => String(n).padStart(2, '0')
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${pad(minutes)}:${pad(seconds)}`
}

/** 千分位整数 */
export function formatNumber(value: number | string | undefined | null): string {
  if (value === undefined || value === null || value === '') return '0'
  const num = Number(value)
  if (Number.isNaN(num)) return String(value)
  return num.toLocaleString('zh-CN')
}

/** 传输速率，入参为字节每秒 */
export function formatSpeed(bytesPerSecond: number): string {
  if (!bytesPerSecond || bytesPerSecond <= 0) return ''
  return `${formatBytes(bytesPerSecond)}/s`
}

/** 超长文本截断 */
export function truncate(text: string | undefined, maxLength = 60): string {
  if (!text || text.length <= maxLength) return text ?? ''
  return `${text.slice(0, maxLength - 1)}…`
}

export interface StateCounter {
  count: number
  state: string
}

export interface SplitMessage {
  /** 原始消息主体 */
  text: string
  /** 状态计数汇总 */
  counters: StateCounter[]
}

/**
 * 拆解后端聚合消息。
 *
 * 服务端会把状态汇总拼在原始消息之后，
 * 形如 "the task is waiting…; 2 pending; 1 running"。
 * 这里切成主体文本与状态计数两部分，便于分层展示。
 */
export function splitStateMessage(message: string | undefined): SplitMessage {
  if (!message) return { text: '', counters: [] }
  const segments = message
    .split(';')
    .map((s) => s.trim())
    .filter(Boolean)

  const counters: StateCounter[] = []
  const texts: string[] = []
  for (const segment of segments) {
    const matched = /^(\d+)\s+(\w+)$/.exec(segment)
    if (matched?.[1] && matched[2]) {
      counters.push({ count: Number(matched[1]), state: matched[2] })
    } else {
      texts.push(segment)
    }
  }
  return { text: texts.join('；'), counters }
}

export interface Breadcrumb {
  name: string
  /** 从根到当前的累积路径 */
  path: string
}

/** 工作区路径拆成面包屑分段 */
export function toBreadcrumbs(path: string | undefined): Breadcrumb[] {
  const clean = (path ?? '').replace(/^\/+|\/+$/g, '')
  if (!clean) return []
  let cumulative = ''
  return clean
    .split('/')
    .filter(Boolean)
    .map((name) => {
      cumulative = cumulative ? `${cumulative}/${name}` : name
      return { name, path: cumulative }
    })
}

/** 触发文本文件下载，用于导出 YAML 配置与日志 */
export function downloadText(filename: string, content: string, mime = 'text/plain'): void {
  const blob = new Blob([content], { type: `${mime};charset=utf-8` })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  // 交给浏览器完成下载后再释放
  window.setTimeout(() => URL.revokeObjectURL(url), 1_000)
}

/** 复制到剪贴板，返回是否成功 */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // 非 HTTPS 或权限受限时降级到 execCommand
    try {
      const textarea = document.createElement('textarea')
      textarea.value = text
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      const ok = document.execCommand('copy')
      document.body.removeChild(textarea)
      return ok
    } catch {
      return false
    }
  }
}
