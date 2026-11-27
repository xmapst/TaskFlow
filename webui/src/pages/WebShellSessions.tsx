import { useEffect, useRef, useState } from 'react'
import { Alert, Button, Modal, Space } from '@arco-design/web-react'
import { IconFullscreen, IconFullscreenExit } from '@arco-design/web-react/icon'
import { Terminal } from '@xterm/xterm'
import { EmptyHint, LiveBadge, LoadingBlock, MetaItem, MetaList, Page } from '@/components/common/Layout'
import { ListCard, ListGrid, ListTable, ListToolbar, ListViewSwitch, useListView } from '@/components/common/ListView'
import { stream } from '@/api/socket'
import { fitTerminalViewport } from '@/utils/terminalViewport'
import '@xterm/xterm/css/xterm.css'
import '../components/common/TermModal.scss'
import './WebShellSessions.scss'

interface Session { id: string; connectedAt: string; remoteAddr?: string }

/** 服务端推来的整份列表 */
interface SessionFrame { sessions?: Session[] }

function LiveTerminal({ session }: { session: Session }) {
  const viewport = useRef<HTMLDivElement>(null)
  const host = useRef<HTMLDivElement>(null)
  const [message, setMessage] = useState('正在连接…')
  useEffect(() => {
    if (!viewport.current || !host.current) return
    const term = new Terminal({ disableStdin: true, scrollback: 1000, theme: { background: '#000000' } })
    for (const code of [0, 1, 2, 8, 52]) term.parser.registerOscHandler(code, () => true)
    term.open(host.current)
    const stopFitting = fitTerminalViewport(viewport.current, host.current)
    const resize = (cols: unknown, rows: unknown) => {
      if (typeof cols === 'number' && typeof rows === 'number' && Number.isInteger(cols) && Number.isInteger(rows) &&
          cols > 0 && rows > 0 && cols <= 1000 && rows <= 1000 && cols * rows <= 150_000) term.resize(cols, rows)
    }
    // 串行写入：事件帧是连续到达的，终端解析必须保持顺序
    let pending = Promise.resolve()
    const write = (text: string) => {
      pending = pending.then(() => new Promise<void>((resolve) => term.write(text, () => resolve())))
    }
    // 首帧是录像头；之后每帧是一行 asciinema v2 事件
    let started = false
    const subscription = stream().subscribe('terminal-watch', { id: session.id }, {
      onUpdate: (frame) => {
        const event = frame.data
        if (!started) {
          const header = event as { version?: unknown; width?: unknown; height?: unknown } | null
          if (header?.version !== 2) {
            subscription.close()
            setMessage('会话数据格式无效。')
            return
          }
          resize(header.width, header.height)
          started = true
          setMessage('实时观看 · 只读')
          return
        }
        if (!Array.isArray(event) || typeof event[2] !== 'string') return
        if (event[1] === 'r') {
          const size = /^(\d{1,4})x(\d{1,4})$/.exec(event[2])
          if (size) resize(Number(size[1]), Number(size[2]))
        } else if (event[1] === 'o') {
          write(event[2])
        }
      },
      onDone: () => setMessage('观看连接已结束。'),
      onError: (reason) => setMessage(reason === 'session not found' ? '会话已结束或不存在。' : reason),
      onClose: () => setMessage('观看连接中断，正在重连…'),
    })
    return () => { subscription.close(); stopFitting(); term.dispose() }
  }, [session.id])
  return <>
    <div ref={viewport} className="live-terminal__viewport" aria-label="实时终端"><div ref={host} className="live-terminal__screen" /></div>
    <p role="status" className="live-terminal__status">{message}</p>
  </>
}

export function WebShellSessionsPage() {
  const [view, switchView] = useListView('aef-session-view', 'table')
  const [refresh, setRefresh] = useState(0)
  const [connection, setConnection] = useState<'connecting' | 'live' | 'reconnecting' | 'closed'>('connecting')
  const [rows, setRows] = useState<Session[]>([])
  const [watching, setWatching] = useState<Session | null>(null)
  const [expanded, setExpanded] = useState(false)
  const [disconnect, setDisconnect] = useState<Session | null>(null)
  const [disconnecting, setDisconnecting] = useState(false)
  const [actionError, setActionError] = useState('')
  useEffect(() => {
    const subscription = stream().subscribe('terminal-sessions', undefined, {
      // 首帧即当前全量列表，之后每次增删都推一份新的全量列表
      onUpdate: (frame) => {
        setRows((frame.data as SessionFrame | undefined)?.sessions ?? [])
        setConnection('live')
      },
      // 连接掉了客户端会自己重连，重连后的首帧会把列表重新对齐
      onClose: () => setConnection('reconnecting'),
      onError: () => setConnection('closed'),
    })
    return () => subscription.close()
  }, [refresh])
  const connectedTime = (row: Session) => new Date(row.connectedAt).toLocaleString()
  const actions = (row: Session) => <Space wrap>
    <Button size="small" onClick={() => setWatching(row)}>观看</Button>
    <Button size="small" status="danger" onClick={() => { setActionError(''); setDisconnect(row) }}>强制断开</Button>
  </Space>
  return <>
    <Page toolbar={<ListToolbar left={<span>{rows.length} 个会话</span>} right={<>
      <LiveBadge live={connection === 'live'} {...(connection === 'closed' ? { label: '已断开' } : {})} />
      <ListViewSwitch value={view} onChange={switchView} />
    </>} />}>
      <div className="webshell-sessions">
        {connection === 'reconnecting' && <Alert type="warning" content="列表连接中断，正在重连；当前列表可能不是最新。" />}
        {connection === 'closed' && <Alert type="error" content="无法连接在线会话列表。" action={<Button size="small" onClick={() => { setConnection('connecting'); setRefresh((value) => value + 1) }}>重试</Button>} />}
        {actionError && <Alert type="error" content={actionError} />}
        {connection === 'connecting' ? <LoadingBlock tip="加载在线会话" /> : rows.length === 0 ?
          <EmptyHint description={connection === 'live' ? '暂无在线会话' : '等待会话列表'} /> : view === 'table' ?
          <ListTable<Session> rowKey="id" data={rows} scroll={{ x: 680 }} columns={[
            { title: '会话 ID', dataIndex: 'id', ellipsis: true },
            { title: '连接时间', width: 175, render: (_, row) => connectedTime(row) },
            { title: '对端地址', dataIndex: 'remoteAddr', width: 150, ellipsis: true },
            { title: '操作', width: 160, render: (_, row) => actions(row) },
          ]} /> : <ListGrid>{rows.map((row) => <ListCard key={row.id} title={row.id} footer={actions(row)}>
            <MetaList><MetaItem label="连接时间" value={connectedTime(row)} mono /><MetaItem label="对端地址" value={row.remoteAddr} mono /></MetaList>
          </ListCard>)}</ListGrid>}
      </div>
    </Page>
    <Modal
      title={<div className="term-modal__title">
        <span title={watching?.id}>实时观看{watching ? ` · ${watching.id}` : ''}</span>
        <Button type="text" size="small" aria-label={expanded ? '退出最大化' : '最大化'}
          title={expanded ? '退出最大化' : '最大化'} icon={expanded ? <IconFullscreenExit /> : <IconFullscreen />}
          onClick={() => setExpanded((value) => !value)} />
      </div>}
      visible={watching !== null} footer={null}
      className={`term-modal live-terminal${expanded ? ' term-modal--expanded' : ''}`}
      onCancel={() => { setWatching(null); setExpanded(false) }}
    >
      {watching && <LiveTerminal key={watching.id} session={watching} />}
    </Modal>
    <Modal title="强制断开会话" visible={disconnect !== null} okText="断开" cancelText="取消" okButtonProps={{ status: 'danger' }}
      confirmLoading={disconnecting} onCancel={() => { if (!disconnecting) setDisconnect(null) }} onOk={async () => {
        if (!disconnect || disconnecting) return
        const id = disconnect.id
        setDisconnecting(true)
        try {
          await stream().call<void>('terminal.disconnect', { id })
        } catch (err) {
          const message = (err as Error).message
          setActionError(message === 'session not found' ? '会话已结束或不存在。' : message)
        }
        finally { setDisconnecting(false); setDisconnect(null) }
      }}>此操作将终止该 WebShell 连接。确认继续？</Modal>
  </>
}
