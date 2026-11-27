import dagre from '@dagrejs/dagre'
import type { XYPosition } from '@xyflow/react'
import { dump, load } from 'js-yaml'

export type Mapping = Record<string, unknown>

export function isMapping(value: unknown): value is Mapping {
  return value !== null && typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype
}

export function normalizeName(name: string): string {
  return name.replace(/[^a-zA-Z\p{Script=Han}0-9_.~-]/gu, '')
}

export const nodeWidth = 176
export const nodeHeight = 56

type DraftFields = {
  name: string
  desc: string
  timeout: string | number
  extra?: Mapping
}

export type StepDraftEdge = {
  source: number
  trigger?: string | undefined
  extra?: Mapping
}

type StepDraftRouting = {
  join?: 'all' | 'any' | undefined
  emit?: string[] | undefined
  extra?: Mapping
}

export type StepDraft = DraftFields & {
  id: number
  position: XYPosition
  kind: string
  /** 执行器私有参数（options）。xexec 支持 shell，container 支持 image、shell、mounts；由后端校验 */
  options: Mapping
  edges: StepDraftEdge[]
  routing?: StepDraftRouting | undefined
  content: string
}

export type TaskDraft = Omit<DraftFields, 'name' | 'desc'> & {
  workdir: string
  /** 为 true 时任务结束后删除工作目录；省略等同于 false（保留） */
  cleanup?: boolean | undefined
  steps: StepDraft[]
}

export const emptyDraft: TaskDraft = { workdir: '', timeout: '', steps: [] }

export function taskWorkdirError(value: unknown): string | undefined {
  if (value === undefined || value === '') return undefined
  if (typeof value !== 'string') return '任务 workdir 必须是字符串'
  const parts = value.split('/')
  if (value.startsWith('/') || /[\\:\0]/.test(value) || parts.includes('..') || !parts.some((part) => part && part !== '.')) {
    return '任务 workdir 必须是工作区内的相对目录，使用 / 分隔，不能包含 ..'
  }
  return undefined
}

function yamlDump(value: unknown): string {
  return dump(value, { lineWidth: -1 })
}

export type TaskFormat = 'yaml' | 'json'

export function parseTaskText(text: string, format: TaskFormat): unknown {
  if (format === 'yaml') return load(text)
  const value: unknown = JSON.parse(text)
  // JSON.parse accepts duplicate keys, but the server rejects them.
  load(text)
  return value
}

export function draftToText(draft: TaskDraft, format: TaskFormat): string {
  const names = new Map(draft.steps.map((step) => [step.id, step.name]))
  const task = {
    ...draft.extra,
    workdir: draft.workdir || undefined,
    cleanup: draft.cleanup ? true : undefined,
    timeout: draft.timeout === '' ? undefined : draft.timeout,
    step: draft.steps.map((step) => ({
      ...step.extra,
      name: step.name || undefined,
      desc: step.desc || undefined,
      kind: step.kind,
      options: serializeOptions(step.options),
      timeout: step.timeout === '' ? undefined : step.timeout,
      routing: serializeRouting(step, names),
      content: step.content,
    })),
  }
  return format === 'json' ? JSON.stringify(task, null, 2) + '\n' : yamlDump(task)
}

function nonEmptyText(value: unknown): boolean {
  return typeof value === 'string' && value.trim() !== ''
}

/**
 * options 的序列化视图：去掉还没填写的挂载行（例如刚点“添加挂载”就保存），
 * 全部为空时省略 options。其余键原样保留，由服务端决定是否接受。
 */
function serializeOptions(options: Mapping): Mapping | undefined {
  const result: Mapping = {}
  for (const [key, value] of Object.entries(options)) {
    if (key === 'mounts' && Array.isArray(value)) {
      const mounts = value.filter((item) => isMapping(item) && (
        ['source', 'target', 'type'].some((field) => nonEmptyText(item[field])) || item.readOnly === true
      ))
      if (mounts.length) result[key] = mounts
      continue
    }
    result[key] = value
  }
  return Object.keys(result).length ? result : undefined
}

function serializeRouting(step: StepDraft, names: Map<number, string>): Mapping | undefined {
  const edges = step.edges.map(({ source, extra, trigger }) => ({
    ...extra, step: names.get(source) ?? '', trigger: trigger || undefined,
  }))
  const { join, emit, extra } = step.routing ?? {}
  if (!edges.length && !join && !emit?.length && !Object.keys(extra ?? {}).length) return undefined
  return { ...extra, edges: edges.length ? edges : undefined, join, emit: emit?.length ? emit : undefined }
}

export function blockedDependencies(steps: StepDraft[], id: number): Set<number> {
  const downstream = new Map<number, number[]>()
  for (const step of steps) {
    for (const { source } of step.edges) {
      const targets = downstream.get(source)
      if (targets) targets.push(step.id)
      else downstream.set(source, [step.id])
    }
  }
  const blocked = new Set([id])
  for (const current of blocked) {
    for (const next of downstream.get(current) ?? []) blocked.add(next)
  }
  return blocked
}

export function draftEdgeID(source: number, target: number): string {
  return `${source}:${target}`
}

export function removeDraftElements(draft: TaskDraft, stepIDs: Set<number>, edgeIDs: Set<string>): TaskDraft {
  return { ...draft, steps: draft.steps.filter(({ id }) => !stepIDs.has(id)).map((step) => ({
    ...step, edges: step.edges.filter(({ source }) => !stepIDs.has(source) && !edgeIDs.has(draftEdgeID(source, step.id))),
  })) }
}

function assertRoundTrip(value: unknown, seen = new Set<object>()): void {
  if (typeof value === 'number' && (!Number.isFinite(value) || (Number.isInteger(value) && !Number.isSafeInteger(value)))) {
    throw new Error('存在无法无损转换的数字，请在 YAML 模式中继续编辑')
  }
  if (!value || typeof value !== 'object' || seen.has(value)) return
  seen.add(value)
  for (const child of Object.values(value)) assertRoundTrip(child, seen)
}

function stringField(value: unknown, label: string): string {
  if (value == null) return ''
  if (typeof value !== 'string') throw new Error(`${label}必须是字符串`)
  return value
}

function draftFields({ name, desc, timeout, ...extra }: Mapping, label: string): DraftFields {
  if (timeout != null && typeof timeout !== 'string' && typeof timeout !== 'number') {
    throw new Error(`${label}的 timeout 必须是时长字符串或整数纳秒`)
  }
  return { name: stringField(name, `${label}的 name `), desc: stringField(desc, `${label}的 desc `), timeout: timeout ?? '', extra }
}

export function editDuration(text: string): string | number {
  const value = Number(text)
  return /^[+-]?\d+$/.test(text) && Number.isSafeInteger(value) ? value : text
}

const taskFields = new Set(['workdir', 'cleanup', 'timeout', 'env', 'step'])
const stepFields = new Set(['name', 'desc', 'timeout', 'env', 'kind', 'options', 'content', 'routing', 'retryPolicy'])

function checkFields(value: Mapping, allowed: ReadonlySet<string>, label: string): void {
  const unknown = Object.keys(value).filter((key) => !allowed.has(key))
  if (unknown.length) throw new Error(`${label}包含未知字段：${unknown.join('、')}`)
}

export function textToDraft(text: string, previous: TaskDraft, format: TaskFormat): TaskDraft {
  const task = parseTaskText(text, format)
  if (!isMapping(task)) throw new Error('任务配置必须是一个对象')
  checkFields(task, taskFields, '任务配置')
  const { step, workdir, cleanup, timeout, ...extra } = task
  const workdirError = taskWorkdirError(workdir)
  if (workdirError) throw new Error(workdirError)
  if (cleanup != null && typeof cleanup !== 'boolean') throw new Error('任务的 cleanup 必须是布尔值')
  if (timeout != null && typeof timeout !== 'string' && typeof timeout !== 'number') {
    throw new Error('任务的 timeout 必须是时长字符串或整数纳秒')
  }
  // 步骤列表可为空，以便在创建前切换编辑模式。
  if (step != null && !Array.isArray(step)) throw new Error('step 必须是步骤列表')
  assertRoundTrip(task)

  const previousByName = new Map(previous.steps.filter((item) => normalizeName(item.name)).map((item) => [normalizeName(item.name), item]))
  let nextID = previous.steps.reduce((id, item) => Math.max(id, item.id), 0)
  const names = new Map<string, number>()
  const graph = new dagre.graphlib.Graph().setGraph({ rankdir: 'LR', ranksep: 80, nodesep: 30, marginx: 48, marginy: 48 })
  graph.setDefaultEdgeLabel(() => ({}))
  const steps = (step ?? []).map((item: unknown, index: number) => {
    const label = `第 ${index + 1} 个步骤`
    if (!isMapping(item)) throw new Error(`${label}必须是对象`)
    checkFields(item, stepFields, label)
    const { kind, options, content, routing, ...rest } = item
    if (options != null && !isMapping(options)) throw new Error(`${label}的 options 必须是对象`)
    const base = draftFields(rest, label)
    const name = normalizeName(base.name)
    const existing = name ? previousByName.get(name) : undefined
    const id = existing?.id ?? ++nextID
    if (name) {
      if (names.has(name)) throw new Error(`步骤名称「${name}」重复`)
      names.set(name, id)
    }
    graph.setNode(String(id), { width: nodeWidth, height: nodeHeight })
    return {
      ...base, id, label, routing, position: existing?.position,
      kind: stringField(kind, `${label}的 kind `), options: options ?? {},
      content: stringField(content, `${label}的 content `),
    }
  }).map(({ routing, label, ...item }): Omit<StepDraft, 'position'> & { position: XYPosition | undefined } => {
    const resolve = (dependency: string) => {
      const id = names.get(normalizeName(dependency))
      if (id === undefined) throw new Error(`${label}依赖的「${dependency}」不存在`)
      if (id === item.id) throw new Error(`${label}不能依赖自身`)
      graph.setEdge(String(id), String(item.id))
      return id
    }
    const edges: StepDraftEdge[] = []
    if (routing == null) return { ...item, edges }
    if (!isMapping(routing)) throw new Error(`${label}的 routing 必须是对象`)
    const { edges: labeled, join, emit, ...extra } = routing
    if (join != null && join !== '' && join !== 'all' && join !== 'any') throw new Error(`${label}的 routing.join 必须是 all 或 any`)
    if (emit != null && (!Array.isArray(emit) || emit.some((tag) => typeof tag !== 'string'))) {
      throw new Error(`${label}的 routing.emit 必须是字符串列表`)
    }
    if (labeled != null && !Array.isArray(labeled)) throw new Error(`${label}的 routing.edges 必须是列表`)
    for (const edge of labeled ?? []) {
      if (!isMapping(edge) || typeof edge.step !== 'string' || (edge.trigger != null && typeof edge.trigger !== 'string')) {
        throw new Error(`${label}的 routing.edges 需要步骤名称和可选的触发标签`)
      }
      const { step, trigger, ...extra } = edge
      edges.push({ source: resolve(step), trigger: trigger ?? undefined, extra })
    }
    return { ...item, edges, routing: { join: join || undefined, emit: emit ?? undefined, extra } }
  })
  if (!dagre.graphlib.alg.isAcyclic(graph)) throw new Error('步骤存在循环依赖')
  const layout = steps.some((item) => !item.position)
  if (layout) dagre.layout(graph)
  return {
    workdir: stringField(workdir, '任务的 workdir '),
    cleanup: cleanup === true ? true : undefined,
    timeout: timeout ?? '',
    extra,
    steps: steps.map((item) => {
      if (!layout && item.position) return { ...item, position: item.position }
      const { x, y } = graph.node(String(item.id))
      return { ...item, position: { x: x - nodeWidth / 2, y: y - nodeHeight / 2 } }
    }),
  }
}

export function isApproval(kind: string): boolean {
  return kind.trim().toLowerCase() === 'approval'
}

/** 无法安全投影到表单时，以原文编辑审批配置。 */
export function approvalFields(content: string): { request: Mapping; callback: Mapping; config: Mapping } | null {
  try {
    const config = content.trim() ? load(content) : {}
    if (!isMapping(config)) return null
    const { request = {}, callback = {} } = config
    if (!isMapping(request) || !isMapping(callback)) return null
    if ([request.url, request.method, callback.url, callback.approved, callback.rejected].some((value) => value != null && typeof value !== 'string')) return null
    assertRoundTrip(config)
    return { request, callback, config }
  } catch {
    return null
  }
}

export function updateApproval(content: string, section: 'request' | 'callback', field: string, value: string): string {
  const fields = approvalFields(content)
  if (!fields) return content
  return yamlDump({ ...fields.config, [section]: { ...fields[section], [field]: value.trim() || undefined } })
}

export function validateApprovalFields(draft: TaskDraft): string[] {
  return draft.steps.flatMap((step, index) => {
    if (!isApproval(step.kind)) return []
    const fields = approvalFields(step.content)
    if (!fields) return [] // 原文配置由后端校验。
    return [[fields.request.url, '请求地址'], [fields.callback.url, '回调地址']].flatMap(([value, label]) => {
      const text = typeof value === 'string' ? value.trim() : ''
      const url = URL.canParse(text) ? new URL(text) : null
      return url?.hostname && /^https?:\/\//i.test(text) ? [] : [
        `第 ${index + 1} 个步骤的${label}必须是完整的 HTTP 或 HTTPS URL`,
      ]
    })
  })
}
