import Zmodem from 'zmodem-ts'
import type { DownloadTarget, DownloadWriter } from './terminalDownload'

const chunkSize = 32 * 1024
const downloadLimit = 64 * 1024 * 1024
const maxDownloads = 100
const streamAckThreshold = 128 * 1024
const maxProtocolFileSize = 0xffff_ffff // ZMODEM file offsets are 32 bit.
const downloadFinishWait = 1_000
const hexPrefix = [0x2a, 0x2a, 0x18, 0x42] as const // ** ZDLE B

// Return 0 for an incomplete header, -1 for a non-header.
function hexHeaderLength(data: Uint8Array, start: number, initial: boolean): number {
  const available = data.length - start
  for (let i = 0; i < Math.min(available, 18); i++) {
    const byte = data[start + i]!
    if (i < hexPrefix.length) {
      if (byte !== hexPrefix[i]) return -1
    } else if (initial && i === 4) {
      if (byte !== 0x30) return -1
    } else if (initial && i === 5) {
      if (byte !== 0x30 && byte !== 0x31) return -1
    } else {
      const lower = byte | 0x20
      if (!(byte >= 0x30 && byte <= 0x39) && !(lower >= 0x61 && lower <= 0x66)) return -1
    }
  }
  if (available < 19) return 0
  const eol = data[start + 18]! & 0x7f
  if (eol === 0x0a) return 19
  if (eol !== 0x0d) return -1
  if (available < 20) return 0
  return (data[start + 19]! & 0x7f) === 0x0a ? 20 : -1
}

interface Session {
  on(event: 'session_end', handler: () => void): void
  abort(): void
  aborted(): boolean
}

interface UploadSession extends Session {
  send_offer(file: { name: string; size: number; mtime: Date }): Promise<{
    send(data: Uint8Array): void
    end(): Promise<void>
  } | undefined>
  close(): Promise<void>
}

interface DownloadSession extends Session {
  on(event: 'session_end', handler: () => void): void
  on(event: 'offer', handler: (offer: Offer) => void): void
  start(): Promise<void>
}

interface Offer {
  get_details(): { name: string; size: number }
  skip(): Promise<void>
  accept(options: { on_input: (chunk: number[]) => void }): Promise<void>
}

export interface TransferState {
  mode: 'idle' | 'choose' | 'upload' | 'download'
  message: string
  downloads: readonly { name: string; url?: string }[]
  progress?: { file: number; count: number; bytes: number; total: number }
}

interface Active {
  session: Session
  stop: AbortController
  finishTimer?: ReturnType<typeof setTimeout>
  target?: DownloadTarget
  writer?: DownloadWriter
  pendingAck?: Uint8Array<ArrayBuffer>
  pendingReady?: Uint8Array<ArrayBuffer>
}

// Some library promises do not settle if the session ends mid-transfer.
async function untilStopped<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  let abort: () => void = () => {}
  const stopped = new Promise<never>((_, reject) => {
    abort = () => reject(signal.reason)
    if (signal.aborted) abort()
    else signal.addEventListener('abort', abort, { once: true })
  })
  try {
    return await Promise.race([promise, stopped])
  } finally {
    signal.removeEventListener('abort', abort)
  }
}

export function terminalZmodem(options: {
  send: (frame: Uint8Array<ArrayBuffer>) => void
  bufferedAmount: () => number
  output: (data: Uint8Array<ArrayBuffer>) => void
  update: (state: TransferState) => void
  downloadTarget?: DownloadTarget
  saveDownload?: (url: string, name: string) => void
  debug?: (event: string, fields?: Record<string, string | number | boolean>) => void
}) {
  const debug = options.debug ?? (() => {})
  let phase = 'idle'
  let sentBytes = 0
  let receivedBytes = 0
  let maxFrameBytes = 0
  let active: Active | undefined
  let uploadSession: UploadSession | undefined
  let disposed = false
  let timer: ReturnType<typeof setTimeout> | undefined
  let mode: TransferState['mode'] = 'idle'
  let message = ''
  let downloaded = 0
  let downloads: TransferState['downloads'] = []
  let pending = new Uint8Array(0)
  let detecting = false

  let progress: TransferState['progress']
  function update(nextMode: TransferState['mode'], text: string) {
    mode = nextMode
    message = text
    if (mode !== 'upload') progress = undefined
    if (!disposed) options.update({ mode, message, downloads, ...(progress && { progress }) })
  }

  function clearDownloads() {
    for (const { url } of downloads) if (url) URL.revokeObjectURL(url)
    downloads = []
    downloaded = 0
    update(mode, message)
  }

  function report(event: string, fields?: Record<string, string | number | boolean>) {
    debug(event, { phase, sentBytes, receivedBytes, maxFrameBytes, ...fields })
  }

  function stopTransfer(text: string, reason: string, error?: unknown) {
    const current = active
    if (!current) return
    report('transfer stopped', { reason, errorType: error instanceof TypeError ? 'TypeError' : typeof error })
    active = undefined
    uploadSession = undefined
    clearTimeout(timer)
    clearTimeout(current.finishTimer)
    current.stop.abort()
    void current.writer?.discard()
    try {
      current.session.abort()
    } finally {
      update('idle', text)
    }
  }

  function keepAlive() {
    clearTimeout(timer)
    if (active) timer = setTimeout(() => stopTransfer('文件传输超时。', 'idle_timeout'), 90_000)
  }

  async function receiveFile(current: Active, offer: Offer) {
    const { name, size } = offer.get_details()
    phase = 'download_offer'
    report('download offered', { declaredBytes: size })
    const limit = current.target ? maxProtocolFileSize : downloadLimit - downloaded
    if (!Number.isSafeInteger(size) || size < 0 || size > limit || downloads.length >= maxDownloads) {
      const ceiling = current.target ? '单文件小于 4 GiB，每批最多 100 个' : '每批 64 MiB 或 100 个文件'
      update('download', `已跳过文件：超过下载上限（${ceiling}），或文件大小无效。`)
      await untilStopped(offer.skip(), current.stop.signal)
      return
    }
    phase = 'download_data'
    // Never interpret a sender's name as a local path.
    let filename = Array.from(name.split(/[/\\]/).pop() ?? '').slice(0, 55).join('')
      .replace(/[\u0000-\u001f\u007f<>:"|?*]/g, '_').replace(/[ .]+$/, '') || 'download'
    if (/^(con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\.|$)/i.test(filename)) filename = `_${filename}`
    let received = 0
    let writer: DownloadWriter | undefined
    let committed = false
    let url: string | undefined
    // Only the compatibility path buffers whole files.
    const parts: Uint8Array<ArrayBuffer>[] = []
    try {
      if (current.target) {
        writer = await current.target(filename, current.stop.signal, () => {
          if (active !== current || !current.pendingAck) return
          options.send(current.pendingAck)
          delete current.pendingAck
        }, (error) => {
          if (active === current) stopTransfer('写入本地文件失败，传输已取消。', 'download_write_failed', error)
        })
        current.stop.signal.throwIfAborted()
        if (writer) current.writer = writer
        if (!writer && size > downloadLimit - downloaded) {
          update('download', '已跳过文件：浏览器无法启动流式下载，且文件超过 64 MiB 暂存上限。')
          await untilStopped(offer.skip(), current.stop.signal)
          return
        }
        update('download', `正在下载：${filename}`)
      }
      await untilStopped(offer.accept({ on_input: (chunk) => {
        received += chunk.length
        if (received > size) throw new Error('ZMODEM file exceeds declared size')
        if (writer) writer.push(chunk)
        else parts.push(Uint8Array.from(chunk))
      } }), current.stop.signal)
      current.stop.signal.throwIfAborted()
      if (received !== size) throw new Error('ZMODEM file size mismatch')
      if (writer) {
        await writer.commit()
        committed = true
        current.stop.signal.throwIfAborted()
      } else {
        url = URL.createObjectURL(new Blob(parts, { type: 'application/octet-stream' }))
        downloaded += received
        options.saveDownload?.(url, filename)
      }
    } finally {
      delete current.writer
      if (writer && !committed) void writer.discard()
    }
    report('download file completed', { fileBytes: received })
    downloads = [...downloads, { name: filename, ...(url && { url }) }]
    update(mode, `已交给浏览器下载：${filename}`)
    if (active === current && current.pendingReady) {
      const ready = current.pendingReady
      delete current.pendingReady
      delete current.pendingAck
      options.send(ready)
    }
  }

  function finishTransfer(current: Active, text: string) {
    active = undefined
    uploadSession = undefined
    clearTimeout(timer)
    clearTimeout(current.finishTimer)
    update('idle', text)
  }

  const createSentry = () => new Zmodem.Sentry({
    to_terminal: (bytes: number[]) => {
      if (bytes.length && !(detecting && active)) options.output(Uint8Array.from(bytes))
    },
    sender: (bytes: number[]) => {
      if (disposed) return
      const frame = new Uint8Array(bytes.length + 1)
      frame.set(bytes, 1) // Leading NUL marks protocol traffic, not terminal keystrokes.
      const first = sentBytes === 0
      const firstOversized = maxFrameBytes <= 64 * 1024 && frame.length > 64 * 1024
      sentBytes += frame.length
      maxFrameBytes = Math.max(maxFrameBytes, frame.length)
      if (first || firstOversized) report(firstOversized ? 'frame exceeds server default read limit' : 'first protocol frame sent', { frameBytes: frame.length })
      const current = active
      if (current?.writer && hexPrefix.every((byte, i) => bytes[i] === byte) && bytes[4] === 0x30) {
        if (bytes[5] === 0x31) { // ZRINIT after ZEOF: acknowledge only once the file is saved.
          current.pendingReady = frame
          return
        }
        if (bytes[5] === 0x33) { // ZACK: slow windowed senders when disk I/O lags.
          if (current.writer.queuedBytes >= streamAckThreshold) {
            current.pendingAck = frame
            return
          }
          delete current.pendingAck
        }
      }
      // The receiver only replies with hex ZFIN after the library has parsed
      // the sender's ZFIN. Some PTYs lose the final OO when sz exits.
      if (current && !uploadSession && current.finishTimer === undefined &&
          hexPrefix.every((byte, i) => bytes[i] === byte) && bytes[4] === 0x30 && bytes[5] === 0x38) {
        phase = 'download_wait_final_oo'
        current.finishTimer = setTimeout(() => {
          if (active !== current) return
          report('download final OO missing')
          // Drop the waiting parser without sending CAN into a shell that may
          // already have resumed. Keep all files that passed validation.
          sentry = createSentry()
          finishTransfer(current, '传输已结束（未收到对端结束标记）。')
        }, downloadFinishWait)
      }
      options.send(frame)
    },
    on_retract: () => report('handshake retracted'),
    on_detect: (detection: { get_session_role(): string; confirm(): Session }) => {
      const direction = detection.get_session_role() === 'send' ? 'upload' : 'download'
      phase = direction === 'upload' ? 'file_picker' : 'download_handshake'
      sentBytes = receivedBytes = maxFrameBytes = 0
      report('handshake detected', { direction })
      const session = detection.confirm()
      const current: Active = { session, stop: new AbortController() }
      active = current
      session.on('session_end', () => {
        if (active !== current) return
        const aborted = session.aborted()
        report('session ended', { aborted })
        if (aborted || current.writer) {
          current.stop.abort()
          void current.writer?.discard()
        }
        finishTransfer(current, aborted ? '对端取消了文件传输。' : '文件传输已结束。')
      })
      keepAlive()
      if (direction === 'upload') {
        uploadSession = session as UploadSession
        update('choose', '终端正在等待上传，请选择文件。')
      } else {
        const receiver = session as DownloadSession
        clearDownloads()
        update('download', '正在接收文件…')
        receiver.on('offer', (offer) => {
          void receiveFile(current, offer).catch((error: unknown) => {
            if (active === current) stopTransfer(current.target ? '接收或保存文件失败。' : '文件接收失败。', 'receive_file_failed', error)
          })
        })
        if (options.downloadTarget) current.target = options.downloadTarget
        void receiver.start().catch((error: unknown) => {
          if (active === current) stopTransfer('无法开始文件接收。', 'receiver_start_failed', error)
        })
      }
    },
  })
  let sentry = createSentry()

  // Sentry can miss split initial headers; the upload parser also rejects
  // partial replies. Reassemble hex headers without buffering download data.
  function consumeHeaders(chunk: Uint8Array<ArrayBuffer>) {
    let data = chunk
    if (pending.length) {
      data = new Uint8Array(pending.length + chunk.length)
      data.set(pending)
      data.set(chunk, pending.length)
      pending = new Uint8Array(0)
    }
    const forward = (bytes: Uint8Array<ArrayBuffer>) => {
      if (active) sentry.consume(Array.from(bytes))
      else options.output(bytes)
    }
    let plain = 0
    for (let i = data.indexOf(hexPrefix[0]); i >= 0; i = data.indexOf(hexPrefix[0], i + 1)) {
      const length = hexHeaderLength(data, i, !uploadSession)
      if (length < 0) continue
      if (i > plain) forward(data.subarray(plain, i))
      if (length === 0) {
        pending = data.slice(i)
        return
      }
      plain = i + length
      // lrzsz can retry ZRQINIT before the receiver handshake finishes.
      // The confirmed receiver cannot accept a second initial header.
      if (active && !uploadSession && data[i + 4] === 0x30 && data[i + 5] === 0x30) {
        i = plain - 1
        continue
      }
      detecting = !active
      try {
        if (detecting) debug('initial hex header assembled', { bytes: length })
        sentry.consume(Array.from(data.subarray(i, plain)))
      } finally {
        detecting = false
      }
      if (active && !uploadSession) break
      i = plain - 1
    }
    if (plain < data.length) forward(data.subarray(plain))
  }

  return {
    get busy() { return !!active },
    consume(data: ArrayBuffer) {
      if (disposed) return
      try {
        if (active) receivedBytes += data.byteLength
        keepAlive()
        if (active && !uploadSession && !pending.length) sentry.consume(data)
        else consumeHeaders(new Uint8Array(data))
      } catch (error) {
        if (!active) report('handshake parser failed', { errorType: typeof error })
        stopTransfer('文件传输异常，已取消。', 'protocol_parse_failed', error)
      }
    },
    async upload(files: readonly File[]) {
      const current = active
      const sender = uploadSession
      if (!current || !sender || mode !== 'choose' || files.length === 0) return
      const { signal } = current.stop
      try {
        for (const [index, file] of files.entries()) {
          phase = 'upload_offer'
          report('upload offering file', { fileBytes: file.size })
          progress = { file: index + 1, count: files.length, bytes: 0, total: file.size }
          const label = `正在上传：${file.name}`
          update('upload', label)
          const transfer = await untilStopped(sender.send_offer({
            name: file.name, size: file.size, mtime: new Date(file.lastModified),
          }), signal)
          report('upload offer answered', { accepted: !!transfer, fileBytes: file.size })
          if (!transfer) continue
          phase = 'upload_data'
          let lastUpdate = 0
          for (let offset = 0; offset < file.size; offset += chunkSize) {
            while (options.bufferedAmount() > 512 * 1024) {
              await new Promise((resolve) => setTimeout(resolve, 20))
              signal.throwIfAborted()
            }
            const data = await file.slice(offset, offset + chunkSize).arrayBuffer()
            signal.throwIfAborted()
            transfer.send(new Uint8Array(data))
            keepAlive()
            const bytes = offset + data.byteLength
            const now = performance.now()
            if (bytes === file.size || now - lastUpdate >= 100) {
              progress = { file: index + 1, count: files.length, bytes, total: file.size }
              lastUpdate = now
              update('upload', label)
            }
          }
          phase = 'upload_wait_file_ack'
          update('upload', `等待对端确认：${file.name}`)
          report('upload waiting for file acknowledgement')
          await untilStopped(transfer.end(), signal)
          report('upload file acknowledged')
        }
        phase = 'upload_wait_session_end'
        report('upload closing session')
        await untilStopped(sender.close(), signal)
      } catch (error) {
        if (active === current) stopTransfer('文件上传失败。', 'upload_failed', error)
      }
    },
    cancel() { stopTransfer('已取消文件传输。', 'user_cancel') },
    disconnect() {
      if (pending.length && !active) {
        debug('incomplete hex header at disconnect', { bytes: pending.length })
        options.output(pending)
      }
      pending = new Uint8Array(0)
      stopTransfer('连接已断开，文件传输中止。', 'socket_closed')
    },
    clearDownloads,
    dispose() {
      disposed = true
      stopTransfer('', 'terminal_disposed')
      clearDownloads()
    },
  }
}
