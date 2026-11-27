import{n as e}from"./rolldown-runtime-WkwZ5CU-.js";import{C as t,E as n,I as r,T as i,_ as a,b as o,c as s,d as c,f as l,h as u,m as d,o as f,v as p,w as m,x as h}from"./arco-CCVr4m1H.js";import{c as g}from"./react-D1roDxa1.js";import{c as _,f as v,h as y,i as b,l as x,m as S,o as C,p as w,r as T,s as E,t as D,u as O}from"./flow-Cirzzr2q.js";import{M as ee,N as k,U as A,V as j,X as M,Y as te,a as N,d as P,i as ne,j as re,o as ie,p as ae,r as oe,s as se,t as ce,tt as F,u as le,w as ue,x as de}from"./index-DSSoQPVv.js";import{a as fe,c as pe,i as me,l as I,n as he,o as ge,r as _e,s as ve,t as L}from"./CodeEditor-DuhHe_aD.js";import{t as R}from"./StatTile-5EvphB_S.js";import{i as z,r as B}from"./vendor-khcdT6WH.js";import{a as ye,i as be,n as xe,o as Se,r as Ce,t as we}from"./ListView-B4oboRP7.js";var V=e(r(),1),H=null,U=S((e,t)=>{let n=()=>{let{page:n,size:r,prefix:i}=t();H?H.update({page:n,size:r,prefix:i}):H=A().subscribe(`tasks`,{page:n,size:r,prefix:i},{onOpen:()=>e({connection:`live`,error:null}),onUpdate:n=>{let r=t(),i=n.data??null,a=pe(i,{page:r.page,size:r.size});a.current===r.page&&a.size===r.size&&e({tasks:a.tasks,totalPages:a.totalPages,loading:!1,error:null,connection:`live`})},onError:t=>e({connection:`offline`,loading:!1,error:t}),onClose:()=>e({connection:`retrying`})})},r=()=>{H&&n()};return{tasks:[],page:1,size:20,totalPages:0,prefix:``,loading:!0,error:null,connection:`idle`,connect:()=>{H||(e({connection:`connecting`,loading:!0}),n())},disconnect:()=>{H?.close(),H=null,e({connection:`idle`})},setPage:t=>{e({page:t}),r()},setSize:t=>{e({size:t,page:1}),r()},setPrefix:t=>{e({prefix:t,page:1}),r()},refetch:()=>{e({loading:!0,error:null}),t().connection===`offline`&&(H?.close(),H=null),n()}}});function Te(e){let t={total:e.length,running:0,pending:0,paused:0,failed:0,stopped:0,skipped:0};for(let n of e)switch(n.state){case`running`:t.running+=1;break;case`pending`:t.pending+=1;break;case`paused`:t.paused+=1;break;case`failed`:t.failed+=1;break;case`stopped`:t.stopped+=1;break;case`skipped`:t.skipped+=1}return t}function Ee(){let e=U(e=>e.tasks),t=U(e=>e.page),n=U(e=>e.size),r=U(e=>e.totalPages),i=U(e=>e.prefix),a=U(e=>e.loading),o=U(e=>e.error),s=U(e=>e.connection),c=U(e=>e.connect),l=U(e=>e.disconnect),u=U(e=>e.setPage),d=U(e=>e.setSize),f=U(e=>e.setPrefix),p=U(e=>e.refetch);return(0,V.useEffect)(()=>(c(),()=>l()),[c,l]),{tasks:e,page:t,size:n,totalPages:r,prefix:i,loading:a,error:o,connection:s,setPage:u,setSize:d,setPrefix:f,refetch:p}}var De=`# 发布演练：准备工作区 → 并行检查 → 可选人工审批 → 容器复核 → 本地发布 / 失败诊断
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
`;function W(e){return typeof e==`object`&&!!e&&Object.getPrototypeOf(e)===Object.prototype}function G(e){return e.replace(/[^a-zA-Z\p{Script=Han}0-9_.~-]/gu,``)}var Oe={workdir:``,timeout:``,steps:[]};function ke(e){if(e===void 0||e===``)return;if(typeof e!=`string`)return`任务 workdir 必须是字符串`;let t=e.split(`/`);if(e.startsWith(`/`)||/[\\:\0]/.test(e)||t.includes(`..`)||!t.some(e=>e&&e!==`.`))return`任务 workdir 必须是工作区内的相对目录，使用 / 分隔，不能包含 ..`}function Ae(e){return B(e,{lineWidth:-1})}function K(e,t){if(t===`yaml`)return z(e);let n=JSON.parse(e);return z(e),n}function q(e,t){let n=new Map(e.steps.map(e=>[e.id,e.name])),r={...e.extra,workdir:e.workdir||void 0,cleanup:e.cleanup?!0:void 0,timeout:e.timeout===``?void 0:e.timeout,step:e.steps.map(e=>({...e.extra,name:e.name||void 0,desc:e.desc||void 0,kind:e.kind,options:Me(e.options),timeout:e.timeout===``?void 0:e.timeout,routing:Ne(e,n),content:e.content}))};return t===`json`?JSON.stringify(r,null,2)+`
`:Ae(r)}function je(e){return typeof e==`string`&&e.trim()!==``}function Me(e){let t={};for(let[n,r]of Object.entries(e))if(n===`mounts`&&Array.isArray(r)){let e=r.filter(e=>W(e)&&([`source`,`target`,`type`].some(t=>je(e[t]))||e.readOnly===!0));e.length&&(t[n]=e)}else t[n]=r;return Object.keys(t).length?t:void 0}function Ne(e,t){let n=e.edges.map(({source:e,extra:n,trigger:r})=>({...n,step:t.get(e)??``,trigger:r||void 0})),{join:r,emit:i,extra:a}=e.routing??{};if(n.length||r||i?.length||Object.keys(a??{}).length)return{...a,edges:n.length?n:void 0,join:r,emit:i?.length?i:void 0}}function Pe(e,t){let n=new Map;for(let t of e)for(let{source:e}of t.edges){let r=n.get(e);r?r.push(t.id):n.set(e,[t.id])}let r=new Set([t]);for(let e of r)for(let t of n.get(e)??[])r.add(t);return r}function Fe(e,t){return`${e}:${t}`}function Ie(e,t,n){return{...e,steps:e.steps.filter(({id:e})=>!t.has(e)).map(e=>({...e,edges:e.edges.filter(({source:r})=>!t.has(r)&&!n.has(Fe(r,e.id)))}))}}function J(e,t=new Set){if(typeof e==`number`&&(!Number.isFinite(e)||Number.isInteger(e)&&!Number.isSafeInteger(e)))throw Error(`存在无法无损转换的数字，请在 YAML 模式中继续编辑`);if(e&&typeof e==`object`&&!t.has(e)){t.add(e);for(let n of Object.values(e))J(n,t)}}function Y(e,t){if(e==null)return``;if(typeof e!=`string`)throw Error(`${t}必须是字符串`);return e}function Le({name:e,desc:t,timeout:n,...r},i){if(n!=null&&typeof n!=`string`&&typeof n!=`number`)throw Error(`${i}的 timeout 必须是时长字符串或整数纳秒`);return{name:Y(e,`${i}的 name `),desc:Y(t,`${i}的 desc `),timeout:n??``,extra:r}}function Re(e){let t=Number(e);return/^[+-]?\d+$/.test(e)&&Number.isSafeInteger(t)?t:e}var ze=new Set([`workdir`,`cleanup`,`timeout`,`env`,`step`]),Be=new Set([`name`,`desc`,`timeout`,`env`,`kind`,`options`,`content`,`routing`,`retryPolicy`]);function Ve(e,t,n){let r=Object.keys(e).filter(e=>!t.has(e));if(r.length)throw Error(`${n}包含未知字段：${r.join(`、`)}`)}function He(e,t,n){let r=K(e,n);if(!W(r))throw Error(`任务配置必须是一个对象`);Ve(r,ze,`任务配置`);let{step:i,workdir:a,cleanup:o,timeout:s,...c}=r,l=ke(a);if(l)throw Error(l);if(o!=null&&typeof o!=`boolean`)throw Error(`任务的 cleanup 必须是布尔值`);if(s!=null&&typeof s!=`string`&&typeof s!=`number`)throw Error(`任务的 timeout 必须是时长字符串或整数纳秒`);if(i!=null&&!Array.isArray(i))throw Error(`step 必须是步骤列表`);J(r);let u=new Map(t.steps.filter(e=>G(e.name)).map(e=>[G(e.name),e])),d=t.steps.reduce((e,t)=>Math.max(e,t.id),0),f=new Map,p=new D.graphlib.Graph().setGraph({rankdir:`LR`,ranksep:80,nodesep:30,marginx:48,marginy:48});p.setDefaultEdgeLabel(()=>({}));let m=(i??[]).map((e,t)=>{let n=`第 ${t+1} 个步骤`;if(!W(e))throw Error(`${n}必须是对象`);Ve(e,Be,n);let{kind:r,options:i,content:a,routing:o,...s}=e;if(i!=null&&!W(i))throw Error(`${n}的 options 必须是对象`);let c=Le(s,n),l=G(c.name),m=l?u.get(l):void 0,h=m?.id??++d;if(l){if(f.has(l))throw Error(`步骤名称「${l}」重复`);f.set(l,h)}return p.setNode(String(h),{width:176,height:56}),{...c,id:h,label:n,routing:o,position:m?.position,kind:Y(r,`${n}的 kind `),options:i??{},content:Y(a,`${n}的 content `)}}).map(({routing:e,label:t,...n})=>{let r=e=>{let r=f.get(G(e));if(r===void 0)throw Error(`${t}依赖的「${e}」不存在`);if(r===n.id)throw Error(`${t}不能依赖自身`);return p.setEdge(String(r),String(n.id)),r},i=[];if(e==null)return{...n,edges:i};if(!W(e))throw Error(`${t}的 routing 必须是对象`);let{edges:a,join:o,emit:s,...c}=e;if(o!=null&&o!==``&&o!==`all`&&o!==`any`)throw Error(`${t}的 routing.join 必须是 all 或 any`);if(s!=null&&(!Array.isArray(s)||s.some(e=>typeof e!=`string`)))throw Error(`${t}的 routing.emit 必须是字符串列表`);if(a!=null&&!Array.isArray(a))throw Error(`${t}的 routing.edges 必须是列表`);for(let e of a??[]){if(!W(e)||typeof e.step!=`string`||e.trigger!=null&&typeof e.trigger!=`string`)throw Error(`${t}的 routing.edges 需要步骤名称和可选的触发标签`);let{step:n,trigger:a,...o}=e;i.push({source:r(n),trigger:a??void 0,extra:o})}return{...n,edges:i,routing:{join:o||void 0,emit:s??void 0,extra:c}}});if(!D.graphlib.alg.isAcyclic(p))throw Error(`步骤存在循环依赖`);let h=m.some(e=>!e.position);return h&&D.layout(p),{workdir:Y(a,`任务的 workdir `),cleanup:o===!0||void 0,timeout:s??``,extra:c,steps:m.map(e=>{if(!h&&e.position)return{...e,position:e.position};let{x:t,y:n}=p.node(String(e.id));return{...e,position:{x:t-88,y:n-28}}})}}function X(e){return e.trim().toLowerCase()===`approval`}function Ue(e){try{let t=e.trim()?z(e):{};if(!W(t))return null;let{request:n={},callback:r={}}=t;return!W(n)||!W(r)||[n.url,n.method,r.url,r.approved,r.rejected].some(e=>e!=null&&typeof e!=`string`)?null:(J(t),{request:n,callback:r,config:t})}catch{return null}}function We(e,t,n,r){let i=Ue(e);return i?Ae({...i.config,[t]:{...i[t],[n]:r.trim()||void 0}}):e}function Ge(e){return e.steps.flatMap((e,t)=>{if(!X(e.kind))return[];let n=Ue(e.content);return n?[[n.request.url,`请求地址`],[n.callback.url,`回调地址`]].flatMap(([e,n])=>{let r=typeof e==`string`?e.trim():``;return(URL.canParse(r)?new URL(r):null)?.hostname&&/^https?:\/\//i.test(r)?[]:[`第 ${t+1} 个步骤的${n}必须是完整的 HTTP 或 HTTPS URL`]}):[]})}var Z=y();function Ke(e,t,n,r,i){let a=e;for(;i.some(e=>e.name.trim()===`step-${a}`);)a++;return{id:e,position:r,name:`step-${a}`,desc:``,kind:t,options:{...n},timeout:``,edges:[],content:``}}var qe=[{kind:`xexec`,options:{shell:`sh`},label:`Shell 脚本`,icon:`>_`,color:`blue`},{kind:`xexec`,options:{shell:`powershell`},label:`PowerShell 脚本`,icon:`PS`,color:`blue`},{kind:`xexec`,options:{shell:`cmd`},label:`CMD 脚本`,icon:`C:`,color:`blue`},{kind:`xexec`,options:{shell:`python3`},label:`Python 脚本`,icon:`Py`,color:`yellow`},{kind:`mkdir`,options:{},label:`创建目录`,icon:`+/`,color:`cyan`},{kind:`touch`,options:{},label:`创建文件`,icon:`+`,color:`cyan`},{kind:`container`,options:{image:`debian:latest`},label:`容器执行`,icon:`▣`,color:`purple`},{kind:`approval`,options:{},label:`人工审批`,icon:`✓`,color:`red`}],Q=(e,t)=>typeof e[t]==`string`?e[t]:``;function Je({kind:e,options:t={}}){let n=e.trim().toLowerCase(),r=Q(t,`shell`),i=qe.find(e=>e.kind===n&&(n!==`xexec`||Q(e.options,`shell`)===r));return(0,Z.jsx)(`span`,{"aria-hidden":`true`,className:`task-create__badge task-create__badge--${i?.color??`blue`}`,children:i?.icon??`>_`})}function Ye(e,t){let n=Q(t,`shell`);if(e.trim().toLowerCase()===`container`){let e=Array.isArray(t.mounts)?t.mounts.length:0;return`container · ${n||`缺省`} · ${Q(t,`image`)||`未填镜像`}${e?` · ${e} 挂载`:``}`}return n?`${e} · ${n}`:e}function Xe({data:e,selected:t}){return(0,Z.jsxs)(`div`,{className:`task-create__node${t?` is-selected`:``}`,title:`双击或右键打开步骤配置`,children:[(0,Z.jsx)(_,{type:`target`,position:w.Left,"aria-label":`上游连接点`}),(0,Z.jsx)(Je,{kind:e.kind,options:e.options}),(0,Z.jsxs)(`span`,{className:`task-create__node-text`,children:[(0,Z.jsx)(`strong`,{title:e.name,children:e.name||`未命名步骤`}),(0,Z.jsx)(`small`,{title:e.kind,children:Ye(e.kind,e.options)})]}),(0,Z.jsx)(_,{type:`source`,position:w.Right,"aria-label":`下游连接点`})]})}var Ze={draftStep:Xe};function $(e){return typeof e==`string`||typeof e==`number`?String(e):``}function Qe({value:e,onChange:t}){if(e!=null&&(!Array.isArray(e)||!e.every(W)))return(0,Z.jsx)(`p`,{children:`环境变量格式不正确，请在 YAML 中修正。`});let n=Array.isArray(e)?e:[],r=(e,r,i)=>{t(n.map((t,n)=>n===e?{...t,[r]:i}:t))},a=(e,r)=>{t(n.map((t,n)=>{if(n!==e)return t;let i=Object.fromEntries(Object.entries(t).filter(([e])=>e!==`secret`));return r?{...i,secret:!0}:i}))};return(0,Z.jsxs)(`fieldset`,{className:`task-create-config__group`,children:[(0,Z.jsx)(`legend`,{children:`环境变量`}),(0,Z.jsx)(`p`,{className:`task-create-config__hint`,children:`标记为敏感的变量，其值在步骤日志中显示为 ***。脚本仍能读到原值。`}),n.map((e,o)=>(0,Z.jsxs)(`div`,{className:`task-create-config__env-row`,children:[(0,Z.jsx)(m,{"aria-label":`变量 ${o+1} 名称`,placeholder:`变量名`,value:$(e.name),onChange:e=>r(o,`name`,e)}),(0,Z.jsx)(m,{"aria-label":`变量 ${o+1} 值`,placeholder:`变量值`,value:$(e.value),onChange:e=>r(o,`value`,e)}),(0,Z.jsxs)(`label`,{className:`task-create-config__inline`,children:[(0,Z.jsx)(s,{size:`small`,"aria-label":`变量 ${o+1} 标记为敏感`,checked:e.secret===!0,onChange:e=>a(o,e)}),`敏感`]}),(0,Z.jsx)(i,{size:`small`,status:`danger`,type:`text`,"aria-label":`删除变量 ${o+1}`,onClick:()=>t(n.filter((e,t)=>t!==o)),children:`删除`})]},o)),(0,Z.jsx)(i,{size:`small`,onClick:()=>t([...n,{name:``,value:``}]),children:`添加环境变量`})]})}function $e({kind:e,content:t,onChange:n}){let r;try{r=t.trim()?z(t):{}}catch{r=null}if(!W(r)||r.path!=null&&typeof r.path!=`string`||e===`touch`&&(r.overwrite!=null&&typeof r.overwrite!=`boolean`||r.content!=null&&typeof r.content!=`string`))return(0,Z.jsxs)(`label`,{children:[`内容块原文（请修正字段格式）`,(0,Z.jsx)(m.TextArea,{value:t,onChange:n,autoSize:{minRows:3,maxRows:10}})]});let i=e=>n(B({...r,...e},{lineWidth:-1}));return(0,Z.jsxs)(Z.Fragment,{children:[(0,Z.jsxs)(`label`,{children:[e===`mkdir`?`目录路径`:`文件路径`,(0,Z.jsx)(m,{value:$(r.path),onChange:e=>i({path:e}),placeholder:`工作区内的路径`})]}),e===`touch`&&(0,Z.jsxs)(Z.Fragment,{children:[(0,Z.jsxs)(`label`,{className:`task-create-config__inline`,children:[`覆盖已有文件`,(0,Z.jsx)(s,{checked:r.overwrite===!0,onChange:e=>i({overwrite:e})})]}),(0,Z.jsxs)(`label`,{children:[`文件内容`,(0,Z.jsx)(m.TextArea,{value:$(r.content),onChange:e=>i({content:e}),autoSize:{minRows:3,maxRows:10}})]})]})]})}var et=[`sh`,`ash`,`bash`,`dash`,`zsh`,`ksh`,`csh`,`tcsh`,`fish`,`python`,`python2`,`python3`,`py`,`py2`,`py3`,`cmd`,`bat`,`powershell`,`pwsh`,`ps`,`ps1`];function tt(e,t,n,r){let i={...e.options};r?i[n]=r:delete i[n],t({options:i})}function nt({step:e,update:t}){let n=Q(e.options,`shell`);return(0,Z.jsxs)(`div`,{className:`task-create-config__field`,children:[(0,Z.jsx)(`span`,{children:`解释器`}),(0,Z.jsx)(p,{"aria-label":`解释器`,showSearch:!0,allowCreate:!0,allowClear:!0,options:et,...n?{value:n}:{},placeholder:`缺省（Linux: sh、Windows: cmd）`,onChange:n=>tt(e,t,`shell`,n??``)})]})}var rt=[{value:`bind`,label:`bind（宿主机路径）`},{value:`volume`,label:`volume（Docker 卷）`},{value:`tmpfs`,label:`tmpfs（内存盘）`},{value:`npipe`,label:`npipe（Windows 命名管道）`}],it={bind:`宿主机绝对路径，如 /var/run/docker.sock`,volume:`卷名`,npipe:`管道路径，如 \\\\.\\pipe\\docker_engine`};function at({options:e,onChange:t}){let n=e.mounts;if(n!=null&&(!Array.isArray(n)||!n.every(W)))return(0,Z.jsx)(`p`,{children:`挂载列表格式不正确，请在“原始参数”或 YAML 中修正。`});let r=Array.isArray(n)?n:[],a=n=>{let r=Object.fromEntries(Object.entries(e).filter(([e])=>e!==`mounts`));t(n.length?{...r,mounts:n}:r)},o=(e,t)=>{a(r.map((n,r)=>{if(r!==e)return n;let i={...n,...t};for(let e of Object.keys(i))(i[e]===void 0||i[e]===``||i[e]===!1||e===`type`&&i[e]===`bind`)&&delete i[e];return i}))};return(0,Z.jsxs)(`fieldset`,{className:`task-create-config__group`,children:[(0,Z.jsx)(`legend`,{children:`挂载`}),(0,Z.jsx)(`p`,{className:`task-create-config__hint`,children:`任务工作目录（/workspace，Windows 容器为 C:\\workspace）和脚本目录（/taskflow）始终自动挂载，不能覆盖；这里追加的挂载默认可写。`}),r.map((e,t)=>{let n=typeof e.type==`string`&&e.type?e.type:`bind`;return(0,Z.jsxs)(`div`,{className:`task-create-config__mount-row`,children:[(0,Z.jsx)(m,{"aria-label":`挂载 ${t+1} 源`,disabled:n===`tmpfs`,placeholder:n===`tmpfs`?`tmpfs 无需源`:it[n]??`源`,value:n===`tmpfs`?``:$(e.source),onChange:e=>o(t,{source:e})}),(0,Z.jsx)(m,{"aria-label":`挂载 ${t+1} 目标`,placeholder:`容器内目标路径`,value:$(e.target),onChange:e=>o(t,{target:e})}),(0,Z.jsx)(p,{"aria-label":`挂载 ${t+1} 类型`,options:rt,value:n,onChange:e=>o(t,e===`tmpfs`?{type:e,source:void 0}:{type:e})}),(0,Z.jsxs)(`label`,{className:`task-create-config__inline`,children:[(0,Z.jsx)(s,{size:`small`,"aria-label":`挂载 ${t+1} 只读`,checked:e.readOnly===!0,onChange:e=>o(t,{readOnly:e})}),`只读`]}),(0,Z.jsx)(i,{size:`small`,status:`danger`,type:`text`,"aria-label":`删除挂载 ${t+1}`,onClick:()=>a(r.filter((e,n)=>n!==t)),children:`删除`})]},t)}),(0,Z.jsx)(i,{size:`small`,onClick:()=>a([...r,{source:``,target:``}]),children:`添加挂载`})]})}function ot({options:e,onChange:t}){let n=Object.keys(e).length?B(e,{lineWidth:-1}):``,[r,i]=(0,V.useState)(n),[a,o]=(0,V.useState)(``);return(0,V.useEffect)(()=>{try{let t=r.trim()?z(r):{};if(W(t)&&B(t,{sortKeys:!0})===B(e,{sortKeys:!0}))return}catch{}i(n),o(``)},[n]),(0,Z.jsxs)(`fieldset`,{className:`task-create-config__group`,children:[(0,Z.jsx)(`legend`,{children:`原始参数（options）`}),(0,Z.jsx)(`p`,{className:`task-create-config__hint`,children:`执行器私有参数的完整内容，用 YAML 书写，与上方表单双向同步；未识别的键由服务端校验并拒绝。`}),(0,Z.jsx)(m.TextArea,{"aria-label":`原始 options`,value:r,onChange:e=>{i(e);try{let n=e.trim()?z(e):{};if(!W(n))throw Error(`options 必须是对象`);o(``),t(n)}catch(e){o(e instanceof Error?e.message.split(`
`)[0]??``:String(e))}},autoSize:{minRows:4,maxRows:14},placeholder:`image: debian:latest
shell: [python3, -u]`}),a&&(0,Z.jsx)(`p`,{role:`alert`,className:`task-create-config__hint task-create-config__error`,children:a})]})}function st({extra:e,approval:t,onChange:n}){let r=W(e.retryPolicy)?e.retryPolicy:{},i=(e,t)=>{let i={...r};t==null||t===``?delete i[e]:i[e]=t,n({retryPolicy:Object.keys(i).length?i:void 0})};return(0,Z.jsxs)(`fieldset`,{className:`task-create-config__group`,children:[(0,Z.jsx)(`legend`,{children:`重试策略`}),t&&(0,Z.jsx)(`p`,{className:`task-create-config__hint`,children:`仅在发起审批失败时重试。审批被拒绝或等待结果超时，不会重新发起。`}),e.retryPolicy!=null&&!W(e.retryPolicy)?(0,Z.jsx)(`p`,{children:`重试策略格式不正确，请在 YAML 中修正。`}):(0,Z.jsxs)(`div`,{className:`task-create-config__retry`,children:[(0,Z.jsxs)(`label`,{children:[`最大尝试次数（含首次，-1 为无限）`,(0,Z.jsx)(u,{min:-1,precision:0,value:typeof r.maxAttempts==`number`?r.maxAttempts:void 0,onChange:e=>i(`maxAttempts`,e)})]}),(0,Z.jsxs)(`label`,{children:[`间隔倍数`,(0,Z.jsx)(u,{min:0,step:.1,value:typeof r.multiplier==`number`?r.multiplier:void 0,onChange:e=>i(`multiplier`,e)})]}),(0,Z.jsxs)(`label`,{children:[`抖动比例`,(0,Z.jsx)(u,{min:0,max:1,step:.1,value:typeof r.jitter==`number`?r.jitter:void 0,onChange:e=>i(`jitter`,e)})]}),(0,Z.jsxs)(`label`,{children:[`首次重试间隔`,(0,Z.jsx)(m,{value:$(r.interval),onChange:e=>i(`interval`,Re(e)),placeholder:`如 2s`})]}),(0,Z.jsxs)(`label`,{children:[`最大重试间隔`,(0,Z.jsx)(m,{value:$(r.maxInterval),onChange:e=>i(`maxInterval`,Re(e)),placeholder:`如 30s`})]})]})]})}function ct({step:e,steps:t,update:n}){let r=Pe(t,e.id),a=t.filter(e=>!r.has(e.id)&&G(e.name)).map(e=>({value:e.id,label:e.name})),o=(t,r)=>{n({edges:e.edges.map((e,n)=>n===t?r(e):e)})},s=e.routing?.emit??[],c=t=>n({routing:{...e.routing,emit:t}});return(0,Z.jsxs)(`div`,{className:`task-create__fields`,children:[(0,Z.jsxs)(`fieldset`,{className:`task-create-config__group`,children:[(0,Z.jsx)(`legend`,{children:`入边与汇合`}),(0,Z.jsxs)(`div`,{className:`task-create-config__field`,children:[(0,Z.jsx)(`span`,{children:`汇合策略`}),(0,Z.jsx)(p,{"aria-label":`汇合策略`,options:[{value:`all`,label:`全部匹配（all）`},{value:`any`,label:`至少一条匹配（any）`}],value:e.routing?.join??`all`,onChange:t=>n({routing:{...e.routing,join:t}})})]}),(0,Z.jsx)(`p`,{className:`task-create-config__hint`,children:`两种策略都会等待所有上游结束。上游取消会沿入边传播。`}),e.edges.map((t,r)=>(0,Z.jsxs)(`div`,{className:`task-create-config__env-row`,children:[(0,Z.jsx)(p,{"aria-label":`入边 ${r+1} 的上游步骤`,showSearch:!0,options:a,value:t.source,onChange:e=>o(r,t=>({...t,source:e}))}),(0,Z.jsx)(m,{"aria-label":`入边 ${r+1} 的触发标签`,placeholder:`success（默认）`,value:t.trigger??``,onChange:e=>o(r,t=>({...t,trigger:e}))}),(0,Z.jsx)(i,{size:`small`,status:`danger`,type:`text`,"aria-label":`删除入边 ${r+1}`,onClick:()=>n({edges:e.edges.filter((e,t)=>t!==r)}),children:`删除`})]},r)),(0,Z.jsx)(p,{"aria-label":`添加上游步骤`,showSearch:!0,placeholder:a.length?`添加上游步骤`:`没有可添加的上游步骤`,value:``,options:a,disabled:!a.length,onChange:t=>n({edges:[...e.edges,{source:t}]})}),(0,Z.jsx)(`p`,{className:`task-create-config__hint`,children:`留空等待 success；填写 failure 仅在上游最终失败时触发，也可使用上游声明的自定义标签。同一上游可添加多条不同标签的入边。`})]}),(0,Z.jsxs)(`fieldset`,{className:`task-create-config__group`,children:[(0,Z.jsx)(`legend`,{children:`成功时发出的标签`}),(0,Z.jsx)(`p`,{className:`task-create-config__hint`,children:`不配置时只发出 success。添加自定义标签后，如仍需放行默认成功边，也要保留 success。标签区分大小写。`}),s.map((e,t)=>(0,Z.jsxs)(`div`,{className:`task-create-config__tag-row`,children:[(0,Z.jsx)(m,{"aria-label":`成功标签 ${t+1}`,placeholder:`success（默认）`,value:e,error:e===`failure`,onChange:e=>c(s.map((n,r)=>r===t?e:n))}),(0,Z.jsx)(i,{size:`small`,status:`danger`,type:`text`,"aria-label":`删除成功标签 ${t+1}`,onClick:()=>c(s.filter((e,n)=>n!==t)),children:`删除`})]},t)),s.includes(`failure`)&&(0,Z.jsx)(`p`,{role:`alert`,className:`task-create-config__hint`,children:`failure 是保留标签，不能在成功时发出。`}),(0,Z.jsx)(i,{size:`small`,onClick:()=>c([...s,s.length?``:`success`]),children:`添加成功标签`})]})]})}function lt({step:e,steps:t,update:n,tab:r}){let i=e.kind.trim().toLowerCase(),a=i!==`approval`&&i!==`mkdir`&&i!==`touch`,o=e.extra??{},s=e=>n({extra:{...o,...e}}),c=X(e.kind)?Ue(e.content):null,l=(t,r,i)=>{n({content:We(e.content,t,r,i)})},u=(e,t)=>String(c?.[e][t]??``);return(0,Z.jsxs)(f,{defaultActiveTab:r??`basic`,className:`task-create-config__tabs`,children:[(0,Z.jsx)(f.TabPane,{title:`基本信息`,children:(0,Z.jsxs)(`div`,{className:`task-create__fields task-create-config__grid`,children:[(0,Z.jsxs)(`label`,{children:[`步骤名称`,(0,Z.jsx)(m,{value:e.name,onChange:e=>n({name:e})})]}),(0,Z.jsxs)(`label`,{children:[`步骤类型`,(0,Z.jsx)(m,{"aria-label":`步骤类型`,readOnly:!0,value:e.kind.trim()})]}),(0,Z.jsxs)(`label`,{className:`task-create-config__wide`,children:[`步骤超时`,(0,Z.jsx)(m,{value:String(e.timeout),onChange:e=>n({timeout:Re(e)}),placeholder:`如 5m`})]}),(0,Z.jsxs)(`label`,{className:`task-create-config__wide`,children:[`步骤描述`,(0,Z.jsx)(m,{value:e.desc,onChange:e=>n({desc:e})})]})]})},`basic`),a&&(0,Z.jsx)(f.TabPane,{title:`执行器参数${Object.keys(e.options).length?` (${Object.keys(e.options).length})`:``}`,children:(0,Z.jsxs)(`div`,{className:`task-create__fields`,children:[i===`container`&&(0,Z.jsxs)(`fieldset`,{className:`task-create-config__group`,children:[(0,Z.jsx)(`legend`,{children:`执行器`}),(0,Z.jsxs)(`label`,{children:[`镜像`,(0,Z.jsx)(m,{value:Q(e.options,`image`),onChange:t=>tt(e,n,`image`,t),placeholder:`如 debian:latest`})]})]}),i===`container`&&(0,Z.jsx)(at,{options:e.options,onChange:e=>n({options:e})}),(0,Z.jsx)(ot,{options:e.options,onChange:e=>n({options:e})})]})},`options`),(0,Z.jsx)(f.TabPane,{title:`依赖与分支`,children:(0,Z.jsx)(ct,{step:e,steps:t,update:n})},`routing`),(0,Z.jsx)(f.TabPane,{title:`执行内容`,children:c?(0,Z.jsxs)(`div`,{className:`task-create__fields task-create-config__grid`,children:[(0,Z.jsxs)(`label`,{className:`task-create-config__wide`,children:[`审批请求地址`,(0,Z.jsx)(m,{value:u(`request`,`url`),onChange:e=>l(`request`,`url`,e),placeholder:`https://approval.example.com/requests`})]}),(0,Z.jsxs)(`div`,{className:`task-create-config__field`,children:[(0,Z.jsx)(`span`,{children:`请求方法`}),(0,Z.jsx)(p,{"aria-label":`请求方法`,options:[`POST`,`PUT`],value:u(`request`,`method`).trim().toUpperCase()||`POST`,onChange:e=>l(`request`,`method`,e)})]}),(0,Z.jsxs)(`label`,{className:`task-create-config__wide`,children:[`回调地址`,(0,Z.jsx)(m,{value:u(`callback`,`url`),onChange:e=>l(`callback`,`url`,e),placeholder:`https://runner.example.com/api/v1/approval/callback`})]}),(0,Z.jsxs)(`label`,{children:[`通过状态`,(0,Z.jsx)(m,{value:u(`callback`,`approved`),onChange:e=>l(`callback`,`approved`,e),placeholder:`approved`})]}),(0,Z.jsxs)(`label`,{children:[`拒绝状态`,(0,Z.jsx)(m,{value:u(`callback`,`rejected`),onChange:e=>l(`callback`,`rejected`,e),placeholder:`rejected`})]})]}):i===`mkdir`||i===`touch`?(0,Z.jsx)(`div`,{className:`task-create__fields`,children:(0,Z.jsx)($e,{kind:i,content:e.content,onChange:e=>n({content:e})})}):(0,Z.jsxs)(`div`,{className:`task-create__fields task-create-config__script`,children:[(i===`xexec`||i===`container`)&&(0,Z.jsx)(nt,{step:e,update:n}),(0,Z.jsxs)(`div`,{className:`task-create-config__field task-create-config__editor`,role:`group`,"aria-label":X(e.kind)?`审批配置原文`:`执行内容`,children:[(0,Z.jsx)(`span`,{children:X(e.kind)?`审批配置原文`:`执行内容`}),(0,Z.jsx)(`div`,{className:`task-create-config__editor-body`,children:(0,Z.jsx)(L,{value:e.content,onChange:e=>n({content:e}),language:F(e.kind,Q(e.options,`shell`))})})]})]})},`content`),(0,Z.jsx)(f.TabPane,{title:`环境变量`,children:(0,Z.jsx)(`div`,{className:`task-create__fields`,children:(0,Z.jsx)(Qe,{value:o.env,onChange:e=>s({env:e.length?e:void 0})})})},`env`),(0,Z.jsx)(f.TabPane,{title:`重试策略`,children:(0,Z.jsx)(`div`,{className:`task-create__fields`,children:(0,Z.jsx)(st,{extra:o,approval:X(e.kind),onChange:s})})},`retry`)]})}function ut({draft:e,onChange:t,layoutVersion:n}){let{screenToFlowPosition:r,fitView:a}=b(),o=(0,V.useRef)(null),s=(0,V.useRef)(null),[l,u]=(0,V.useState)();(0,V.useEffect)(()=>{if(!n)return;let e=requestAnimationFrame(()=>{a({maxZoom:1,padding:.2})});return()=>cancelAnimationFrame(e)},[a,n]),(0,V.useEffect)(()=>{l&&s.current?.querySelector(`button`)?.focus({preventScroll:!0})},[l]);let[f,p]=(0,V.useState)(),[m,h]=(0,V.useState)(),[g,_]=(0,V.useState)(),[y,x]=(0,V.useState)(!1),S=(0,V.useMemo)(()=>e.steps.map(e=>({id:String(e.id),type:`draftStep`,position:e.position,selected:f===e.id,width:176,height:56,data:{name:e.name,kind:e.kind,options:e.options}})),[e.steps,f]),w=(0,V.useMemo)(()=>e.steps.flatMap(e=>{let t=new Map;for(let{source:n,trigger:r}of e.edges){let e=t.get(n)??new Set;e.add(r||`success`),t.set(n,e)}return[...t].map(([t,n])=>{let r=Fe(t,e.id);return{id:r,source:String(t),target:String(e.id),selected:m===r,label:n.size===1&&n.has(`success`)?void 0:[...n].join(` / `),type:`smoothstep`,markerEnd:{type:v.ArrowClosed}}})}),[e.steps,m]);function D(n,r,i){let a=1+Math.max(0,...e.steps.map(e=>e.id)),o=Ke(a,n,r,i,e.steps);t(e=>({...e,steps:[...e.steps,o]})),_({values:o}),p(a),h(void 0),u(void 0)}function ee(e,t){let n=o.current?.getBoundingClientRect();n&&u({x:Math.max(0,Math.min(e-n.left,n.width-180)),y:Math.max(0,Math.min(t-n.top,n.height-310)),position:r({x:e,y:t})})}function k({source:t,target:n}){let r=e.steps.find(e=>e.id===Number(t)),i=e.steps.find(e=>e.id===Number(n));return!!r&&!!i&&!!G(r.name)&&!i.edges.some(({source:e})=>e===r.id)&&!Pe(e.steps,i.id).has(r.id)}function A(e,n){t(t=>Ie(t,e,n)),f!==void 0&&e.has(f)&&p(void 0),m!==void 0&&n.has(m)&&h(void 0)}function j(e){p(e),h(void 0)}function M(t,n){let r=e.steps.find(e=>e.id===t);r&&(j(t),_({values:r,tab:n}))}function te(){if(!g)return;let{id:e,name:n,kind:r,options:i,desc:a,timeout:o,content:s,edges:c,routing:l,extra:u}=g.values;t(t=>({...t,steps:t.steps.map(t=>t.id===e?{...t,name:n,kind:r,options:i,desc:a,timeout:o,content:s,edges:c,routing:l,extra:u??{}}:t)})),_(void 0)}function N(){if(!g)return;let t=e.steps.find(e=>e.id===g.values.id);t&&B(t,{sortKeys:!0})!==B(g.values,{sortKeys:!0})?x(!0):_(void 0)}let P=w.find(e=>e.id===m);return(0,Z.jsxs)(`div`,{className:`task-create__builder`,children:[(0,Z.jsxs)(`div`,{ref:o,className:`task-create__canvas`,children:[P&&(0,Z.jsxs)(`div`,{className:`task-create__canvas-actions`,children:[(0,Z.jsx)(i,{size:`small`,onClick:()=>M(Number(P.target),`routing`),children:`编辑连线`}),(0,Z.jsx)(i,{size:`small`,status:`danger`,onClick:()=>A(new Set,new Set([P.id])),children:`删除连线`})]}),(0,Z.jsxs)(E,{nodes:S,edges:w,nodeTypes:Ze,fitView:S.length>0,fitViewOptions:{maxZoom:1,padding:.2},minZoom:.15,onNodesChange:e=>{let n=new Map;for(let t of S)for(let r of e.getForElement(t.id))r.type===`position`&&r.position&&n.set(Number(t.id),r.position),r.type===`select`&&r.selected&&j(Number(t.id));n.size&&t(e=>({...e,steps:e.steps.map(e=>{let t=n.get(e.id);return t?{...e,position:t}:e})}))},onNodeClick:(e,t)=>{j(Number(t.id)),u(void 0)},onNodeDoubleClick:(e,t)=>M(Number(t.id)),onNodeContextMenu:(e,t)=>{e.preventDefault(),u(void 0),M(Number(t.id))},onPaneClick:()=>{j(),u(void 0)},onPaneContextMenu:e=>{e.preventDefault(),ee(e.clientX,e.clientY)},onEdgeClick:(e,t)=>{p(void 0),h(t.id)},onEdgeDoubleClick:(e,t)=>M(Number(t.target),`routing`),onDelete:({nodes:e,edges:t})=>A(new Set(e.map(e=>Number(e.id))),new Set(t.map(e=>e.id))),deleteKeyCode:g?null:[`Backspace`,`Delete`],isValidConnection:k,onConnect:e=>{k(e)&&t(t=>({...t,steps:t.steps.map(t=>t.id===Number(e.target)?{...t,edges:[...t.edges,{source:Number(e.source)}]}:t)}))},proOptions:{hideAttribution:!0},children:[(0,Z.jsx)(C,{variant:T.Dots,gap:20,size:1}),(0,Z.jsx)(O,{showInteractive:!1})]}),l&&(0,Z.jsx)(`div`,{ref:s,className:`task-create__step-picker`,role:`group`,"aria-label":`可选步骤组件`,style:{left:l.x,top:l.y},onBlur:e=>{e.currentTarget.contains(e.relatedTarget)||u(void 0)},onKeyDown:e=>{e.key===`Escape`&&u(void 0)},children:qe.map(({kind:e,options:t,label:n})=>(0,Z.jsxs)(`button`,{type:`button`,onClick:()=>D(e,t,l.position),children:[(0,Z.jsx)(Je,{kind:e,options:t}),n]},`${e}:${Q(t,`shell`)}:${Q(t,`image`)}`))})]}),(0,Z.jsx)(d,{visible:!!g,title:`步骤配置`,placement:`right`,width:`min(760px, 100vw)`,zIndex:1002,className:`task-create-config task-create-config--step`,maskClosable:!0,escToExit:!y,autoFocus:!0,onCancel:N,footer:g&&(0,Z.jsxs)(`div`,{className:`task-create-config__actions`,children:[(0,Z.jsx)(i,{status:`danger`,type:`text`,onClick:()=>{A(new Set([g.values.id]),new Set),_(void 0)},children:`删除步骤`}),(0,Z.jsx)(i,{onClick:N,children:`取消`}),(0,Z.jsx)(i,{type:`primary`,disabled:!!g.values.routing?.emit?.includes(`failure`),onClick:te,children:`保存`})]}),children:g&&(0,Z.jsx)(lt,{tab:g.tab,step:g.values,steps:e.steps,update:e=>_(t=>t&&{...t,values:{...t.values,...e}})},`${g.values.id}:${g.tab??`basic`}`)}),(0,Z.jsx)(c,{visible:y,title:`放弃未保存的改动？`,maskStyle:{zIndex:1003},wrapStyle:{zIndex:1003},maskClosable:!1,cancelText:`继续编辑`,okText:`放弃改动`,okButtonProps:{status:`danger`},onCancel:()=>x(!1),onOk:()=>{x(!1),_(void 0)},children:`关闭后，这次对配置的修改不会写入画布或 YAML。`})]})}function dt(e){return(0,Z.jsx)(x,{children:(0,Z.jsx)(ut,{...e})})}var ft=/^[+-]?(?:0|(?:(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:ns|us|µs|μs|ms|s|m|h))+)$/;function pt(e,t=`yaml`){let n=[],r=[];if(!e.trim())return{ok:!1,errors:[`配置内容为空`],warnings:r};let i;try{i=K(e,t)}catch(e){let n=e instanceof Error?e.message:String(e);return{ok:!1,errors:[`${t.toUpperCase()} 语法错误：${n}`],warnings:r}}if(!W(i))return{ok:!1,errors:[`配置必须是一个对象`],warnings:r};function a(e,t){e!=null&&(typeof e==`number`&&Number.isInteger(e)||typeof e==`string`&&ft.test(e)||n.push(`${t} 必须是 Go 时长（如 2m）或整数纳秒`))}function o(e,t){if(e==null)return;if(!Array.isArray(e)){n.push(`${t} 必须是 name/value 列表`);return}let r=new Set;for(let[i,a]of e.entries())!W(a)||typeof a.name!=`string`||a.value!=null&&typeof a.value!=`string`?n.push(`${t}第 ${i+1} 项须使用字符串 name 和 value`):(r.has(a.name)&&n.push(`${t}「${a.name}」重复`),r.add(a.name))}(`name`in i||`desc`in i)&&n.push(`任务顶层不再接受 name 或 desc；任务名由服务端生成，可用 workdir 指定工作目录`);let s=ke(i.workdir);s&&n.push(s),i.cleanup!=null&&typeof i.cleanup!=`boolean`&&n.push(`任务 cleanup 必须是布尔值（true 或 false）`),a(i.timeout,`任务 timeout`),o(i.env,`全局环境变量`);let c=i.step;if(!Array.isArray(c)||c.length===0)return n.push(`step 不能为空，至少需要一个步骤`),{ok:!1,errors:n,warnings:r};let l=new Map,u=c.map((e,t)=>`第 ${t+1} 个步骤`),d=c.map(()=>[]);c.forEach((e,t)=>{let i=u[t];if(!W(e))n.push(`${i} 必须是对象`);else{if(typeof e.kind==`string`&&e.kind.trim()||n.push(`${i} 缺少有效的 kind（脚本类型）`),typeof e.content==`string`&&e.content.trim()||n.push(`${i} 缺少有效的 content（脚本内容）`),e.name!=null&&typeof e.name!=`string`)n.push(`${i} 的 name 必须是字符串`);else{let a=G(e.name??``);a?(u[t]=`步骤「${a}」`,a!==e.name&&r.push(`${i} 名称将规范化为「${a}」`),l.has(a)?n.push(`步骤名称「${a}」重复`):l.set(a,t)):r.push(`${i} 未指定有效的 name，将由服务端生成，无法被其他步骤依赖`)}if(e.routing!=null){if(!W(e.routing))n.push(`${i} 的 routing 必须是对象`);else{let{edges:r,emit:a,join:o}=e.routing;if(o!=null&&o!==``&&o!==`all`&&o!==`any`&&n.push(`${i} 的 routing.join 必须是 all 或 any`),a!=null&&(!Array.isArray(a)||a.some(e=>typeof e!=`string`||e===`failure`))&&n.push(`${i} 的 routing.emit 必须是字符串列表，不能包含 failure`),r!=null&&!Array.isArray(r))n.push(`${i} 的 routing.edges 必须是列表`);else if(Array.isArray(r)){let e=new Set(d[t]);for(let t of r)!W(t)||typeof t.step!=`string`||!G(t.step)||t.trigger!=null&&typeof t.trigger!=`string`?n.push(`${i} 的每条 routing.edges 需要有效的 step 和可选的 trigger 字符串`):e.add(G(t.step));d[t]=[...e]}}}if(a(e.timeout,`${i}的 timeout`),o(e.env,`${i}环境变量`),e.retryPolicy!=null){if(!W(e.retryPolicy))n.push(`${i} 的 retryPolicy 必须是对象`);else{a(e.retryPolicy.interval,`${i}的 retryPolicy.interval`),a(e.retryPolicy.maxInterval,`${i}的 retryPolicy.maxInterval`);let t=e.retryPolicy.jitter;t!=null&&(typeof t!=`number`||!Number.isFinite(t)||t<0||t>1)&&n.push(`${i} 的 retryPolicy.jitter 必须是 0 到 1 的数值`)}}}});let f=c.map(()=>0),p=c.map(()=>[]);for(let[e,t]of d.entries())for(let r of t){let t=l.get(r);t===void 0?n.push(`${u[e]}依赖的「${r}」不存在`):t===e?n.push(`${u[e]}不能依赖自身`):(f[e]++,p[t].push(e))}let m=f.flatMap((e,t)=>e===0?[t]:[]);for(let e=0;e<m.length;e++)for(let t of p[m[e]])--f[t]===0&&m.push(t);return m.length!==c.length&&n.push(`步骤存在循环依赖`),{ok:n.length===0,errors:n,warnings:r}}function mt({onClose:e,onCreated:t}){let[{text:r,format:o,draft:s,importError:l,layoutVersion:u},d]=(0,V.useState)({text:q(Oe,`yaml`),format:`yaml`,draft:Oe,importError:``,layoutVersion:0}),[f,p]=(0,V.useState)(!1),[m,h]=(0,V.useState)(!0),g=(0,V.useId)(),_=(0,V.useCallback)(e=>{d(t=>{if(e===t.text)return t;try{let n=He(e,t.draft,t.format),r=new Set(t.draft.steps.map(({id:e})=>e)),i=t.layoutVersion+Number(n.steps.some(({id:e})=>!r.has(e)));return{...t,text:e,draft:n,importError:``,layoutVersion:i}}catch(n){return{...t,text:e,importError:n instanceof Error?n.message:String(n)}}})},[]),v=(0,V.useCallback)(e=>{d(t=>{if(t.importError)return t;let n=e(t.draft),r=q(n,t.format),i=r===q(t.draft,t.format)?t.text:r;return{...t,draft:n,text:i}})},[]),y=(0,V.useMemo)(()=>{let e=pt(r,o);if(l)return e.ok?{...e,ok:!1,errors:[l]}:e;let t=[...e.errors,...Ge(s)];return{...e,ok:t.length===0,errors:t}},[s,o,l,r]),b=I(async e=>{t((await me(e.text,e.format)).name)},{successMessage:`任务已创建`,errorPrefix:`创建失败`}),x=(0,V.useCallback)(()=>{p(!0),y.ok&&b.run({text:r,format:o})},[y.ok,b,r,o]);return(0,Z.jsx)(c,{visible:!0,onCancel:e,title:(0,Z.jsxs)(`div`,{className:`task-create__header`,children:[(0,Z.jsxs)(`span`,{className:`task-create__title`,children:[(0,Z.jsx)(ae,{size:16}),`新建任务`]}),(0,Z.jsxs)(`div`,{className:`task-create__header-actions`,children:[(0,Z.jsx)(i,{size:`small`,type:`text`,onClick:()=>{let e=o===`json`?JSON.stringify(K(De,`yaml`),null,2)+`
`:De;_(e),p(!1)},children:`重置为模板`}),(0,Z.jsx)(i,{size:`small`,onClick:e,children:`取消`}),(0,Z.jsx)(i,{size:`small`,type:`primary`,loading:b.loading,onClick:x,children:`创建`})]})]}),footer:null,closable:!1,className:`task-create task-create--builder`,alignCenter:!1,style:{width:`100%`,top:0},maskClosable:!1,escToExit:!1,children:(0,Z.jsxs)(`div`,{className:`task-create__body`,children:[f&&y.errors.length>0?(0,Z.jsx)(`div`,{className:`task-create__notice`,role:`alert`,children:(0,Z.jsx)(n,{type:`error`,title:`配置存在问题`,closable:!0,onClose:()=>p(!1),content:(0,Z.jsx)(`ul`,{className:`task-create__issues`,children:y.errors.map((e,t)=>(0,Z.jsx)(`li`,{children:e},t))})})}):y.warnings.length>0?(0,Z.jsx)(`div`,{className:`task-create__notice`,children:(0,Z.jsx)(n,{type:`warning`,title:`请确认以下情况`,closable:!0,content:(0,Z.jsx)(`ul`,{className:`task-create__issues`,children:y.warnings.map((e,t)=>(0,Z.jsx)(`li`,{children:e},t))})})},y.warnings.join(`
`)):null,(0,Z.jsxs)(`div`,{className:m?`task-create__split task-create__split--expanded`:`task-create__split`,children:[(0,Z.jsxs)(`section`,{className:`task-create__pane`,"aria-label":`可视化`,children:[(0,Z.jsx)(`div`,{className:`task-create__pane-title`,children:(0,Z.jsx)(`strong`,{children:`可视化`})}),l&&(0,Z.jsx)(n,{type:`warning`,title:`画布暂停同步，请修正配置`,content:l}),(0,Z.jsx)(`div`,{className:`task-create__pane-content`,inert:!!l,children:(0,Z.jsx)(dt,{draft:s,onChange:v,layoutVersion:u})})]}),(0,Z.jsx)(`div`,{className:`task-create__divider`,children:(0,Z.jsx)(`button`,{type:`button`,className:`task-create__editor-toggle`,"aria-label":m?`折叠配置`:`展开配置`,title:m?`折叠配置`:`展开配置`,"aria-controls":g,"aria-expanded":m,onClick:()=>h(e=>!e),children:m?(0,Z.jsx)(P,{size:14}):(0,Z.jsx)(le,{size:14})})}),(0,Z.jsxs)(`section`,{id:g,className:`task-create__pane`,"aria-label":`任务配置`,hidden:!m,children:[(0,Z.jsx)(`div`,{className:`task-create__pane-title`,children:(0,Z.jsxs)(a.Group,{type:`button`,size:`small`,"aria-label":`配置格式`,value:o,onChange:e=>{d(t=>t.format===e||t.importError?t:{...t,format:e,text:q(t.draft,e)})},disabled:!!l,children:[(0,Z.jsx)(a,{value:`yaml`,children:`YAML`}),(0,Z.jsx)(a,{value:`json`,children:`JSON`})]})}),(0,Z.jsx)(`div`,{className:`task-create__pane-content`,children:(0,Z.jsx)(L,{value:r,onChange:_,language:o})})]})]})]})})}var ht=`aef-task-view`;function gt(){let e=g(),{tasks:n,page:r,size:a,totalPages:s,prefix:c,loading:u,error:d,connection:f,setPage:p,setSize:_,setPrefix:v,refetch:y}=Ee(),[b,x]=Se(ht),[S,C]=(0,V.useState)(!1),[w,T]=(0,V.useState)(null),E=(0,V.useMemo)(()=>Te(n),[n]),D=(0,V.useMemo)(()=>w?n.filter(e=>e.state===w):n,[n,w]),O=(0,V.useCallback)(t=>{e(`/tasks/${encodeURIComponent(t)}`)},[e]),A=I(async e=>{await fe(e)},{successMessage:`任务已删除`,errorPrefix:`删除失败`,confirm:{title:`删除任务`,content:`将删除该任务及其记录，此操作不可撤销。`,okText:`删除`,danger:!0}}),P=I(async(e,t)=>{await ve(e,t)},{errorPrefix:`操作失败`}),ae=I(async e=>{let t=await ge(e);ee(`${e}.yaml`,t,`application/yaml`)},{successMessage:`配置已导出`,errorPrefix:`导出失败`}),F=(0,V.useCallback)(t=>{let n=M(t.state);return(0,Z.jsx)(h,{trigger:`click`,position:`br`,droplist:(0,Z.jsxs)(o,{onClickMenuItem:n=>{n===`dump`?ae.run(t.name):n===`delete`?A.run(t.name):n===`workspace`?e(`/tasks/${encodeURIComponent(t.name)}?tab=workspace`):P.run(t.name,n)},children:[n.map(e=>(0,Z.jsx)(o.Item,{children:te[e].label},e)),(0,Z.jsx)(o.Item,{children:`工作目录`},`workspace`),(0,Z.jsx)(o.Item,{children:`导出配置`},`dump`),(0,Z.jsx)(o.Item,{className:`task-menu__danger`,children:`删除任务`},`delete`)]}),children:(0,Z.jsx)(i,{size:`small`,type:`secondary`,icon:(0,Z.jsx)(de,{size:14}),onClick:e=>e.stopPropagation()})})},[ae,A,P,e]),le=(0,V.useMemo)(()=>[{title:`任务`,dataIndex:`name`,width:260,render:(e,t)=>(0,Z.jsxs)(`div`,{className:`task-cell`,children:[(0,Z.jsx)(he,{state:t.state}),(0,Z.jsx)(`button`,{type:`button`,className:`task-cell__name`,onClick:()=>O(t.name),children:t.name})]})},{title:`步骤`,dataIndex:`count`,width:80,align:`right`,render:(e,t)=>(0,Z.jsx)(`span`,{className:`u-mono`,children:t.count??0})},{title:`状态`,dataIndex:`state`,width:108,render:(e,t)=>(0,Z.jsx)(_e,{state:t.state,size:`small`})},{title:`开始时间`,dataIndex:`time.start`,width:208,render:(e,t)=>(0,Z.jsx)(`span`,{className:`u-mono task-cell__start`,children:re(t.time?.start)})},{title:`耗时`,width:96,align:`right`,render:(e,t)=>(0,Z.jsx)(`span`,{className:`u-mono`,children:k(t.time?.start,t.time?.end)||(t.time?.start?`进行中`:`—`)})},{title:`执行信息`,dataIndex:`message`,render:(e,n)=>{let{text:r,counters:i}=j(n.message);return(0,Z.jsxs)(`div`,{className:`task-msg`,children:[r?(0,Z.jsx)(t,{content:r,children:(0,Z.jsx)(`span`,{className:`task-msg__text u-truncate`,children:r})}):null,i.length>0?(0,Z.jsx)(`span`,{className:`task-msg__counters`,children:i.map(e=>(0,Z.jsx)(_e,{state:e.state,size:`small`,variant:`soft`,className:`task-msg__counter`},e.state))}):null]})}},{title:``,width:56,align:`center`,render:(e,t)=>F(t)}],[O,F]),pe=(0,Z.jsx)(be,{left:(0,Z.jsxs)(Z.Fragment,{children:[(0,Z.jsx)(i,{type:`primary`,icon:(0,Z.jsx)(ue,{size:15}),onClick:()=>C(!0),children:`新建任务`}),(0,Z.jsx)(m.Search,{allowClear:!0,placeholder:`按名称前缀搜索`,value:c,onChange:v,className:`task-toolbar__search`})]}),right:(0,Z.jsxs)(Z.Fragment,{children:[(0,Z.jsx)(oe,{live:f===`live`}),f===`offline`?(0,Z.jsx)(i,{size:`small`,type:`secondary`,onClick:()=>void y(),children:`重新加载`}):null,(0,Z.jsx)(ye,{value:b,onChange:x})]})}),me=(0,Z.jsxs)(`div`,{className:`task-stats-wrap`,children:[(0,Z.jsx)(`p`,{className:`task-stats-caption`,children:`统计与筛选仅作用于当前页`}),(0,Z.jsxs)(`div`,{className:`task-stats`,children:[(0,Z.jsx)(R,{label:`执行中`,value:E.running,token:`running`,active:w===`running`,onClick:()=>T(w===`running`?null:`running`)}),(0,Z.jsx)(R,{label:`等待中`,value:E.pending,token:`pending`,active:w===`pending`,onClick:()=>T(w===`pending`?null:`pending`)}),(0,Z.jsx)(R,{label:`已挂起`,value:E.paused,token:`paused`,active:w===`paused`,onClick:()=>T(w===`paused`?null:`paused`)}),(0,Z.jsx)(R,{label:`已失败`,value:E.failed,token:`failed`,active:w===`failed`,onClick:()=>T(w===`failed`?null:`failed`)}),(0,Z.jsx)(R,{label:`已完成`,value:E.stopped,token:`stopped`,active:w===`stopped`,onClick:()=>T(w===`stopped`?null:`stopped`)})]})]}),L=s>1?(0,Z.jsx)(`div`,{className:`list-pagination`,children:(0,Z.jsx)(l,{current:r,pageSize:a,total:s*a,sizeCanChange:!0,pageSizeChangeResetCurrent:!0,sizeOptions:[20,40,60,100],onChange:(e,t)=>{t===a?p(e):_(t)}})}):null;return(0,Z.jsxs)(Z.Fragment,{children:[(0,Z.jsx)(se,{toolbar:pe,stats:me,footer:L,children:u&&n.length===0?(0,Z.jsx)(ne,{tip:`正在连接实时通道`}):d&&n.length===0?(0,Z.jsx)(ce,{description:`无法获取任务列表`,hint:d,action:(0,Z.jsx)(i,{type:`primary`,onClick:()=>void y(),children:`重试`})}):D.length===0?(0,Z.jsx)(ce,{description:w?`当前页没有该状态的任务`:c?`没有匹配的任务`:`还没有任务`,hint:w?`统计与筛选均只作用于当前页，可翻页或清除筛选后再查看。`:c?`搜索按名称前缀匹配，请尝试更短的关键词。`:`创建任务后即可在此查看编排结构与实时执行状态。`,action:w?(0,Z.jsx)(i,{onClick:()=>T(null),children:`清除筛选`}):(0,Z.jsx)(i,{type:`primary`,icon:(0,Z.jsx)(ue,{size:15}),onClick:()=>C(!0),children:`新建任务`})}):b===`table`?(0,Z.jsx)(Ce,{rowKey:`name`,data:D,columns:le,scroll:{x:1010},onRow:e=>({onDoubleClick:()=>O(e.name)})}):(0,Z.jsx)(xe,{children:D.map(e=>{let{text:n,counters:r}=j(e.message);return(0,Z.jsxs)(we,{title:e.name,tone:e.state,leading:(0,Z.jsx)(he,{state:e.state}),actions:F(e),onOpen:()=>O(e.name),footer:(0,Z.jsxs)(Z.Fragment,{children:[(0,Z.jsx)(_e,{state:e.state,size:`small`}),r.length>0?(0,Z.jsx)(`span`,{className:`task-card__counters`,children:r.map(e=>(0,Z.jsx)(t,{content:`${e.count} 个步骤处于该状态`,mini:!0,children:(0,Z.jsxs)(`span`,{className:`task-card__counter`,children:[(0,Z.jsx)(he,{state:e.state}),(0,Z.jsx)(`span`,{className:`u-mono`,children:e.count})]})},e.state))}):null]}),children:[(0,Z.jsxs)(ie,{children:[(0,Z.jsx)(N,{label:`步骤数`,value:e.count??0,mono:!0}),(0,Z.jsx)(N,{label:`开始`,value:re(e.time?.start),mono:!0}),(0,Z.jsx)(N,{label:`耗时`,value:k(e.time?.start,e.time?.end)||(e.time?.start?`进行中`:void 0),mono:!0})]}),n?(0,Z.jsx)(`p`,{className:`task-card__msg`,title:n,children:n}):null]},e.name)})})}),S&&(0,Z.jsx)(mt,{onClose:()=>C(!1),onCreated:e=>{C(!1),O(e)}})]})}export{gt as TasksPage};