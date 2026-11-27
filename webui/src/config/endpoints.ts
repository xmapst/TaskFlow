/**
 * 运行时地址解析与接口端点
 *
 * 一次构建、到处部署：生产环境不写死任何 host，全部由当前页面地址推导。
 *
 * 这里改为读取 Vite 注入的 BASE_URL，与实际构建时的部署路径一致，
 * 且本项目采用 HashRouter，路由段不会污染 pathname。
 */

/** 部署基础路径，恒以 / 开头、不以 / 结尾（根部署时为空串） */
function resolveBasePath(): string {
  const base = import.meta.env.BASE_URL || '/'
  const normalized = base.endsWith('/') ? base.slice(0, -1) : base
  return normalized === '/' ? '' : normalized
}

const BASE_PATH = resolveBasePath()

/** HTTP 请求前缀。同源部署用相对路径，天然适配 http/https 与反向代理 */
export const HTTP_BASE = BASE_PATH

/** WebSocket 前缀，协议随页面在 ws/wss 间切换 */
export const WS_BASE = ((): string => {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}${BASE_PATH}`
})()

const API_V1 = `${HTTP_BASE}/api/v1`
const WS_V1 = `${WS_BASE}/api/v1`

/** 路径段编码：任务名与步骤名允许中文及 -_.~，需转义后拼接 */
const seg = (value: string): string => encodeURIComponent(value)

/** HTTP 接口端点：任务与步骤的读写、终端的会话与录像都在实时连接上，这里只剩工作目录、上传与系统类接口 */
export const API = {
  workspace: (task: string): string => `${API_V1}/task/${seg(task)}/workspace`,

  /** tus 断点续传端点，由 internal/server/handler.go 挂载 */
  fileUpload: (): string => `${API_V1}/files/`,

  system: (): string => `${API_V1}/system`,

  version: (): string => `${HTTP_BASE}/version`,
  healthyz: (): string => `${HTTP_BASE}/healthyz`,
  heartbeat: (): string => `${HTTP_BASE}/heartbeat`,
} as const

/**
 * WebSocket 端点。
 *
 * 后端只有两条连接，职责不同：
 *  - /api/v1/ws：常驻的编排连接，主题 event / system / tasks / task / steps /
 *    step / step-log / terminal-sessions / terminal-watch / terminal-replay，
 *    调用 task.* / step.manage / system.update / terminal.disconnect / replay.list；
 *  - /api/v1/terminal：交互式 shell，按需建立，关掉终端即断开。
 *
 * 工作目录的列表、下载、上传与删除仍走 HTTP。
 */
export const WS = {
  /** cols/rows 为浏览器端初始终端尺寸，用于创建 PTY 和录像头；缺省时后端用 80x24。 */
  terminal: (size?: { cols: number; rows: number }): string =>
    size && size.cols > 0 && size.rows > 0
      ? `${WS_V1}/terminal?cols=${Math.trunc(size.cols)}&rows=${Math.trunc(size.rows)}`
      : `${WS_V1}/terminal`,
  /** 全站唯一的实时连接 */
  stream: (): string => `${WS_V1}/ws`,
} as const
