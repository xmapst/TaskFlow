/**
 * 把步骤列表排成从左到右的 DAG。
 *
 * 排版目标不是"尽量紧凑"，而是"一眼能顺着方向读"：
 *  - 层间距明显大于层内间距，横向推进的方向感一眼可见；
 *  - 连线走圆角阶梯而非直角折线，减少尖角造成的杂乱；
 *  - 连线带箭头，方向不靠猜；
 *  - 连线配色只强调「正在发生」和「出错」，其余走中性色 —— 节点卡片已经
 *    逐条表达了状态，边再逐条上色，整张图就只剩颜色没有结构。
 */

import dagre from '@dagrejs/dagre'
import { MarkerType, type Edge, type Node, type SmoothStepPathOptions } from '@xyflow/react'
import type { ExecState, StepRes } from '@/types/api'
import type { StepNodeData } from './StepNode'

/** 节点卡片尺寸，必须与 StepNode.scss 中的实际尺寸一致，否则布局会重叠 */
export const NODE_WIDTH = 256
export const NODE_HEIGHT = 88

/** smoothstep 的圆角与折线偏移不在 Edge 基础类型上，这里补一条本地类型 */
type FlowEdge = Edge & { pathOptions?: SmoothStepPathOptions }

/** 连线的视觉分组：决定描边粗细、透明度与虚线，见 StepFlow.scss */
export type EdgeTone = 'active' | 'failed' | 'muted' | 'faint' | 'normal'

export interface EdgeLook {
  className: string
  /** 箭头必须显式给色：SVG marker 不继承 path 的 stroke */
  color: string
  tone: EdgeTone
}

/**
 * 连线外观由「目标步骤的状态」决定：一条边的意义是"依赖已满足、流程走到这里"，
 * 所以它的形态应当跟随被滋养的那一端。
 */
export function edgeLook(state: ExecState): EdgeLook {
  switch (state) {
    case 'running':
      return { className: 'flow-edge flow-edge--active', color: 'var(--c-state-running)', tone: 'active' }
    case 'failed':
      return { className: 'flow-edge flow-edge--failed', color: 'var(--c-state-failed)', tone: 'failed' }
    case 'paused':
      return { className: 'flow-edge flow-edge--muted', color: 'var(--c-state-paused)', tone: 'muted' }
    case 'skipped':
      return { className: 'flow-edge flow-edge--faint', color: 'var(--c-graph-edge)', tone: 'faint' }
    case 'pending':
    case 'unknown':
      return { className: 'flow-edge flow-edge--muted', color: 'var(--c-graph-edge)', tone: 'muted' }
    default:
      return { className: 'flow-edge flow-edge--normal', color: 'var(--c-graph-edge)', tone: 'normal' }
  }
}

export interface LayoutResult {
  nodes: Node<StepNodeData>[]
  edges: Edge[]
}

/**
 * 把步骤列表转换为带坐标的节点与连线。
 *
 * @param steps 步骤列表（后端已按依赖深度排序）
 * @param taskName 所属任务名，节点操作需要
 */
export function layoutSteps(steps: readonly StepRes[], taskName: string): LayoutResult {
  if (steps.length === 0) return { nodes: [], edges: [] }

  const graph = new dagre.graphlib.Graph()
  graph.setGraph({
    rankdir: 'LR',
    // 层间距 108 ≈ 半个卡片宽，而层内间距只有 34：横向推进的方向感由此建立
    ranksep: 108,
    nodesep: 34,
    edgesep: 26,
  })
  graph.setDefaultEdgeLabel(() => ({}))

  const known = new Set(steps.map((step) => step.name))

  for (const step of steps) {
    graph.setNode(step.name, { width: NODE_WIDTH, height: NODE_HEIGHT })
  }

  const edges: FlowEdge[] = []
  for (const step of steps) {
    // 合并同一对节点之间的触发标签，避免多条连线重叠
    const triggersByDependency = new Map<string, Set<string>>()
    for (const edge of step.routing?.edges ?? []) {
      const triggers = triggersByDependency.get(edge.step) ?? new Set<string>()
      triggers.add(edge.trigger || 'success')
      triggersByDependency.set(edge.step, triggers)
    }
    for (const [dependency, triggers] of triggersByDependency) {
      // 依赖可能指向已被删除或未创建的步骤，跳过以免 dagre 生成孤立节点
      if (!known.has(dependency)) continue
      graph.setEdge(dependency, step.name)

      const look = edgeLook(step.state)
      edges.push({
        id: `${dependency}->${step.name}`,
        source: dependency,
        target: step.name,
        type: 'smoothstep',
        pathOptions: { borderRadius: 14, offset: 20 },
        // 「成功」是默认触发条件，不标注；其余触发条件才需要说明
        label: triggers.size === 1 && triggers.has('success') ? undefined : [...triggers].join(' / '),
        // 流动效果由 .flow-edge--active 的 CSS 动画负责，不用 xyflow 自带的 animated：
        // 两者叠加会出现两套 dash 参数互相打断
        className: look.className,
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: look.color,
          width: 16,
          height: 16,
          markerUnits: 'userSpaceOnUse',
        },
      })
    }
  }

  dagre.layout(graph)

  const nodes: Node<StepNodeData>[] = steps.map((step) => {
    const positioned = graph.node(step.name)
    // dagre 给的是中心点，xyflow 需要左上角
    const x = positioned ? positioned.x - NODE_WIDTH / 2 : 0
    const y = positioned ? positioned.y - NODE_HEIGHT / 2 : 0
    return {
      id: step.name,
      type: 'step',
      position: { x, y },
      data: { step, taskName },
      // 尺寸交给 CSS，此处声明用于 minimap 与 fitView 计算
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
    }
  })

  return { nodes, edges }
}
