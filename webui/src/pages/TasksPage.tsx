/**
 * 任务管理页
 *
 * 数据来自 WebSocket 长连接，翻页与搜索通过同一条连接下发参数。
 *
 * 关于统计口径：后端未提供全局状态计数接口，任务列表接口的 total 也只是
 * 总页数，因此统计磁贴只能反映「当前页」。这一点必须在界面上写明，
 * 否则用户会把它误当成全局态势。
 */

import { type JSX, useCallback, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Button,
  Dropdown,
  Input,
  Menu,
  Pagination,
  Tooltip,
  type TableColumnProps,
} from '@arco-design/web-react'
import type { ManageAction, TaskRes } from '@/types/api'
import { computeTaskStats, useTaskListStoreBinding } from './TasksPage.hooks'
import { availableActions, ACTION_META } from '@/constants/state'
import { deleteTask, dumpTask, manageTask } from '@/api/task'
import { useAsyncAction } from '@/hooks/useAsyncAction'
import { displayTime, downloadText, elapsedBetween, splitStateMessage } from '@/utils/format'
import { Page, EmptyHint, LiveBadge, LoadingBlock, MetaItem, MetaList } from '@/components/common/Layout'
import { StateTag, StateDot } from '@/components/common/StateTag'
import { StatTile } from '@/components/common/StatTile'
import { IconMore, IconPlus } from '@/components/common/icons'
import { TaskCreateModal } from './TaskCreateModal'
import { ListCard, ListGrid, ListTable, ListToolbar, ListViewSwitch, useListView } from '@/components/common/ListView'
import './TasksPage.scss'

const VIEW_KEY = 'aef-task-view'

export function TasksPage(): JSX.Element {
  const navigate = useNavigate()
  const { tasks, page, size, totalPages, prefix, loading, error, connection, setPage, setSize, setPrefix, refetch } =
    useTaskListStoreBinding()

  const [view, switchView] = useListView(VIEW_KEY)
  const [creating, setCreating] = useState(false)
  /** 状态筛选，仅作用于当前页 */
  const [stateFilter, setStateFilter] = useState<string | null>(null)

  const stats = useMemo(() => computeTaskStats(tasks), [tasks])

  const visibleTasks = useMemo(
    () => (stateFilter ? tasks.filter((task) => task.state === stateFilter) : tasks),
    [tasks, stateFilter],
  )

  const openDetail = useCallback(
    (taskName: string) => {
      navigate(`/tasks/${encodeURIComponent(taskName)}`)
    },
    [navigate],
  )

  // ---------- 任务操作 ----------

  const removeAction = useAsyncAction(
    async (taskName: string) => {
      await deleteTask(taskName)
    },
    {
      successMessage: '任务已删除',
      errorPrefix: '删除失败',
      confirm: {
        title: '删除任务',
        content: '将删除该任务及其记录，此操作不可撤销。',
        okText: '删除',
        danger: true,
      },
    },
  )

  const manageAction = useAsyncAction(
    async (taskName: string, action: ManageAction) => {
      await manageTask(taskName, action)
    },
    { errorPrefix: '操作失败' },
  )

  const exportAction = useAsyncAction(
    async (taskName: string) => {
      const yaml = await dumpTask(taskName)
      downloadText(`${taskName}.yaml`, yaml, 'application/yaml')
    },
    { successMessage: '配置已导出', errorPrefix: '导出失败' },
  )

  // ---------- 渲染片段 ----------

  const renderActions = useCallback(
    (task: TaskRes): JSX.Element => {
      const actions = availableActions(task.state)
      return (
        <Dropdown
          trigger="click"
          position="br"
          droplist={
            <Menu
              onClickMenuItem={(key) => {
                if (key === 'dump') {
                  void exportAction.run(task.name)
                  return
                }
                if (key === 'delete') {
                  void removeAction.run(task.name)
                  return
                }
                if (key === 'workspace') {
                  navigate(`/tasks/${encodeURIComponent(task.name)}?tab=workspace`)
                  return
                }
                // 后端异步下发指令，这里只确认已受理
                void manageAction.run(task.name, key as ManageAction)
              }}
            >
              {actions.map((action) => (
                <Menu.Item key={action}>{ACTION_META[action].label}</Menu.Item>
              ))}
              <Menu.Item key="workspace">工作目录</Menu.Item>
              <Menu.Item key="dump">导出配置</Menu.Item>
              <Menu.Item key="delete" className="task-menu__danger">
                删除任务
              </Menu.Item>
            </Menu>
          }
        >
          <Button
            size="small"
            type="secondary"
            icon={<IconMore size={14} />}
            onClick={(event) => event.stopPropagation()}
          />
        </Dropdown>
      )
    },
    [exportAction, removeAction, manageAction, navigate],
  )

  const columns: TableColumnProps<TaskRes>[] = useMemo(
    () => [
      {
        title: '任务',
        dataIndex: 'name',
        width: 260,
        render: (_value, task) => (
          <div className="task-cell">
            <StateDot state={task.state} />
            <button type="button" className="task-cell__name" onClick={() => openDetail(task.name)}>
              {task.name}
            </button>
          </div>
        ),
      },
      {
        title: '步骤',
        dataIndex: 'count',
        width: 80,
        align: 'right',
        render: (_value, task) => <span className="u-mono">{task.count ?? 0}</span>,
      },
      {
        title: '状态',
        dataIndex: 'state',
        width: 108,
        render: (_value, task) => <StateTag state={task.state} size="small" />,
      },
      {
        title: '开始时间',
        dataIndex: 'time.start',
        width: 208,
        render: (_value, task) => <span className="u-mono task-cell__start">{displayTime(task.time?.start)}</span>,
      },
      {
        title: '耗时',
        width: 96,
        align: 'right',
        render: (_value, task) => (
          <span className="u-mono">
            {elapsedBetween(task.time?.start, task.time?.end) || (task.time?.start ? '进行中' : '—')}
          </span>
        ),
      },
      {
        title: '执行信息',
        dataIndex: 'message',
        render: (_value, task) => {
          const { text, counters } = splitStateMessage(task.message)
          return (
            <div className="task-msg">
              {text ? (
                <Tooltip content={text}>
                  <span className="task-msg__text u-truncate">{text}</span>
                </Tooltip>
              ) : null}
              {counters.length > 0 ? (
                <span className="task-msg__counters">
                  {counters.map((counter) => (
                    <StateTag
                      key={counter.state}
                      state={counter.state}
                      size="small"
                      variant="soft"
                      className="task-msg__counter"
                    />
                  ))}
                </span>
              ) : null}
            </div>
          )
        },
      },
      {
        title: '',
        width: 56,
        align: 'center',
        render: (_value, task) => renderActions(task),
      },
    ],
    [openDetail, renderActions],
  )

  const toolbar = (
    <ListToolbar left={<>
        <Button type="primary" icon={<IconPlus size={15} />} onClick={() => setCreating(true)}>
          新建任务
        </Button>
        <Input.Search
          allowClear
          placeholder="按名称前缀搜索"
          value={prefix}
          onChange={setPrefix}
          className="task-toolbar__search"
        />
      </>} right={<>
        <LiveBadge live={connection === 'live'} />
        {connection === 'offline' ? (
          <Button size="small" type="secondary" onClick={() => void refetch()}>
            重新加载
          </Button>
        ) : null}
        <ListViewSwitch value={view} onChange={switchView} />
    </>} />
  )

  const statsRow = (
    <div className="task-stats-wrap">
      <p className="task-stats-caption">统计与筛选仅作用于当前页</p>
      <div className="task-stats">
        <StatTile
          label="执行中"
          value={stats.running}
          token="running"
          active={stateFilter === 'running'}
          onClick={() => setStateFilter(stateFilter === 'running' ? null : 'running')}
        />
        <StatTile
          label="等待中"
          value={stats.pending}
          token="pending"
          active={stateFilter === 'pending'}
          onClick={() => setStateFilter(stateFilter === 'pending' ? null : 'pending')}
        />
        <StatTile
          label="已挂起"
          value={stats.paused}
          token="paused"
          active={stateFilter === 'paused'}
          onClick={() => setStateFilter(stateFilter === 'paused' ? null : 'paused')}
        />
        <StatTile
          label="已失败"
          value={stats.failed}
          token="failed"
          active={stateFilter === 'failed'}
          onClick={() => setStateFilter(stateFilter === 'failed' ? null : 'failed')}
        />
        <StatTile
          label="已完成"
          value={stats.stopped}
          token="stopped"
          active={stateFilter === 'stopped'}
          onClick={() => setStateFilter(stateFilter === 'stopped' ? null : 'stopped')}
        />
      </div>
    </div>
  )

  const footer =
    totalPages > 1 ? (
      <div className="list-pagination">
        <Pagination
          current={page}
          pageSize={size}
          // 后端只给总页数，这里换算成总条数供组件使用
          total={totalPages * size}
          sizeCanChange
          pageSizeChangeResetCurrent
          sizeOptions={[20, 40, 60, 100]}
          onChange={(nextPage, nextSize) => {
            if (nextSize !== size) {
              setSize(nextSize)
              return
            }
            setPage(nextPage)
          }}
        />
      </div>
    ) : null

  const body = (): JSX.Element => {
    if (loading && tasks.length === 0) return <LoadingBlock tip="正在连接实时通道" />

    if (error && tasks.length === 0) {
      return (
        <EmptyHint
          description="无法获取任务列表"
          hint={error}
          action={
            <Button type="primary" onClick={() => void refetch()}>
              重试
            </Button>
          }
        />
      )
    }

    if (visibleTasks.length === 0) {
      return (
        <EmptyHint
          description={stateFilter ? '当前页没有该状态的任务' : prefix ? '没有匹配的任务' : '还没有任务'}
          hint={
            stateFilter
              ? '统计与筛选均只作用于当前页，可翻页或清除筛选后再查看。'
              : prefix
                ? '搜索按名称前缀匹配，请尝试更短的关键词。'
                : '创建任务后即可在此查看编排结构与实时执行状态。'
          }
          action={
            stateFilter ? (
              <Button onClick={() => setStateFilter(null)}>清除筛选</Button>
            ) : (
              <Button type="primary" icon={<IconPlus size={15} />} onClick={() => setCreating(true)}>
                新建任务
              </Button>
            )
          }
        />
      )
    }

    if (view === 'table') {
      return (
        <ListTable
          rowKey="name"
          data={visibleTasks}
          columns={columns}
          scroll={{ x: 1010 }}
          onRow={(task) => ({
            onDoubleClick: () => openDetail(task.name),
          })}
        />
      )
    }

    return (
      <ListGrid>
        {visibleTasks.map((task) => {
          const { text, counters } = splitStateMessage(task.message)
          return (
            <ListCard
              key={task.name}
              title={task.name}
              tone={task.state}
              leading={<StateDot state={task.state} />}
              actions={renderActions(task)}
              onOpen={() => openDetail(task.name)}
              footer={<>
                <StateTag state={task.state} size="small" />
                {counters.length > 0 ? (
                  <span className="task-card__counters">
                    {counters.map((counter) => (
                      <Tooltip
                        key={counter.state}
                        content={`${counter.count} 个步骤处于该状态`}
                        mini
                      >
                        <span className="task-card__counter">
                          <StateDot state={counter.state} />
                          <span className="u-mono">{counter.count}</span>
                        </span>
                      </Tooltip>
                    ))}
                  </span>
                ) : null}
              </>}
            >
              <MetaList>
                <MetaItem label="步骤数" value={task.count ?? 0} mono />
                <MetaItem label="开始" value={displayTime(task.time?.start)} mono />
                <MetaItem
                  label="耗时"
                  value={
                    elapsedBetween(task.time?.start, task.time?.end) ||
                    (task.time?.start ? '进行中' : undefined)
                  }
                  mono
                />
              </MetaList>

              {text ? (
                <p className="task-card__msg" title={text}>
                  {text}
                </p>
              ) : null}

            </ListCard>
          )
        })}
      </ListGrid>
    )
  }

  return (
    <>
      <Page toolbar={toolbar} stats={statsRow} footer={footer}>
        {body()}
      </Page>
      {creating && (
        <TaskCreateModal
          onClose={() => setCreating(false)}
          onCreated={(taskName) => {
            setCreating(false)
            openDetail(taskName)
          }}
        />
      )}
    </>
  )
}
