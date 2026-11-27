/**
 * 日志查看器
 *
 * 用虚拟滚动渲染：一次执行可能产生上万行输出，全量挂 DOM 会让页面卡死。
 * @tanstack/react-virtual 只渲染视口内的行，滚动开销与总行数无关。
 *
 * 自动滚动的规则参考终端习惯：贴着底部时跟随新日志，用户手动上翻后
 * 停止跟随（否则无法阅读历史），回到底部再恢复。
 */

import { type JSX, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { Input } from '@arco-design/web-react'
import type { StepLogRes } from '@/types/api'
import { formatNanoClock } from '@/utils/format'
import './LogViewer.scss'

/** 单行高度，须与 LogViewer.scss 的 .log-viewer__row 一致 */
const ROW_HEIGHT = 20
/** 判定「贴底」的容差像素 */
const BOTTOM_THRESHOLD = 24

export interface LogViewerProps {
  lines: readonly StepLogRes[]
  /** 是否显示时间戳列 */
  showTimestamp?: boolean
  /** 是否显示行号列 */
  showLineNumber?: boolean
  /** 已达行数上限的提示 */
  truncated?: boolean
  /** 无日志时的占位文案 */
  emptyText?: string
}

export function LogViewer({
  lines,
  showTimestamp = true,
  showLineNumber = true,
  truncated = false,
  emptyText = '暂无输出',
}: LogViewerProps): JSX.Element {
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const [keyword, setKeyword] = useState('')
  /** 是否跟随新日志滚动 */
  const followRef = useRef(true)
  const [following, setFollowing] = useState(true)

  const filtered = useMemo(() => {
    const trimmed = keyword.trim()
    if (!trimmed) return lines
    const lower = trimmed.toLowerCase()
    return lines.filter((line) => line.content.toLowerCase().includes(lower))
  }, [lines, keyword])

  const virtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT,
    // 预渲染额外行，快速滚动时减少白屏
    overscan: 24,
  })

  /** 滚动到底部 */
  const scrollToBottom = useCallback(() => {
    const element = scrollRef.current
    if (!element) return
    element.scrollTop = element.scrollHeight
  }, [])

  // 新日志到达时，仅在跟随模式下滚到底部
  useEffect(() => {
    if (!followRef.current) return
    // 等虚拟列表完成尺寸计算再滚，否则滚不到真正的底部
    const frame = requestAnimationFrame(scrollToBottom)
    return () => cancelAnimationFrame(frame)
  }, [filtered.length, scrollToBottom])

  const handleScroll = useCallback(() => {
    const element = scrollRef.current
    if (!element) return
    const atBottom =
      element.scrollHeight - element.scrollTop - element.clientHeight <= BOTTOM_THRESHOLD
    if (atBottom !== followRef.current) {
      followRef.current = atBottom
      setFollowing(atBottom)
    }
  }, [])

  const resumeFollow = useCallback(() => {
    followRef.current = true
    setFollowing(true)
    scrollToBottom()
  }, [scrollToBottom])

  const virtualRows = virtualizer.getVirtualItems()

  return (
    <div className="log-viewer">
      <div className="log-viewer__bar">
        <Input.Search
          allowClear
          size="small"
          placeholder="过滤日志"
          value={keyword}
          onChange={setKeyword}
          className="log-viewer__search"
        />
        <div className="log-viewer__bar-meta">
          {keyword.trim() ? (
            <span className="log-viewer__count u-mono">
              {filtered.length} / {lines.length}
            </span>
          ) : (
            <span className="log-viewer__count u-mono">{lines.length} 行</span>
          )}
          {truncated ? (
            <span className="log-viewer__warn" title="已达显示上限，仅保留最近的日志">
              已截断
            </span>
          ) : null}
        </div>
      </div>

      <div className="log-viewer__body">
        {filtered.length === 0 ? (
          <div className="log-viewer__empty">{keyword.trim() ? '无匹配的日志行' : emptyText}</div>
        ) : (
          <div ref={scrollRef} className="log-viewer__scroll" onScroll={handleScroll}>
            <div className="log-viewer__spacer" style={{ height: virtualizer.getTotalSize() }}>
              {virtualRows.map((row) => {
                const line = filtered[row.index]
                if (!line) return null
                return (
                  <div
                    key={line.line}
                    className="log-viewer__row"
                    style={{ transform: `translateY(${row.start}px)` }}
                  >
                    {showLineNumber ? (
                      <span className="log-viewer__lineno u-mono">{line.line}</span>
                    ) : null}
                    {showTimestamp ? (
                      <span className="log-viewer__time u-mono">
                        {formatNanoClock(line.timestamp)}
                      </span>
                    ) : null}
                    <span className="log-viewer__text">{line.content}</span>
                  </div>
                )
              })}
            </div>
          </div>
        )}

        {/* 脱离底部时提供一键回到最新，避免手动长距离滚动 */}
        {!following && filtered.length > 0 ? (
          <button type="button" className="log-viewer__jump" onClick={resumeFollow}>
            回到最新
          </button>
        ) : null}
      </div>
    </div>
  )
}
