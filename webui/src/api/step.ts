/**
 * 步骤接口
 *
 * 列表、详情与日志都是 /api/v1/ws 上的订阅（steps / step / step-log 主题），
 * 这里只剩管理动作，它是一次性调用。
 */

import type { ManageAction } from '@/types/api'
import { stream } from './socket'

/**
 * 管理步骤：终止 / 挂起 / 恢复。
 *
 * 走实时连接的 step.manage 调用。后端先校验所属任务处于
 * running/pending/paused，再校验步骤本身，任一不满足即报错，
 * 因此按钮可用性需同时考虑任务与步骤状态。
 */
export function manageStep(
  taskName: string,
  stepName: string,
  action: ManageAction,
  duration?: string,
): Promise<void> {
  return stream().call<void>('step.manage', {
    task: taskName,
    step: stepName,
    action,
    duration: duration ?? '',
  })
}
