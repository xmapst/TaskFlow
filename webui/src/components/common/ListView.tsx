import { type ReactNode, useState } from 'react'
import { Radio, Table, type TableProps } from '@arco-design/web-react'
import { IconGrid, IconList } from './icons'
import './ListView.scss'

export type ListViewMode = 'grid' | 'table'

export function useListView(key: string, fallback: ListViewMode = 'grid') {
  const [view, setView] = useState<ListViewMode>(() => {
    const saved = localStorage.getItem(key)
    return saved === 'grid' || saved === 'table' ? saved : fallback
  })
  const changeView = (next: ListViewMode) => {
    setView(next)
    localStorage.setItem(key, next)
  }
  return [view, changeView] as const
}

export function ListViewSwitch({ value, onChange }: { value: ListViewMode; onChange: (value: ListViewMode) => void }) {
  return <Radio.Group type="button" size="small" aria-label="列表显示方式" value={value} onChange={onChange}>
    <Radio value="grid" aria-label="卡片视图"><span className="list-view-option" title="卡片视图"><IconGrid size={14} />卡片</span></Radio>
    <Radio value="table" aria-label="条目视图"><span className="list-view-option" title="条目视图"><IconList size={14} />条目</span></Radio>
  </Radio.Group>
}

export function ListToolbar({ left, right }: { left?: ReactNode; right?: ReactNode }) {
  return <div className="list-toolbar">
    <div className="list-toolbar__left">{left}</div>
    <div className="list-toolbar__right">{right}</div>
  </div>
}

export function ListTable<T>(props: TableProps<T>) {
  return <Table<T> pagination={false} border={false} size="small" {...props} />
}

export function ListGrid({ children }: { children: ReactNode }) {
  return <div className="list-grid">{children}</div>
}

interface ListCardProps {
  title: string
  leading?: ReactNode
  actions?: ReactNode
  footer?: ReactNode
  children?: ReactNode
  tone?: string
  onOpen?: () => void
}

export function ListCard({ title, leading, actions, footer, children, tone, onOpen }: ListCardProps) {
  return <article className="list-card" data-tone={tone} onClick={onOpen}>
    <header className="list-card__head">
      {leading}
      <h3 className="list-card__title u-truncate" title={title}>
        {onOpen ? <button type="button" className="list-card__open u-truncate" onClick={(event) => { event.stopPropagation(); onOpen() }}>{title}</button> : title}
      </h3>
      {actions && <div className="list-card__actions" onClick={(event) => event.stopPropagation()}>{actions}</div>}
    </header>
    {children}
    {footer && <footer className="list-card__footer" onClick={(event) => event.stopPropagation()}>{footer}</footer>}
  </article>
}
