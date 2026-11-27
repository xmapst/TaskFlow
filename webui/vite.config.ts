import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  base: '/',
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 3000,
    host: '0.0.0.0',
    proxy: {
      // 接口与实时通道统一代理到后端默认监听端口，ws 一并透传
      '/api': {
        target: 'http://localhost:2376',
        changeOrigin: false,
        secure: false,
        ws: true,
      },
      '/version': { target: 'http://localhost:2376', changeOrigin: false, secure: false },
      '/healthyz': { target: 'http://localhost:2376', changeOrigin: false, secure: false },
      '/heartbeat': { target: 'http://localhost:2376', changeOrigin: false, secure: false },
    },
  },
  build: {
    // 直接产出到后端 embed 的静态目录（internal/server 用 go:embed 收录）
    outDir: '../internal/server/static',
    assetsDir: 'assets',
    emptyOutDir: true,
    sourcemap: false,
    target: 'es2022',
    cssMinify: true,
    chunkSizeWarningLimit: 1200,
    rolldownOptions: {
      onLog(level, log, handler) {
        // @xyflow/react 的间接依赖带有 React Compiler 指令；打包阶段不使用该指令。
        if (level === 'warn' && log.code === 'MODULE_LEVEL_DIRECTIVE' &&
          log.message.includes('"use no memo"') &&
          /[\\/]react-compiler-runtime[\\/]dist[\\/]index\.js\b/.test(log.id ?? log.loc?.file ?? log.message)) {
          return
        }
        handler(level, log)
      },
      output: {
        // 按依赖体量分包，避免首屏一次性加载全部第三方代码。
        // 用函数式而非对象式：Rollup 的类型定义里对象式与函数式互斥，
        // 且函数式能覆盖到间接依赖（如 arco 依赖的 b-tween）。
        manualChunks(id: string): string | undefined {
          if (!id.includes('node_modules')) return undefined
          if (/[\\/]node_modules[\\/](react|react-dom|scheduler|react-router)/.test(id)) {
            return 'react'
          }
          if (id.includes('@arco-design')) return 'arco'
          if (id.includes('@xyflow') || id.includes('dagre')) return 'flow'
          if (id.includes('codemirror') || id.includes('@lezer')) return 'editor'
          return 'vendor'
        },
      },
    },
  },
})
