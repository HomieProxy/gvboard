import { reactive } from 'vue'
import { toast as sonner } from 'vue-sonner'
import { ApiError, copyText } from '@gv/shared'
import { i18n } from './i18n'

const t = (k: string) => i18n.global.t(k)

export function errText(e: unknown): string {
  if (e instanceof ApiError || e instanceof Error) return e.message
  return String(e)
}

export function toast(type: 'success' | 'error' | 'warning' | 'info', text: string) {
  sonner[type](text)
}

export async function copy(text: string) {
  copied(await copyText(text))
}

/** 复制结果提示（Markdown 内容中 data-copy 元素的 copied 事件也用它） */
export function copied(ok: boolean) {
  toast(ok ? 'success' : 'error', t(ok ? 'common.copied' : 'common.copyFailed'))
}

export async function run(fn: () => Promise<unknown>, ok?: string): Promise<boolean> {
  try {
    await fn()
    if (ok) toast('success', ok)
    return true
  } catch (e) {
    toast('error', errText(e))
    return false
  }
}

/** 全局确认框状态，由 App.vue 中的 ConfirmHost 渲染。 */
export const confirmState = reactive({
  open: false,
  content: '',
  danger: false,
  busy: false,
  resolve: (_ok: boolean) => {},
  action: async () => {},
})

/** 二次确认后执行 fn；返回是否执行成功。 */
export function confirm(content: string, fn: () => Promise<unknown>, ok?: string, danger = false): Promise<boolean> {
  return new Promise((resolve) => {
    Object.assign(confirmState, {
      open: true,
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
