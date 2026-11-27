/**
 * 路由定义
 *
 * 使用 HashRouter：页面路径放在 URL 哈希中，直接访问和刷新时
 * 服务端只需提供根路径的 HTML 和 /assets/ 静态资源。
 */

import { type JSX, Suspense, lazy } from 'react'
import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from './components/layout/AppShell'
import { LoadingBlock } from './components/common/Layout'

// 按页面切分，首屏只加载任务列表所需代码
const TasksPage = lazy(() =>
  import('./pages/TasksPage').then((module) => ({ default: module.TasksPage })),
)
const TaskDetailPage = lazy(() =>
  import('./pages/TaskDetailPage').then((module) => ({ default: module.TaskDetailPage })),
)
const EventsPage = lazy(() =>
  import('./pages/EventsPage').then((module) => ({ default: module.EventsPage })),
)

const TerminalPage = lazy(() =>
  import('./pages/TerminalPage').then((module) => ({ default: module.TerminalPage })),
)

const ReplayPage = lazy(() =>
  import('./pages/ReplayPage').then((module) => ({ default: module.ReplayPage })),
)

const WebShellSessionsPage = lazy(() =>
  import('./pages/WebShellSessions').then((module) => ({ default: module.WebShellSessionsPage })),
)

export function App(): JSX.Element {
  return (
    <HashRouter>
      <Routes>
        <Route element={<AppShell />}>
          <Route path="/" element={<Navigate to="/tasks" replace />} />
          <Route
            path="/tasks"
            element={
              <Suspense fallback={<LoadingBlock tip="加载任务管理" />}>
                <TasksPage />
              </Suspense>
            }
          />
          <Route
            path="/tasks/:taskName"
            element={
              <Suspense fallback={<LoadingBlock tip="加载任务详情" />}>
                <TaskDetailPage />
              </Suspense>
            }
          />
          <Route
            path="/events"
            element={
              <Suspense fallback={<LoadingBlock tip="加载事件中心" />}>
                <EventsPage />
              </Suspense>
            }
          />
          <Route path="/terminal" element={<Suspense fallback={<LoadingBlock tip="加载终端" />}><TerminalPage /></Suspense>} />
          <Route path="/terminal/sessions" element={<Suspense fallback={<LoadingBlock tip="加载在线会话" />}><WebShellSessionsPage /></Suspense>} />
          <Route path="/terminal/replays" element={<Suspense fallback={<LoadingBlock tip="加载录像回放" />}><ReplayPage /></Suspense>} />
          {/* 未匹配路径回落到任务列表 */}
          <Route path="*" element={<Navigate to="/tasks" replace />} />
        </Route>
      </Routes>
    </HashRouter>
  )
}
