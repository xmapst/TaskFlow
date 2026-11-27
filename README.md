# TaskFlow

TaskFlow 是一个跨平台任务编排与执行服务，通过 API 定义和运行自定义任务，无需另行部署第三方调度服务。它基于有向无环图（DAG）调度步骤：有依赖的步骤按依赖顺序执行，互不依赖的步骤可以并行执行。

你可以通过 API 批量运行 Shell、PowerShell、Python 等脚本，用于自动化运维、进程巡检、软件安装与卸载、应用更新和补丁安装。

## 任务编排

![TaskFlow 任务 DAG：并行执行、失败重试、汇合与外部审批](images/dag.png)

并行分支汇合后发起外部审批。审批通过才执行发布；审批被拒绝或超时，发布不会执行。

## 主要功能

- [x] 支持 Windows、Linux 和 macOS
- [x] 动态调整工作池大小
- [x] 基于 DAG 编排和执行任务
- [x] 支持强制终止、暂停、恢复任务或步骤
- [x] 支持为任务或步骤设置超时
- [x] 按任务隔离工作目录，支持浏览、上传和下载文件
- [x] 支持通过 `--self_url` 配置自更新
- [x] 支持 WebShell
- [x] 支持外部审批步骤

## 命令行帮助

```text
用法：
  taskflow_linux_amd64_v1 [选项]
  taskflow_linux_amd64_v1 [命令]

可用命令：
  completion  生成命令补全脚本
  help        查看命令帮助
  version     查看版本信息

选项：
      --addr          服务监听地址（默认 tcp://127.0.0.1:2376）
      --exec_timeout  默认执行超时（默认 24h）
      --log_level     日志级别：debug、info、warn 或 error（默认 debug）
      --log_output    日志输出：file 或 stdout（默认 file）
      --pool_size     初始工作池大小（默认逻辑 CPU 核数的两倍）
      --root_dir      数据、日志及工作区的根目录
      --self_url      自更新发布地址
      --help          显示用法
  -v, --version       显示版本信息并退出

运行 taskflow_linux_amd64_v1 [命令] --help 查看指定命令的详细用法。
```

## 安装为系统服务

### Windows

以管理员身份打开 PowerShell，注册并启动服务：

```powershell
New-Service -Name taskflow -BinaryPathName "C:\taskflow\bin\taskflow_windows_amd64_v1.exe" -DisplayName "TaskFlow" -StartupType Automatic
sc.exe failure taskflow reset= 0 actions= restart/0/restart/0/restart/0
sc.exe start taskflow
```

### Linux

以具有管理员权限的用户创建并启动 systemd 服务：

```shell
cat > /etc/systemd/system/taskflow.service <<'EOF'
[Unit]
Description=TaskFlow 任务编排与执行服务
Documentation=https://github.com/xmapst/taskflow.git
After=network.target nss-lookup.target

[Service]
NoNewPrivileges=true
ExecStart=/usr/local/taskflow/bin/taskflow_linux_amd64_v1
Restart=on-failure
RestartSec=10s
LimitNOFILE=infinity

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now taskflow.service
```

## 本地编译（Linux）

需要 Docker、Make 和支持 Vite 8 的 Node.js（20.19+ 或 22.12+）。

```shell
git clone git@github.com:xmapst/taskflow.git
cd taskflow
make
```

## API 请求示例

### 创建任务

向 `POST /api/v1/task` 发送 JSON 或 YAML 格式的任务定义，并将 `Content-Type` 分别设为 `application/json` 或 `application/yaml`。任务创建后会进入队列。任务名由服务端生成；可选的 `workdir` 指定工作区内的相对目录，省略时默认使用任务名。可选的 `cleanup` 设为 `true` 时，任务结束后会删除该工作目录；默认 `false`，保留目录以便查看、下载产物或被后续任务复用。多个任务可以指定同一个 `workdir`，同时运行时需自行避免互相覆盖文件。

每个步骤需要指定 `kind` 和 `content`。没有入边的步骤可以并行执行；要按顺序执行，请在每个后续步骤中显式声明前置步骤。

`kind` 选择执行器：`xexec`、`mkdir`、`touch`、`container` 和 `approval`。`xexec` 在 `options.shell` 中指定工作节点已安装的解释器，默认使用 `sh`（Windows 下为 `cmd`）。`container` 在 `options.image` 中指定镜像，可在 `options.shell` 中指定容器内的解释器。

```sh
curl -X POST -H 'Content-Type: application/yaml' --data-binary @- \
  'http://localhost:2376/api/v1/task' <<'YAML'
# workdir: "example"  # 可选，相对于服务端工作区
# cleanup: true       # 可选，任务结束后删除工作目录，默认 false（保留）
timeout: 10m
step:
  - name: prepare
    kind: xexec
    options:
      shell: sh
    content: printf 'ready\n'
  - name: check
    kind: xexec
    options:
      shell: sh
    routing:
      edges:
        - step: prepare
    retryPolicy:
      maxAttempts: 2
      interval: 2s
    content: printf 'check complete\n'
  - name: finish
    kind: xexec
    options:
      shell: sh
    routing:
      edges:
        - step: check
    content: printf 'done\n'
YAML
```

#### 带标签的依赖、失败分支与汇合

所有前置依赖都通过 `routing.edges` 声明。以下是带标签的分支、失败处理和汇合示例：

```yaml
step:
  - name: prepare
    kind: xexec
    options:
      shell: sh
    content: printf 'ready\n'
    routing:
      emit:
        - success
        - publish
  - name: publish
    kind: xexec
    options:
      shell: sh
    content: printf 'published\n'
    routing:
      edges:
        - step: prepare
          trigger: publish
  - name: recover
    kind: xexec
    options:
      shell: sh
    content: printf 'recovered\n'
    routing:
      edges:
        - step: publish
          trigger: failure
  - name: report
    kind: xexec
    options:
      shell: sh
    content: printf 'reported\n'
    routing:
      join: any
      edges:
        - step: publish
        - step: recover
```

- `routing.edges` 的 `trigger` 缺省为 `success`；`failure` 只在上游最终失败时触发（不包括重试中的失败、跳过或取消）。失败处理器成功不会把整个任务的失败改为成功。
- `routing.join` 默认 `all`；`any` 表示所有上游结束后，只要有一条入边匹配就可执行。取消会沿入边传播，不会被 `any` 忽略。
- 成功步骤默认只发出 `success`；若配置 `routing.emit`，只发出所列标签。希望默认成功边仍能放行时，应把 `success` 也列出。标签区分大小写，`failure` 不能通过 `emit` 发出。
- Web UI 中，双击步骤打开「依赖与分支」，可设置入边的触发标签、汇合策略及成功标签；双击画布连线可直接打开下游步骤的该页签。画布与 YAML / JSON 双向同步。新建任务默认在画布右侧展开配置编辑器，可通过「折叠配置」把空间让给画布；再次展开会保留当前配置和格式。任务详情流程图也会展示带标签的依赖。

任务定义的 JSON 和 YAML 字段名区分大小写，未知字段会被拒绝。触发标签是字符串，不执行表达式。

#### 步骤重试

`retryPolicy.maxAttempts` 包含首次尝试。不配置 `retryPolicy` 时不会重试。设为 `-1` 可在任务超时或取消前持续重试；不可重试错误仍会立即结束。`retryPolicy.jitter` 可设为 `0` 到 `1`，用于随机缩短重试等待时间，减少同时重试。尝试次数用完后仍失败的步骤会使任务失败，阻止依赖它的步骤执行，但独立分支仍可继续运行。

审批步骤只在发起审批失败时重试（例如请求异常或收到非 2xx 响应）；审批被拒绝、等待审批结果超时或任务取消，都不会重新发起。每次重试使用新的审批 ID，已失败尝试的迟到回调会被忽略。

脚本可通过 `XEXEC_SAVE_ENV_FILE` 回传环境变量。回传值会记录在当前步骤；脚本成功后，也会写入任务级环境。随后启动的步骤从数据库读取这些值，不限于直接下游；步骤自己配置的同名变量优先。若多个步骤写入同名任务变量，以最后写入的值为准；需要保证读取顺序时，请声明依赖。

### 获取任务列表

```shell
curl 'http://localhost:2376/api/v1/task'
```

### 获取任务详情

```shell
curl 'http://localhost:2376/api/v1/task/example'
```

### 查看任务工作目录

```shell
curl 'http://localhost:2376/api/v1/task/example/workspace'
```

### 查看系统状态与调整工作池

`GET /api/v1/system` 返回当前生效的工作池大小、CPU 和内存指标，以及已挂载磁盘的使用情况。Web 控制台通过 `/api/v1/ws` 订阅 `system` 主题；服务端每 3 秒采样一次，有变化时推送。

通过 `PUT` 调整工作池大小：

```sh
curl -i -X PUT http://127.0.0.1:2376/api/v1/system \
  -H 'Content-Type: application/json' \
  -d '{"size":8}'
```

请将地址替换为服务的监听地址。`size` 必须是 1 至 150 之间的整数。返回空响应体的 `202 Accepted` 表示调整已经完成；`503 Service Unavailable` 表示调整服务不可用，或已有一次调整正在进行。缩容时，会等待即将退出的工作进程完成手头任务。调整仅在内存中生效，重启后重新使用 `pool_size` 配置。请在网络或网关层限制此运维接口，仅允许受信任的调用方访问。

### 控制任务

将示例中的 `example` 替换为任务名：

```shell
# 强制终止任务
curl -X PUT 'http://localhost:2376/api/v1/task/example?action=kill'

# 暂停任务（仅待执行的任务可暂停）
curl -X PUT 'http://localhost:2376/api/v1/task/example?action=pause'

# 暂停任务 5 分钟
curl -X PUT 'http://localhost:2376/api/v1/task/example?action=pause&duration=5m'

# 恢复任务
curl -X PUT 'http://localhost:2376/api/v1/task/example?action=resume'
```

### 获取步骤控制台输出

```shell
curl 'http://localhost:2376/api/v1/task/example/step/prepare/log'
```

### 控制步骤

将示例中的 `example` 和 `prepare` 分别替换为任务名和步骤名：

```shell
# 强制终止步骤
curl -X PUT 'http://localhost:2376/api/v1/task/example/step/prepare?action=kill'

# 暂停步骤（仅待执行的步骤可暂停）
curl -X PUT 'http://localhost:2376/api/v1/task/example/step/prepare?action=pause'

# 暂停步骤 5 分钟
curl -X PUT 'http://localhost:2376/api/v1/task/example/step/prepare?action=pause&duration=5m'

# 恢复步骤
curl -X PUT 'http://localhost:2376/api/v1/task/example/step/prepare?action=resume'
```

### 响应状态码

| `code` | 含义 |
| --- | --- |
| `0` | 成功 |
| `1001` | 执行中 |
| `1002` | 失败 |
| `1003` | 未找到 |
| `1004` | 待执行 |
| `1005` | 已暂停 |
| `1006` | 已跳过 |


## WebShell 工作台

WebShell 使用单个工作区，最多支持 4 个独立终端分屏。点击分屏右上角的「×」只关闭该终端；其余分屏会自动补位并保持连接。每个分屏可单独复制、粘贴和传输文件。

在终端内右键可复制、粘贴、拆分、清屏、上传和下载；上传和下载作用于右键点击的终端。顶部只保留帮助和最大化按钮。请在空白 Shell 提示符下启动文件传输：上传到当前目录，下载可逐行输入文件路径。`sz` 下载自动交给浏览器，不需要站点授权保存目录。保存位置遵循浏览器设置；若开启「下载前询问保存位置」，浏览器仍会弹出自己的提示。HTTPS 或 localhost 上支持 Service Worker 的浏览器可流式下载。右键菜单为 `sz` 启用发送窗口，手动下载大文件也可用 `sz -w 262144 -- 文件名`。流式下载不可用时，会在接收完成后自动触发普通下载（每批最多暂存 64 MiB）。

`rz`（上传）和 `sz 文件名`（下载）由 Shell 中安装的 `lrzsz` 执行，协议经当前 WebShell 的 PTY 原样传输。请在 WebShell 的第一层 Shell 运行这两个命令。普通 `tmux` pane 不会把 ZMODEM 字节原样透传；从 Windows WebShell 进入 `wsl.exe` 也多了一层 ConPTY 转换。需要操作 WSL 内的文件时，建议在 WSL 内运行 Linux 版 TaskFlow，直接使用 Linux PTY。

上传和下载均在转发 `ZFILE` 文件头和元数据之前调用 `FileOffered` 审计回调。回调拒绝或报错时，该文件的提议不会到达接收方，当前终端连接会断开。内置回调默认允许通过校验的文件提议；文件名和大小由发送方声明，不能代替文件系统权限检查。

服务在 `info` 级别输出 `WebShell ZMODEM file audit` 结构化日志，记录会话、方向、协议声明的文件名和大小，以及逐文件结果。`acknowledged` 表示观察到接收方在发送方 `ZEOF` 后回复确认，不表示已核验磁盘路径或文件落盘，也不代表文件授权。审计不记录文件内容。
