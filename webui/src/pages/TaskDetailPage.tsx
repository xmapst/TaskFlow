/**
 * 任务详情页
 *
 * 顶层用三个平级 Tab 组织：详情（任务信息/进度/环境变量/步骤清单）、
 * 编排视图（DAG 画布）、工作目录。三者同级而非「侧栏 + 主区」的从属关系，
 * 每个 Tab 都能拿到整块可用高度，避免信息栏与画布互相挤占空间。
 *
 * 任务详情、步骤列表和步骤日志共用实时 WebSocket 连接。
 * 切换 Tab 不会中断订阅；步骤日志仅在抽屉打开时订阅。
 */

import { type JSX, useCallback, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Button, Dropdown, Menu, Progress, Tabs, Tooltip } from '@arco-design/web-react'
import type { ManageAction } from '@/types/api'
import { useTaskDetail } from '@/hooks/useTaskDetail'
import { computeStepStats, sortStepsStable, useStepList } from '@/hooks/useStepList'
import { useAsyncAction } from '@/hooks/useAsyncAction'
import { deleteTask, dumpTask, manageTask } from '@/api/task'
import { manageStep } from '@/api/step'
import { ACTION_META, availableActions, getStateLabel } from '@/constants/state'
import {
  displayTime,
  downloadText,
  elapsedBetween,
  formatDuration,
  splitStateMessage,
} from '@/utils/format'
import {
  EmptyHint,
  EnvList,
  LiveBadge,
  LoadingBlock,
  MetaItem,
  MetaList,
  Panel,
} from '@/components/common/Layout'
import { StatTile } from '@/components/common/StatTile'
import { StateDot, StateTag } from '@/components/common/StateTag'
import { StepFlow } from '@/components/flow/StepFlow'
import { WorkspacePanel } from '@/components/workspace/WorkspacePanel'
import { StepDrawer } from './StepDrawer'
import {
  IconChevronLeft,
  IconFolder,
  IconInfo,
  IconKey,
  IconMore,
} from '@/components/common/icons'
import './TaskDetailPage.scss'

type MainTab = 'detail' | 'flow' | 'workspace'

function resolveInitialTab(value: string | null): MainTab {
  return value === 'workspace' || value === 'flow' ? value : 'detail'
}

export function TaskDetailPage(): JSX.Element {
  const { taskName: rawName } = useParams<{ taskName: string }>()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  // 路由参数已由 react-router 解码，直接使用
  const taskName = rawName ?? ''

  const detail = useTaskDetail(taskName || null)
  const stepList = useStepList(taskName || null)
  const [activeStep, setActiveStep] = useState<string | null>(null)
  /** 右键菜单的目标步骤与位置 */
  const [contextMenu, setContextMenu] = useState<{ step: string; x: number; y: number } | null>(null)
  /** 只在挂载时读取一次，之后不与 URL 双向同步，避免和用户手动切 Tab 打架 */
  const [mainTab, setMainTab] = useState<MainTab>(() => resolveInitialTab(searchParams.get('tab')))

  const steps = useMemo(() => sortStepsStable(stepList.steps), [stepList.steps])
  const stats = useMemo(() => computeStepStats(steps), [steps])
  const taskMessage = useMemo(
    () => splitStateMessage(detail.task?.message || detail.message),
    [detail.task?.message, detail.message],
  )

  const contextStep = useMemo(
    () => (contextMenu ? steps.find((step) => step.name === contextMenu.step) : undefined),
    [contextMenu, steps],
  )

  // ---------- 任务级操作 ----------

  const manageTaskAction = useAsyncAction(
    async (action: ManageAction) => {
      await manageTask(taskName, action)
    },
    { errorPrefix: '操作失败' },
  )

  const exportAction = useAsyncAction(
    async () => {
      const yamlText = await dumpTask(taskName)
      downloadText(`${taskName}.yaml`, yamlText, 'application/yaml')
    },
    { successMessage: '配置已导出', errorPrefix: '导出失败' },
  )

  const deleteAction = useAsyncAction(
    async () => {
      await deleteTask(taskName)
      navigate('/tasks')
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

  // ---------- 步骤级操作 ----------

  const manageStepAction = useAsyncAction(
    async (stepName: string, action: ManageAction) => {
      await manageStep(taskName, stepName, action)
    },
    { errorPrefix: '操作失败' },
  )

  const closeContext = useCallback(() => setContextMenu(null), [])

  const handleContextStep = useCallback((stepName: string, position: { x: number; y: number }) => {
    setContextMenu({ step: stepName, x: position.x, y: position.y })
  }, [])

  if (!taskName) {
    return <EmptyHint description="缺少任务名称" hint="请从任务列表进入详情页。" />
  }

  const taskActions = availableActions(detail.task?.state)
  const envCount = detail.task?.env?.length ?? 0

  return (
    <div className="task-detail" onClick={closeContext}>
      {/* ---------- 顶部操作条 ---------- */}
      <header className="task-detail__bar">
        <div className="task-detail__bar-left">
          <Button
            type="text"
            size="small"
            icon={<IconChevronLeft size={15} />}
            onClick={() => navigate('/tasks')}
          >
            返回列表
          </Button>
          <span className="task-detail__divider" />
          <StateDot state={detail.task?.state} />
          <h2 className="task-detail__name" title={taskName}>
            {taskName}
          </h2>
          <StateTag state={detail.task?.state} size="small" />
        </div>

        <div className="task-detail__bar-right">
          <LiveBadge live={detail.live && stepList.live} finished={stepList.finished} />
          {taskActions.map((action) => (
            <Button
              key={action}
              size="small"
              type={ACTION_META[action].danger ? 'outline' : 'secondary'}
              // status 在 Arco 的类型里不接受 undefined
              status={ACTION_META[action].danger ? 'danger' : 'default'}
              loading={manageTaskAction.loading}
              onClick={() => void manageTaskAction.run(action)}
            >
              {ACTION_META[action].label}
            </Button>
          ))}
          <Dropdown
            trigger="click"
            position="br"
            droplist={
              <Menu
                onClickMenuItem={(key) => {
                  if (key === 'dump') void exportAction.run()
                  if (key === 'delete') void deleteAction.run()
                  if (key === 'workspace') setMainTab('workspace')
                }}
              >
                <Menu.Item key="workspace">工作目录</Menu.Item>
                <Menu.Item key="dump">导出配置</Menu.Item>
                <Menu.Item key="delete">删除任务</Menu.Item>
              </Menu>
            }
          >
            <Button size="small" type="secondary" icon={<IconMore size={14} />} />
          </Dropdown>
        </div>
      </header>

      {/* ---------- 主体：三个平级 Tab ---------- */}
      <div className="task-detail__body">
        <Tabs
          activeTab={mainTab}
          onChange={(key) => setMainTab(resolveInitialTab(key))}
          className="task-detail__tabs"
        >
          <Tabs.TabPane key="detail" title="详情">
            <div className="task-detail__overview-tab">
              {/* ---------- 概览：进度 + 统计磁贴 ---------- */}
              <section className="task-detail__overview">
                <div className="task-detail__overview-progress">
                  <span className="task-detail__progress-text u-mono">{stats.progress}%</span>
                  <Progress
                    percent={stats.progress}
                    showText={false}
                    size="large"
                    status={stats.progress === 100 ? 'success' : 'normal'}
                  />
                </div>
                {taskMessage.text ? <p className="task-detail__msg">{taskMessage.text}</p> : null}
                <div className="task-detail__stats">
                  {(
                    [
                      ['running', stats.running],
                      ['pending', stats.pending],
                      ['paused', stats.paused],
                      ['failed', stats.failed],
                      ['stopped', stats.stopped],
                      ['skipped', stats.skipped],
                    ] as const
                  ).map(([state, count]) => (
                    <StatTile key={state} label={getStateLabel(state)} value={count} token={state} />
                  ))}
                </div>
              </section>

              {/* ---------- 详情：任务信息 / 全局环境变量 / 步骤清单 三列平铺 ---------- */}
              <div className={`task-detail__grid${envCount > 0 ? '' : ' task-detail__grid--no-env'}`}>
                <Panel
                  title="任务信息"
                  icon={<IconInfo size={15} />}
                  grow
                  className="task-detail__grid-col"
                >
                  {detail.loading && !detail.task ? (
                    <LoadingBlock tip="加载中" />
                  ) : (
                    <MetaList>
                      <MetaItem label="步骤总数" value={detail.task?.count ?? steps.length} mono />
                      <MetaItem label="超时" value={formatDuration(detail.task?.timeout)} />
                      <MetaItem label="开始时间" value={displayTime(detail.task?.time?.start)} mono />
                      <MetaItem label="结束时间" value={displayTime(detail.task?.time?.end)} mono />
                      <MetaItem
                        label="耗时"
                        value={
                          elapsedBetween(detail.task?.time?.start, detail.task?.time?.end) ||
                          (detail.task?.time?.start ? '进行中' : undefined)
                        }
                        mono
                      />
                      <MetaItem label="工作目录" value={detail.task?.workdir} mono wrap />
                      <MetaItem label="结束后清理" value={detail.task?.cleanup === undefined ? undefined : detail.task.cleanup ? '是' : '否（保留工作目录）'} />
                    </MetaList>
                  )}
                </Panel>

                {envCount > 0 ? (
                  <Panel
                    title="全局环境变量"
                    icon={<IconKey size={15} />}
                    extra={<span className="u-mono task-detail__count">{envCount}</span>}
                    grow
                    className="task-detail__grid-col"
                  >
                    <EnvList env={detail.task?.env} />
                  </Panel>
                ) : null}

                <Panel
                  title="步骤清单"
                  icon={<IconFolder size={15} />}
                  extra={<span className="u-mono task-detail__count">{steps.length}</span>}
                  flush
                  grow
                  className="task-detail__grid-col"
                >
                  {steps.length === 0 ? (
                    <EmptyHint description="暂无步骤" compact />
                  ) : (
                    <ul className="step-list">
                      {steps.map((step) => (
                        <li key={step.name}>
                          <button
                            type="button"
                            className={`step-list__item${activeStep === step.name ? ' is-active' : ''}`}
                            onClick={() => setActiveStep(step.name)}
                          >
                            <StateDot state={step.state} />
                            <span className="step-list__name u-truncate" title={step.name}>
                              {step.name}
                            </span>
                            {step.state === 'failed' && step.code !== 0 ? (
                              <Tooltip content={`退出码 ${step.code}`} mini>
                                <span className="step-list__code u-mono">{step.code}</span>
                              </Tooltip>
                            ) : null}
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                </Panel>
              </div>
            </div>
          </Tabs.TabPane>

          <Tabs.TabPane key="flow" title="编排视图">
            {stepList.loading && steps.length === 0 ? (
              <LoadingBlock tip="正在加载步骤" />
            ) : steps.length === 0 ? (
              <EmptyHint
                description="该任务暂无步骤数据"
                hint={stepList.error ?? '若任务刚创建，步骤会在调度后出现；也可能任务已被删除。'}
              />
            ) : (
              <StepFlow
                steps={steps}
                taskName={taskName}
                onSelectStep={setActiveStep}
                onContextStep={handleContextStep}
              />
            )}
          </Tabs.TabPane>

          <Tabs.TabPane key="workspace" title="工作目录" destroyOnHide>
            <WorkspacePanel taskName={taskName} workdir={detail.task?.workdir} />
          </Tabs.TabPane>
        </Tabs>
      </div>

      {/* ---------- 节点右键菜单 ---------- */}
      {contextMenu && contextStep ? (
        <div
          className="step-context"
          style={{ left: contextMenu.x, top: contextMenu.y }}
          onClick={(event) => event.stopPropagation()}
        >
          <div className="step-context__head u-truncate">{contextStep.name}</div>
          <button
            type="button"
            className="step-context__item"
            onClick={() => {
              setActiveStep(contextStep.name)
              closeContext()
            }}
          >
            查看详情与日志
          </button>
          {availableActions(contextStep.state).map((action) => (
            <button
              key={action}
              type="button"
              className={`step-context__item${
                ACTION_META[action].danger ? ' step-context__item--danger' : ''
              }`}
              onClick={() => {
                void manageStepAction.run(contextStep.name, action)
                closeContext()
              }}
            >
              {ACTION_META[action].label}
            </button>
          ))}
          {availableActions(contextStep.state).length === 0 ? (
            <div className="step-context__empty">该状态下无可用操作</div>
          ) : null}
        </div>
      ) : null}

      {/* ---------- 步骤抽屉 ---------- */}
      <StepDrawer taskName={taskName} stepName={activeStep} onClose={() => setActiveStep(null)} />
    </div>
  )
}
