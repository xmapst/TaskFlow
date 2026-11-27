/** 新建任务的完整 DAG 示例：本地产物发布演练，不部署到外部环境。 */
export const TASK_TEMPLATE = `# 发布演练：准备工作区 → 并行检查 → 可选人工审批 → 容器复核 → 本地发布 / 失败诊断
# 默认运行 Unix + 本地演练分支，需要 sh、bash、Python 和本地 Docker Engine（首次运行会拉取镜像）。
# 除容器步骤拉取镜像外，整个流程只写工作区，不访问网络。
# Windows：把 plan 的 unix 标签替换为 windows，使用 CMD、PowerShell 和 Python。
# 接入审批：将 plan 的 local-demo 替换为 require-approval，并替换 review 的两个示例 URL。
# 观察失败分支：将 DEMO_FAIL 改为 "1"；verify-python 通过退出码文件返回 42，重试用尽后触发诊断。
# 跳过容器步骤：删除 container-check，并把 record-release 的入边改回 local-gate。

# workdir: "release-demo" # 可选，相对于服务端工作区；省略则使用自动生成的任务名
# cleanup: true          # 可选，任务结束后是否删除工作目录，默认 false（保留）
timeout: 15m
env:
  - name: RELEASE_CHANNEL
    value: staging
  - name: DEMO_FAIL
    value: "0"

step:
  - name: prepare-workspace
    desc: 在任务工作目录下创建演练目录
    kind: mkdir
    content: |-
      path: release-demo

  - name: write-manifest
    desc: 写入供不同平台共同检查的清单
    kind: touch
    routing:
      edges:
        - step: prepare-workspace
    content: |-
      path: release-demo/manifest.txt
      overwrite: true
      content: |-
        artifact=demo-package
        version=1.0.0

  - name: plan
    desc: 用静态成功标签选择平台和发布模式，不需要改动后续的依赖
    kind: xexec
    options:
      shell: python3
    routing:
      edges:
        - step: write-manifest
          trigger: success
      # emit 非空时替代默认的 success；这里同时发出两个独立维度的标签。
      emit:
        - unix
        - local-demo
    content: |-
      import os
      print('Preparing release for ' + os.environ['RELEASE_CHANNEL'])

  # 同一平台内的两个检查互不依赖，可以并行。未选中的平台分支会跳过。
  - name: check-sh
    desc: 在 Unix 上检查清单是否非空
    kind: xexec
    options:
      shell: sh
    routing:
      edges:
        - step: plan
          trigger: unix
    content: |-
      set -eu
      test -s release-demo/manifest.txt
      printf '%s\\n' 'Manifest exists and is not empty'

  - name: check-bash
    desc: 在 Unix 上检查清单中的版本行
    kind: xexec
    options:
      shell: bash
    routing:
      edges:
        - step: plan
          trigger: unix
    content: |-
      set -eu
      grep -Fx 'version=1.0.0' release-demo/manifest.txt

  - name: check-cmd
    desc: 在 Windows 上检查清单是否存在
    kind: xexec
    options:
      shell: cmd
    routing:
      edges:
        - step: plan
          trigger: windows
    content: |-
      @echo off
      if not exist release-demo\\manifest.txt exit /b 1
      echo Manifest exists

  - name: check-powershell
    desc: 在 Windows 上检查清单中的版本行
    kind: xexec
    options:
      shell: powershell
    routing:
      edges:
        - step: plan
          trigger: windows
    content: |-
      $ErrorActionPreference = 'Stop'
      if ((Get-Content 'release-demo/manifest.txt') -notcontains 'version=1.0.0') {
        throw 'Unexpected manifest version'
      }
      Write-Output 'Manifest version verified'

  - name: unix-verified
    desc: 两个 Unix 检查都成功后才放行
    kind: xexec
    options:
      shell: python
    routing:
      join: all
      edges:
        - step: check-sh
        - step: check-bash
    content: print('Unix checks passed')

  - name: windows-verified
    desc: 两个 Windows 检查都成功后才放行
    kind: xexec
    options:
      shell: python
    routing:
      join: all
      edges:
        - step: check-cmd
        - step: check-powershell
    content: print('Windows checks passed')

  - name: verify-python
    desc: 等两组平台分支都结束，只需已选平台的检查成功；随后检查清单内容
    kind: xexec
    options:
      shell: python
    timeout: 1m
    env:
      # 仅覆盖此步骤的同名环境变量，其他步骤仍读取 staging。
      - name: RELEASE_CHANNEL
        value: verification
    routing:
      join: any
      edges:
        - step: unix-verified
        - step: windows-verified
      emit:
        - validated
    retryPolicy:
      maxAttempts: 3
      interval: 1s
      maxInterval: 5s
      multiplier: 2
      jitter: 0.2
    content: |-
      import json
      import os
      from pathlib import Path

      channel = os.environ['RELEASE_CHANNEL']
      manifest = Path('release-demo/manifest.txt')
      valid = manifest.is_file() and 'artifact=demo-package' in manifest.read_text(encoding='utf-8').splitlines()
      verified = valid and os.environ.get('DEMO_FAIL') != '1'
      code = 0 if verified else 42

      # xexec 注入文件路径。回传值保存在当前步骤，成功后也写入任务级环境，供后续步骤读取。
      report = {'channel': channel, 'verified': verified, 'exitCode': code}
      with open(os.environ['XEXEC_SAVE_ENV_FILE'], 'w', encoding='utf-8') as result:
          result.write('VERIFY_EXIT_CODE=' + str(code) + '\\n')
          result.write('VERIFY_REPORT<<END_REPORT\\n')
          result.write(json.dumps(report, indent=2) + '\\n')
          result.write('END_REPORT\\n')

      # 脚本正常结束，xexec 优先读取文件中的退出码：0 放行，42 进入重试 / failure 分支。
      Path(os.environ['XEXEC_EXIT_CODE_FILE']).write_text(str(code) + '\\n', encoding='ascii')
      print('Verification exit code:', code)

  - name: local-gate
    desc: 只有清单验证通过且选择了本地演练，才放行发布
    kind: xexec
    options:
      shell: python
    routing:
      join: all
      edges:
        - step: plan
          trigger: local-demo
        - step: verify-python
          trigger: validated
    content: |-
      import json
      import os

      # 直接读取 verify-python 经 XEXEC_SAVE_ENV_FILE 回传的值。
      report = json.loads(os.environ['VERIFY_REPORT'])
      if os.environ['VERIFY_EXIT_CODE'] != '0' or not report['verified']:
          raise SystemExit('Upstream verification did not pass')
      print('Local demo; verified channel:', report['channel'])

  - name: review
    desc: 只有清单验证通过且启用了人工审批，才发起请求
    kind: approval
    timeout: 5m
    routing:
      join: all
      edges:
        - step: plan
          trigger: require-approval
        - step: verify-python
          trigger: validated
      emit:
        - approved
    # 只重试发起失败；审批拒绝或等待结果超时不会重新发起。
    retryPolicy:
      maxAttempts: 3
      interval: 2s
      maxInterval: 10s
      multiplier: 2
    content: |-
      request:
        url: https://approval.example.com/requests
        method: POST
        headers:
          X-Workflow: release-demo
        query:
          project: demo
      callback:
        url: https://runner.example.com/api/v1/approval/callback
        approved: PASS
        rejected: DENY

  - name: container-check
    desc: 在容器里复核清单；镜像、解释器和挂载都写在 options 里，解释器缺省 sh
    kind: container
    options:
      image: alpine:3.20
      # shell 可带参数，写成 "bash -e" 或 [python3, -u]。
      shell: sh
      mounts:
        # tmpfs 只占容器内存，不需要宿主机路径；bind 的 source 必须是宿主机绝对路径。
        - target: /tmp/check
          type: tmpfs
    routing:
      edges:
        - step: local-gate
    content: |-
      set -eu
      test -s release-demo/manifest.txt
      # 验证额外挂载可写；tmpfs 随容器销毁，不落在工作区。
      touch /tmp/check/probe
      echo 'Manifest verified inside container'

  - name: record-release
    desc: 容器复核通过或人工审批通过后，在工作区记录本次发布
    kind: touch
    routing:
      join: any
      edges:
        - step: container-check
        - step: review
          trigger: approved
      emit:
        - published
    content: |-
      path: release-demo/release.txt
      overwrite: true
      content: Demo release completed. No external deployment was performed.

  - name: diagnose-failure
    desc: 任一关键步骤最终失败时记录诊断；跳过的步骤不会触发 failure
    kind: xexec
    options:
      shell: python
    routing:
      join: any
      edges:
        - step: prepare-workspace
          trigger: failure
        - step: write-manifest
          trigger: failure
        - step: plan
          trigger: failure
        - step: check-sh
          trigger: failure
        - step: check-bash
          trigger: failure
        - step: check-cmd
          trigger: failure
        - step: check-powershell
          trigger: failure
        - step: unix-verified
          trigger: failure
        - step: windows-verified
          trigger: failure
        - step: verify-python
          trigger: failure
        - step: local-gate
          trigger: failure
        - step: container-check
          trigger: failure
        - step: review
          trigger: failure
        - step: record-release
          trigger: failure
    content: print('A release step failed. See its logs for the underlying error.')

  - name: finish
    desc: 汇合发布与诊断分支；收尾成功不会清除上游的失败状态
    kind: xexec
    options:
      shell: python
    routing:
      join: any
      edges:
        - step: record-release
          trigger: published
        # 同一个上游可配置多条边；诊断成功或失败都允许收尾。
        - step: diagnose-failure
          trigger: success
        - step: diagnose-failure
          trigger: failure
    content: print('Release workflow finished. See the task result for its status.')
`
