/**
 * 应用入口
 */

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { ConfigProvider } from '@arco-design/web-react'
import { setCreateRoot } from '@arco-design/web-react/es/_util/react-dom'
import zhCN from '@arco-design/web-react/es/locale/zh-CN'
import { App } from './App'
import { stream } from './api/socket'
import { initTheme } from './store/theme'
import './styles/index.scss'

// Arco's static Modal and Message APIs need the React 19 client renderer.
setCreateRoot(createRoot)

// 在首次渲染前写入 data-theme 与 arco-theme，避免深浅主题闪跳
initTheme()

// 实时连接随应用启动建立并常驻；页面进出只增删订阅，不重建连接
stream().open()

const container = document.getElementById('root')
if (!container) {
  throw new Error('未找到挂载点 #root')
}

createRoot(container).render(
  <StrictMode>
    <ConfigProvider locale={zhCN}>
      <App />
    </ConfigProvider>
  </StrictMode>,
)
