import { type JSX, useEffect, useState } from 'react'
import { Alert, Button, Input, Modal } from '@arco-design/web-react'
import { toErrorMessage } from '@/api/http'
import { subscribeSystemInfo, updateWorkerPool, type SystemInfo } from '@/api/system'
import { formatBytes } from '@/utils/format'
import './SystemInfoDialog.scss'

function percent(value: number | null): string {
  return value === null ? '—' : `${value.toFixed(1)}%`
}

export function SystemInfoDialog({ onClose }: { onClose: () => void }): JSX.Element {
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const [draftSize, setDraftSize] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [loadError, setLoadError] = useState('')
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => subscribeSystemInfo((data) => {
    setInfo(data)
    setLoadError('')
  }, setLoadError), [])

  const target = draftSize ?? (info ? String(info.pool_size) : '')
  const size = Number(target.trim())
  const validSize = /^\d+$/.test(target.trim()) && size >= 1 && size <= 150
  const canSave = info !== null && !loadError && !saving && validSize && size !== info.pool_size

  async function save(): Promise<void> {
    if (!canSave) return
    setSaving(true)
    setSaveError('')
    setSaved(false)
    try {
      await updateWorkerPool(size)
      setDraftSize(null)
      setSaved(true)
    } catch (error) {
      setSaveError(`${toErrorMessage(error)}。请根据推送的当前大小确认结果。`)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      visible
      title="系统信息"
      className="system-info"
      style={{ width: 'min(760px, calc(100vw - 24px))' }}
      footer={null}
      onCancel={onClose}
      maskClosable={!saving}
      closable={!saving}
      escToExit={!saving}
    >
      <div className="system-info__body">
        <p>系统信息自动更新，— 表示不可用</p>
        {loadError && <Alert type="warning" title="系统信息暂不可用" content={loadError} />}
        {!loadError && !info && <p role="status">正在等待系统信息推送…</p>}
        {info && <>
          <section aria-labelledby="system-pool-title">
            <h2 id="system-pool-title">工作池</h2>
            <p>当前大小：<strong>{info.pool_size}</strong></p>
            <form className="system-info__pool" onSubmit={(event) => { event.preventDefault(); void save() }}>
              <label htmlFor="system-pool-size">目标大小</label>
              <Input
                id="system-pool-size"
                style={{ width: '8rem' }}
                inputMode="numeric"
                value={target}
                disabled={saving || !!loadError}
                error={target !== '' && !validSize}
                aria-describedby="system-pool-help"
                onChange={(value) => { setDraftSize(value); setSaved(false) }}
              />
              <Button htmlType="submit" type="primary" loading={saving} disabled={!canSave}>应用</Button>
            </form>
            <p id="system-pool-help">请输入 1–150 的整数。缩容可能需要等待正在运行的任务完成。</p>
            {saving && <p role="status">正在调整工作池，请等待服务端确认…</p>}
            {saved && <Alert type="success" content="工作池大小已调整" />}
            {saveError && <Alert type="error" title="未能确认调整成功" content={saveError} />}
          </section>
          <section aria-labelledby="system-resources-title">
            <h2 id="system-resources-title">资源使用</h2>
            <dl className="system-info__metrics">
              <div><dt>逻辑 CPU 核心</dt><dd>{info.cpu_cores ?? '—'}</dd></div>
              <div><dt>CPU 使用率</dt><dd>{percent(info.cpu_usage_percent)}</dd></div>
              <div><dt>内存总量</dt><dd>{formatBytes(info.memory_total_bytes)}</dd></div>
              <div><dt>内存使用率</dt><dd>{percent(info.memory_used_percent)}</dd></div>
            </dl>
          </section>
          <section aria-labelledby="system-disks-title">
            <h2 id="system-disks-title">已挂载磁盘</h2>
            {info.disks === null ? <p>磁盘信息不可用</p> : info.disks.length === 0 ? <p>无已挂载磁盘</p> : (
              <div className="system-info__disks">
                <table aria-labelledby="system-disks-title">
                  <thead><tr><th>挂载点</th><th>设备</th><th>文件系统</th><th>容量</th><th>已用</th><th>使用率</th></tr></thead>
                  <tbody>{info.disks.map((disk, index) => (
                    <tr key={index}>
                      <td>{disk.mountpoint}</td><td>{disk.device}</td><td>{disk.fs_type}</td>
                      <td>{formatBytes(disk.total_bytes)}</td><td>{formatBytes(disk.used_bytes)}</td>
                      <td>{percent(disk.used_percent)}</td>
                    </tr>
                  ))}</tbody>
                </table>
              </div>
            )}
          </section>
        </>}
      </div>
    </Modal>
  )
}
