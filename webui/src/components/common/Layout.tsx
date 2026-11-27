/**
 * 共享布局与展示件
 *
 * 都是小型无状态组件，集中在一处便于统一间距与排版；
 * 拆成多文件反而让「同类元素长得不一样」的风险变高。
 */

import type { JSX } from 'react'
import type { ReactNode } from 'react'
import { Empty, Spin, Tooltip } from '@arco-design/web-react'
import { EMPTY } from '@/utils/format'
import './Layout.scss'

// ============================================================
// 页面骨架
// ============================================================

export interface PageProps {
  /** 工具栏区域：主操作与筛选 */
  toolbar?: ReactNode
  /** 统计区域 */
  stats?: ReactNode
  /** 底部区域，通常是分页 */
  footer?: ReactNode
  children: ReactNode
  /** 内容区是否自行滚动 */
  scrollable?: boolean
  /** 内容区作为 flex 列容器，子级（如 grow 的 Panel）可撑满剩余高度并各自滚动；与 scrollable 互斥 */
  fill?: boolean
}

export function Page({ toolbar, stats, footer, children, scrollable = true, fill = false }: PageProps): JSX.Element {
  const contentClass = fill ? ' page__content--fill' : scrollable ? ' page__content--scroll' : ''
  return (
    <div className="page">
      {toolbar ? <div className="page__toolbar">{toolbar}</div> : null}
      {stats ? <div className="page__stats">{stats}</div> : null}
      <div className={`page__content${contentClass}`}>{children}</div>
      {footer ? <div className="page__footer">{footer}</div> : null}
    </div>
  )
}

// ============================================================
// 面板
// ============================================================

export interface PanelProps {
  title: ReactNode
  icon?: ReactNode
  /** 标题右侧的附加信息，如计数或状态 */
  extra?: ReactNode
  /** 头部右侧的操作区 */
  actions?: ReactNode
  children: ReactNode
  /** 取消内容区内边距，用于图表与日志等自带留白的内容 */
  flush?: boolean
  /** 让面板在 flex 容器中占满剩余高度 */
  grow?: boolean
  className?: string
}

export function Panel({
  title,
  icon,
  extra,
  actions,
  children,
  flush = false,
  grow = false,
  className,
}: PanelProps): JSX.Element {
  const classes = ['panel', grow ? 'panel--grow' : '', className ?? ''].filter(Boolean).join(' ')
  return (
    <section className={classes}>
      <header className="panel__head">
        <div className="panel__title">
          {icon ? <span className="panel__icon">{icon}</span> : null}
          <span className="panel__title-text">{title}</span>
          {extra ? <span className="panel__extra">{extra}</span> : null}
        </div>
        {actions ? <div className="panel__actions">{actions}</div> : null}
      </header>
      <div className={`panel__body${flush ? ' panel__body--flush' : ''}`}>{children}</div>
    </section>
  )
}

// ============================================================
// 键值信息项
// ============================================================

export interface MetaItemProps {
  label: string
  /** 值为空时自动显示占位符 */
  value?: ReactNode
  /** 值使用等宽字体，适合时间、退出码、路径 */
  mono?: boolean
  /** 超长值是否允许换行 */
  wrap?: boolean
  /** 悬浮提示，用于展示被截断的完整值 */
  tip?: string
}

export function MetaItem({ label, value, mono, wrap, tip }: MetaItemProps): JSX.Element {
  const isEmpty = value === undefined || value === null || value === ''
  const classes = [
    'meta-item__value',
    mono ? 'u-mono' : '',
    wrap ? 'u-break' : 'u-truncate',
    isEmpty ? 'meta-item__value--empty' : '',
  ]
    .filter(Boolean)
    .join(' ')

  const content = <span className={classes}>{isEmpty ? EMPTY : value}</span>

  return (
    <div className="meta-item">
      <span className="meta-item__label">{label}</span>
      {tip && !isEmpty ? (
        <Tooltip content={tip} mini>
          {content}
        </Tooltip>
      ) : (
        content
      )}
    </div>
  )
}

/** 键值项的容器，控制统一的行间距 */
export function MetaList({ children }: { children: ReactNode }): JSX.Element {
  return <div className="meta-list">{children}</div>
}

// ============================================================
// 空态与加载态
// ============================================================

export interface EmptyHintProps {
  description: ReactNode
  /** 补充说明，解释为何为空及如何解决 */
  hint?: ReactNode
  action?: ReactNode
  compact?: boolean
}

export function EmptyHint({ description, hint, action, compact }: EmptyHintProps): JSX.Element {
  return (
    <div className={`empty-hint${compact ? ' empty-hint--compact' : ''}`}>
      <Empty description={description} />
      {hint ? <p className="empty-hint__text">{hint}</p> : null}
      {action ? <div className="empty-hint__action">{action}</div> : null}
    </div>
  )
}

/** 区块级加载态，用于首屏与切换数据源时 */
export function LoadingBlock({ tip = '加载中' }: { tip?: string }): JSX.Element {
  return (
    <div className="loading-block">
      <Spin tip={tip} />
    </div>
  )
}

// ============================================================
// 实时连接指示
// ============================================================

export interface LiveBadgeProps {
  live: boolean
  /** 服务端已推完数据（任务进入终态） */
  finished?: boolean
  /** 自定义标签文案 */
  label?: string
}

/**
 * 实时连接状态指示。
 *
 * 区分三态很重要：「已结束」与「已断开」在视觉上必须不同，
 * 否则用户无法判断日志停止滚动是因为任务完成还是连接掉了。
 */
export function LiveBadge({ live, finished, label }: LiveBadgeProps): JSX.Element {
  if (finished) {
    return (
      <span className="live-badge live-badge--done" title="服务端已推送完毕">
        <span className="live-badge__dot" />
        {label ?? '已结束'}
      </span>
    )
  }
  if (live) {
    return (
      <span className="live-badge live-badge--on" title="实时连接正常">
        <span className="live-badge__dot" />
        {label ?? '实时'}
      </span>
    )
  }
  return (
    <span className="live-badge live-badge--off" title="实时连接已断开，正在重试">
      <span className="live-badge__dot" />
      {label ?? '连接中'}
    </span>
  )
}

// ============================================================
// 环境变量列表
// ============================================================

export interface EnvListProps {
  env: readonly { name: string; value: string }[] | undefined
}

/** 环境变量以等宽键值对呈现，值可能很长故允许换行 */
export function EnvList({ env }: EnvListProps): JSX.Element {
  if (!env || env.length === 0) {
    return <p className="env-list__empty">未配置环境变量</p>
  }
  return (
    <table className="env-list">
      <thead>
        <tr><th scope="col">名称</th><th scope="col">值</th></tr>
      </thead>
      <tbody>
        {env.map((item) => (
          <tr key={item.name}>
            <td className="env-list__name u-mono">{item.name}</td>
            <td className="env-list__value u-mono u-break">{item.value || EMPTY}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
