import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Drawer, Input, InputNumber, Modal, Select, Switch, Tabs } from '@arco-design/web-react'
import {
  Background, BackgroundVariant, Controls, Handle, MarkerType, Position, ReactFlow, ReactFlowProvider,
  useReactFlow, type Connection, type Edge, type Node, type NodeProps, type XYPosition,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { dump, load } from 'js-yaml'
import { CodeEditor } from '@/components/editor/CodeEditor'
import { scriptLanguage } from '@/constants/state'
import {
  approvalFields, blockedDependencies, draftEdgeID, editDuration, isApproval, isMapping,
  nodeHeight, nodeWidth, normalizeName, removeDraftElements, updateApproval,
  type Mapping, type StepDraft, type StepDraftEdge, type TaskDraft,
} from './taskDraft'

function newStep(id: number, kind: string, options: Mapping, position: XYPosition, steps: StepDraft[]): StepDraft {
  let number = id
  while (steps.some((step) => step.name.trim() === `step-${number}`)) number++
  return { id, position, name: `step-${number}`, desc: '', kind, options: { ...options }, timeout: '', edges: [], content: '' }
}

type PaletteEntry = { kind: string; options: Mapping; label: string; icon: string; color: string }

const palette: PaletteEntry[] = [
  { kind: 'xexec', options: { shell: 'sh' }, label: 'Shell 脚本', icon: '>_', color: 'blue' },
  { kind: 'xexec', options: { shell: 'powershell' }, label: 'PowerShell 脚本', icon: 'PS', color: 'blue' },
  { kind: 'xexec', options: { shell: 'cmd' }, label: 'CMD 脚本', icon: 'C:', color: 'blue' },
  { kind: 'xexec', options: { shell: 'python3' }, label: 'Python 脚本', icon: 'Py', color: 'yellow' },
  { kind: 'mkdir', options: {}, label: '创建目录', icon: '+/', color: 'cyan' },
  { kind: 'touch', options: {}, label: '创建文件', icon: '+', color: 'cyan' },
  { kind: 'container', options: { image: 'debian:latest' }, label: '容器执行', icon: '▣', color: 'purple' },
  { kind: 'approval', options: {}, label: '人工审批', icon: '✓', color: 'red' },
]

type DraftNode = Node<{ name: string; kind: string; options: Mapping }, 'draftStep'>

/** 读取执行器参数里的字符串值；缺省或非字符串都按空串处理。 */
const optionText = (options: Mapping, key: string): string => (typeof options[key] === 'string' ? options[key] : '')

function StepBadge({ kind, options = {} }: { kind: string; options?: Mapping }) {
  const base = kind.trim().toLowerCase()
  const shell = optionText(options, 'shell')
  const entry = palette.find((item) => item.kind === base && (base !== 'xexec' || optionText(item.options, 'shell') === shell))
  return <span aria-hidden="true" className={`task-create__badge task-create__badge--${entry?.color ?? 'blue'}`}>{entry?.icon ?? '>_'}</span>
}

/** 节点副标题：普通类型显示为“类型 · 解释器”；container 显示为“container · 解释器 · 镜像”，有额外挂载时追加数量，过长由样式截断。 */
function nodeKindLabel(kind: string, options: Mapping): string {
  const shell = optionText(options, 'shell')
  if (kind.trim().toLowerCase() === 'container') {
    const mounts = Array.isArray(options.mounts) ? options.mounts.length : 0
    return `container · ${shell || '缺省'} · ${optionText(options, 'image') || '未填镜像'}${mounts ? ` · ${mounts} 挂载` : ''}`
  }
  return shell ? `${kind} · ${shell}` : kind
}

function DraftStepNode({ data, selected }: NodeProps<DraftNode>) {
  return (
    <div className={`task-create__node${selected ? ' is-selected' : ''}`} title="双击或右键打开步骤配置">
      <Handle type="target" position={Position.Left} aria-label="上游连接点" />
      <StepBadge kind={data.kind} options={data.options} />
      <span className="task-create__node-text">
        <strong title={data.name}>{data.name || '未命名步骤'}</strong>
        <small title={data.kind}>{nodeKindLabel(data.kind, data.options)}</small>
      </span>
      <Handle type="source" position={Position.Right} aria-label="下游连接点" />
    </div>
  )
}

const nodeTypes = { draftStep: DraftStepNode }

function scalarText(value: unknown): string {
  return typeof value === 'string' || typeof value === 'number' ? String(value) : ''
}

function EnvFields({ value, onChange }: { value: unknown; onChange: (env: Mapping[]) => void }) {
  if (value != null && (!Array.isArray(value) || !value.every(isMapping))) {
    return <p>环境变量格式不正确，请在 YAML 中修正。</p>
  }
  const rows: Mapping[] = Array.isArray(value) ? value : []
  const edit = (index: number, key: 'name' | 'value', text: string) => {
    onChange(rows.map((row, i) => i === index ? { ...row, [key]: text } : row))
  }
  // Turn the flag on with a literal true and drop the key when off, so the
  // YAML stays free of `secret: false`.
  const toggleSecret = (index: number, secret: boolean) => {
    onChange(rows.map((row, i) => {
      if (i !== index) return row
      const rest = Object.fromEntries(Object.entries(row).filter(([key]) => key !== 'secret'))
      return secret ? { ...rest, secret: true } : rest
    }))
  }
  return (
    <fieldset className="task-create-config__group">
      <legend>环境变量</legend>
      <p className="task-create-config__hint">标记为敏感的变量，其值在步骤日志中显示为 ***。脚本仍能读到原值。</p>
      {rows.map((row, index) => (
        <div className="task-create-config__env-row" key={index}>
          <Input aria-label={`变量 ${index + 1} 名称`} placeholder="变量名" value={scalarText(row.name)} onChange={(text) => edit(index, 'name', text)} />
          <Input aria-label={`变量 ${index + 1} 值`} placeholder="变量值" value={scalarText(row.value)} onChange={(text) => edit(index, 'value', text)} />
          <label className="task-create-config__inline">
            <Switch size="small" aria-label={`变量 ${index + 1} 标记为敏感`} checked={row.secret === true}
              onChange={(secret) => toggleSecret(index, secret)} />
            敏感
          </label>
          <Button size="small" status="danger" type="text" aria-label={`删除变量 ${index + 1}`} onClick={() => onChange(rows.filter((_row, i) => i !== index))}>删除</Button>
        </div>
      ))}
      <Button size="small" onClick={() => onChange([...rows, { name: '', value: '' }])}>添加环境变量</Button>
    </fieldset>
  )
}

function FileConfigFields({ kind, content, onChange }: { kind: 'mkdir' | 'touch'; content: string; onChange: (content: string) => void }) {
  let fields: unknown
  try {
    fields = content.trim() ? load(content) : {}
  } catch {
    fields = null
  }
  if (!isMapping(fields) || (fields.path != null && typeof fields.path !== 'string') ||
      (kind === 'touch' && ((fields.overwrite != null && typeof fields.overwrite !== 'boolean') ||
        (fields.content != null && typeof fields.content !== 'string')))) {
    return <label>内容块原文（请修正字段格式）<Input.TextArea value={content} onChange={onChange} autoSize={{ minRows: 3, maxRows: 10 }} /></label>
  }
  const edit = (patch: Mapping) => onChange(dump({ ...fields, ...patch }, { lineWidth: -1 }))
  return <>
    <label>{kind === 'mkdir' ? '目录路径' : '文件路径'}<Input value={scalarText(fields.path)} onChange={(path) => edit({ path })} placeholder="工作区内的路径" /></label>
    {kind === 'touch' && <>
      <label className="task-create-config__inline">覆盖已有文件<Switch checked={fields.overwrite === true} onChange={(overwrite) => edit({ overwrite })} /></label>
      <label>文件内容<Input.TextArea value={scalarText(fields.content)} onChange={(content) => edit({ content })} autoSize={{ minRows: 3, maxRows: 10 }} /></label>
    </>}
  </>
}

const shellOptions = ['sh', 'ash', 'bash', 'dash', 'zsh', 'ksh', 'csh', 'tcsh', 'fish', 'python', 'python2', 'python3', 'py', 'py2', 'py3', 'cmd', 'bat', 'powershell', 'pwsh', 'ps', 'ps1']

/** 只改一个选项键，其余参数原样保留，后续执行器的参数不会被前端丢掉。 */
function setOptionText(step: StepDraft, update: (patch: Partial<StepDraft>) => void, key: string, value: string) {
  const options = { ...step.options }
  if (value) options[key] = value
  else delete options[key]
  update({ options })
}

/** 解释器选择：放在脚本编辑器上方，随时能看出当前是什么类型的脚本。 */
function ShellField({ step, update }: { step: StepDraft; update: (patch: Partial<StepDraft>) => void }) {
  const shell = optionText(step.options, 'shell')
  return (
    <div className="task-create-config__field">
      <span>解释器</span>
      <Select aria-label="解释器" showSearch allowCreate allowClear options={shellOptions}
        {...(shell ? { value: shell } : {})}
        placeholder="缺省（Linux: sh、Windows: cmd）"
        onChange={(value?: string) => setOptionText(step, update, 'shell', value ?? '')} />
    </div>
  )
}

const mountTypes = [
  { value: 'bind', label: 'bind（宿主机路径）' },
  { value: 'volume', label: 'volume（Docker 卷）' },
  { value: 'tmpfs', label: 'tmpfs（内存盘）' },
  { value: 'npipe', label: 'npipe（Windows 命名管道）' },
]

const mountSourceHint: Record<string, string> = {
  bind: '宿主机绝对路径，如 /var/run/docker.sock',
  volume: '卷名',
  npipe: '管道路径，如 \\\\.\\pipe\\docker_engine',
}

/** 容器步骤的 options.mounts：每行 源 / 目标 / 类型 / 只读。工作目录和脚本目录由系统固定挂载，这里只追加。 */
function MountFields({ options, onChange }: { options: Mapping; onChange: (options: Mapping) => void }) {
  const value = options.mounts
  if (value != null && (!Array.isArray(value) || !value.every(isMapping))) {
    return <p>挂载列表格式不正确，请在“原始参数”或 YAML 中修正。</p>
  }
  const rows: Mapping[] = Array.isArray(value) ? value : []
  const setRows = (next: Mapping[]) => {
    const rest = Object.fromEntries(Object.entries(options).filter(([key]) => key !== 'mounts'))
    onChange(next.length ? { ...rest, mounts: next } : rest)
  }
  const edit = (index: number, patch: Mapping) => {
    setRows(rows.map((row, i) => {
      if (i !== index) return row
      const merged = { ...row, ...patch }
      // 与缺省值相同的键不写出：bind 是缺省类型，只读默认关闭。
      for (const key of Object.keys(merged)) {
        if (merged[key] === undefined || merged[key] === '' || merged[key] === false || (key === 'type' && merged[key] === 'bind')) delete merged[key]
      }
      return merged
    }))
  }
  return (
    <fieldset className="task-create-config__group">
      <legend>挂载</legend>
      <p className="task-create-config__hint">
        任务工作目录（/workspace，Windows 容器为 C:\workspace）和脚本目录（/taskflow）始终自动挂载，不能覆盖；这里追加的挂载默认可写。
      </p>
      {rows.map((row, index) => {
        const type = typeof row.type === 'string' && row.type ? row.type : 'bind'
        return (
          <div className="task-create-config__mount-row" key={index}>
            <Input aria-label={`挂载 ${index + 1} 源`} disabled={type === 'tmpfs'}
              placeholder={type === 'tmpfs' ? 'tmpfs 无需源' : mountSourceHint[type] ?? '源'}
              value={type === 'tmpfs' ? '' : scalarText(row.source)} onChange={(source) => edit(index, { source })} />
            <Input aria-label={`挂载 ${index + 1} 目标`} placeholder="容器内目标路径"
              value={scalarText(row.target)} onChange={(target) => edit(index, { target })} />
            <Select aria-label={`挂载 ${index + 1} 类型`} options={mountTypes} value={type}
              onChange={(next: string) => edit(index, next === 'tmpfs' ? { type: next, source: undefined } : { type: next })} />
            <label className="task-create-config__inline">
              <Switch size="small" aria-label={`挂载 ${index + 1} 只读`} checked={row.readOnly === true}
                onChange={(readOnly) => edit(index, { readOnly })} />
              只读
            </label>
            <Button size="small" status="danger" type="text" aria-label={`删除挂载 ${index + 1}`}
              onClick={() => setRows(rows.filter((_row, i) => i !== index))}>删除</Button>
          </div>
        )
      })}
      <Button size="small" onClick={() => setRows([...rows, { source: '', target: '' }])}>添加挂载</Button>
    </fieldset>
  )
}

/** 以 YAML 展示并编辑整个 options，便于查看表单没有覆盖的键；解析失败时保留原文并提示，不改动草稿。 */
function RawOptionsFields({ options, onChange }: { options: Mapping; onChange: (options: Mapping) => void }) {
  const canonical = Object.keys(options).length ? dump(options, { lineWidth: -1 }) : ''
  const [text, setText] = useState(canonical)
  const [error, setError] = useState('')
  // 表单字段改动后同步原文；用户正在输入且仍然合法时不覆盖其排版。
  useEffect(() => {
    try {
      const parsed = text.trim() ? load(text) : {}
      if (isMapping(parsed) && dump(parsed, { sortKeys: true }) === dump(options, { sortKeys: true })) return
    } catch {
      // 原文已不合法，下面用草稿覆盖。
    }
    setText(canonical)
    setError('')
  }, [canonical])
  const change = (next: string) => {
    setText(next)
    try {
      const parsed = next.trim() ? load(next) : {}
      if (!isMapping(parsed)) throw new Error('options 必须是对象')
      setError('')
      onChange(parsed)
    } catch (err) {
      setError(err instanceof Error ? err.message.split('\n')[0] ?? '' : String(err))
    }
  }
  return (
    <fieldset className="task-create-config__group">
      <legend>原始参数（options）</legend>
      <p className="task-create-config__hint">执行器私有参数的完整内容，用 YAML 书写，与上方表单双向同步；未识别的键由服务端校验并拒绝。</p>
      <Input.TextArea aria-label="原始 options" value={text} onChange={change} autoSize={{ minRows: 4, maxRows: 14 }}
        placeholder={'image: debian:latest\nshell: [python3, -u]'} />
      {error && <p role="alert" className="task-create-config__hint task-create-config__error">{error}</p>}
    </fieldset>
  )
}

function StepRetryFields({ extra, approval, onChange }: { extra: Mapping; approval: boolean; onChange: (patch: Mapping) => void }) {
  const retry = isMapping(extra.retryPolicy) ? extra.retryPolicy : {}
  const editRetry = (key: string, value: unknown) => {
    const next = { ...retry }
    if (value == null || value === '') delete next[key]
    else next[key] = value
    onChange({ retryPolicy: Object.keys(next).length ? next : undefined })
  }
  return (
    <fieldset className="task-create-config__group">
      <legend>重试策略</legend>
      {approval && <p className="task-create-config__hint">仅在发起审批失败时重试。审批被拒绝或等待结果超时，不会重新发起。</p>}
      {extra.retryPolicy != null && !isMapping(extra.retryPolicy) ? <p>重试策略格式不正确，请在 YAML 中修正。</p> : (
        <div className="task-create-config__retry">
          <label>最大尝试次数（含首次，-1 为无限）<InputNumber min={-1} precision={0} value={typeof retry.maxAttempts === 'number' ? retry.maxAttempts : undefined} onChange={(value) => editRetry('maxAttempts', value)} /></label>
          <label>间隔倍数<InputNumber min={0} step={0.1} value={typeof retry.multiplier === 'number' ? retry.multiplier : undefined} onChange={(value) => editRetry('multiplier', value)} /></label>
          <label>抖动比例<InputNumber min={0} max={1} step={0.1} value={typeof retry.jitter === 'number' ? retry.jitter : undefined} onChange={(value) => editRetry('jitter', value)} /></label>
          <label>首次重试间隔<Input value={scalarText(retry.interval)} onChange={(value) => editRetry('interval', editDuration(value))} placeholder="如 2s" /></label>
          <label>最大重试间隔<Input value={scalarText(retry.maxInterval)} onChange={(value) => editRetry('maxInterval', editDuration(value))} placeholder="如 30s" /></label>
        </div>
      )}
    </fieldset>
  )
}

type StepFieldsProps = { step: StepDraft; steps: StepDraft[]; update: (patch: Partial<StepDraft>) => void }

function StepRoutingFields({ step, steps, update }: StepFieldsProps) {
  const blocked = blockedDependencies(steps, step.id)
  const sources = steps.filter((candidate) => !blocked.has(candidate.id) && normalizeName(candidate.name))
    .map((candidate) => ({ value: candidate.id, label: candidate.name }))
  const editEdge = (index: number, edit: (edge: StepDraftEdge) => StepDraftEdge) => {
    update({ edges: step.edges.map((edge, i) => i === index ? edit(edge) : edge) })
  }
  const emit = step.routing?.emit ?? []
  const editEmit = (emit: string[]) => update({ routing: { ...step.routing, emit } })
  return (
    <div className="task-create__fields">
      <fieldset className="task-create-config__group">
        <legend>入边与汇合</legend>
        <div className="task-create-config__field"><span>汇合策略</span><Select
          aria-label="汇合策略"
          options={[{ value: 'all', label: '全部匹配（all）' }, { value: 'any', label: '至少一条匹配（any）' }]}
          value={step.routing?.join ?? 'all'}
          onChange={(join: 'all' | 'any') => update({ routing: { ...step.routing, join } })}
        /></div>
        <p className="task-create-config__hint">两种策略都会等待所有上游结束。上游取消会沿入边传播。</p>
        {step.edges.map((edge, index) => (
          <div className="task-create-config__env-row" key={index}>
            <Select aria-label={`入边 ${index + 1} 的上游步骤`} showSearch options={sources} value={edge.source}
              onChange={(source: number) => editEdge(index, (edge) => ({ ...edge, source }))} />
            <Input aria-label={`入边 ${index + 1} 的触发标签`} placeholder="success（默认）"
              value={edge.trigger ?? ''} onChange={(trigger) => editEdge(index, (edge) => ({ ...edge, trigger }))} />
            <Button size="small" status="danger" type="text" aria-label={`删除入边 ${index + 1}`}
              onClick={() => update({ edges: step.edges.filter((_edge, i) => i !== index) })}>删除</Button>
          </div>
        ))}
        <Select aria-label="添加上游步骤" showSearch placeholder={sources.length ? '添加上游步骤' : '没有可添加的上游步骤'}
          value="" options={sources} disabled={!sources.length}
          onChange={(source: number) => update({ edges: [...step.edges, { source }] })} />
        <p className="task-create-config__hint">留空等待 success；填写 failure 仅在上游最终失败时触发，也可使用上游声明的自定义标签。同一上游可添加多条不同标签的入边。</p>
      </fieldset>
      <fieldset className="task-create-config__group">
        <legend>成功时发出的标签</legend>
        <p className="task-create-config__hint">不配置时只发出 success。添加自定义标签后，如仍需放行默认成功边，也要保留 success。标签区分大小写。</p>
        {emit.map((tag, index) => (
          <div className="task-create-config__tag-row" key={index}>
            <Input aria-label={`成功标签 ${index + 1}`} placeholder="success（默认）" value={tag} error={tag === 'failure'}
              onChange={(value) => editEmit(emit.map((tag, i) => i === index ? value : tag))} />
            <Button size="small" status="danger" type="text" aria-label={`删除成功标签 ${index + 1}`}
              onClick={() => editEmit(emit.filter((_tag, i) => i !== index))}>删除</Button>
          </div>
        ))}
        {emit.includes('failure') && <p role="alert" className="task-create-config__hint">failure 是保留标签，不能在成功时发出。</p>}
        <Button size="small" onClick={() => editEmit([...emit, emit.length ? '' : 'success'])}>添加成功标签</Button>
      </fieldset>
    </div>
  )
}

function StepConfigFields({ step, steps, update, tab }: StepFieldsProps & { tab?: 'routing' | undefined }) {
  const kind = step.kind.trim().toLowerCase()
  const hasOptions = kind !== 'approval' && kind !== 'mkdir' && kind !== 'touch'
  const extra = step.extra ?? {}
  const editExtra = (patch: Mapping) => update({ extra: { ...extra, ...patch } })
  const fields = isApproval(step.kind) ? approvalFields(step.content) : null
  const edit = (section: 'request' | 'callback', field: string, value: string) => {
    update({ content: updateApproval(step.content, section, field, value) })
  }
  const read = (section: 'request' | 'callback', field: string) => String(fields?.[section][field] ?? '')
  return (
    <Tabs defaultActiveTab={tab ?? 'basic'} className="task-create-config__tabs">
      <Tabs.TabPane key="basic" title="基本信息">
        <div className="task-create__fields task-create-config__grid">
          <label>步骤名称<Input value={step.name} onChange={(name) => update({ name })} /></label>
          <label>步骤类型<Input aria-label="步骤类型" readOnly value={step.kind.trim()} /></label>
          <label className="task-create-config__wide">步骤超时<Input value={String(step.timeout)} onChange={(timeout) => update({ timeout: editDuration(timeout) })} placeholder="如 5m" /></label>
          <label className="task-create-config__wide">步骤描述<Input value={step.desc} onChange={(desc) => update({ desc })} /></label>
        </div>
      </Tabs.TabPane>
      {hasOptions && (
        <Tabs.TabPane key="options" title={`执行器参数${Object.keys(step.options).length ? ` (${Object.keys(step.options).length})` : ''}`}>
          <div className="task-create__fields">
            {kind === 'container' && (
              <fieldset className="task-create-config__group">
                <legend>执行器</legend>
                <label>镜像<Input value={optionText(step.options, 'image')} onChange={(image) => setOptionText(step, update, 'image', image)} placeholder="如 debian:latest" /></label>
              </fieldset>
            )}
            {kind === 'container' && <MountFields options={step.options} onChange={(options) => update({ options })} />}
            <RawOptionsFields options={step.options} onChange={(options) => update({ options })} />
          </div>
        </Tabs.TabPane>
      )}
      <Tabs.TabPane key="routing" title="依赖与分支">
        <StepRoutingFields step={step} steps={steps} update={update} />
      </Tabs.TabPane>
      <Tabs.TabPane key="content" title="执行内容">
        {fields ? (
          <div className="task-create__fields task-create-config__grid">
            <label className="task-create-config__wide">审批请求地址<Input value={read('request', 'url')} onChange={(value) => edit('request', 'url', value)} placeholder="https://approval.example.com/requests" /></label>
            <div className="task-create-config__field"><span>请求方法</span><Select aria-label="请求方法" options={['POST', 'PUT']} value={read('request', 'method').trim().toUpperCase() || 'POST'} onChange={(value: string) => edit('request', 'method', value)} /></div>
            <label className="task-create-config__wide">回调地址<Input value={read('callback', 'url')} onChange={(value) => edit('callback', 'url', value)} placeholder="https://runner.example.com/api/v1/approval/callback" /></label>
            <label>通过状态<Input value={read('callback', 'approved')} onChange={(value) => edit('callback', 'approved', value)} placeholder="approved" /></label>
            <label>拒绝状态<Input value={read('callback', 'rejected')} onChange={(value) => edit('callback', 'rejected', value)} placeholder="rejected" /></label>
          </div>
        ) : kind === 'mkdir' || kind === 'touch' ? (
          <div className="task-create__fields">
            <FileConfigFields kind={kind} content={step.content} onChange={(content) => update({ content })} />
          </div>
        ) : (
          <div className="task-create__fields task-create-config__script">
            {(kind === 'xexec' || kind === 'container') && <ShellField step={step} update={update} />}
            <div className="task-create-config__field task-create-config__editor" role="group" aria-label={isApproval(step.kind) ? '审批配置原文' : '执行内容'}>
              <span>{isApproval(step.kind) ? '审批配置原文' : '执行内容'}</span>
              <div className="task-create-config__editor-body">
                <CodeEditor value={step.content} onChange={(content) => update({ content })}
                  language={scriptLanguage(step.kind, optionText(step.options, 'shell'))} />
              </div>
            </div>
          </div>
        )}
      </Tabs.TabPane>
      <Tabs.TabPane key="env" title="环境变量">
        <div className="task-create__fields">
          <EnvFields value={extra.env} onChange={(env) => editExtra({ env: env.length ? env : undefined })} />
        </div>
      </Tabs.TabPane>
      <Tabs.TabPane key="retry" title="重试策略">
        <div className="task-create__fields">
          <StepRetryFields extra={extra} approval={isApproval(step.kind)} onChange={editExtra} />
        </div>
      </Tabs.TabPane>
    </Tabs>
  )
}

type StepConfigDialog = { values: StepDraft; tab?: 'routing' | undefined }

type BuilderProps = { draft: TaskDraft; layoutVersion: number; onChange: (update: (draft: TaskDraft) => TaskDraft) => void }

function TaskBuilder({ draft, onChange, layoutVersion }: BuilderProps) {
  const { screenToFlowPosition, fitView } = useReactFlow<DraftNode>()
  const canvas = useRef<HTMLDivElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const [picker, setPicker] = useState<{ x: number; y: number; position: XYPosition }>()
  useEffect(() => {
    if (!layoutVersion) return
    const frame = requestAnimationFrame(() => { void fitView({ maxZoom: 1, padding: 0.2 }) })
    return () => cancelAnimationFrame(frame)
  }, [fitView, layoutVersion])
  useEffect(() => {
    if (picker) menu.current?.querySelector('button')?.focus({ preventScroll: true })
  }, [picker])
  const [selectedStep, setSelectedStep] = useState<number>()
  const [selectedEdge, setSelectedEdge] = useState<string>()
  const [config, setConfig] = useState<StepConfigDialog>()
  const [discardConfirm, setDiscardConfirm] = useState(false)

  const nodes = useMemo<DraftNode[]>(() => draft.steps.map((item) => ({
    id: String(item.id), type: 'draftStep', position: item.position, selected: selectedStep === item.id,
    width: nodeWidth, height: nodeHeight, data: { name: item.name, kind: item.kind, options: item.options },
  })), [draft.steps, selectedStep])

  const edges = useMemo<Edge[]>(() => draft.steps.flatMap((item) => {
    // Render one line per pair; multiple triggers on that line are edited on the target step.
    const grouped = new Map<number, Set<string>>()
    for (const { source, trigger } of item.edges) {
      const labels = grouped.get(source) ?? new Set<string>()
      labels.add(trigger || 'success')
      grouped.set(source, labels)
    }
    return [...grouped].map(([source, labels]) => {
      const id = draftEdgeID(source, item.id)
      return {
        id, source: String(source), target: String(item.id), selected: selectedEdge === id,
        label: labels.size === 1 && labels.has('success') ? undefined : [...labels].join(' / '),
        type: 'smoothstep', markerEnd: { type: MarkerType.ArrowClosed },
      }
    })
  }), [draft.steps, selectedEdge])

  function addStep(kind: string, options: Mapping, position: XYPosition) {
    const id = 1 + Math.max(0, ...draft.steps.map((item) => item.id))
    const step = newStep(id, kind, options, position, draft.steps)
    onChange((current) => ({ ...current, steps: [...current.steps, step] }))
    setConfig({ values: step })
    setSelectedStep(id)
    setSelectedEdge(undefined)
    setPicker(undefined)
  }

  function openPicker(clientX: number, clientY: number) {
    const bounds = canvas.current?.getBoundingClientRect()
    if (!bounds) return
    setPicker({
      x: Math.max(0, Math.min(clientX - bounds.left, bounds.width - 180)),
      y: Math.max(0, Math.min(clientY - bounds.top, bounds.height - 310)),
      position: screenToFlowPosition({ x: clientX, y: clientY }),
    })
  }

  function validConnection({ source, target }: Connection | Edge): boolean {
    const parent = draft.steps.find((item) => item.id === Number(source))
    const child = draft.steps.find((item) => item.id === Number(target))
    return !!parent && !!child && !!normalizeName(parent.name) && !child.edges.some(({ source }) => source === parent.id)
      && !blockedDependencies(draft.steps, child.id).has(parent.id)
  }

  function removeElements(stepIDs: Set<number>, edgeIDs: Set<string>) {
    onChange((current) => removeDraftElements(current, stepIDs, edgeIDs))
    if (selectedStep !== undefined && stepIDs.has(selectedStep)) setSelectedStep(undefined)
    if (selectedEdge !== undefined && edgeIDs.has(selectedEdge)) setSelectedEdge(undefined)
  }

  function selectStep(id?: number) {
    setSelectedStep(id)
    setSelectedEdge(undefined)
  }

  function openStepConfig(id: number, tab?: 'routing') {
    const step = draft.steps.find((item) => item.id === id)
    if (!step) return
    selectStep(id)
    setConfig({ values: step, tab })
  }

  function saveConfig() {
    if (!config) return
    const { id, name, kind, options, desc, timeout, content, edges, routing, extra } = config.values
    onChange((current) => ({ ...current, steps: current.steps.map((step) => step.id === id
      ? { ...step, name, kind, options, desc, timeout, content, edges, routing, extra: extra ?? {} } : step) }))
    setConfig(undefined)
  }

  function closeConfig() {
    if (!config) return
    const original = draft.steps.find((step) => step.id === config.values.id)
    if (original && dump(original, { sortKeys: true }) !== dump(config.values, { sortKeys: true })) {
      setDiscardConfirm(true)
    } else {
      setConfig(undefined)
    }
  }

  const activeEdge = edges.find((edge) => edge.id === selectedEdge)

  return (
    <div className="task-create__builder">
      <div ref={canvas} className="task-create__canvas">
        {activeEdge && (
          <div className="task-create__canvas-actions">
            <Button size="small" onClick={() => openStepConfig(Number(activeEdge.target), 'routing')}>编辑连线</Button>
            <Button size="small" status="danger" onClick={() => removeElements(new Set(), new Set([activeEdge.id]))}>删除连线</Button>
          </div>
        )}
        <ReactFlow<DraftNode>
          nodes={nodes} edges={edges} nodeTypes={nodeTypes}
          fitView={nodes.length > 0} fitViewOptions={{ maxZoom: 1, padding: 0.2 }} minZoom={0.15}
          onNodesChange={(changes) => {
            const positions = new Map<number, XYPosition>()
            for (const item of nodes) {
              for (const change of changes.getForElement(item.id)) {
                if (change.type === 'position' && change.position) positions.set(Number(item.id), change.position)
                if (change.type === 'select' && change.selected) selectStep(Number(item.id))
              }
            }
            if (positions.size) onChange((current) => ({ ...current, steps: current.steps.map((item) => {
              const position = positions.get(item.id)
              return position ? { ...item, position } : item
            }) }))
          }}
          onNodeClick={(_event, node) => { selectStep(Number(node.id)); setPicker(undefined) }}
          onNodeDoubleClick={(_event, node) => openStepConfig(Number(node.id))}
          onNodeContextMenu={(event, node) => {
            event.preventDefault()
            setPicker(undefined)
            openStepConfig(Number(node.id))
          }}
          onPaneClick={() => { selectStep(); setPicker(undefined) }}
          onPaneContextMenu={(event) => {
            event.preventDefault()
            openPicker(event.clientX, event.clientY)
          }}
          onEdgeClick={(_event, edge) => { setSelectedStep(undefined); setSelectedEdge(edge.id) }}
          onEdgeDoubleClick={(_event, edge) => openStepConfig(Number(edge.target), 'routing')}
          onDelete={({ nodes: removedNodes, edges: removedEdges }) => removeElements(
            new Set(removedNodes.map((node) => Number(node.id))), new Set(removedEdges.map((edge) => edge.id)),
          )}
          deleteKeyCode={config ? null : ['Backspace', 'Delete']}
          isValidConnection={validConnection}
          onConnect={(connection) => {
            if (!validConnection(connection)) return
            onChange((current) => ({ ...current, steps: current.steps.map((item) => item.id === Number(connection.target)
              ? { ...item, edges: [...item.edges, { source: Number(connection.source) }] } : item) }))
          }}
          proOptions={{ hideAttribution: true }}
        >
          <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
          <Controls showInteractive={false} />
        </ReactFlow>
        {picker && <div
          ref={menu}
          className="task-create__step-picker"
          role="group"
          aria-label="可选步骤组件"
          style={{ left: picker.x, top: picker.y }}
          onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget)) setPicker(undefined)
          }}
          onKeyDown={(event) => {
            if (event.key === 'Escape') setPicker(undefined)
          }}
        >
          {palette.map(({ kind, options, label }) => (
            <button key={`${kind}:${optionText(options, 'shell')}:${optionText(options, 'image')}`} type="button" onClick={() => addStep(kind, options, picker.position)}>
              <StepBadge kind={kind} options={options} />{label}
            </button>
          ))}
        </div>}

      </div>
      <Drawer
        visible={!!config}
        title="步骤配置"
        placement="right"
        width="min(760px, 100vw)"
        zIndex={1002}
        className="task-create-config task-create-config--step"
        maskClosable
        escToExit={!discardConfirm}
        autoFocus
        onCancel={closeConfig}
        footer={config && (
          <div className="task-create-config__actions">
            <Button status="danger" type="text" onClick={() => {
              removeElements(new Set([config.values.id]), new Set())
              setConfig(undefined)
            }}>删除步骤</Button>
            <Button onClick={closeConfig}>取消</Button>
            <Button type="primary" disabled={!!config.values.routing?.emit?.includes('failure')} onClick={saveConfig}>保存</Button>
          </div>
        )}
      >
        {config && (
          <StepConfigFields key={`${config.values.id}:${config.tab ?? 'basic'}`} tab={config.tab} step={config.values} steps={draft.steps} update={(patch) => setConfig((current) => current
            ? { ...current, values: { ...current.values, ...patch } } : current)} />
        )}
      </Drawer>
      <Modal
        visible={discardConfirm}
        title="放弃未保存的改动？"
        maskStyle={{ zIndex: 1003 }}
        wrapStyle={{ zIndex: 1003 }}
        maskClosable={false}
        cancelText="继续编辑"
        okText="放弃改动"
        okButtonProps={{ status: 'danger' }}
        onCancel={() => setDiscardConfirm(false)}
        onOk={() => { setDiscardConfirm(false); setConfig(undefined) }}
      >
        关闭后，这次对配置的修改不会写入画布或 YAML。
      </Modal>
    </div>
  )
}

export function TaskCreateForm(props: BuilderProps) {
  return <ReactFlowProvider><TaskBuilder {...props} /></ReactFlowProvider>
}
