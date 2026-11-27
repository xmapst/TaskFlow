import { useEffect, useRef, useState, type JSX } from 'react'
import { Alert, Button, Modal, Pagination } from '@arco-design/web-react'
import { IconFullscreen, IconFullscreenExit, IconPause, IconPlayArrow, IconRefresh } from '@arco-design/web-react/icon'
import { Terminal } from '@xterm/xterm'
import { EmptyHint, LoadingBlock, MetaItem, MetaList, Page } from '@/components/common/Layout'
import { ListCard, ListGrid, ListTable, ListToolbar, ListViewSwitch, useListView } from '@/components/common/ListView'
import { stream } from '@/api/socket'
import { fitTerminalViewport } from '@/utils/terminalViewport'
import '@xterm/xterm/css/xterm.css'
import '../components/common/TermModal.scss'
import './ReplayPage.scss'
const pageSize = 50
const maxBytes = 64 << 20

type Recording = { date: string; id: string; size: number }
type ReplayPage = { items: Recording[]; total: number }
type Frame = [time: number, kind: 'o' | 'r', value: string]
type Cast = { cols: number; rows: number; frames: Frame[] }

function validSize(cols: unknown, rows: unknown): cols is number {
  return typeof cols === 'number' && typeof rows === 'number' &&
    Number.isInteger(cols) && Number.isInteger(rows) && cols > 0 && rows > 0 &&
    cols <= 1000 && rows <= 1000 && cols * rows <= 150_000
}

function formatTime(seconds: number): string {
  const minutes = Math.floor(seconds / 60)
  const tail = String(Math.floor(seconds % 60)).padStart(2, '0')
  return minutes < 60 ? `${minutes}:${tail}` : `${Math.floor(minutes / 60)}:${String(minutes % 60).padStart(2, '0')}:${tail}`
}

function ReplayPlayer({ cast }: { cast: Cast }): JSX.Element {
  const stage = useRef<HTMLDivElement>(null)
  const host = useRef<HTMLDivElement>(null)
  const controls = useRef<{ play: (playing: boolean) => void; rate: (speed: number) => void; seek: (time: number) => void } | null>(null)
  const duration = cast.frames.at(-1)?.[0] ?? 0
  const [position, setPosition] = useState(0)
  const [speed, setSpeed] = useState(1)
  const [playing, setPlaying] = useState(true)

  useEffect(() => {
    const container = stage.current
    const element = host.current
    if (!container || !element) return
    const term = new Terminal({ cols: cast.cols, rows: cast.rows, disableStdin: true, scrollback: 1000, theme: { background: '#000000' } })
    // Recordings are untrusted. Ignore title, hyperlink and clipboard control sequences.
    for (const code of [0, 1, 2, 8, 52]) term.parser.registerOscHandler(code, () => true)
    term.open(element)
    const stopFitting = fitTerminalViewport(container, element)

    let next = 0
    let time = 0
    let rate = 1
    let running = true
    let rebuilding = false
    let pendingWrite = false
    let clock = performance.now()
    let lastUpdate = -Infinity
    let animation: number | undefined
    const requestTick = () => { animation ??= requestAnimationFrame(tick) }
    const advance = (now: number) => {
      if (running && !rebuilding) time = Math.min(duration, time + Math.max(0, now - clock) * rate / 1000)
      clock = now
    }
    const tick = (now: number) => {
      animation = undefined
      advance(now)
      if (!pendingWrite) {
        if ((cast.frames[next - 1]?.[0] ?? 0) > time) {
          term.reset()
          term.clear()
          term.resize(cast.cols, cast.rows)
          next = 0
        }
        // Batch output, but wait for xterm to parse it before applying a resize or reset.
        let output = ''
        const end = next + 512
        while (next < end) {
          const frame = cast.frames[next]
          if (!frame || frame[0] > time) break
          if (frame[1] === 'r') {
            if (output) break
            const size = /^(\d{1,4})x(\d{1,4})$/.exec(frame[2])
            if (size && validSize(Number(size[1]), Number(size[2]))) term.resize(Number(size[1]), Number(size[2]))
          } else {
            output += frame[2]
          }
          next++
          if (output.length >= 64 * 1024) break
        }
        if (output) {
          pendingWrite = true
          term.write(output, () => { pendingWrite = false })
        } else if ((cast.frames[next]?.[0] ?? Infinity) > time) {
          rebuilding = false
          if (running && next === cast.frames.length && time === duration) {
            running = false
            setPosition(duration)
            setPlaying(false)
          }
        }
      }
      if (running && now - lastUpdate >= 80) {
        setPosition(time)
        lastUpdate = now
      }
      if (running || rebuilding || pendingWrite) requestTick()
    }
    controls.current = {
      play(value) {
        advance(performance.now())
        running = value
        setPosition(time)
        setPlaying(value)
        requestTick()
      },
      rate(value) { advance(performance.now()); rate = value; setSpeed(value) },
      seek(value) { time = value; rebuilding = true; setPosition(value); requestTick() },
    }
    requestTick()
    return () => {
      controls.current = null
      if (animation !== undefined) cancelAnimationFrame(animation)
      stopFitting()
      term.dispose()
    }
  }, [cast, duration])

  const toggle = () => {
    if (!playing && position >= duration) controls.current?.seek(0)
    controls.current?.play(!playing)
  }
  return <>
    <div ref={stage} className="replay-stage" aria-label="终端录像"><div ref={host} className="replay-stage__screen" /></div>
    <div className="replay-controls">
      <input className="replay-controls__progress" type="range" aria-label="播放进度"
        aria-valuetext={`${formatTime(position)} / ${formatTime(duration)}`}
        min={0} max={duration || 1} step="any" value={position} disabled={duration === 0}
        style={{ backgroundSize: `${duration ? position / duration * 100 : 0}% 100%` }}
        onChange={({ currentTarget }) => controls.current?.seek(currentTarget.valueAsNumber)} />
      <div className="replay-controls__row">
        <button type="button" aria-label={playing ? '暂停' : '播放'} title={playing ? '暂停' : '播放'} onClick={toggle}>
          {playing ? <IconPause /> : <IconPlayArrow />}
        </button>
        <button type="button" aria-label="从头播放" title="从头播放" onClick={() => { controls.current?.seek(0); controls.current?.play(true) }}><IconRefresh /></button>
        <span className="replay-controls__time">{formatTime(position)} / {formatTime(duration)}</span>
        <label className="replay-controls__speed">
          <span>倍速</span>
          <select aria-label="播放速度" value={speed} onChange={({ currentTarget }) => controls.current?.rate(Number(currentTarget.value))}>
            {[0.5, 1, 2, 4].map((value) => <option key={value} value={value}>{value}×</option>)}
          </select>
        </label>
      </div>
    </div>
  </>
}

export function ReplayPage(): JSX.Element {
  const [view, switchView] = useListView('aef-replay-view', 'table')
  const [offset, setOffset] = useState(0)
  const [page, setPage] = useState<ReplayPage | null>(null)
  const [selected, setSelected] = useState<Recording | null>(null)
  const [cast, setCast] = useState<Cast | null>(null)
  const [listError, setListError] = useState('')
  const [castError, setCastError] = useState('')
  const [revision, setRevision] = useState(0)
  const [expanded, setExpanded] = useState(false)

  useEffect(() => {
    let cancelled = false
    stream().call<ReplayPage>('replay.list', { limit: pageSize, offset })
      .then((data) => {
        if (!data || !Array.isArray(data.items) || !Number.isSafeInteger(data.total) || data.total < 0) throw new Error('录像列表格式无效。')
        if (cancelled) return
        if (offset > 0 && offset >= data.total) setOffset(Math.max(0, Math.ceil(data.total / pageSize) - 1) * pageSize)
        else setPage(data)
      })
      .catch((error: Error) => { if (!cancelled) setListError(error.message) })
    return () => { cancelled = true }
  }, [offset, revision])

  useEffect(() => {
    if (!selected) return
    let text = ''
    let bytes = 0
    let previous = 0
    let cast: Cast | undefined
    let stopped = false
    const fail = (message: string) => {
      if (stopped) return
      stopped = true
      subscription.close()
      setCastError(message)
    }
    const parseLine = (line: string) => {
      if (!line.trim()) return
      const data: unknown = JSON.parse(line)
      if (!cast) {
        const header = data as { version?: unknown; width?: unknown; height?: unknown } | null
        if (header?.version !== 2 || typeof header.height !== 'number' || !validSize(header.width, header.height)) {
          throw new Error('录像头无效。')
        }
        cast = { cols: header.width, rows: header.height, frames: [] }
        return
      }
      if (!Array.isArray(data) || data.length !== 3 || typeof data[0] !== 'number' ||
        !Number.isFinite(data[0]) || data[0] < previous || typeof data[2] !== 'string') {
        throw new Error('录像事件格式无效。')
      }
      previous = data[0]
      if (data[1] === 'o' || data[1] === 'r') cast.frames.push([data[0], data[1], data[2]])
      if (cast.frames.length > 500_000) throw new Error('录像事件太多，无法在浏览器播放。')
    }
    // 服务端按块推 NDJSON 文本，块边界与换行无关，这里重新拼行
    const consume = () => {
      let start = 0
      for (let end = text.indexOf('\n'); end !== -1; end = text.indexOf('\n', start)) {
        parseLine(text.slice(start, end))
        start = end + 1
      }
      text = text.slice(start)
      if (text.length > 1 << 20) throw new Error('录像单行过长。')
    }
    const subscription = stream().subscribe('terminal-replay', { id: selected.id }, {
      onUpdate: (frame) => {
        if (stopped || typeof frame.data !== 'string') return
        bytes += frame.data.length
        text += frame.data
        try {
          if (bytes > maxBytes) throw new Error('录像解压后超过 64 MiB，无法在浏览器播放。')
          consume()
        } catch (err) { fail((err as Error).message) }
      },
      onDone: () => {
        if (stopped) return
        stopped = true
        try {
          parseLine(text)
          const result = cast
          if (!result) throw new Error('录像为空。')
          setCast(result)
        } catch (err) { setCastError((err as Error).message) }
      },
      onError: (message) => fail(message === 'replay not found' ? '录像不存在，或服务尚未启用回放。' : message),
      onClose: () => fail('实时连接中断，请重试。'),
    })
    return () => { stopped = true; subscription.close() }
  }, [selected])

  const closeReplay = () => {
    setSelected(null)
    setCast(null)
    setCastError('')
    setExpanded(false)
  }
  const goTo = (nextOffset: number) => { setPage(null); setListError(''); setOffset(nextOffset); setRevision(revision + 1) }
  const openReplay = (item: Recording) => {
    setCast(null)
    setCastError('')
    setSelected(item)
  }
  const play = (item: Recording) => <Button type="text" size="small" onClick={() => openReplay(item)}>播放</Button>
  const recordingKey = (item: Recording) => `${item.date}/${item.id}`
  const displaySize = (item: Recording) => `${Math.ceil(item.size / 1024)} KiB`
  return <>
    <Page toolbar={<ListToolbar left={<>
      <Button onClick={() => goTo(0)}>刷新列表</Button>
      <span>录像日期为 UTC</span>
    </>} right={<ListViewSwitch value={view} onChange={switchView} />} />}
      footer={page && page.total > 0 ? <div className="list-pagination"><Pagination
        current={offset / pageSize + 1} pageSize={pageSize} total={page.total}
        showTotal showJumper onChange={(next) => goTo((next - 1) * pageSize)} /></div> : null}>
      {listError ? <Alert type="error" content={listError} /> : !page ? <LoadingBlock tip="加载录像列表" /> :
        page.items.length === 0 ? <EmptyHint description="暂无录像" /> : view === 'table' ?
          <ListTable<Recording> rowKey={recordingKey} data={page.items} scroll={{ x: 560 }} columns={[
            { title: '日期 (UTC)', dataIndex: 'date', width: 135 },
            { title: '录像 ID', dataIndex: 'id', ellipsis: true },
            { title: '大小', width: 110, render: (_, item) => displaySize(item) },
            { title: '操作', width: 90, render: (_, item) => play(item) },
          ]} /> : <ListGrid>{page.items.map((item) => <ListCard key={recordingKey(item)} title={item.id} footer={play(item)}>
            <MetaList><MetaItem label="日期 (UTC)" value={item.date} mono /><MetaItem label="大小" value={displaySize(item)} mono /></MetaList>
          </ListCard>)}</ListGrid>}
    </Page>
    <Modal
      title={<div className="term-modal__title replay-modal__title">
        <span title={selected?.id}>{selected ? `录像回放 · ${selected.date} · ${selected.id}` : '录像回放'}</span>
        <button type="button" aria-label={expanded ? '退出最大化' : '最大化'}
          title={expanded ? '退出最大化' : '最大化'} onClick={() => setExpanded(!expanded)}>
          {expanded ? <IconFullscreenExit /> : <IconFullscreen />}
        </button>
      </div>}
      visible={selected !== null}
      onCancel={closeReplay}
      footer={null}
      maskClosable
      escToExit
      className={`term-modal replay-modal${expanded ? ' term-modal--expanded' : ''}`}
    >
      {selected && (cast ? <ReplayPlayer cast={cast} /> : <div className="replay-modal__notice">
        {castError ? <Alert type="error" content={castError} /> : <p role="status">正在加载录像…</p>}
      </div>)}
    </Modal>
  </>
}
