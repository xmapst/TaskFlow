// Only synthetic, one-use download URLs in this directory are intercepted.
const tickets = new Map()
const scope = new URL(self.registration.scope)

self.addEventListener('install', (event) => event.waitUntil(self.skipWaiting()))

self.addEventListener('message', (event) => {
  const { id, name } = event.data ?? {}
  const port = event.ports[0]
  if (!port || typeof id !== 'string' || !/^[a-f0-9-]{36}$/.test(id) || tickets.has(id) || typeof name !== 'string') return
  const filename = encodeURIComponent(name).replace(/['()*]/g, (char) => `%${char.charCodeAt(0).toString(16)}`)
  const timer = setTimeout(() => {
    tickets.delete(id)
    port.close()
  }, 15_000)
  tickets.set(id, { port, timer, filename })
  port.postMessage('ready')
})

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url)
  if (event.request.method !== 'GET' || url.origin !== scope.origin || !url.pathname.startsWith(`${scope.pathname}file/`)) return
  const id = url.pathname.slice(scope.pathname.length + 'file/'.length)
  const ticket = tickets.get(id)
  if (!ticket) {
    event.respondWith(new Response(null, { status: 404 }))
    return
  }
  tickets.delete(id)
  clearTimeout(ticket.timer)
  const { port, filename } = ticket
  let settle
  event.waitUntil(new Promise((resolve) => { settle = resolve }))
  let pulled
  function finish() {
    pulled?.()
    port.close()
    settle()
  }
  const stream = new ReadableStream({
    start(controller) {
      port.onmessage = ({ data }) => {
        if (data instanceof ArrayBuffer) {
          controller.enqueue(new Uint8Array(data))
          pulled?.()
          pulled = undefined
        } else if (data === 'end') {
          controller.close()
          port.postMessage('committed')
          finish()
        } else if (data === 'abort') {
          controller.error(new Error('Terminal download cancelled'))
          finish()
        }
      }
    },
    pull() {
      return new Promise((resolve) => {
        pulled = resolve
        port.postMessage('pull')
      })
    },
    cancel() {
      port.postMessage('cancel')
      finish()
    },
  }, { highWaterMark: 0 })
  event.respondWith(new Response(stream, { headers: {
    'Content-Type': 'application/octet-stream',
    'Content-Disposition': `attachment; filename="download"; filename*=UTF-8''${filename}`,
    'Cache-Control': 'no-store',
    'X-Content-Type-Options': 'nosniff',
  } }))
})
