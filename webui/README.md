# TaskFlow Web UI

基于 React、TypeScript 和 Arco Design 的任务管理前端。使用 React Flow 展示步骤依赖，使用 CodeMirror 编辑 YAML，并通过 tus 支持断点续传。

## 本地开发

使用 Node.js 22.12 或更新版本。以下命令在 `webui/` 目录执行：

```bash
npm ci
npm run dev
```

Vite 默认监听 <http://localhost:3000>，并将 API 和实时连接转发到 `http://localhost:2376`。代理配置见 `vite.config.ts`。

## 类型检查与生产构建

```bash
npm run typecheck
npm run build
```

`npm run build` 会先运行 TypeScript 类型检查，再将静态资源输出至 `../internal/server/static`。该目录由 Go 后端通过 `go:embed` 嵌入；要让后端二进制包含最新的 Web UI，请先构建前端，再编译 Go 后端。

## 源码目录

- `src/api/`、`src/types/`：后端接口及类型定义。
- `src/pages/`：任务列表、任务详情、YAML 创建表单和事件页面。
- `src/components/`：共享界面组件、DAG 和编辑器。
- `src/hooks/`、`src/store/`：共享逻辑和应用状态。
