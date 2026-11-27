/**
 * CodeMirror 6 编辑器
 *
 * 两个设计要点：
 *  1. 主题、语言、只读三项用 Compartment 包裹，切换时通过 reconfigure
 *     局部替换扩展，而不是销毁重建编辑器 —— 重建会丢失光标、选区、
 *     撤销栈与滚动位置；
 *  2. 受控值的同步只在「外部值与编辑器当前内容不一致」时执行，
 *     否则用户每次输入都会触发一次 setValue，导致光标跳回开头。
 */

import { type JSX, useEffect, useRef } from 'react'
import { EditorState, Compartment, Annotation } from '@codemirror/state'
import {
  EditorView,
  keymap,
  lineNumbers,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  drawSelection,
  dropCursor,
  rectangularSelection,
  crosshairCursor,
} from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import {
  indentOnInput,
  bracketMatching,
  foldGutter,
  foldKeymap,
  syntaxHighlighting,
  defaultHighlightStyle,
  StreamLanguage,
} from '@codemirror/language'
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search'
import { closeBrackets, closeBracketsKeymap, autocompletion, completionKeymap } from '@codemirror/autocomplete'
import { lintKeymap } from '@codemirror/lint'
import { yaml } from '@codemirror/lang-yaml'
import { json } from '@codemirror/lang-json'
import { python } from '@codemirror/lang-python'
import { javascript } from '@codemirror/lang-javascript'
import { powerShell } from '@codemirror/legacy-modes/mode/powershell'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import { oneDarkHighlightStyle } from '@codemirror/theme-one-dark'
import { useThemeStore } from '@/store/theme'
import './CodeEditor.scss'

const externalUpdate = Annotation.define<boolean>()

export type EditorLanguage = 'yaml' | 'json' | 'python' | 'javascript' | 'shell' | 'powershell' | 'cmd' | 'text'

export interface CodeEditorProps {
  value: string
  onChange?: (value: string) => void
  language?: EditorLanguage
  readOnly?: boolean
  /** 高度，CSS 值。默认撑满父容器 */
  height?: string
  placeholder?: string
  className?: string
}

// CMD 批处理的基础词法高亮，不使用 Shell 的引号和变量规则。
const cmdLanguage = StreamLanguage.define({
  token(stream) {
    if (stream.eatSpace()) return null
    if (stream.match(/^(?:::.*|@?rem(?:\s.*|$))/i)) return 'comment'
    if (stream.match(/^"[^"]*"?/)) return 'string'
    if (stream.match(/^(?:%[^%\s]+%|%%?[A-Z]|%~?[0-9*]|![^!\s]+!)/i)) return 'variableName'
    if (stream.match(/^(?:echo|setlocal|endlocal|set|if|else|for|in|do|goto|call|exit|shift|pause|cd|pushd|popd)\b/i)) return 'keyword'
    if (stream.match(/^[&|<>^@=]/)) return 'operator'
    if (stream.match(/^\d+\b/)) return 'number'
    if (!stream.eatWhile(/[\w.-]/)) stream.next()
    return null
  },
})

function languageExtension(language: EditorLanguage) {
  switch (language) {
    case 'yaml':
      return yaml()
    case 'json':
      return json()
    case 'python':
      return python()
    case 'javascript':
      return javascript()
    case 'shell':
      return StreamLanguage.define(shell)
    case 'powershell':
      return StreamLanguage.define(powerShell)
    case 'cmd':
      return cmdLanguage
    case 'text':
    default:
      return []
  }
}

const languageConf = new Compartment()
const themeConf = new Compartment()
const readOnlyConf = new Compartment()

const darkEditorTheme = EditorView.theme({}, { dark: true })

/** 只切换语法配色和 CodeMirror 的明暗模式，背景由应用主题控制。 */
function themeExtension(mode: 'dark' | 'light') {
  return mode === 'dark'
    ? [darkEditorTheme, syntaxHighlighting(oneDarkHighlightStyle)]
    : syntaxHighlighting(defaultHighlightStyle, { fallback: true })
}

export function CodeEditor({
  value,
  onChange,
  language = 'yaml',
  readOnly = false,
  height,
  className,
}: CodeEditorProps): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const viewRef = useRef<EditorView | null>(null)
  /** 回调放 ref：避免每次父组件重渲染都重建编辑器 */
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange

  const themeMode = useThemeStore((state) => state.mode)

  // 仅在挂载时创建实例，后续变更全部通过 reconfigure 完成
  useEffect(() => {
    const host = hostRef.current
    if (!host) return

    const state = EditorState.create({
      doc: value,
      extensions: [
        lineNumbers(),
        highlightActiveLineGutter(),
        highlightSpecialChars(),
        history(),
        foldGutter(),
        drawSelection(),
        dropCursor(),
        EditorState.allowMultipleSelections.of(true),
        indentOnInput(),
        bracketMatching(),
        closeBrackets(),
        autocompletion(),
        rectangularSelection(),
        crosshairCursor(),
        highlightActiveLine(),
        highlightSelectionMatches(),
        keymap.of([
          ...closeBracketsKeymap,
          ...defaultKeymap,
          ...searchKeymap,
          ...historyKeymap,
          ...foldKeymap,
          ...completionKeymap,
          ...lintKeymap,
          indentWithTab,
        ]),
        // 长行自动换行：脚本内容常有超长命令
        EditorView.lineWrapping,
        languageConf.of(languageExtension(language)),
        themeConf.of(themeExtension(themeMode)),
        readOnlyConf.of([EditorView.editable.of(!readOnly), EditorState.readOnly.of(readOnly)]),
        EditorView.updateListener.of((update) => {
          if (!update.docChanged || update.transactions.every((tr) => tr.annotation(externalUpdate))) return
          onChangeRef.current?.(update.state.doc.toString())
        }),
      ],
    })

    const view = new EditorView({ state, parent: host })
    viewRef.current = view

    return () => {
      view.destroy()
      viewRef.current = null
    }
    // 挂载时建一次即可；语言/主题/只读的后续变更由下方 effect 处理。
    // value 不入依赖：它是受控值，由专门的同步 effect 负责。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 外部值变更时同步进编辑器，跳过由自身输入引发的回流
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const current = view.state.doc.toString()
    if (current === value) return
    view.dispatch({
      changes: { from: 0, to: view.state.doc.length, insert: value },
      annotations: externalUpdate.of(true),
    })
  }, [value])

  useEffect(() => {
    viewRef.current?.dispatch({
      effects: languageConf.reconfigure(languageExtension(language)),
    })
  }, [language])

  useEffect(() => {
    viewRef.current?.dispatch({
      effects: themeConf.reconfigure(themeExtension(themeMode)),
    })
  }, [themeMode])

  useEffect(() => {
    viewRef.current?.dispatch({
      effects: readOnlyConf.reconfigure([
        EditorView.editable.of(!readOnly),
        EditorState.readOnly.of(readOnly),
      ]),
    })
  }, [readOnly])

  return (
    <div
      ref={hostRef}
      className={`code-editor${className ? ` ${className}` : ''}`}
      style={height ? { height } : undefined}
    />
  )
}
