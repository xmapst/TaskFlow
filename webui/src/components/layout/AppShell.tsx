/**
 * 应用外壳：侧栏 + 顶栏 + 内容区
 *
 * 侧栏承载导航与版本信息，顶栏承载当前位置、服务连通状态与主题切换。
 * 事件中心的未读数以角标呈现在侧栏，让用户在其他页面也能感知到新事件。
 */

import { type JSX, useCallback, useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { Badge, Button, Tooltip } from '@arco-design/web-react'
import { SystemInfoDialog } from './SystemInfoDialog'
import { useThemeStore } from '@/store/theme'
import { useEventStore } from '@/store/events'
import { useVersion } from '@/hooks/useVersion'
import {
  IconActivity,
  IconChevronLeft,
  IconChevronRight,
  IconList,
  IconLogo,
  IconMoon,
  IconPlay,
  IconSun,
  IconTasks,
  IconTerminal,
} from '@/components/common/icons'
import './AppShell.scss'

interface NavItem {
  to: string
  label: string
  icon: JSX.Element
  /** 该项是否展示未读角标 */
  badge?: boolean
  end?: boolean
}

const NAV_ITEMS: NavItem[] = [
  { to: '/tasks', label: '任务', icon: <IconTasks size={18} /> },
  { to: '/events', label: '事件', icon: <IconActivity size={18} />, badge: true },
  { to: '/terminal', label: '终端', icon: <IconTerminal size={18} />, end: true },
  { to: '/terminal/sessions', label: '会话', icon: <IconList size={18} /> },
  { to: '/terminal/replays', label: '录像', icon: <IconPlay size={18} /> },
]

const COLLAPSE_KEY = 'aef-sidebar-collapsed'

export function AppShell(): JSX.Element {
  const location = useLocation()
  const themeMode = useThemeStore((state) => state.mode)
  const toggleTheme = useThemeStore((state) => state.toggle)
  const { version, healthy } = useVersion()

  const unread = useEventStore((state) => state.unread)
  const connectEvents = useEventStore((state) => state.connect)
  const disconnectEvents = useEventStore((state) => state.disconnect)
  const markRead = useEventStore((state) => state.markRead)

  const [collapsed, setCollapsed] = useLocalCollapsed()
  const [systemOpen, setSystemOpen] = useState(false)

  // 事件流全局常驻：切换页面不断开，否则会漏掉期间发生的事件
  useEffect(() => {
    connectEvents()
    return disconnectEvents
  }, [connectEvents, disconnectEvents])

  // 进入事件中心即清零未读
  useEffect(() => {
    if (location.pathname.startsWith('/events')) markRead()
  }, [location.pathname, markRead])

  const currentLabel =
    NAV_ITEMS.find((item) => location.pathname === item.to ||
      (!item.end && location.pathname.startsWith(`${item.to}/`)))?.label ?? '任务管理'

  return (
    <div className={`shell${collapsed ? ' shell--collapsed' : ''}`}>
      <aside className="shell__side">
        <div className="shell__brand">
          <span className="shell__brand-mark">
            <IconLogo size={22} />
          </span>
          {!collapsed ? (
            <span className="shell__brand-text">
              <strong>TaskFlow</strong>
              <em>任务编排与执行</em>
            </span>
          ) : null}
        </div>

        <nav className="shell__nav">
          {NAV_ITEMS.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end ?? false}
              className={({ isActive }) => `shell__nav-item${isActive ? ' is-active' : ''}`}
              title={collapsed ? item.label : undefined}
            >
              <span className="shell__nav-icon">{item.icon}</span>
              {!collapsed ? <span className="shell__nav-label">{item.label}</span> : null}
              {item.badge && unread > 0 ? (
                <Badge dot className="shell__nav-badge" />
              ) : null}
            </NavLink>
          ))}
        </nav>

        <div className="shell__side-foot">
          {!collapsed && version ? (
            <div className="shell__version">
              <span className="u-mono">{version.version}</span>
              <span className="shell__version-meta u-mono">
                {version.go.os}/{version.go.arch}
              </span>
            </div>
          ) : null}
          <button
            type="button"
            className="shell__collapse"
            onClick={() => setCollapsed(!collapsed)}
            title={collapsed ? '展开侧栏' : '收起侧栏'}
          >
            {collapsed ? <IconChevronRight size={16} /> : <IconChevronLeft size={16} />}
            {!collapsed ? <span>收起</span> : null}
          </button>
        </div>
      </aside>

      <div className="shell__main">
        <header className="shell__top">
          <h1 className="shell__top-title">{currentLabel}</h1>

          <div className="shell__top-right">
            <Tooltip
              content={
                healthy
                  ? `服务正常${version ? ` · 构建于 ${version.buildTime}` : ''}`
                  : '无法连接到服务，请检查后端是否运行'
              }
            >
              <span className={`shell__health${healthy ? '' : ' shell__health--down'}`}>
                <span className="shell__health-dot" />
                {healthy ? '服务正常' : '服务不可达'}
              </span>
            </Tooltip>

            <Button size="small" type="secondary" onClick={() => setSystemOpen(true)}>系统信息</Button>
            <button
              type="button"
              className="shell__theme"
              onClick={toggleTheme}
              title={themeMode === 'dark' ? '切换到浅色主题' : '切换到深色主题'}
            >
              {themeMode === 'dark' ? <IconSun size={16} /> : <IconMoon size={16} />}
            </button>
          </div>
        </header>

        <main className="shell__content">
          <Outlet />
        </main>
      </div>
      {systemOpen && <SystemInfoDialog onClose={() => setSystemOpen(false)} />}
    </div>
  )
}

/** 侧栏折叠状态持久化，刷新后保持用户选择 */
function useLocalCollapsed(): [boolean, (next: boolean) => void] {
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSE_KEY) === 'true')

  const update = useCallback((next: boolean): void => {
    localStorage.setItem(COLLAPSE_KEY, String(next))
    setCollapsed(next)
  }, [])

  return [collapsed, update]
}
