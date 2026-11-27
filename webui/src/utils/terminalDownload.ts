export interface DownloadWriter {
  queuedBytes: number
  push(chunk: number[]): void
  commit(): Promise<void>
  discard(): void
}

export type DownloadTarget = (
  name: string, signal: AbortSignal, onDrain: () => void, onError: (error: unknown) => void,
) => Promise<DownloadWriter | undefined>

const queueLimit = 8 * 1024 * 1024
const startupTimeout = 5_000
const workerPath = '/assets/terminal-download/'
let registration: Promise<ServiceWorker> | undefined

function deferred() {
  let resolve!: () => void
  let reject!: (error: Error) => void
  const promise = new Promise<void>((ok, fail) => { resolve = ok; reject = fail })
  return { promise, resolve, reject }
}

async function startWorker(): Promise<ServiceWorker> {
  const registered = await navigator.serviceWorker.register(`${workerPath}worker.js`)
  const worker = registered.active ?? registered.installing ?? registered.waiting
  if (!worker) throw new Error('Download worker unavailable')
  if (worker.state === 'activated') return worker
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => finish(new Error('Download worker activation timed out')), startupTimeout)
    function finish(error?: Error) {
      clearTimeout(timer)
      worker!.removeEventListener('statechange', check)
      if (error) reject(error)
      else resolve()
    }
    function check() {
      if (worker!.state === 'activated') finish()
      else if (worker!.state === 'redundant') finish(new Error('Download worker not activated'))
    }
    worker.addEventListener('statechange', check)
    check()
  })
  return worker
}

export function browserSave(url: string, name: string) {
  const link = document.createElement('a')
  link.href = url
  link.download = name
  document.body.append(link)
  try { link.click() } finally { link.remove() }
}

// The worker responds to the browser's own download request. Data enters the
// response only on demand so a slow browser can pace windowed ZMODEM senders.
export const streamDownload: DownloadTarget = async (name, signal, onDrain, onError) => {
  if (!navigator.serviceWorker || !crypto.randomUUID) return
  registration ??= startWorker().catch((error: unknown) => { registration = undefined; throw error })
  const worker = await registration.catch(() => undefined)
  signal.throwIfAborted()
  if (!worker) return

  const id = crypto.randomUUID()
  const { port1: port, port2 } = new MessageChannel()
  let chunks: Uint8Array<ArrayBuffer>[] = []
  let queuedBytes = 0
  let requested = false
  let ended = false
  let closed = false
  let started = false
  let frame: HTMLIFrameElement | undefined
  let timer: ReturnType<typeof setTimeout>
  const ready = deferred()
  let committed: ReturnType<typeof deferred> | undefined
  function close(error?: Error) {
    if (closed) return
    closed = true
    clearTimeout(timer)
    signal.removeEventListener('abort', abort)
    port.close()
    frame?.remove()
    chunks = []
    queuedBytes = 0
    if (error) {
      if (!started) ready.reject(error)
      else if (committed) committed.reject(error)
      else if (!signal.aborted) onError(error)
    } else committed?.resolve()
  }
  function abort() {
    if (!closed) port.postMessage('abort')
    close(new Error('Browser download interrupted'))
  }
  function send() {
    if (!requested || closed) return
    if (queuedBytes) {
      const batch = new Uint8Array(queuedBytes)
      let offset = 0
      for (const part of chunks) { batch.set(part, offset); offset += part.length }
      requested = false
      chunks = []
      queuedBytes = 0
      port.postMessage(batch.buffer, [batch.buffer])
      onDrain()
    } else if (ended) {
      requested = false
      port.postMessage('end')
    }
  }
  port.onmessage = ({ data }) => {
    if (closed) return
    if (data === 'ready') {
      try {
        frame = document.createElement('iframe')
        frame.hidden = true
        frame.src = `${workerPath}file/${id}`
        document.body.append(frame)
      } catch { abort() }
    } else if (data === 'pull') {
      if (!started) {
        started = true
        clearTimeout(timer)
        ready.resolve()
      }
      requested = true
      send()
    } else if (data === 'committed') close()
    else if (data === 'cancel') close(new Error('Browser cancelled download'))
  }
  signal.addEventListener('abort', abort, { once: true })
  timer = setTimeout(abort, startupTimeout)
  try {
    worker.postMessage({ id, name }, [port2])
  } catch {
    abort()
    port2.close()
    registration = undefined
  }
  if (!await ready.promise.then(() => true, () => false)) {
    signal.throwIfAborted()
    return undefined // No body consumed; the bounded compatibility download can still run.
  }
  return {
    get queuedBytes() { return queuedBytes },
    push(chunk) {
      if (closed || ended) throw new Error('Browser download unavailable')
      if (queuedBytes + chunk.length > queueLimit) throw new Error('Browser download queue limit exceeded')
      if (!chunk.length) return
      chunks.push(Uint8Array.from(chunk))
      queuedBytes += chunk.length
      send()
    },
    commit() {
      if (closed) return Promise.reject(new Error('Browser download unavailable'))
      if (!committed) {
        committed = deferred()
        ended = true
        send()
      }
      return committed.promise
    },
    discard: abort,
  }
}
