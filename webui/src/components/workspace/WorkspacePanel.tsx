/** 任务详情页中的工作区文件面板，文件通过 tus 上传。 */

import { type JSX, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Breadcrumb,
  Button,
  Modal,
  Progress,
  Table,
  Tooltip,
  type TableColumnProps,
} from '@arco-design/web-react'
import type { FileRes } from '@/types/api'
import { useWorkspace } from '@/hooks/useWorkspace'
import { useUploadQueue } from '@/hooks/useUploadQueue'
import { useAsyncAction } from '@/hooks/useAsyncAction'
import { deleteWorkspacePath, deleteWorkspacePaths, downloadWorkspaceFile } from '@/api/workspace'
import { formatBytes, formatNanoTime, formatSpeed, toBreadcrumbs } from '@/utils/format'
import { EmptyHint, LoadingBlock, Panel } from '@/components/common/Layout'
import {
  IconArrowUp,
  IconDownload,
  IconFile,
  IconFolder,
  IconRefresh,
  IconTrash,
  IconUpload,
} from '@/components/common/icons'
import { WorkspacePreview } from './WorkspacePreview'
import './WorkspacePanel.scss'

export interface WorkspacePanelProps {
  taskName: string
  workdir?: string | undefined
}

export function WorkspacePanel({ taskName, workdir }: WorkspacePanelProps): JSX.Element {
  const workspace = useWorkspace(taskName)
  const [queueVisible, setQueueVisible] = useState(false)
  const [previewFile, setPreviewFile] = useState<FileRes>()
  /** 已勾选条目的工作区相对路径 */
  const [selected, setSelected] = useState<string[]>([])
  const filePicker = useRef<HTMLInputElement>(null)

  const uploader = useUploadQueue(taskName, () => {
    // 上传完成后刷新列表，让新文件立即可见
    workspace.refresh()
  })

  const deleteAction = useAsyncAction(
    async (path: string) => {
      await deleteWorkspacePath(taskName, path)
      workspace.refresh()
    },
    { successMessage: '已删除', errorPrefix: '删除失败' },
  )

  const batchDeleteAction = useAsyncAction(
    async (paths: string[]) => {
      await deleteWorkspacePaths(taskName, paths)
      setSelected([])
      workspace.refresh()
    },
    {
      successMessage: '已删除选中项',
      errorPrefix: '删除失败',
      confirm: {
        title: '删除选中项',
        content: `将递归删除选中的 ${selected.length} 项及其内容，删除后无法恢复。`,
        okText: '删除',
        danger: true,
      },
    },
  )

  // 切换目录时清空勾选：列表整体换了一批条目，旧的选中项没有意义
  useEffect(() => {
    setSelected([])
  }, [workspace.path])

  // 列表刷新后丢弃已经不存在的路径，避免按钮上显示已删掉的条目
  useEffect(() => {
    setSelected((current) => {
      if (current.length === 0) return current
      const present = new Set(workspace.entries.map((entry) => entry.path))
      const next = current.filter((path) => present.has(path))
      return next.length === current.length ? current : next
    })
  }, [workspace.entries])

  const crumbs = useMemo(() => toBreadcrumbs(workspace.path), [workspace.path])

  const openRoot = useCallback(() => workspace.open(''), [workspace])

  const columns: TableColumnProps<FileRes>[] = useMemo(
    () => [
      {
        title: '名称',
        dataIndex: 'name',
        render: (_value, file) => (
          <div className="ws-name">
            <span className={`ws-name__icon${file.isDir ? ' ws-name__icon--dir' : ''}`}>
              {file.isDir ? <IconFolder size={15} /> : <IconFile size={15} />}
            </span>
            {file.isDir ? (
              <button
                type="button"
                className="ws-name__link"
                onClick={() => workspace.open(file.path)}
              >
                {file.name}
              </button>
            ) : (
              <button
                type="button"
                className="ws-name__link"
                title={`预览：${file.name}`}
                onClick={() => setPreviewFile(file)}
              >
                {file.name}
              </button>
            )}
          </div>
        ),
      },
      {
        title: '大小',
        dataIndex: 'size',
        width: 110,
        align: 'right',
        render: (_value, file) => (
          <span className="u-mono">{file.isDir ? '—' : formatBytes(file.size)}</span>
        ),
      },
      {
        title: '权限',
        dataIndex: 'mode',
        width: 130,
        render: (_value, file) => <span className="u-mono ws-mode">{file.mode}</span>,
      },
      {
        title: '修改时间',
        dataIndex: 'modTime',
        width: 180,
        render: (_value, file) => (
          <span className="u-mono">{formatNanoTime(file.modTime)}</span>
        ),
      },
      {
        title: '',
        width: 96,
        align: 'right',
        render: (_value, file) => (
          <div className="ws-actions">
            {!file.isDir ? (
              <Tooltip content="下载" mini>
                <Button
                  size="mini"
                  type="text"
                  icon={<IconDownload size={14} />}
                  onClick={() => downloadWorkspaceFile(taskName, file.path)}
                />
              </Tooltip>
            ) : null}
            <Tooltip content="删除" mini>
              <Button
                size="mini"
                type="text"
                status="danger"
                icon={<IconTrash size={14} />}
                onClick={() => void deleteAction.run(file.path)}
              />
            </Tooltip>
          </div>
        ),
      },
    ],
    [workspace, taskName, deleteAction],
  )

  const toolbar = (
    <div className="ws-toolbar">
      <div className="ws-toolbar__left">
        <Button
          size="small"
          type="secondary"
          icon={<IconArrowUp size={14} />}
          disabled={!workspace.path}
          onClick={workspace.up}
        >
          上一级
        </Button>
        <Button size="small" type="secondary" icon={<IconRefresh size={14} />} onClick={workspace.refresh}>
          刷新
        </Button>
        {selected.length > 0 ? (
          <Button
            size="small"
            type="secondary"
            status="danger"
            icon={<IconTrash size={14} />}
            loading={batchDeleteAction.loading}
            onClick={() => void batchDeleteAction.run(selected)}
          >
            删除选中 ({selected.length})
          </Button>
        ) : null}
      </div>

      <div className="ws-toolbar__right">
        <Button size="small" onClick={() => setQueueVisible(true)}>
          上传队列{uploader.stats.total > 0 ? ` (${uploader.stats.total})` : ''}
        </Button>
        <input
          ref={filePicker}
          type="file"
          multiple
          hidden
          onChange={({ currentTarget }) => {
            const files = Array.from(currentTarget.files ?? [])
            currentTarget.value = ''
            if (files.length === 0) return
            uploader.add(files)
            setQueueVisible(true)
          }}
        />
        <Button size="small" type="primary" icon={<IconUpload size={14} />} onClick={() => filePicker.current?.click()}>
          上传文件
        </Button>
      </div>
    </div>
  )

  return (
    <div className="ws-layout">
      <Panel
        title={
          <Breadcrumb className="ws-crumbs">
            <Breadcrumb.Item onClick={openRoot}>{workdir || '工作目录'}</Breadcrumb.Item>
            {crumbs.map((crumb) => (
              <Breadcrumb.Item key={crumb.path} onClick={() => workspace.open(crumb.path)}>
                {crumb.name}
              </Breadcrumb.Item>
            ))}
          </Breadcrumb>
        }
        actions={toolbar}
        extra={
          workspace.entries.length > 0 ? (
            <span className="u-mono ws-count">{workspace.entries.length} 项</span>
          ) : null
        }
        flush
        grow
      >
        {workspace.loading ? (
          <LoadingBlock tip="读取目录" />
        ) : workspace.absent ? (
          <EmptyHint
            description="工作目录尚未创建"
            hint="任务运行或首次上传文件时会创建工作目录。"
          />
        ) : workspace.error ? (
          <EmptyHint
            description="读取目录失败"
            hint={workspace.error}
            action={<Button type="primary" onClick={workspace.refresh}>重试</Button>}
          />
        ) : workspace.entries.length === 0 ? (
          <EmptyHint description="当前目录为空" hint="可通过上方按钮上传文件到该目录。" />
        ) : (
          <Table
            rowKey="path"
            data={workspace.entries}
            columns={columns}
            pagination={false}
            border={false}
            size="small"
            rowSelection={{
              type: 'checkbox',
              columnWidth: 40,
              selectedRowKeys: selected,
              onChange: (keys) => setSelected(keys.map(String)),
              // 表头的全选框只作用于当前目录的这一层，嵌套内容随所在目录一起删
              checkAll: true,
            }}
          />
        )}
      </Panel>

      {previewFile && (
        <WorkspacePreview key={`${taskName}:${previewFile.path}`} taskName={taskName} file={previewFile}
          onClose={() => setPreviewFile(undefined)} />
      )}

      <Modal
        title="上传队列"
        visible={queueVisible}
        onCancel={() => setQueueVisible(false)}
        footer={<Button onClick={() => setQueueVisible(false)}>关闭</Button>}
        className="ws-uploads"
        style={{ width: 'min(640px, calc(100vw - 24px))' }}
      >
        {uploader.items.length === 0 ? (
          <EmptyHint description="上传队列为空" hint="可在工作目录工具栏选择文件上传。" />
        ) : (
          <>
            <div className="ws-uploads__toolbar">
              <span className="ws-count u-mono">
                {uploader.stats.success}/{uploader.stats.total} 完成
              </span>
              <div className="ws-uploads__actions">
                <Button size="mini" type="text" onClick={uploader.startAll}>
                  全部继续
                </Button>
                <Button size="mini" type="text" onClick={uploader.pauseAll}>
                  全部暂停
                </Button>
                <Button size="mini" type="text" onClick={uploader.clearFinished}>
                  清理已完成
                </Button>
                <Button size="mini" type="text" status="danger" onClick={uploader.clear}>
                  清空
                </Button>
              </div>
            </div>
            <ul className="upload-list">
              {uploader.items.map((item) => (
                <li key={item.id} className={`upload-item upload-item--${item.status}`}>
                  <div className="upload-item__head">
                    <span className="upload-item__name u-truncate" title={item.file.name}>
                      {item.file.name}
                    </span>
                    <span className="upload-item__size u-mono">{formatBytes(item.file.size)}</span>
                  </div>

                  <Progress
                    percent={item.progress}
                    size="small"
                    showText={false}
                    status={
                      item.status === 'error'
                        ? 'error'
                        : item.status === 'success'
                          ? 'success'
                          : 'normal'
                    }
                  />

                  <div className="upload-item__foot">
                    <span className="upload-item__state">{uploadStatusText(item.status)}</span>
                    {item.status === 'uploading' && item.speed > 0 ? (
                      <span className="upload-item__speed u-mono">{formatSpeed(item.speed)}</span>
                    ) : null}
                    {item.error ? (
                      <Tooltip content={item.error}>
                        <span className="upload-item__error u-truncate">{item.error}</span>
                      </Tooltip>
                    ) : null}

                    <span className="upload-item__ops">
                      {item.status === 'uploading' ? (
                        <Button size="mini" type="text" onClick={() => uploader.pause(item.id)}>
                          暂停
                        </Button>
                      ) : null}
                      {item.status === 'paused' ? (
                        <Button size="mini" type="text" onClick={() => uploader.resume(item.id)}>
                          继续
                        </Button>
                      ) : null}
                      {item.status === 'error' ? (
                        <Button size="mini" type="text" onClick={() => uploader.start(item.id)}>
                          重试
                        </Button>
                      ) : null}
                      <Button
                        size="mini"
                        type="text"
                        status="danger"
                        onClick={() => uploader.remove(item.id)}
                      >
                        移除
                      </Button>
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          </>
        )}
      </Modal>
    </div>
  )
}

function uploadStatusText(status: string): string {
  const map: Record<string, string> = {
    waiting: '排队中',
    uploading: '上传中',
    paused: '已暂停',
    error: '失败',
    success: '已完成',
  }
  return map[status] ?? status
}
