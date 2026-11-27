/**
 * DAG 步骤节点
 *
 * 一张卡片只说三件事：叫什么、干完了没有、花了多少代价。
 * 状态用左侧色轨与状态标签双重编码，缩略视图下靠色轨，正常视图下靠标签。
 * 类型（shell / container）是配置事实，放到底部与统计并列，不和名称抢行。
 */

import { type JSX, memo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import type { StepRes } from '@/types/api'
import { getStateMeta, getExitCodeLevel } from '@/constants/state'
import { displayTime, elapsedBetween } from '@/utils/format'
import { StateTag } from '@/components/common/StateTag'
import './StepNode.scss'

export interface StepNodeData extends Record<string, unknown> {
  step: StepRes
  taskName: string
}

function StepNodeInner({ data, selected }: NodeProps): JSX.Element {
  const { step } = data as StepNodeData
  const meta = getStateMeta(step.state)
  const elapsed = elapsedBetween(step.time?.start, step.time?.end)
  const codeLevel = getExitCodeLevel(step.code)

  const classes = [
    'step-node',
    `step-node--${meta.token}`,
    selected ? 'step-node--selected' : '',
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <div
      className={classes}
      style={{
        ['--node-color' as string]: `var(--c-state-${meta.token})`,
        ['--node-soft' as string]: `var(--c-state-${meta.token}-soft)`,
      }}
    >
      {/* 连接点：左入右出，与 LR 布局方向一致。视觉上隐藏，只作连线锚点 */}
      <Handle type="target" position={Position.Left} className="step-node__handle" isConnectable={false} />
      <Handle type="source" position={Position.Right} className="step-node__handle" isConnectable={false} />

      <span className="step-node__rail" />

      <div className="step-node__head">
        <span className="step-node__name" title={step.name}>
          {step.name}
        </span>
        <StateTag state={step.state} size="small" />
      </div>

      <div className="step-node__foot">
        {step.kind ? <span className="step-node__kind u-mono">{step.kind}</span> : null}
        <span className="step-node__stats">
          <span className="step-node__stat">
            <span className="step-node__stat-key">退出码</span>
            <span className={`step-node__stat-value u-mono step-node__stat-value--${codeLevel}`}>
              {step.code ?? '—'}
            </span>
          </span>
          <span className="step-node__stat">
            <span className="step-node__stat-key">耗时</span>
            <span className="step-node__stat-value u-mono">
              {elapsed || (step.time?.start ? '进行中' : '—')}
            </span>
          </span>
        </span>
      </div>

      {/* 起始时间放在 title 里，避免节点信息过载 */}
      <span className="u-sr-only">开始时间 {displayTime(step.time?.start)}</span>
    </div>
  )
}

/**
 * 节点会随 WebSocket 每秒重渲染，memo 后仅在关键字段变化时更新。
 * 比较函数只看影响渲染的字段，忽略 msg（可能是整段日志，比较成本高）。
 */
export const StepNode = memo(StepNodeInner, (previous, next) => {
  const a = (previous.data as StepNodeData).step
  const b = (next.data as StepNodeData).step
  return (
    previous.selected === next.selected &&
    a.name === b.name &&
    a.state === b.state &&
    a.code === b.code &&
    a.kind === b.kind &&
    a.time?.start === b.time?.start &&
    a.time?.end === b.time?.end
  )
})
