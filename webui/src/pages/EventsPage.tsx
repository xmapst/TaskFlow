/** 筛选和查看事件。应用级 WebSocket 连接在切换页面时保持开启。 */

import { type JSX, useCallback, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Input, Radio, Tooltip, type TableColumnProps } from '@arco-design/web-react'
import { useEventStore, type AppEvent, type EventLevel } from '@/store/events'
import { copyText, formatNanoClock, formatNumber } from '@/utils/format'
import { EmptyHint, LiveBadge, Page, Panel } from '@/components/common/Layout'
import { StatTile } from '@/components/common/StatTile'
import { IconCopy, IconPause, IconPlay, IconTrash } from '@/components/common/icons'
import { ListTable, ListToolbar } from '@/components/common/ListView'
import './EventsPage.scss'

const LEVEL_META: Record<EventLevel, { label: string; token: string }> = {
  progress: { label: '进行中', token: 'running' },
  success: { label: '已结束', token: 'stopped' },
  error: { label: '错误', token: 'failed' },
  info: { label: '消息', token: 'skipped' },
}

function EventLevelTag({ level }: { level: EventLevel }) {
  const meta = LEVEL_META[level]
  return <span className="ev-level" style={{
    ['--ev-color' as string]: `var(--c-state-${meta.token})`,
    ['--ev-soft' as string]: `var(--c-state-${meta.token}-soft)`,
  }}>{meta.label}</span>
}

type LevelFilter = EventLevel | 'all'

export function EventsPage(): JSX.Element {
  const navigate = useNavigate()
  const events = useEventStore((state) => state.events)
  const live = useEventStore((state) => state.live)
  const paused = useEventStore((state) => state.paused)
  const setPaused = useEventStore((state) => state.setPaused)
  const clear = useEventStore((state) => state.clear)

  const [level, setLevel] = useState<LevelFilter>('all')
  const [keyword, setKeyword] = useState('')

  const stats = useMemo(() => {
    const result: Record<EventLevel, number> = { progress: 0, success: 0, error: 0, info: 0 }
    for (const event of events) result[event.level] += 1
    return result
  }, [events])

  const filtered = useMemo(() => {
    const trimmed = keyword.trim().toLowerCase()
    return events.filter((event) => {
      if (level !== 'all' && event.level !== level) return false
      if (!trimmed) return true
      return (
        event.task.toLowerCase().includes(trimmed) ||
        event.step.toLowerCase().includes(trimmed) ||
        event.action.toLowerCase().includes(trimmed)
      )
    })
  }, [events, level, keyword])

  const copyAll = useCallback(async () => {
    const text = filtered.map((event) => `[${formatNanoClock(event.at * 1e6)}] ${event.raw}`).join('\n')
    await copyText(text)
  }, [filtered])

  const columns: TableColumnProps<AppEvent>[] = useMemo(
    () => [
      {
        title: '时间',
        dataIndex: 'at',
        width: 92,
        render: (_value, event) => (
          // store 里 at 是毫秒，formatNanoClock 收纳秒，故换算
          <span className="u-mono ev-time">{formatNanoClock(event.at * 1e6)}</span>
        ),
      },
      {
        title: '类型',
        dataIndex: 'level',
        width: 96,
        render: (_value, event) => <EventLevelTag level={event.level} />,
      },
      {
        title: '任务',
        dataIndex: 'task',
        width: 200,
        render: (_value, event) =>
          event.task ? (
            <button
              type="button"
              className="ev-link u-truncate"
              title={`查看任务 ${event.task}`}
              onClick={() => navigate(`/tasks/${encodeURIComponent(event.task)}`)}
            >
              {event.task}
            </button>
          ) : (
            <span className="ev-muted">—</span>
          ),
      },
      {
        title: '步骤',
        dataIndex: 'step',
        width: 180,
        render: (_value, event) =>
          event.step ? (
            <span className="u-truncate" title={event.step}>
              {event.step}
            </span>
          ) : (
            <span className="ev-muted">任务级</span>
          ),
      },
      {
        title: '内容',
        dataIndex: 'action',
        render: (_value, event) => (
          <Tooltip content={event.raw} mini>
            <span className="ev-action u-truncate">{event.action}</span>
          </Tooltip>
        ),
      },
    ],
    [navigate],
  )

  const toolbar = (
    <ListToolbar left={<>
        <Input.Search
          allowClear
          placeholder="搜索任务、步骤或内容"
          value={keyword}
          onChange={setKeyword}
          className="ev-toolbar__search"
        />
        <Radio.Group
          type="button"
          size="small"
          value={level}
          onChange={(value: LevelFilter) => setLevel(value)}
        >
          <Radio value="all">全部</Radio>
          <Radio value="progress">进行中</Radio>
          <Radio value="success">已结束</Radio>
          <Radio value="error">错误</Radio>
        </Radio.Group>
      </>} right={<>
        <LiveBadge live={live} />
        <Button
          size="small"
          type="secondary"
          icon={paused ? <IconPlay size={13} /> : <IconPause size={13} />}
          onClick={() => setPaused(!paused)}
        >
          {paused ? '恢复接收' : '暂停接收'}
        </Button>
        <Button
          size="small"
          type="secondary"
          icon={<IconCopy size={13} />}
          disabled={filtered.length === 0}
          onClick={() => void copyAll()}
        >
          复制
        </Button>
        <Button
          size="small"
          type="secondary"
          status="danger"
          icon={<IconTrash size={13} />}
          disabled={events.length === 0}
          onClick={clear}
        >
          清空
        </Button>
    </>} />
  )

  const statsRow = (
    <div className="ev-stats">
      <StatTile label="进行中" value={stats.progress} token="running" />
      <StatTile label="已结束" value={stats.success} token="stopped" />
      <StatTile label="错误" value={stats.error} token="failed" />
      <StatTile label="消息" value={stats.info} token="skipped" />
    </div>
  )

  return (
    <Page toolbar={toolbar} stats={statsRow} fill>
      <Panel
        title="事件流"
        extra={
          <span className="ev-count u-mono">
            {keyword.trim() || level !== 'all'
              ? `${formatNumber(filtered.length)} / ${formatNumber(events.length)}`
              : `${formatNumber(events.length)} 条`}
          </span>
        }
        flush
        grow
      >
        {events.length === 0 ? (
          <EmptyHint
            description={live ? '等待事件' : '尚未连接到事件流'}
            hint={
              live
                ? '任务执行时会实时推送步骤的准备、执行、结束与错误事件。'
                : '无法接收实时事件，请确认后端服务可达。'
            }
          />
        ) : filtered.length === 0 ? (
          <EmptyHint
            description="没有匹配的事件"
            hint="调整筛选条件或清空搜索关键词后重试。"
            compact
          />
        ) : (
          <ListTable
            rowKey="id"
            data={filtered}
            columns={columns}
            scroll={{ y: true }}
            className="ev-table"
          />
        )}
      </Panel>
    </Page>
  )
}
