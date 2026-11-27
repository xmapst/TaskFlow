/** 在浏览器检查任务 YAML / JSON 的结构与依赖，提交时保留原文。 */

import { type JSX, useCallback, useId, useMemo, useState } from 'react'
import { Alert, Button, Modal, Radio } from '@arco-design/web-react'
import { createTask } from '@/api/task'
import { useAsyncAction } from '@/hooks/useAsyncAction'
import { CodeEditor } from '@/components/editor/CodeEditor'
import { TASK_TEMPLATE } from './taskTemplate'
import { TaskCreateForm } from './TaskCreateForm'
import { draftToText, emptyDraft, isMapping, normalizeName, parseTaskText, taskWorkdirError, textToDraft, validateApprovalFields, type TaskDraft, type TaskFormat } from './taskDraft'
import { IconChevronLeft, IconChevronRight, IconDocument } from '@/components/common/icons'
import './TaskCreateModal.scss'

export interface TaskCreateModalProps {
  onClose: () => void
  onCreated: (taskName: string) => void
}

interface ValidationResult {
  ok: boolean
  /** 阻断提交的错误 */
  errors: string[]
  /** 不阻断但值得提醒的问题 */
  warnings: string[]
}

const GO_DURATION = /^[+-]?(?:0|(?:(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:ns|us|µs|μs|ms|s|m|h))+)$/

/** 本地检查常见错误；Go 端仍负责最终校验。 */
export function validate(text: string, format: TaskFormat = 'yaml'): ValidationResult {
  const errors: string[] = []
  const warnings: string[] = []
  if (!text.trim()) return { ok: false, errors: ['配置内容为空'], warnings }

  let task: unknown
  try {
    // 仅用于校验；提交时发送原文。
    task = parseTaskText(text, format)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    return { ok: false, errors: [`${format.toUpperCase()} 语法错误：${message}`], warnings }
  }
  if (!isMapping(task)) return { ok: false, errors: ['配置必须是一个对象'], warnings }

  function checkDuration(value: unknown, label: string): void {
    if (value == null) return
    if ((typeof value === 'number' && Number.isInteger(value)) ||
        (typeof value === 'string' && GO_DURATION.test(value))) return
    errors.push(`${label} 必须是 Go 时长（如 2m）或整数纳秒`)
  }

  function checkEnv(value: unknown, label: string): void {
    if (value == null) return
    if (!Array.isArray(value)) {
      errors.push(`${label} 必须是 name/value 列表`)
      return
    }
    const seen = new Set<string>()
    for (const [index, entry] of value.entries()) {
      if (!isMapping(entry) || typeof entry.name !== 'string' ||
          (entry.value != null && typeof entry.value !== 'string')) {
        errors.push(`${label}第 ${index + 1} 项须使用字符串 name 和 value`)
        continue
      }
      if (seen.has(entry.name)) errors.push(`${label}「${entry.name}」重复`)
      seen.add(entry.name)
    }
  }

  if ('name' in task || 'desc' in task) errors.push('任务顶层不再接受 name 或 desc；任务名由服务端生成，可用 workdir 指定工作目录')
  const workdirError = taskWorkdirError(task.workdir)
  if (workdirError) errors.push(workdirError)
  if (task.cleanup != null && typeof task.cleanup !== 'boolean') errors.push('任务 cleanup 必须是布尔值（true 或 false）')
  checkDuration(task.timeout, '任务 timeout')
  checkEnv(task.env, '全局环境变量')

  const steps = task.step
  if (!Array.isArray(steps) || steps.length === 0) {
    errors.push('step 不能为空，至少需要一个步骤')
    return { ok: false, errors, warnings }
  }

  const names = new Map<string, number>()
  const labels = steps.map((_, index) => `第 ${index + 1} 个步骤`)
  const dependencies: string[][] = steps.map(() => [])
  steps.forEach((step, index) => {
    const label = labels[index]!
    if (!isMapping(step)) {
      errors.push(`${label} 必须是对象`)
      return
    }
    if (typeof step.kind !== 'string' || !step.kind.trim()) errors.push(`${label} 缺少有效的 kind（脚本类型）`)
    if (typeof step.content !== 'string' || !step.content.trim()) errors.push(`${label} 缺少有效的 content（脚本内容）`)

    if (step.name != null && typeof step.name !== 'string') {
      errors.push(`${label} 的 name 必须是字符串`)
    } else {
      const name = normalizeName(step.name ?? '')
      if (!name) {
        warnings.push(`${label} 未指定有效的 name，将由服务端生成，无法被其他步骤依赖`)
      } else {
        labels[index] = `步骤「${name}」`
        if (name !== step.name) warnings.push(`${label} 名称将规范化为「${name}」`)
        if (names.has(name)) errors.push(`步骤名称「${name}」重复`)
        else names.set(name, index)
      }
    }

    if (step.routing != null) {
      if (!isMapping(step.routing)) {
        errors.push(`${label} 的 routing 必须是对象`)
      } else {
        const { edges, emit, join } = step.routing
        if (join != null && join !== '' && join !== 'all' && join !== 'any') {
          errors.push(`${label} 的 routing.join 必须是 all 或 any`)
        }
        if (emit != null && (!Array.isArray(emit) || emit.some((tag) => typeof tag !== 'string' || tag === 'failure'))) {
          errors.push(`${label} 的 routing.emit 必须是字符串列表，不能包含 failure`)
        }
        if (edges != null && !Array.isArray(edges)) {
          errors.push(`${label} 的 routing.edges 必须是列表`)
        } else if (Array.isArray(edges)) {
          const names = new Set(dependencies[index])
          for (const edge of edges) {
            if (!isMapping(edge) || typeof edge.step !== 'string' ||
                !normalizeName(edge.step) || (edge.trigger != null && typeof edge.trigger !== 'string')) {
              errors.push(`${label} 的每条 routing.edges 需要有效的 step 和可选的 trigger 字符串`)
            } else {
              names.add(normalizeName(edge.step))
            }
          }
          dependencies[index] = [...names]
        }
      }
    }

    checkDuration(step.timeout, `${label}的 timeout`)
    checkEnv(step.env, `${label}环境变量`)
    if (step.retryPolicy != null) {
      if (!isMapping(step.retryPolicy)) {
        errors.push(`${label} 的 retryPolicy 必须是对象`)
      } else {
        checkDuration(step.retryPolicy.interval, `${label}的 retryPolicy.interval`)
        checkDuration(step.retryPolicy.maxInterval, `${label}的 retryPolicy.maxInterval`)
        const jitter = step.retryPolicy.jitter
        if (jitter != null && (typeof jitter !== 'number' || !Number.isFinite(jitter) || jitter < 0 || jitter > 1)) {
          errors.push(`${label} 的 retryPolicy.jitter 必须是 0 到 1 的数值`)
        }
      }
    }
  })

  // 所有任务均使用 DAG。无名步骤可以依赖其他步骤，但不能作为依赖目标。
  const indegree = steps.map(() => 0)
  const dependents: number[][] = steps.map(() => [])
  for (const [index, deps] of dependencies.entries()) {
    for (const name of deps) {
      const dependency = names.get(name)
      if (dependency === undefined) errors.push(`${labels[index]}依赖的「${name}」不存在`)
      else if (dependency === index) errors.push(`${labels[index]}不能依赖自身`)
      else {
        indegree[index]!++
        dependents[dependency]!.push(index)
      }
    }
  }
  const ready = indegree.flatMap((count, index) => count === 0 ? [index] : [])
  for (let i = 0; i < ready.length; i++) {
    for (const dependent of dependents[ready[i]!]!) {
      if (--indegree[dependent]! === 0) ready.push(dependent)
    }
  }
  if (ready.length !== steps.length) errors.push('步骤存在循环依赖')

  return { ok: errors.length === 0, errors, warnings }
}

export function TaskCreateModal({ onClose, onCreated }: TaskCreateModalProps): JSX.Element {
  const [{ text, format, draft, importError, layoutVersion }, setDocument] = useState({
    text: draftToText(emptyDraft, 'yaml'), format: 'yaml' as TaskFormat, draft: emptyDraft, importError: '', layoutVersion: 0,
  })
  const [submitted, setSubmitted] = useState(false)
  const [editorExpanded, setEditorExpanded] = useState(true)
  const editorPaneId = useId()

  const editText = useCallback((text: string) => {
    setDocument((current) => {
      if (text === current.text) return current
      try {
        const draft = textToDraft(text, current.draft, current.format)
        const oldIDs = new Set(current.draft.steps.map(({ id }) => id))
        const layoutVersion = current.layoutVersion + Number(draft.steps.some(({ id }) => !oldIDs.has(id)))
        return { ...current, text, draft, importError: '', layoutVersion }
      } catch (error) {
        return { ...current, text, importError: error instanceof Error ? error.message : String(error) }
      }
    })
  }, [])

  const editDraft = useCallback((update: (draft: TaskDraft) => TaskDraft) => {
    setDocument((current) => {
      if (current.importError) return current
      const draft = update(current.draft)
      const generated = draftToText(draft, current.format)
      // 只移动节点时不改写原文。
      const text = generated === draftToText(current.draft, current.format) ? current.text : generated
      return { ...current, draft, text }
    })
  }, [])

  const validation = useMemo(() => {
    const result = validate(text, format)
    if (importError) return result.ok ? { ...result, ok: false, errors: [importError] } : result
    const errors = [...result.errors, ...validateApprovalFields(draft)]
    return { ...result, ok: errors.length === 0, errors }
  }, [draft, format, importError, text])

  const submitAction = useAsyncAction(
    async (document: { text: string; format: TaskFormat }) => {
      const result = await createTask(document.text, document.format)
      onCreated(result.name)
    },
    { successMessage: '任务已创建', errorPrefix: '创建失败' },
  )

  const handleSubmit = useCallback(() => {
    setSubmitted(true)
    if (!validation.ok) return
    void submitAction.run({ text, format })
  }, [validation.ok, submitAction, text, format])

  const switchFormat = (next: TaskFormat) => {
    setDocument((current) => {
      if (current.format === next || current.importError) return current
      return { ...current, format: next, text: draftToText(current.draft, next) }
    })
  }

  const resetTemplate = () => {
    const template = format === 'json'
      ? JSON.stringify(parseTaskText(TASK_TEMPLATE, 'yaml'), null, 2) + '\n'
      : TASK_TEMPLATE
    editText(template)
    setSubmitted(false)
  }

  return (
    <Modal
      visible
      onCancel={onClose}
      title={
        <div className="task-create__header">
          <span className="task-create__title">
            <IconDocument size={16} />
            新建任务
          </span>
          <div className="task-create__header-actions">
            <Button size="small" type="text" onClick={resetTemplate}>重置为模板</Button>
            <Button size="small" onClick={onClose}>取消</Button>
            <Button size="small" type="primary" loading={submitAction.loading} onClick={handleSubmit}>创建</Button>
          </div>
        </div>
      }
      footer={null}
      closable={false}
      className="task-create task-create--builder"
      alignCenter={false}
      style={{ width: '100%', top: 0 }}
      // 编辑器内容宝贵，禁止点击遮罩误关
      maskClosable={false}
      escToExit={false}
    >
      <div className="task-create__body">
        {submitted && validation.errors.length > 0 ? (
          <div className="task-create__notice" role="alert">
            <Alert
              type="error"
              title="配置存在问题"
              closable
              onClose={() => setSubmitted(false)}
              content={<ul className="task-create__issues">
                {validation.errors.map((message, index) => <li key={index}>{message}</li>)}
              </ul>}
            />
          </div>
        ) : validation.warnings.length > 0 ? (
          <div className="task-create__notice" key={validation.warnings.join('\n')}>
            <Alert
              type="warning"
              title="请确认以下情况"
              closable
              content={<ul className="task-create__issues">
                {validation.warnings.map((message, index) => <li key={index}>{message}</li>)}
              </ul>}
            />
          </div>
        ) : null}

        <div className={editorExpanded ? 'task-create__split task-create__split--expanded' : 'task-create__split'}>
          <section className="task-create__pane" aria-label="可视化">
            <div className="task-create__pane-title">
              <strong>可视化</strong>
            </div>
            {importError && <Alert type="warning" title="画布暂停同步，请修正配置" content={importError} />}
            <div className="task-create__pane-content" inert={!!importError}>
              <TaskCreateForm draft={draft} onChange={editDraft} layoutVersion={layoutVersion} />
            </div>
          </section>
          <div className="task-create__divider">
            <button
              type="button"
              className="task-create__editor-toggle"
              aria-label={editorExpanded ? '折叠配置' : '展开配置'}
              title={editorExpanded ? '折叠配置' : '展开配置'}
              aria-controls={editorPaneId}
              aria-expanded={editorExpanded}
              onClick={() => setEditorExpanded((expanded) => !expanded)}
            >
              {editorExpanded ? <IconChevronRight size={14} /> : <IconChevronLeft size={14} />}
            </button>
          </div>
          <section id={editorPaneId} className="task-create__pane" aria-label="任务配置" hidden={!editorExpanded}>
            <div className="task-create__pane-title">
              <Radio.Group type="button" size="small" aria-label="配置格式" value={format} onChange={switchFormat} disabled={!!importError}>
                <Radio value="yaml">YAML</Radio>
                <Radio value="json">JSON</Radio>
              </Radio.Group>
            </div>
            <div className="task-create__pane-content"><CodeEditor value={text} onChange={editText} language={format} /></div>
          </section>
        </div>
      </div>
    </Modal>
  )
}
