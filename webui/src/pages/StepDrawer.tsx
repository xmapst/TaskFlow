/** 步骤详情、配置和实时日志。 */

import { type JSX, useCallback, useEffect, useState } from 'react'
import { Button, Drawer, Message, Tabs, Tooltip } from '@arco-design/web-react'
import type { ManageAction } from '@/types/api'
import { useStepDetail } from '@/hooks/useStepDetail'
import { linesToText, useStepLog } from '@/hooks/useStepLog'
import { useAsyncAction } from '@/hooks/useAsyncAction'
import { manageStep } from '@/api/step'
import { ACTION_META, availableActions, getExitCodeLevel, scriptLanguage } from '@/constants/state'
import {
  copyText,
  displayTime,
  downloadText,
  elapsedBetween,
  formatDuration,
  formatGoDuration,
} from '@/utils/format'
import { EnvList, LiveBadge, LoadingBlock } from '@/components/common/Layout'
import { StateTag } from '@/components/common/StateTag'
import { CodeEditor } from '@/components/editor/CodeEditor'
import { LogViewer } from '@/components/log/LogViewer'
import { IconCopy, IconDownload } from '@/components/common/icons'
import './StepDrawer.scss'

export interface StepDrawerProps {
  taskName: string
  /** null 表示关闭 */
  stepName: string | null
  onClose: () => void
}

type OptionMount = { source?: unknown; target?: unknown; type?: unknown; readOnly?: unknown }

function mountLabel(mount: OptionMount): string {
  const type = typeof mount.type === 'string' && mount.type ? mount.type : 'bind'
  const source = typeof mount.source === 'string' && mount.source ? mount.source : '—'
  const target = typeof mount.target === 'string' && mount.target ? mount.target : '—'
  return `${source} → ${target} (${type}${mount.readOnly === true ? ', 只读' : ''})`
}

export function StepDrawer({ taskName, stepName, onClose }: StepDrawerProps): JSX.Element {
  const detail = useStepDetail(taskName, stepName)
  const log = useStepLog(taskName, stepName)

  const [activeTab, setActiveTab] = useState('info')
  // 切换步骤时回到第一个 tab
  useEffect(() => {
    setActiveTab('info')
  }, [stepName])

  const step = detail.step
  const actions = availableActions(step?.state)
  const codeLevel = getExitCodeLevel(step?.code)

  const manageAction = useAsyncAction(
    async (action: ManageAction) => {
      if (!stepName) return
      await manageStep(taskName, stepName, action)
    },
    { errorPrefix: '操作失败' },
  )

  const copyLog = useCallback(async () => {
    const ok = await copyText(linesToText(log.lines))
    if (ok) {
      Message.success('日志已复制')
    } else {
      Message.error('复制失败，请手动选择文本')
    }
  }, [log.lines])

  const exportLog = useCallback(() => {
    if (!stepName) return
    downloadText(`${taskName}-${stepName}.log`, linesToText(log.lines))
  }, [taskName, stepName, log.lines])

  const retry = step?.retryPolicy?.maxAttempts ? step.retryPolicy : undefined
  const routing = step?.routing
  const options = step?.options ?? {}
  const shell = typeof options.shell === 'string' ? options.shell : ''
  const image = typeof options.image === 'string' ? options.image : ''
  const optionEntries = Object.entries(options)
  const mounts = Array.isArray(options.mounts)
    ? options.mounts.filter((item): item is OptionMount => typeof item === 'object' && item !== null)
    : []
  const elapsed = elapsedBetween(step?.time?.start, step?.time?.end) || (step?.time?.start ? '进行中' : '—')
  const metrics = [
    { label: '执行器', value: step?.kind || '—' },
    { label: '退出码', value: <span className={`step-drawer__code step-drawer__code--${codeLevel}`}>{step?.code ?? '—'}</span> },
    { label: '耗时', value: elapsed },
    { label: '超时', value: formatDuration(step?.timeout) },
  ]

  return (
    <Drawer
      visible={stepName !== null}
      onCancel={onClose}
      width="min(880px, 100vw)"
      title={
        <div className="step-drawer__title">
          <span className="step-drawer__name u-truncate" title={stepName ?? ''}>
            {stepName}
          </span>
          <StateTag state={step?.state} size="small" />
          <LiveBadge live={log.live} finished={log.finished} />
        </div>
      }
      footer={null}
      className="step-drawer"
      // 抽屉里有大量实时数据，卸载以释放两条连接
      unmountOnExit
    >
      {detail.loading && !step ? (
        <LoadingBlock tip="加载步骤详情" />
      ) : (
        <Tabs
          activeTab={activeTab}
          onChange={setActiveTab}
          size="small"
          destroyOnHide
          className="step-drawer__tabs"
          extra={activeTab === 'output' ? (
            <div className="step-drawer__tab-extra">
              {detail.message && detail.message !== step?.message ? (
                <span className="step-drawer__hint u-truncate">{detail.message}</span>
              ) : null}
              <Button
                size="mini"
                type="text"
                icon={<IconCopy size={13} />}
                disabled={log.lines.length === 0}
                onClick={() => void copyLog()}
              >
                复制
              </Button>
              <Button
                size="mini"
                type="text"
                icon={<IconDownload size={13} />}
                disabled={log.lines.length === 0}
                onClick={exportLog}
              >
                导出
              </Button>
            </div>
          ) : null}
        >
          <Tabs.TabPane key="info" title="执行信息">
            <div className="step-drawer__info">
              {actions.length > 0 && (
                <div className="step-drawer__actions">
                  {actions.map((action) => (
                    <Tooltip
                      key={action}
                      content={action === 'pause'
                        ? `挂起后需手动恢复，或按 ${formatGoDuration(60_000_000_000)} 自动恢复`
                        : ACTION_META[action].label}
                      mini
                    >
                      <Button
                        size="mini"
                        type={ACTION_META[action].danger ? 'outline' : 'secondary'}
                        status={ACTION_META[action].danger ? 'danger' : 'default'}
                        loading={manageAction.loading}
                        onClick={() => void manageAction.run(action)}
                      >
                        {ACTION_META[action].label}
                      </Button>
                    </Tooltip>
                  ))}
                </div>
              )}
              <dl className="step-drawer__metrics">
                {metrics.map(({ label, value }) => (
                  <div key={label}>
                    <dt>{label}</dt>
                    <dd className="u-mono">{value}</dd>
                  </div>
                ))}
              </dl>
              <dl className="step-drawer__times">
                <div><dt>开始时间</dt><dd className="u-mono">{displayTime(step?.time?.start)}</dd></div>
                <div><dt>结束时间</dt><dd className="u-mono">{displayTime(step?.time?.end)}</dd></div>
              </dl>
              {step?.message && <div className="step-drawer__note"><strong>执行消息</strong><p>{step.message}</p></div>}
              {step?.desc && <div className="step-drawer__note"><strong>步骤描述</strong><p>{step.desc}</p></div>}
              {(routing?.edges?.length || routing?.join || routing?.emit?.length || retry) ? (
                <section className="step-drawer__policy">
                  <h4 className="step-drawer__policy-title">{retry ? routing ? '路由与重试策略' : '重试策略' : '路由策略'}</h4>
                  <dl className="step-drawer__rules">
                    {!!routing?.edges?.length && <div><dt>带标签的依赖</dt><dd className="step-drawer__chips">
                      {routing.edges.map(({ step, trigger }) => (
                        <span key={`${step}:${trigger}`} className="step-drawer__chip u-mono">
                          {step}<em>{trigger || 'success'}</em>
                        </span>
                      ))}
                    </dd></div>}
                    {routing?.join && <div><dt>汇合策略</dt><dd>{routing.join}</dd></div>}
                    {!!routing?.emit?.length && <div><dt>成功标签</dt><dd className="step-drawer__chips">
                      {routing.emit.map((tag, index) => (
                        <span key={`${tag}:${index}`} className="step-drawer__chip u-mono">{tag || 'success'}</span>
                      ))}
                    </dd></div>}
                    {retry && <div><dt>重试策略</dt><dd>
                      {retry.maxAttempts < 0 ? '不限尝试次数' : `最多尝试 ${retry.maxAttempts} 次`} · 间隔 {formatDuration(retry.interval)} ·
                      上限 {formatDuration(retry.maxInterval)} · 倍数 {retry.multiplier}
                      {retry.jitter ? ` · 抖动 ${Math.round(retry.jitter * 100)}%` : ''}
                    </dd></div>}
                  </dl>
                </section>
              ) : null}
            </div>
          </Tabs.TabPane>

          <Tabs.TabPane key="options" title={`执行器参数${optionEntries.length ? ` (${optionEntries.length})` : ''}`}>
            <div className="step-drawer__options-pane">
              {(image || mounts.length > 0) && (
                <dl className="step-drawer__rules">
                  {image && <div><dt>镜像</dt><dd className="u-mono">{image}</dd></div>}
                  {mounts.length > 0 && (
                    <div><dt>额外挂载</dt><dd className="step-drawer__chips">
                      {mounts.map((mount, index) => (
                        <span key={index} className="step-drawer__chip u-mono">{mountLabel(mount)}</span>
                      ))}
                    </dd></div>
                  )}
                </dl>
              )}
              {optionEntries.length > 0 ? (
                <>
                  <h5 className="step-drawer__policy-subtitle">options 原文</h5>
                  {/* 接口返回的就是 JSON，按原样展示，未识别的键也一并显示 */}
                  <pre className="step-drawer__options u-mono">{JSON.stringify(options, null, 2)}</pre>
                </>
              ) : (
                <p className="step-drawer__empty">
                  {step?.kind === 'mkdir' || step?.kind === 'touch' || step?.kind === 'approval'
                    ? `${step.kind} 不需要执行器参数，配置直接写在 content 的 YAML 里。`
                    : '未配置 options，使用执行器缺省值（解释器 sh，Windows 容器为 cmd）。'}
                </p>
              )}
            </div>
          </Tabs.TabPane>

          <Tabs.TabPane
            key="env"
            title={`环境变量${step?.env?.length ? ` (${step.env.length})` : ''}`}
          >
            <div className="step-drawer__env">
              <EnvList env={step?.env} />
            </div>
          </Tabs.TabPane>

          <Tabs.TabPane key="input" title="输入">
            <div className="step-drawer__script">
              {step?.content ? (
                <>
                  {(step.kind === 'xexec' || step.kind === 'container') && (
                    <div className="step-drawer__interpreter">
                      <span>解释器</span>
                      <span className="u-mono">{shell || '缺省'}</span>
                    </div>
                  )}
                  <div className="step-drawer__script-editor">
                    <CodeEditor
                      value={step.content}
                      language={scriptLanguage(step.kind, shell)}
                      readOnly
                    />
                  </div>
                </>
              ) : (
                <p className="step-drawer__empty">无脚本内容</p>
              )}
            </div>
          </Tabs.TabPane>

          <Tabs.TabPane key="output" title="输出">
            <div className="step-drawer__log">
              <LogViewer
                lines={log.lines}
                truncated={log.truncated}
                emptyText={
                  step?.state === 'pending'
                    ? '步骤尚未开始执行'
                    : step?.state === 'paused'
                      ? '步骤已挂起'
                      : '暂无输出'
                }
              />
            </div>
          </Tabs.TabPane>
        </Tabs>
      )}
    </Drawer>
  )
}
