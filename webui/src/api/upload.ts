/** tus 断点续传客户端。上传须提供目标任务名作为 `task_name` 元数据。 */

import * as tus from 'tus-js-client'
import { API } from '@/config/endpoints'

export type UploadStatus = 'waiting' | 'uploading' | 'paused' | 'error' | 'success'

export interface UploadTaskState {
  id: string
  file: File
  status: UploadStatus
  /** 0-100 */
  progress: number
  /** 字节每秒，仅上传中有意义 */
  speed: number
  /** 已上传字节 */
  uploaded: number
  /** 失败原因，无错误时为 undefined */
  error?: string | undefined
}

export interface UploadCallbacks {
  onStateChange: (state: UploadTaskState) => void
  onSuccess: (state: UploadTaskState) => void
  onError: (state: UploadTaskState, error: Error) => void
}

export interface UploaderConfig {
  /** 目标任务名，对应后端要求的 task_name 元数据 */
  taskName: string
  /** 分块大小（MB） */
  chunkSizeMB: number
  /** 单文件并行分块数 */
  parallelUploads: number
}

/**
 * 单个文件的上传控制器。
 * 封装 tus.Upload 的生命周期与速率统计，对外暴露 start/pause/resume/abort。
 */
export class UploadTask {
  private upload: tus.Upload | null = null
  private startedAt = 0
  private baselineBytes = 0

  readonly state: UploadTaskState

  constructor(
    id: string,
    file: File,
    private readonly config: UploaderConfig,
    private readonly callbacks: UploadCallbacks,
  ) {
    this.state = { id, file, status: 'waiting', progress: 0, speed: 0, uploaded: 0 }
  }

  private emit(patch: Partial<UploadTaskState>): void {
    Object.assign(this.state, patch)
    this.callbacks.onStateChange({ ...this.state })
  }

  private build(): tus.Upload {
    const relative = (this.file as File & { webkitRelativePath?: string }).webkitRelativePath

    return new tus.Upload(this.file, {
      endpoint: API.fileUpload(),
      chunkSize: this.config.chunkSizeMB * 1024 * 1024,
      parallelUploads: this.config.parallelUploads,
      retryDelays: [0, 1_000, 3_000, 5_000, 10_000],
      removeFingerprintOnSuccess: true,
      // 后端仅发布非 partial 的完整上传；创建时即发送数据可减少一次往返
      uploadDataDuringCreation: true,
      metadata: {
        // 目录上传时保留相对路径，与 multipart 通道行为一致
        filename: relative || this.file.name,
        filetype: this.file.type,
        // 后端强制要求：决定文件落到哪个任务的工作目录
        task_name: this.config.taskName,
      },
      // 分块并行上传时，每个分片的创建请求也需带上 task_name，
      // 否则 PreUploadCreateCallback 会拒绝分片
      metadataForPartialUploads: {
        task_name: this.config.taskName,
      },
      onProgress: (uploaded: number, total: number): void => {
        const elapsed = (Date.now() - this.startedAt) / 1000
        // 以本次启动为基准算速率，续传时不把历史字节计入
        const speed = elapsed > 0 ? (uploaded - this.baselineBytes) / elapsed : 0
        this.emit({
          progress: total > 0 ? Number(((uploaded / total) * 100).toFixed(1)) : 0,
          uploaded,
          speed: Math.max(0, speed),
        })
      },
      onSuccess: (): void => {
        this.emit({ status: 'success', progress: 100, speed: 0, uploaded: this.file.size })
        this.callbacks.onSuccess({ ...this.state })
      },
      onError: (error: Error): void => {
        this.emit({ status: 'error', speed: 0, error: error.message })
        this.callbacks.onError({ ...this.state }, error)
      },
    })
  }

  get file(): File {
    return this.state.file
  }

  get status(): UploadStatus {
    return this.state.status
  }

  /** 开始或重试上传，自动探测并续传此前的进度 */
  async start(): Promise<void> {
    if (this.state.status === 'uploading') return

    this.startedAt = Date.now()
    this.baselineBytes = this.state.uploaded
    this.emit({ status: 'uploading', error: undefined })

    const upload = this.build()
    this.upload = upload

    try {
      const previous = await upload.findPreviousUploads()
      const first = previous[0]
      if (first) upload.resumeFromPreviousUpload(first)
      upload.start()
    } catch (error) {
      const err = error instanceof Error ? error : new Error('上传启动失败')
      this.emit({ status: 'error', speed: 0, error: err.message })
      this.callbacks.onError({ ...this.state }, err)
    }
  }

  pause(): void {
    if (this.state.status !== 'uploading' || !this.upload) return
    void this.upload.abort()
    this.emit({ status: 'paused', speed: 0 })
  }

  resume(): void {
    if (this.state.status !== 'paused') return
    void this.start()
  }

  /** 终止并放弃该文件，同时清理 tus 的本地指纹记录 */
  abort(): void {
    if (this.upload) {
      void this.upload.abort(true)
      this.upload = null
    }
    this.emit({ status: 'waiting', progress: 0, speed: 0, uploaded: 0 })
  }
}
