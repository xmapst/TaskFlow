# 默认执行器

步骤的 `kind` 只选择执行器，执行器自己的参数写在 `options` 里。`options` 是 JSON 对象，由各执行器自己解释，**未声明的键会被拒绝**（写错立即报错，而不是静默忽略），所以不同执行器的参数可以共存在同一个步骤定义里而不互相干扰。

| `kind` | 接受的 `options` 键 |
|---|---|
| `xexec` | `shell` |
| `container` | `image`、`shell`、`mounts` |
| `mkdir`、`touch`、`approval` | 无 |

## xexec

`content` 是脚本，`options.shell` 是解释器。`xexec` 一律把脚本交给 Unix 风格的解释器执行，所以 `shell` 只写一个程序名，不能带参数；可省略，缺省 `sh`（Windows 下 `cmd`）。

```text
step:
  - name: sh
    kind: xexec
    options:
      shell: sh
    content: |-
      ping -c 4 1.1.1.1
```

```text
step:
  - name: bash
    kind: xexec
    options:
      shell: bash
    content: |-
      set -o pipefail
      ping -c 4 1.1.1.1 | tail -2
```

```text
step:
  - name: python
    kind: xexec
    options:
      shell: python
    content: |-
      import os
      os.system('ping -c 4 1.1.1.1')
```

## mkdir

```text
step:
  - name: mkdir
    kind: mkdir
    content: |-
      path: a/b/c/d/e
```

## touch

```text
step:
  - name: touch
    kind: touch
    content: |-
      path: a/b/c/d/e/f.txt
      overwrite: true
      content: |-
        content of f.txt      
```

## container

在容器里执行 `content`。`kind` 固定为 `container`，镜像写在 `options.image` 里，必填：

```text
step:
  - name: build
    kind: container
    options:
      image: debian:latest
    content: |-
      echo hello > out.txt
```

- 镜像就是普通字符串，tag、digest、私有仓库端口都不需要特殊处理，例如 `reg.io:5000/a/b@sha256:...`。
- `options.shell` 是容器内的解释器，可带参数（字符串 `"bash -e"` 或数组 `["python3", "-u"]`），缺省 `sh`，Windows 容器缺省 `cmd`：

  ```text
  step:
    - name: build
      kind: container
      options:
        image: alpine:3.20
        shell: [python3, -u]
      content: |-
        print("hello")
  ```

  脚本后缀按解释器决定（`.sh`、`.py`、`.bat`、`.ps1`）；`cmd` 用 `cmd /c`，`powershell`/`pwsh` 用 `-File`（未额外指定参数时补 `-NoProfile -NonInteractive`），其余把脚本作为最后一个参数；`sh` 和 `python` 会把 CRLF 换成 LF。

- 私有镜像的凭据按 `docker` 命令行的语义在服务端解析，随拉取请求一起发给守护进程（守护进程自己没有任何凭据）：

  | 来源 | 说明 |
  |---|---|
  | `DOCKER_AUTH_CONFIG` | 环境变量里的 config.json 内容，优先于文件；适合没有家目录的服务运行方式 |
  | `$DOCKER_CONFIG/config.json` | 缺省为 `~/.docker/config.json`，即 `docker login` 写入的文件 |
  | `credHelpers` / `credsStore` | 按 CLI 的凭据助手协议调用 `docker-credential-<helper> get`，`Username` 为 `<token>` 时按 identity token 处理 |

  主机别名（`docker.io`、`registry-1.docker.io`、`index.docker.io`、`https://index.docker.io/v1/`）视为同一个 registry。
  日志只写凭据来源，不写用户名和密码；凭据没有配置时，未授权的报错会附带配置提示。凭据不出现在任务定义里，所以也不会落库。

- 每次执行前查询守护进程当前的容器系统（`info` 的 `OSType`），按容器系统适配，与宿主机系统无关：

  | | Linux 容器 | Windows 容器 |
  |---|---|---|
  | 工作目录挂载点 | `/workspace` | `C:\workspace` |
  | 脚本目录挂载点 | `/taskflow` | `C:\taskflow` |
  | 运行用户 | `0:0` | `ContainerAdministrator` |
  | 安全参数 | `no-new-privileges`、`PidsLimit 150`，capabilities 保持 Docker 默认 | 不适用，不传 |

- 默认挂载只有两个（步骤里不需要、也不能配置）：

  | 内容 | Linux 容器 | Windows 容器 |
  |---|---|---|
  | 任务工作目录 | `/workspace` | `C:\workspace` |
  | 步骤脚本目录（该步骤独占的子目录） | `/taskflow` | `C:\taskflow` |

- 步骤需要别的路径时，用 `options.mounts` 追加。每一项支持 `source`、`target`、`type`、`readOnly` 四个键：

  ```text
  step:
    - name: docker-ps
      kind: container
      options:
        image: docker:cli
        mounts:
          - source: /var/run/docker.sock
            target: /var/run/docker.sock
            readOnly: true
          - target: /cache
            type: tmpfs
      content: |-
        docker ps
  ```

  | 键 | 说明 |
  |---|---|
  | `source` | `bind` 是宿主机路径（必须绝对路径）；`volume` 是卷名；`npipe` 是管道路径（如 `\\.\pipe\docker_engine`）；`tmpfs` 不填 |
  | `target` | 容器内的绝对路径，或 `npipe` 的管道路径；必填 |
  | `type` | `bind`（缺省）、`volume`、`tmpfs`、`npipe` |
  | `readOnly` | 是否只读，缺省可写 |

  目标为 `/workspace`、`/taskflow`（Windows 为 `C:\workspace`、`C:\taskflow`）会被拒绝，同一目标也不能挂两次。挂载不做任何默认追加，宿主机凭据目录、Docker 端点等需要用时再显式声明。

- 容器内的 `TASK_WORKSPACE` 为对应的挂载点。
- 退出码即容器内进程的退出码；超时或取消会强制删除容器。
