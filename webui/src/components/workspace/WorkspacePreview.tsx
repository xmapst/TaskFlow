import { useEffect, useState } from 'react'
import { Button, Modal } from '@arco-design/web-react'
import { buildDownloadUrl, downloadWorkspaceFile } from '@/api/workspace'
import { EmptyHint, LoadingBlock } from '@/components/common/Layout'
import type { FileRes } from '@/types/api'

const previewLimit = 10 * 1024 * 1024
const textExtension = /\.(?:txt|log|md|csv|tsv|jsonl?|ya?ml|toml|ini|cfg|conf|xml|html?|svg|css|js|ts|sh|ps1|py|go|sql)$/i
const imageMime = /^image\/(?:png|jpeg|gif|webp|bmp|avif)$/

type Preview = { text: string } | { imageUrl: string } | { error: string }

export function WorkspacePreview({ taskName, file, onClose }: {
  taskName: string
  file: FileRes
  onClose: () => void
}) {
  const [preview, setPreview] = useState<Preview>()
  const url = buildDownloadUrl(taskName, file.path)

  useEffect(() => {
    const controller = new AbortController()
    let imageUrl: string | undefined
    async function load() {
      try {
        const tooLarge = '文件超过 10 MiB，请下载后查看。'
        if (file.size > previewLimit) throw new Error(tooLarge)
        const response = await fetch(url, { signal: controller.signal })
        // 目录和 API 错误也可能返回 HTTP 200，但只有文件响应带有附件头。
        if (!response.ok || !response.headers.has('Content-Disposition') || !response.body) {
          throw new Error('无法读取文件，文件可能已被移动或删除。')
        }
        const mime = (response.headers.get('Content-Type') ?? '').split(';')[0]!.trim().toLowerCase()
        const isImage = imageMime.test(mime)
        if (!isImage && !mime.startsWith('text/') && !textExtension.test(file.name)) {
          throw new Error('不支持预览此文件格式，请下载后查看。')
        }
        const reader = response.body.getReader()
        const chunks: Uint8Array<ArrayBuffer>[] = []
        let size = 0
        try {
          while (true) {
            const { value, done } = await reader.read()
            if (done) break
            size += value.byteLength
            if (size > previewLimit) throw new Error(tooLarge)
            chunks.push(new Uint8Array(value))
          }
        } finally {
          reader.releaseLock()
        }
        if (controller.signal.aborted) return
        const data = new Blob(chunks, { type: mime })
        if (isImage) {
          imageUrl = URL.createObjectURL(data)
          setPreview({ imageUrl })
        } else {
          try {
            const text = new TextDecoder('utf-8', { fatal: true }).decode(await data.arrayBuffer())
            if (!controller.signal.aborted) setPreview({ text })
          } catch {
            throw new Error('无法以 UTF-8 读取此文件，请下载后查看。')
          }
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setPreview({ error: error instanceof Error ? error.message : '无法读取文件。' })
          controller.abort()
        }
      }
    }
    void load()
    return () => {
      controller.abort()
      if (imageUrl) URL.revokeObjectURL(imageUrl)
    }
  }, [file.name, file.size, url])

  return (
    <Modal
      title={<span className="ws-preview__title" title={file.name}>预览：{file.name}</span>}
      visible
      onCancel={onClose}
      className="ws-preview"
      style={{ width: 'min(880px, calc(100vw - 24px))' }}
      footer={[
        <Button key="download" onClick={() => downloadWorkspaceFile(taskName, file.path)}>下载</Button>,
        <Button key="close" type="primary" onClick={onClose}>关闭</Button>,
      ]}
    >
      {!preview ? <LoadingBlock tip="读取文件" />
        : 'error' in preview ? <EmptyHint description="无法预览" hint={preview.error} />
          : 'imageUrl' in preview ? (
            <img className="ws-preview__image" src={preview.imageUrl} alt={file.name}
              onError={() => setPreview({ error: '无法显示图片，请下载后查看。' })} />
          ) : preview.text ? <pre className="ws-preview__text">{preview.text}</pre>
            : <EmptyHint description="文件为空" />}
    </Modal>
  )
}
