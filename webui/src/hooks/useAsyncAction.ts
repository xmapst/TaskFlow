/**
 * 异步操作统一收口
 *
 * 所有写操作（终止、挂起、删除、上传、导出）都经由此处，
 * 以保证三件事在全站表现一致：
 *  1. 执行期间按钮进入 loading，杜绝重复提交；
 *  2. 成败都有明确的用户反馈，不出现「点了没反应」；
 *  3. 破坏性操作先弹确认，文案取自 ACTION_META。
 */

import { useCallback, useState } from 'react'
import { Message, Modal } from '@arco-design/web-react'
import { isCanceled, toErrorMessage } from '@/api/http'

export interface AsyncActionOptions {
  /** 成功提示；留空则不提示 */
  successMessage?: string
  /** 失败提示前缀 */
  errorPrefix?: string
  /** 需要二次确认时的配置 */
  confirm?: {
    title: string
    content: string
    okText?: string
    danger?: boolean
  }
}

export interface AsyncActionResult<A extends unknown[]> {
  run: (...args: A) => Promise<boolean>
  loading: boolean
}

/**
 * 包装一个异步操作。
 * @returns run 返回是否成功，便于调用方决定后续动作（如关闭弹窗）
 */
export function useAsyncAction<A extends unknown[]>(
  action: (...args: A) => Promise<unknown>,
  options: AsyncActionOptions = {},
): AsyncActionResult<A> {
  const [loading, setLoading] = useState(false)

  const run = useCallback(
    async (...args: A): Promise<boolean> => {
      const { confirm, successMessage, errorPrefix } = options

      if (confirm) {
        const confirmed = await new Promise<boolean>((resolve) => {
          Modal.confirm({
            title: confirm.title,
            content: confirm.content,
            okText: confirm.okText ?? '确认',
            cancelText: '取消',
            okButtonProps: confirm.danger ? { type: 'primary', status: 'danger' } : {},
            onOk: () => resolve(true),
            onCancel: () => resolve(false),
          })
        })
        if (!confirmed) return false
      }

      setLoading(true)
      try {
        await action(...args)
        if (successMessage) Message.success(successMessage)
        return true
      } catch (error) {
        // 主动取消（组件卸载、参数变更）不打扰用户
        if (!isCanceled(error)) {
          const text = toErrorMessage(error, '操作失败')
          Message.error(errorPrefix ? `${errorPrefix}：${text}` : text)
        }
        return false
      } finally {
        setLoading(false)
      }
    },
    // options 为对象字面量，逐字段依赖以免每次渲染都重建 run
    [action, options],
  )

  return { run, loading }
}
