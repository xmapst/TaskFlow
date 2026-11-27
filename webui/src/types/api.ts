/**
 * 后端接口类型定义
 *
 * 字段对应 Go 结构体的 JSON 标签；业务类型见 api/iproto，
 * 响应包络 ApiResponse<T> 对应 api.Response[T]。
 *
 * Go 端使用 omitempty 或 omitzero 时，响应可能省略对应字段。
 */

// ============================================================
// 统一响应包装
// ============================================================

/**
 * 后端响应包络见 api/response.go。业务状态在 code 中，msg 为字符串，
 * 多条消息以 "; " 连接。
 */
export interface ApiResponse<T> {
  code: ResponseCode
  msg: string
  /** UnixNano 时间戳 */
  timestamp: number
  data: T
}

/** 后端 response.Code 的全部取值 */
export type ResponseCode = 0 | 1001 | 1002 | 1003 | 1004 | 1005 | 1006

// ============================================================
// 基础类型
// ============================================================

/** 环境变量，iproto.SEnv */
export interface EnvVar {
  name: string
  value: string
}

/**
 * 起止时间，iproto.STimeRes。
 * 后端由 STimeStr()/ETimeStr() 生成，已是格式化字符串，原样展示即可。
 */
export interface TimeRange {
  start?: string
  end?: string
}

/**
 * 分页信息，iproto.SPageRes。
 * total 是总页数，不是总记录数。
 */
export interface PageRes {
  current: number
  size: number
  total: number
}

/** 分页请求，HTTP 查询串和 WebSocket JSON 均使用 page / size / prefix。 */
export interface PageReq {
  page?: number
  size?: number
  prefix?: string
}

/**
 * 重试策略，iproto.SRetryPolicy。
 * interval 与 maxInterval 是 Go time.Duration，即纳秒整数。
 */
export interface RetryPolicy {
  interval: number
  maxInterval: number
  maxAttempts: number
  multiplier: number
  jitter?: number
}

// ============================================================
// 任务
// ============================================================

/** 执行状态，对应 models.StateMap 的全部取值 */
export type ExecState = 'stopped' | 'running' | 'failed' | 'unknown' | 'pending' | 'paused' | 'skipped'

/**
 * 任务信息，iproto.STaskRes。
 *
 * 各接口返回的字段完整度不同：
 *  - GET /task（列表）：仅 name/state/count/cleanup/message/time，
 *    其余字段由详情接口提供；
 *  - GET /task/:task（详情）：额外含 workdir/timeout/env。
 * 因此列表项与详情共用此类型，差异字段一律可选。
 */
export interface TaskRes {
  name: string
  state: ExecState
  /** 步骤总数。为 0 时 Go 端 omitempty 会省略该字段 */
  count?: number
  /** 相对于服务端工作区的目录，不是 API 中的任务名 */
  workdir?: string
  /** Go time.Duration，纳秒 */
  timeout?: number
  /** 任务结束后是否删除工作目录 */
  cleanup?: boolean
  /**
   * 执行消息。服务端会把状态汇总拼在原始消息后，
   * 形如 "the task is waiting…; 2 pending; 1 running"。
   */
  message: string
  env?: EnvVar[]
  time?: TimeRange
}

/** 任务列表，iproto.STaskListDetailRes。无任务时后端返回 null */
export interface TaskListRes {
  page: PageRes
  tasks: TaskRes[] | null
}

/** 创建任务的返回值，iproto.STaskCreateRes */
export interface TaskCreateRes {
  name: string
  count?: number
}

/**
 * 创建任务的请求体，iproto.STaskReq。
 *
 * JSON 和 YAML 的时长字段都支持 Go 时长字符串（如 "2m"）或整数纳秒。
 * 此类型主要用于导出配置的结构说明与表单校验。
 */
export interface TaskReq {
  /** 省略时使用服务端生成的任务名 */
  workdir?: string
  /** 为 true 时任务结束后删除工作目录，省略时为 false（保留） */
  cleanup?: boolean
  /** Go 时长字符串或整数纳秒 */
  timeout?: number | string
  env?: EnvVar[]
  /** 必填且不可为空数组，后端 review 会校验 */
  step: StepReq[]
}

// ============================================================
// 步骤
// ============================================================

export interface StepRouting {
  edges?: { step: string; trigger?: string }[]
  join?: 'all' | 'any'
  emit?: string[]
}

/**
 * 步骤信息，iproto.SStepRes。
 *
 * 列表包含步骤的状态、执行器和路由。详情还可返回脚本、环境变量、
 * 超时和重试策略。
 */
export interface StepRes {
  name: string
  state: ExecState
  /** 退出码，0 为成功。后端 api.ExecCode，负值为内部约定如 killed */
  code: number
  desc?: string
  /** Go time.Duration，纳秒 */
  timeout?: number
  routing?: StepRouting
  /** 步骤状态消息 */
  message: string
  env?: EnvVar[]
  /** 步骤类型：xexec、mkdir、touch、approval 或 container */
  kind?: string
  /** 执行器私有参数：xexec 支持 shell；container 支持 image、shell、mounts */
  options?: Record<string, unknown>
  /** 脚本内容 */
  content?: string
  retryPolicy?: RetryPolicy
  time?: TimeRange
}

/** 创建任务时的步骤定义，iproto.SStepReq */
export interface StepReq {
  name?: string
  desc?: string
  timeout?: number | string
  routing?: StepRouting
  env?: EnvVar[]
  /** 必填 */
  kind: string
  /** 执行器私有参数：xexec 支持 shell；container 支持 image、shell、mounts */
  options?: Record<string, unknown>
  /** 必填 */
  content: string
  retryPolicy?: Partial<Omit<RetryPolicy, 'interval' | 'maxInterval'> & { interval: number | string; maxInterval: number | string }>
}

/** 单行日志，iproto.SStepLogRes */
export interface StepLogRes {
  /** UnixNano 时间戳 */
  timestamp: number
  /** 行号，从 1 开始 */
  line: number
  content: string
}

// ============================================================
// 工作目录
// ============================================================

/** 文件或目录条目，iproto.SFileRes */
export interface FileRes {
  name: string
  /** 相对任务工作目录的路径，以 / 分隔且带前导斜杠 */
  path: string
  size: number
  /** Go FileMode 字符串，如 -rw-r--r-- */
  mode: string
  /** UnixNano 时间戳 */
  modTime: number
  isDir: boolean
}

/** 目录列表，iproto.SFileListRes。空目录时 files 为 null */
export interface FileListRes {
  total: number
  files: FileRes[] | null
}

// ============================================================
// 系统
// ============================================================

/** 版本信息，iproto.SVersion */
export interface VersionRes {
  buildTime: string
  version: string
  git: {
    branch: string
    commit: string
    url: string
  }
  go: {
    arch: string
    os: string
    version: string
  }
  user: {
    email: string
    name: string
  }
}

/** 健康检查，iproto.SHealthyz */
export interface HealthyzRes {
  server: string
  client: string
  state: string
}

// ============================================================
// 管理操作
// ============================================================

/** 管理操作。 */
export type ManageAction = 'kill' | 'pause' | 'resume'

/** 管理操作的查询参数 */
export interface ManageQuery {
  action: ManageAction
  /** Go duration 字面量如 1m、30s。仅 pause 有意义，留空需手工恢复 */
  duration?: string
}
