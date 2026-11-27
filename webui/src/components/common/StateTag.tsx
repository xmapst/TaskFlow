/** 执行状态标签，统一使用 `constants/state.ts` 中的文案与配色。 */

import type { JSX } from 'react'
import type { CSSProperties } from 'react'
import { getStateMeta } from '@/constants/state'
import './StateTag.scss'

export interface StateTagProps {
  state: string | undefined
  /** dot 只显示圆点与文字，solid 为实心底，soft 为浅底（默认） */
  variant?: 'soft' | 'solid' | 'dot'
  size?: 'small' | 'default'
  className?: string
}

export function StateTag({
  state,
  variant = 'soft',
  size = 'default',
  className,
}: StateTagProps): JSX.Element {
  const meta = getStateMeta(state)

  // 通过内联自定义属性把状态色注入，样式表只消费变量，无需为每个状态写一条规则
  const style: CSSProperties = {
    ['--tag-color' as string]: `var(--c-state-${meta.token})`,
    ['--tag-soft' as string]: `var(--c-state-${meta.token}-soft)`,
  }

  const classes = [
    'state-tag',
    `state-tag--${variant}`,
    `state-tag--${size}`,
    meta.pulse ? 'state-tag--pulse' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <span className={classes} style={style} title={meta.label}>
      <span className="state-tag__dot" aria-hidden="true" />
      <span className="state-tag__label">{meta.label}</span>
    </span>
  )
}

/** 仅圆点，用于表格行首等空间紧张处 */
export function StateDot({ state }: { state: string | undefined }): JSX.Element {
  const meta = getStateMeta(state)
  const style: CSSProperties = {
    ['--tag-color' as string]: `var(--c-state-${meta.token})`,
  }
  return (
    <span
      className={`state-dot${meta.pulse ? ' state-dot--pulse' : ''}`}
      style={style}
      title={meta.label}
      aria-label={meta.label}
    />
  )
}
