import { useEffect, useRef, useState, type JSX, type KeyboardEvent } from 'react'
import { Alert, Button, Dropdown, Input, Menu, Message, Modal } from '@arco-design/web-react'
import { IconClose, IconFullscreen, IconFullscreenExit, IconQuestionCircle } from '@arco-design/web-react/icon'
import { useThemeStore, type ThemeMode } from '@/store/theme'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WS } from '@/config/endpoints'
import { terminalZmodem, type TransferState } from '@/utils/terminalZmodem'
import { browserSave, streamDownload } from '@/utils/terminalDownload'
import { terminalInputChunks } from '@/utils/terminalInput'
import { maxPanes, workspaceAction, initialWorkspace, transferDownloadCommand, type SplitDirection } from '@/utils/terminalWorkspace'
import '@xterm/xterm/css/xterm.css'
import './TerminalPage.scss'

const terminalThemes = {
  light: { background: '#fffbff', foreground: '#313431', cursor: '#313431', cursorAccent: '#fffbff', selectionBackground: '#c9def8', brightBlack: '#626972', blue: '#175ba9', brightBlue: '#205aac', yellow: '#815e00', brightYellow: '#866100', green: '#187143', brightGreen: '#217945', cyan: '#006d78', brightCyan: '#006e78', white: '#4c5058', brightWhite: '#313431' },
  dark: { background: '#111827' },
}

function openTerminal(host: HTMLDivElement, update: (state: TransferState) => void, connected: (ready: boolean) => void) {
  const term = new Terminal({ cursorBlink: true, scrollback: 2000, theme: terminalThemes[useThemeStore.getState().mode] })
  const fit = new FitAddon()
  term.loadAddon(fit)
  term.open(host)
  // Fit before connecting so the PTY and the recording header start at the real size.
  const fitVisible = () => { if (host.clientWidth && host.clientHeight) fit.fit() }
  fitVisible()
  const ws = new WebSocket(WS.terminal(term))
  const debugEnabled = new URLSearchParams(window.location.search).get('zmodem_debug') === '1'
  const debug = (event: string, fields?: Record<string, string | number | boolean>) => {
    if (debugEnabled) console.info('[WebShell ZMODEM]', event, fields ?? {})
  }
  let loggedClosedSend = false
  ws.binaryType = 'arraybuffer'

  const encoder = new TextEncoder()
  const send = (data: Uint8Array<ArrayBuffer>) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(data)
    else if (data[0] === 0 && !loggedClosedSend) {
      loggedClosedSend = true
      debug('protocol frame not sent', { readyState: ws.readyState, frameBytes: data.length })
    }
  }
  const zmodem = terminalZmodem({
    send,
    debug,
    bufferedAmount: () => ws.bufferedAmount,
    downloadTarget: streamDownload,
    saveDownload: browserSave,
    output: (data) => term.write(data),
    update,
  })
  let protocol = false
  const selectedTransfer = () => zmodem
  const sendInput = (data: Uint8Array<ArrayBuffer>, utf8: boolean) => {
    if (selectedTransfer().busy) return
    for (const chunk of terminalInputChunks(data, utf8)) {
      if (ws.readyState !== WebSocket.OPEN) break
      send(chunk)
    }
  }
  const input = term.onData((data) => sendInput(encoder.encode(data), true))
  const binary = term.onBinary((data) => sendInput(Uint8Array.from(data, (char) => char.charCodeAt(0)), false))
  const sendSize = ({ cols, rows }: { cols: number; rows: number }) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ cols, rows }))
  }
  const size = term.onResize(sendSize)
  const observer = new ResizeObserver(fitVisible)
  observer.observe(host)

  const stopInteraction = () => {
    observer.disconnect()
    input.dispose()
    binary.dispose()
    size.dispose()
  }
  ws.onopen = () => {
    debug('socket opened')
    fitVisible()
    sendSize(term)
  }
  ws.onmessage = ({ data }: MessageEvent<string | ArrayBuffer>) => {
    if (typeof data === 'string') {
      let message
      try { message = JSON.parse(data) } catch { return }
      if (!protocol && message?.zmodem === true) {
        protocol = true
        connected(true)
        debug('server capability received', { protocol: 'zmodem' })
      }
    } else if (protocol) zmodem.consume(data)
    else term.write(new Uint8Array(data))
  }
  ws.onerror = () => debug('socket error', { readyState: ws.readyState })
  ws.onclose = ({ code, wasClean }) => {
    const transfer = selectedTransfer()
    debug('socket closed', { code, wasClean, zmodemEnabled: protocol, transferActive: transfer.busy })
    transfer.disconnect()
    connected(false)
    const message = code === 1000 ? '终端会话已结束。' : `终端连接已断开（${code}）。`
    term.writeln(`\r\n${message}`)
    // Keep the last screen and completed downloads available until the page is closed.
    stopInteraction()
  }
  return {
    get transfer() { return selectedTransfer() },
    debug,
    focus: () => { fitVisible(); term.focus() },
    selection: () => term.getSelection(),
    clear: () => { term.clear(); term.focus() },
    setTheme: (mode: ThemeMode) => { term.options.theme = terminalThemes[mode] },
    paste: (text: string) => {
      if (ws.readyState !== WebSocket.OPEN || zmodem.busy) return false
      term.paste(text)
      term.focus()
      return true
    },
    command: (text: string) => {
      if (!protocol || ws.readyState !== WebSocket.OPEN || zmodem.busy) return false
      term.input(`${text}\r`, true)
      return true
    },
    close() {
      ws.onopen = ws.onmessage = ws.onerror = ws.onclose = null
      zmodem.dispose()
      if (ws.readyState < WebSocket.CLOSING) ws.close()
      stopInteraction()
      term.dispose()
    },
  }
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / 1024 ** 2).toFixed(1)} MiB`
}

function TerminalPane({ id, active, select, close, split, canSplit }: {
  id: number; active: boolean; select: () => void; close: () => void
  split: (direction: SplitDirection) => void; canSplit: boolean
}): JSX.Element {
  const host = useRef<HTMLDivElement>(null)
  const pane = useRef<HTMLElement>(null)
  const uploadPicker = useRef<HTMLInputElement>(null)
  const pending = useRef<{ files: File[] | undefined; timer: number } | undefined>(undefined)
  const [waiting, setWaiting] = useState(false)
  const [connected, setConnected] = useState(false)
  const [downloading, setDownloading] = useState(false)
  const [paths, setPaths] = useState('')
  const [pathError, setPathError] = useState('')
  const [expanded, setExpanded] = useState(true)
  const picker = useRef<HTMLInputElement>(null)
  const session = useRef<ReturnType<typeof openTerminal> | undefined>(undefined)
  const [transfer, setTransfer] = useState<TransferState>({ mode: 'idle', message: '', downloads: [] })
  const [surface, setSurface] = useState<'upload' | 'download'>()
  const [transferError, setTransferError] = useState('')
  const [pickerFallback, setPickerFallback] = useState(false)
  const transferring = transfer.mode !== 'idle'

  function openPicker(automatic = false) {
    const input = picker.current
    if (!input) return
    try {
      if (typeof input.showPicker === 'function') {
        input.showPicker()
        setPickerFallback(false)
      } else {
        // click() can be silently blocked. On older browsers keep the manual entry available.
        setPickerFallback(true)
        if (!automatic) input.click()
        return
      }
      session.current?.debug('native file picker requested')
    } catch {
      setPickerFallback(true)
      session.current?.debug('native file picker blocked', { userActivation: navigator.userActivation?.isActive ?? false })
    }
  }

  function cancelUpload() {
    session.current?.transfer.cancel()
    session.current?.focus()
  }

  function closeTransfer() {
    session.current?.transfer.clearDownloads()
    setSurface(undefined)
  }

  function clearPending() {
    clearTimeout(pending.current?.timer)
    pending.current = undefined
  }

  function requestTransfer(command: string, files?: File[]) {
    if (pending.current || !session.current?.command(command)) return false
    setTransferError('')
    setWaiting(true)
    pending.current = { files, timer: window.setTimeout(() => {
      clearPending()
      setWaiting(false)
      setTransferError('未收到 ZMODEM 握手。请确认当前面板处于空闲 Shell 提示符，已安装 lrzsz，且没有在 tmux 中运行。')
    }, 12_000) }
    return true
  }

  async function copySelection() {
    const text = session.current?.selection()
    if (!text) { Message.info('请先选中终端文本。'); return }
    try {
      await navigator.clipboard.writeText(text)
      Message.success('已复制选中文本。')
    } catch { Message.warning('浏览器未授予剪贴板权限，请使用系统复制快捷键。') }
  }

  async function pasteClipboard() {
    const current = session.current
    try {
      const text = await navigator.clipboard.readText()
      if (session.current !== current || pane.current?.closest('[hidden]') || pane.current?.dataset.active !== 'true') return
      if (current?.paste(text)) Message.success('已粘贴到终端。')
      else Message.info('当前终端无法接收输入。')
    } catch { Message.warning('浏览器未授予剪贴板权限，请使用系统粘贴快捷键。') }
  }

  useEffect(() => {
    if (active && !pane.current?.contains(document.activeElement)) session.current?.focus()
  }, [active])

  useEffect(() => {
    const element = host.current
    if (!element) return
    const input = picker.current
    input?.addEventListener('cancel', cancelUpload)
    // Let the development StrictMode setup/cleanup cycle finish before opening a shell.
    const start = window.setTimeout(() => {
      session.current = openTerminal(element, (state) => {
        setTransfer(state)
        const queued = pending.current
        if (state.mode === 'choose' || state.mode === 'download') {
          clearPending()
          setWaiting(false)
          setTransferError('')
          setExpanded(true)
        }
        if (state.mode === 'upload' || state.mode === 'download') setSurface(state.mode)
        if (state.mode !== 'choose') setPickerFallback(false)
        if (state.mode === 'choose') {
          if (queued?.files) {
            void session.current?.transfer.upload(queued.files)
          } else {
            setSurface(undefined)
            // A background pane should never open a system dialog.
            if (!element.closest('[hidden]') && pane.current?.dataset.active === 'true') openPicker(true)
            else setPickerFallback(true)
          }
        }
      }, (ready) => {
        setConnected(ready)
        if (!ready) { clearPending(); setWaiting(false) }
      })
      if (pane.current?.dataset.active === 'true' && !element.closest('[hidden]')) session.current.focus()
    }, 0)
    return () => {
      input?.removeEventListener('cancel', cancelUpload)
      window.clearTimeout(start)
      clearPending()
      session.current?.close()
      session.current = undefined
    }
  }, [])

  useEffect(() => useThemeStore.subscribe(({ mode }) => session.current?.setTheme(mode)), [])

  const transferUnavailable = !connected || transferring || waiting

  const shortcuts = [
    { key: 'copy', label: '复制', hint: 'Ctrl Shift C', code: 'KeyC', shift: true, disabled: false },
    { key: 'paste', label: '粘贴', hint: 'Ctrl Shift V', code: 'KeyV', shift: true, disabled: !connected || transferring },
    { key: 'side-by-side', label: '垂直拆分', hint: 'Ctrl Shift S', code: 'KeyS', shift: true, disabled: !canSplit, divider: true },
    { key: 'stacked', label: '水平拆分', hint: 'Ctrl Shift D', code: 'KeyD', shift: true, disabled: !canSplit },
    { key: 'clear', label: '清屏', hint: 'Ctrl K', code: 'KeyK', shift: false, disabled: transferring, divider: true },
  ] as const

  function runAction(key: string) {
    select()
    switch (key) {
      case 'copy': void copySelection(); break
      case 'paste': void pasteClipboard(); break
      case 'clear': session.current?.clear(); break
      case 'upload': uploadPicker.current?.click(); break
      case 'download': setPaths(''); setPathError(''); setDownloading(true); break
      case 'side-by-side': case 'stacked': split(key); break
    }
  }

  function handleShortcut(event: KeyboardEvent<HTMLDivElement>) {
    if (event.nativeEvent.isComposing || !event.ctrlKey || event.altKey || event.metaKey) return
    const action = shortcuts.find((item) => item.code === event.code && item.shift === event.shiftKey)
    if (!action) return
    // Do not also send the mapped shortcut to the PTY or invoke the browser action.
    event.preventDefault()
    event.stopPropagation()
    if (event.type === 'keydown' && !event.repeat && !action.disabled) runAction(action.key)
  }

  const contextMenu = (
    <Menu className="terminal-actions" onClickMenuItem={runAction}>
      {shortcuts.map((action) => (
        <Menu.Item key={action.key} disabled={action.disabled} className={'divider' in action ? 'terminal-actions__divider' : ''}>
          <span className="terminal-actions__row">{action.label}<kbd>{action.hint}</kbd></span>
        </Menu.Item>
      ))}
      <Menu.Item key="upload" disabled={transferUnavailable} className="terminal-actions__divider">上传文件</Menu.Item>
      <Menu.Item key="download" disabled={transferUnavailable}>下载文件</Menu.Item>
    </Menu>
  )

  return (
    <section ref={pane} className="terminal-pane" aria-label={`终端 ${id}`} data-active={active} onFocus={select} onPointerDown={select}>
      <button type="button" className="terminal-page__icon terminal-pane__close" aria-label={`关闭终端 ${id}`} title="关闭此终端"
        onPointerDown={(event) => { event.preventDefault(); event.stopPropagation() }}
        onFocus={(event) => event.stopPropagation()} onClick={close}><IconClose /></button>
      {waiting && <small role="status">等待当前 Shell 发出文件传输握手…</small>}
      {transferError && <Alert type="error" content={transferError} closable onClose={() => setTransferError('')} />}
      <div className="terminal-pane__body">
      <Dropdown trigger="contextMenu" position="bl" droplist={contextMenu}>
        <div ref={host} className="terminal-page__screen" onKeyDownCapture={handleShortcut} onKeyUpCapture={handleShortcut} />
      </Dropdown>
      <input ref={uploadPicker} hidden aria-label="选择文件并上传到当前目录" type="file" multiple onChange={({ currentTarget }) => {
        const files = Array.from(currentTarget.files ?? [])
        currentTarget.value = ''
        if (files.length && !requestTransfer('rz', files)) setTransferError('当前终端无法启动上传。')
      }} />
      <input ref={picker} hidden aria-label="选择上传文件" type="file" multiple onChange={({ currentTarget }) => {
        const files = Array.from(currentTarget.files ?? [])
        currentTarget.value = ''
        if (files.length) void session.current?.transfer.upload(files)
        else cancelUpload()
        session.current?.focus()
      }} />
      {transfer.mode === 'choose' && pickerFallback && (
        <div className="terminal-page__upload-picker" role="group" aria-label="文件上传（rz）">
          <span role="status">{transfer.message}</span>
          <Button type="primary" onClick={() => openPicker()}>选择文件</Button>
          <Button status="danger" onClick={cancelUpload}>取消上传</Button>
        </div>
      )}
      </div>
      {surface && <div className="terminal-pane__transfer" aria-label="文件传输">
        <div className="terminal-pane__toolbar">
          <Button size="mini" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
            {expanded ? '收起' : '展开'}{surface === 'upload' ? '上传' : '下载'}{transfer.downloads.length ? ` (${transfer.downloads.length})` : ''}
          </Button>
          {transferring ? (
            <Button size="mini" status="danger" onClick={cancelUpload}>取消传输</Button>
          ) : <Button size="mini" onClick={closeTransfer}>清除记录</Button>}
        </div>
        {expanded && <div className="terminal-page__transfer">
          <p role="status">{transfer.message}</p>
          {surface === 'upload' && transfer.progress && (
            <label className="terminal-page__upload-progress">
              文件 {transfer.progress.file}/{transfer.progress.count} · {formatBytes(transfer.progress.bytes)} / {formatBytes(transfer.progress.total)}
              <progress max={transfer.progress.total || 1} value={transfer.progress.total ? transfer.progress.bytes : undefined} />
            </label>
          )}
          {surface === 'download' && transfer.downloads.length > 0 && (
            <ul aria-label="已接收的文件">
              {transfer.downloads.map(({ name, url }) => (
                <li key={url ?? name}>{url ? <a href={url} download={name}>重新下载 {name}</a> : <>{name}（查看浏览器下载列表）</>}</li>
              ))}
            </ul>
          )}
        </div>}
      </div>}
      <Modal className="terminal-transfer-modal" title={`从终端 ${id} 下载文件`} visible={active && downloading} onCancel={() => setDownloading(false)} onOk={() => {
        const command = transferDownloadCommand(paths, true)
        if (!command) { setPathError('请输入有效文件路径，每行一个；复杂文件名请直接在终端运行 sz。'); return }
        if (!requestTransfer(command)) { setPathError('当前终端无法启动下载。'); return }
        setDownloading(false)
        setPaths('')
        session.current?.focus()
      }}>
        <p>请确认终端停在空白 Shell 提示符。相对路径以当前 Shell 目录为准，每行一个文件。</p>
        <p>文件将自动交给浏览器下载，保存位置由浏览器设置决定。</p>
        <Input.TextArea aria-label="下载文件路径" value={paths} onChange={setPaths} autoSize={{ minRows: 3, maxRows: 6 }} />
        {pathError && <p role="alert">{pathError}</p>}
      </Modal>
    </section>
  )
}

export function TerminalPage(): JSX.Element {
  const [workspace, setWorkspace] = useState(initialWorkspace)
  const [maximized, setMaximized] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const dispatch = (action: Parameters<typeof workspaceAction>[1]) => setWorkspace((state) => workspaceAction(state, action))
  return (
    <section className="terminal-page" data-maximized={maximized} aria-label="WebShell 工作台">
      <div className="terminal-page__header">
        <span className="terminal-page__title">WebShell</span>
        <div className="terminal-page__controls">
          <button type="button" className="terminal-page__icon" aria-label="终端帮助" title="终端帮助" onClick={() => setHelpOpen(true)}><IconQuestionCircle /></button>
          <button type="button" className="terminal-page__icon" aria-label={maximized ? '退出最大化' : '最大化'} title={maximized ? '退出最大化' : '最大化'}
            aria-pressed={maximized} onClick={() => setMaximized((value) => !value)}>{maximized ? <IconFullscreenExit /> : <IconFullscreen />}</button>
        </div>
      </div>
      {workspace.panes.length === 0 ? (
        <div className="terminal-page__empty"><Button onClick={() => dispatch({ type: 'open-pane' })}>打开终端</Button></div>
      ) : (
        <div className="terminal-page__grid" data-count={workspace.panes.length} data-direction={workspace.direction}>
          {workspace.panes.map((id) => (
            <TerminalPane key={id} id={id} active={workspace.active === id}
              select={() => dispatch({ type: 'select-pane', id })}
              close={() => dispatch({ type: 'close-pane', id })}
              split={(direction) => dispatch({ type: 'split', direction })}
              canSplit={workspace.panes.length < maxPanes} />
          ))}
        </div>
      )}
      <Modal title="终端帮助" visible={helpOpen} footer={null} onCancel={() => setHelpOpen(false)} className="terminal-transfer-modal">
        <ul>
          <li>终端输出会保存为录像。</li>
          <li>最多可打开四个终端分屏。点击分屏右上角的「×」可单独关闭。</li>
          <li>分屏、上传和下载针对当前选中的终端。右键菜单提供复制、粘贴、分屏、清屏、上传和下载。</li>
          <li>文件传输需要 Shell 安装 lrzsz。请在空白 Shell 提示符下操作，不要在 tmux 内运行 rz 或 sz。</li>
        </ul>
      </Modal>
    </section>
  )
}
