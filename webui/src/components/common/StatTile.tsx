/**
 * 统计磁贴
 *
 * 用于任务管理页顶部的状态计数。可点击时兼作筛选入口。
 * 数值为 0 时降低视觉权重，避免一排零值抢走注意力。
 */

import type { JSX } from 'react'
import type { CSSProperties, ReactNode } from 'react'
import './StatTile.scss'

export interface StatTileProps {
  label: string
  value: number
  /** 状态令牌名，取自 STATE_META.token；不传则用强调色 */
  token?: string
  icon?: ReactNode
  /** 附加说明，如「当前页」 */
  hint?: string
  active?: boolean
  onClick?: () => void
}

export function StatTile({
  label,
  value,
  token,
  icon,
  hint,
  active = false,
  onClick,
}: StatTileProps): JSX.Element {
  const style: CSSProperties = {
    ['--tile-color' as string]: token ? `var(--c-state-${token})` : 'var(--c-accent)',
    ['--tile-soft' as string]: token ? `var(--c-state-${token}-soft)` : 'var(--c-accent-soft)',
  }

  const classes = [
    'stat-tile',
    onClick ? 'stat-tile--clickable' : '',
    active ? 'stat-tile--active' : '',
    value === 0 ? 'stat-tile--empty' : '',
  ]
    .filter(Boolean)
    .join(' ')

  const content = (
    <>
      {icon ? <span className="stat-tile__icon">{icon}</span> : null}
      <span className="stat-tile__body">
        <span className="stat-tile__value u-mono">{value}</span>
        <span className="stat-tile__label">{label}</span>
        {hint ? <span className="stat-tile__hint">{hint}</span> : null}
      </span>
    </>
  )

  if (!onClick) {
    return (
      <div className={classes} style={style}>
        {content}
      </div>
    )
  }

  return (
    <button type="button" className={classes} style={style} onClick={onClick}>
      {content}
    </button>
  )
}
