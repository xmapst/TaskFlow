/**
 * 实时传输层：全站唯一的多路复用 WebSocket（/api/v1/ws）
 *
 * 一条常驻连接上既能订阅长期主题，也能发起一次性调用，靠 id 区分帧归属：
 *
 *   客户端 → 服务端（每帧一个 JSON）
 *     {"id","action":"subscribe","topic","params"}
 *     {"id","action":"unsubscribe"}
 *     {"id","action":"call","method","params"}
 *
 *   服务端 → 客户端
 *     {"id","topic","event":"update","code","message","data"}  订阅的新值
 *     {"id","topic","event":"done"}                            订阅到此为止
 *     {"id","topic","event":"error","message"}                 订阅或本次请求失败
 *     {"id","event":"result","data"}                           调用的返回值
 *
 * 主题：event / system / tasks / task / steps / step / step-log /
 *       terminal-sessions / terminal-watch / terminal-replay
 * 方法：task.create / task.manage / task.delete / task.dump / step.manage /
 *       system.update / terminal.disconnect / replay.list
 *
 * 唯一的例外是交互式 shell：它传的是原始 PTY 字节流，打开终端时单独连
 * /api/v1/terminal，关掉终端即断开。
 *
 * 若干设计要点：
 *  - 应用启动即建立并常驻：没有订阅也不主动断开，页面来回切换不重连；
 *  - 断线后指数退避、无上限重连，重连成功时把所有活跃订阅原样重发，调用方只
 *    感知 onOpen，不需要自己重新订阅；
 *  - 进行中的调用在断线时一律拒绝而不是重发 —— 创建任务这类调用不幂等，
 *    重发可能造成重复提交；
 *  - 服务端 30s 一次 Ping 保活，浏览器自动回 Pong，这里不用管心跳。
 *  - 浏览器看不到那些 Ping 帧，所以自己每 PING_EVERY 发一次应用层 ping；
 *    LIFELINE 内一帧都没收到就判定链路已死（半开的 TCP 连接不会触发
 *    close 事件），主动丢弃并重连。
 */

import type { ResponseCode } from '@/types/api'
import { WS } from '@/config/endpoints'

const RETRY_BASE = 1_000
const MAX_RETRY_DELAY = 15_000
/** 退避翻倍的上限：再往后只是把延迟钉在 MAX_RETRY_DELAY，不必继续翻 */
const MAX_RETRY_SHIFT = 5
/** 单次调用的兜底超时：服务端不回复时不能让 Promise 永远悬着 */
const CALL_TIMEOUT = 30_000
/** 显式传 0 表示不设超时，用于服务端本身就可能耗时很久的调用 */
const NO_TIMEOUT = 0
/** 探活间隔：空闲时每 20 秒往服务端要一个回包 */
const PING_EVERY = 20_000
/** 多久收不到任何帧就认为连接已经断了 */
const LIFELINE = 45_000
/** 心跳检查的粒度，比 PING_EVERY 细，保证探测及时 */
const HEARTBEAT_EVERY = 5_000

/** 服务端帧，字段与服务端 httpx.StreamFrame 一一对应 */
export interface StreamFrame<T = unknown> {
  id?: string
  topic?: string
  event: 'update' | 'done' | 'error' | 'result'
  code?: ResponseCode
  message?: string
  data?: T
}

export interface StreamHandlers<T = unknown> {
  /** 订阅有新值。首帧即当前快照，之后仅在变化时到达 */
  onUpdate?: (frame: StreamFrame<T>) => void
  /** 订阅失败或中途出错，此后不会再有帧 */
  onError?: (message: string) => void
  /** 订阅正常结束（如任务进入终态），此后不会再有帧 */
  onDone?: () => void
  /** 订阅已生效：连接建立、重建后补发成功，或换参数重订成功 */
  onOpen?: () => void
  /** 连接断开，即将重连 */
  onClose?: () => void
}

export interface StreamSubscription {
  /** 取消订阅 */
  close: () => void
  /** 以新参数重订阅：沿用同一 id，服务端替换旧会话，连接不中断 */
  update: (params?: unknown) => void
}

interface Subscriber {
  id: string
  topic: string
  params?: unknown
  handlers: StreamHandlers
  /** 当前这条连接上是否已发出 subscribe；断线后复位，重连时补发 */
  sent: boolean
}

interface PendingCall {
  method: string
  params?: unknown
  sent: boolean
  resolve: (value: unknown) => void
  reject: (error: Error) => void
  timer: number
}

export class StreamClient {
  private socket: WebSocket | null = null
  private retryTimer: number | null = null
  private heartbeatTimer: number | null = null
  private retries = 0
  private seq = 0
  /** 最近一次收到帧的时间戳，探活据此判断链路是否还活着 */
  private lastSeenAt = 0
  /** 最近一次发出应用层 ping 的时间戳 */
  private lastPingAt = 0

  /** 活跃订阅，重连后按此表原样重订阅 */
  private readonly subscribers = new Map<string, Subscriber>()
  /** 等待 result 的调用 */
  private readonly calls = new Map<string, PendingCall>()

  constructor(private readonly url: string) {
    // 网络恢复、或系统休眠唤醒后回到前台时，攒下的退避往往还要等十几秒；
    // 这两种时机直接重连一次，失败再回到正常退避。
    window.addEventListener('online', () => this.wake())
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') this.wake()
    })
  }

  /** 建立常驻连接：应用启动时调用一次，之后断线交给退避重连接管 */
  open(): void {
    this.ensure()
  }

  /** 订阅一个主题；返回的句柄用于取消订阅或换参数重订 */
  subscribe(topic: string, params?: unknown, handlers: StreamHandlers = {}): StreamSubscription {
    const id = this.nextID('s')
    this.subscribers.set(id, { id, topic, params, handlers, sent: false })
    this.ensure()
    this.flush()

    return {
      close: () => {
        if (!this.subscribers.delete(id)) return
        this.send({ id, action: 'unsubscribe' })
      },
      update: (next) => {
        const sub = this.subscribers.get(id)
        if (!sub) return
        sub.params = next
        // 交给 flush 发送：连接若已断开，重连时会带着新参数补发
        sub.sent = false
        this.flush()
      },
    }
  }

  /**
   * 发起一次性调用。
   *
   * @param timeout 本地超时毫秒数，传 NO_TIMEOUT 表示不设上限：
   *        像调整工作池这种缩容要等退役 worker 跑完当前任务的调用，
   *        固定 30 秒会在服务端仍在执行时先行失败。
   */
  call<T = unknown>(method: string, params?: unknown, timeout = CALL_TIMEOUT): Promise<T> {
    const id = this.nextID('c')
    return new Promise<T>((resolve, reject) => {
      const timer = timeout > NO_TIMEOUT ? window.setTimeout(() => {
        this.calls.delete(id)
        reject(new Error('请求超时，请检查网络后重试'))
      }, timeout) : NO_TIMEOUT
      this.calls.set(id, {
        method,
        params,
        sent: false,
        resolve: resolve as (value: unknown) => void,
        reject,
        timer,
      })
      this.ensure()
      this.flush()
    })
  }

  private nextID(prefix: string): string {
    this.seq += 1
    return `${prefix}${this.seq}`
  }

  /** 建立连接；已有连接（含正在连接）时直接返回 */
  private ensure(): void {
    if (this.socket || this.retryTimer !== null) return

    let socket: WebSocket
    try {
      socket = new WebSocket(this.url)
    } catch {
      this.scheduleRetry()
      return
    }
    this.socket = socket

    socket.onopen = (): void => {
      this.retries = 0
      this.lastSeenAt = Date.now()
      this.lastPingAt = 0
      this.startHeartbeat()
      this.flush()
    }

    socket.onmessage = (event: MessageEvent<unknown>): void => {
      this.lastSeenAt = Date.now()
      if (typeof event.data !== 'string') return
      let frame: StreamFrame
      try {
        frame = JSON.parse(event.data) as StreamFrame
      } catch {
        // 服务端只发 JSON，解析失败说明帧有问题，丢弃
        return
      }
      this.receive(frame)
    }

    // error 之后浏览器通常还会补一个 close，但握手中途失败时不一定；
    // 这里直接按断线处理，免得连接卡在半死不活的状态里没人管。
    socket.onerror = (): void => this.drop(socket)

    socket.onclose = (): void => this.drop(socket)
  }

  /**
   * 丢弃当前连接并按断线处理。
   *
   * 只认「当前」这条 socket：旧连接的 close 事件可能在新连接建立之后才到，
   * 那时它不该再把新建的连接一起清掉。
   */
  private drop(socket: WebSocket): void {
    if (this.socket !== socket) return
    this.socket = null
    this.stopHeartbeat()
    // 半开的连接 close() 要等内核超时，不能靠 onclose 来推进，立即重连
    try {
      socket.close()
    } catch {
      // 已经关掉的连接再关一次无所谓
    }
    this.onDown()
  }

  private startHeartbeat(): void {
    this.stopHeartbeat()
    this.heartbeatTimer = window.setInterval(() => this.beat(), HEARTBEAT_EVERY)
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer === null) return
    window.clearInterval(this.heartbeatTimer)
    this.heartbeatTimer = null
  }

  /** 定时检查链路：长时间收不到任何帧就重连，空闲时补一次应用层 ping */
  private beat(): void {
    if (!this.isOpen) return
    const now = Date.now()
    if (now - this.lastSeenAt > LIFELINE) {
      // 连着但一帧都不回：多半是半开连接，服务端早就不在了
      if (this.socket) this.drop(this.socket)
      return
    }
    if (now - this.lastPingAt < PING_EVERY) return
    this.lastPingAt = now
    this.send({ id: this.nextID('p'), action: 'ping' })
  }

  /**
   * 网络或页面恢复：立刻重连一次。
   *
   * 后台标签页的定时器会被浏览器节流，回到前台时这条连接可能早就死了，
   * 所以先检查一次存活，再决定是丢弃还是直接重连。
   */
  private wake(): void {
    const socket = this.socket
    if (socket) {
      if (Date.now() - this.lastSeenAt <= LIFELINE) return
      this.drop(socket)
    }
    if (this.retryTimer !== null) {
      window.clearTimeout(this.retryTimer)
      this.retryTimer = null
    }
    this.retries = 0
    this.ensure()
  }

  /** 连接就绪时补发尚未发出的订阅与调用；已发出的不重复发送 */
  private flush(): void {
    if (!this.isOpen) return
    for (const sub of this.subscribers.values()) {
      if (sub.sent) continue
      sub.sent = true
      this.send({ id: sub.id, action: 'subscribe', topic: sub.topic, params: sub.params })
      sub.handlers.onOpen?.()
    }
    for (const [id, call] of this.calls) {
      if (call.sent) continue
      call.sent = true
      this.send({ id, action: 'call', method: call.method, params: call.params })
    }
  }

  private send(payload: unknown): void {
    if (!this.isOpen || !this.socket) return
    this.socket.send(JSON.stringify(payload))
  }

  private get isOpen(): boolean {
    return this.socket?.readyState === WebSocket.OPEN
  }

  private receive(frame: StreamFrame): void {
    const id = frame.id ?? ''
    switch (frame.event) {
      case 'result': {
        const call = this.calls.get(id)
        if (!call) return
        this.settle(id, call)
        call.resolve(frame.data)
        return
      }
      case 'error': {
        const call = this.calls.get(id)
        if (call) {
          this.settle(id, call)
          call.reject(new Error(frame.message || '请求失败'))
          return
        }
        const sub = this.subscribers.get(id)
        if (!sub) return
        // 订阅失败后服务端不会再发 done，这里直接收尾
        this.subscribers.delete(id)
        sub.handlers.onError?.(frame.message || '订阅失败')
        return
      }
      case 'done': {
        const sub = this.subscribers.get(id)
        if (!sub) return
        this.subscribers.delete(id)
        sub.handlers.onDone?.()
        return
      }
      default: {
        this.subscribers.get(id)?.handlers.onUpdate?.(frame)
      }
    }
  }

  private settle(id: string, call: PendingCall): void {
    window.clearTimeout(call.timer)
    this.calls.delete(id)
  }

  /** 连接意外断开：拒绝进行中的调用，通知订阅方并重连 */
  private onDown(): void {
    for (const [id, call] of this.calls) {
      this.settle(id, call)
      call.reject(new Error('实时连接中断，请重试'))
    }
    for (const sub of this.subscribers.values()) {
      sub.sent = false
      sub.handlers.onClose?.()
    }
    this.scheduleRetry()
  }

  /** 常驻连接没有放弃一说：退避到上限后一直按上限重试，直到服务端回来 */
  private scheduleRetry(): void {
    if (this.retryTimer !== null) return
    this.retries += 1
    const shift = Math.min(this.retries - 1, MAX_RETRY_SHIFT)
    const delay = Math.min(RETRY_BASE * 2 ** shift, MAX_RETRY_DELAY)
    this.retryTimer = window.setTimeout(() => {
      this.retryTimer = null
      this.ensure()
    }, delay)
  }
}

let shared: StreamClient | null = null

/**
 * 全站唯一的实时连接：编排数据、事件流、终端管理面都在这一条上。
 *
 * 由 main.tsx 在启动时 open() 一次建立，之后常驻；页面进出只增删订阅，
 * 不复用、不重建连接。交互式 shell 是另一条独立的按需连接（见 WS.terminal）。
 */
export function stream(): StreamClient {
  shared ??= new StreamClient(WS.stream())
  return shared
}
