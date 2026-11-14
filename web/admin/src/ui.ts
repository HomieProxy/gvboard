import { reactive } from 'vue'
import { toast } from 'vue-sonner'
import { ApiError } from '@gv/shared'

export const message = {
  success: (text: string) => toast.success(text),
  error: (text: string) => toast.error(text),
  warning: (text: string) => toast.warning(text),
  info: (text: string) => toast.info(text),
}

export function errText(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}

/** 执行请求，失败时提示错误，成功时可选提示；返回是否成功。 */
export async function run(fn: () => Promise<unknown>, ok?: string): Promise<boolean> {
  try {
    await fn()
    if (ok) message.success(ok)
    return true
  } catch (e) {
    message.error(errText(e))
    return false
  }
}

/** 全局确认框状态，由 App.vue 中的 ConfirmHost 渲染。 */
export const confirmState = reactive({
  open: false,
  title: '确认',
  content: '',
  danger: true,
  busy: false,
  resolve: (_ok: boolean) => {},
  action: async () => {},
})

/** 二次确认后执行 fn；返回是否执行成功。 */
export function confirm(content: string, fn: () => Promise<unknown>, ok = '操作成功', danger = true): Promise<boolean> {
  return new Promise((resolve) => {
    Object.assign(confirmState, {
      open: true,
      title: '确认',
      content,
      danger,
      busy: false,
      resolve,
      action: async () => {
        confirmState.busy = true
        const done = await run(fn, ok)
        confirmState.busy = false
        if (done) {
          confirmState.open = false
          resolve(true)
        }
      },
    })
  })
}

/** 仅询问，不执行请求；返回用户是否点了确定。 */
export function ask(title: string, content: string, danger = false): Promise<boolean> {
  return new Promise((resolve) => {
    Object.assign(confirmState, {
      open: true,
      title,
      content,
      danger,
      busy: false,
      resolve,
      action: async () => {
        confirmState.open = false
        resolve(true)
      },
    })
  })
}
