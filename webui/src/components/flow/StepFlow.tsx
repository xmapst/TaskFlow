/**
 * DAG 流程画布
 *
 * 一个关键取舍：布局只在「拓扑结构变化」时重算，状态变化不触发重排。
 * 后端每秒推送一次步骤列表，如果每帧都跑 dagre 并替换坐标，节点会持续
 * 抖动、用户手动拖动的位置也会被冲掉。这里用拓扑指纹判断是否需要重排，
 * 状态更新只替换节点的 data 与连线的样式类。
 */

import { type JSX, useCallback, useEffect, useMemo, useRef } from 'react'
import {
  ReactFlow,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  useNodesState,
  useEdgesState,
  useReactFlow,
  ReactFlowProvider,
  type Edge,
  type Node,
  type NodeMouseHandler,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { StepRes } from '@/types/api'
import { StepNode, type StepNodeData } from './StepNode'
import { layoutSteps, edgeLook } from './layout'
import './StepFlow.scss'

const nodeTypes = { step: StepNode }

export interface StepFlowProps {
  steps: readonly StepRes[]
  taskName: string
  /** 点击节点，用于打开步骤详情 */
  onSelectStep?: (stepName: string) => void
  /** 右键节点，用于弹出操作菜单 */
  onContextStep?: (stepName: string, position: { x: number; y: number }) => void
}

/**
 * 拓扑指纹：只包含节点集合与依赖关系。
 * 状态、退出码、时间的变化不改变指纹，因此不会触发重排。
 */
function topologyKey(steps: readonly StepRes[]): string {
  return steps
    .map((step) => `${step.name}<${JSON.stringify(step.routing?.edges ?? [])}`)
    .sort()
    .join('|')
}

function StepFlowInner({
  steps,
  taskName,
  onSelectStep,
  onContextStep,
}: StepFlowProps): JSX.Element {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<StepNodeData>>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const { fitView } = useReactFlow()

  const lastTopology = useRef('')
  /** 首次布局后自动 fitView，之后不再打扰用户的视口 */
  const fitted = useRef(false)

  const topology = useMemo(() => topologyKey(steps), [steps])

  useEffect(() => {
    if (steps.length === 0) {
      setNodes([])
      setEdges([])
      lastTopology.current = ''
      fitted.current = false
      return
    }

    if (topology !== lastTopology.current) {
      // 拓扑变化：重新布局
      const result = layoutSteps(steps, taskName)
      setNodes(result.nodes)
      setEdges(result.edges)
      lastTopology.current = topology

      if (!fitted.current) {
        fitted.current = true
        // 等一帧让节点完成挂载再计算视口，否则尺寸为 0
        requestAnimationFrame(() => {
          // maxZoom 1：小图不放大到失真，大图才缩小，保持卡片始终是"读得清"的尺寸
          fitView({ padding: 0.16, duration: 260, maxZoom: 1 })
        })
      }
      return
    }

    // 拓扑未变：只更新节点数据与连线状态，保留现有坐标
    const byName = new Map(steps.map((step) => [step.name, step]))
    setNodes((previous) =>
      previous.map((node) => {
        const step = byName.get(node.id)
        if (!step) return node
        const currentStep = node.data.step
        // 关键字段未变则保持原对象引用，配合 StepNode 的 memo 免去重渲染
        if (
          currentStep.state === step.state &&
          currentStep.code === step.code &&
          currentStep.time?.start === step.time?.start &&
          currentStep.time?.end === step.time?.end
        ) {
          return node
        }
        return { ...node, data: { step, taskName } }
      }),
    )

    setEdges((previous) =>
      previous.map((edge) => {
        const target = byName.get(edge.target)
        if (!target) return edge
        const className = edgeLook(target.state).className
        if (edge.className === className) return edge
        return { ...edge, className }
      }),
    )
  }, [steps, topology, taskName, setNodes, setEdges, fitView])

  const handleNodeClick = useCallback<NodeMouseHandler>(
    (_event, node) => {
      onSelectStep?.(node.id)
    },
    [onSelectStep],
  )

  const handleNodeContextMenu = useCallback<NodeMouseHandler>(
    (event, node) => {
      event.preventDefault()
      onContextStep?.(node.id, { x: event.clientX, y: event.clientY })
    },
    [onContextStep],
  )

  return (
    <div className="step-flow">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        nodeTypes={nodeTypes}
        onNodeClick={handleNodeClick}
        onNodeContextMenu={handleNodeContextMenu}
        minZoom={0.2}
        maxZoom={1.6}
        // 只读画布：不允许手工连线或删除，拓扑由任务配置决定
        nodesConnectable={false}
        nodesDraggable
        elementsSelectable
        deleteKeyCode={null}
        // 大图上只渲染视口内的节点与连线
        onlyRenderVisibleElements
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} className="step-flow__bg" />
        <MiniMap
          className="step-flow__minimap"
          position="bottom-right"
          pannable
          zoomable={false}
          nodeStrokeWidth={0}
        />
        <Controls showInteractive={false} className="step-flow__controls" />
      </ReactFlow>
    </div>
  )
}

/** useReactFlow 需要 Provider 上下文，故在此包一层 */
export function StepFlow(props: StepFlowProps): JSX.Element {
  return (
    <ReactFlowProvider>
      <StepFlowInner {...props} />
    </ReactFlowProvider>
  )
}
