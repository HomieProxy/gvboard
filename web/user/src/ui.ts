import type { MessageApi, DialogApi, LoadingBarApi } from 'naive-ui'
import { ApiError, copyText } from '@gv/shared'
import { i18n } from './i18n'

// 由 UiBridge 组件在 Provider 内注册，使提示框跟随当前主题与语言。
export const ui: { message?: MessageApi; dialog?: DialogApi; loading?: LoadingBarApi } = {}

const t = (k: string) => i18n.global.t(k)

export function errText(e: unknown): string {
  if (e instanceof ApiError || e instanceof Error) return e.message
  return String(e)
}

export function toast(type: 'success' | 'error' | 'warning' | 'info', text: string) {
  ui.message?.[type](text)
}

export async function copy(text: string) {
  const ok = await copyText(text)
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

export function confirm(content: string, fn: () => Promise<unknown>, ok?: string): Promise<boolean> {
  return new Promise((resolve) => {
    if (!ui.dialog) return resolve(false)
    ui.dialog.warning({
      title: t('common.confirm'),
      content,
      positiveText: t('common.ok'),
      negativeText: t('common.cancel'),
      onPositiveClick: async () => resolve(await run(fn, ok)),
      onNegativeClick: () => resolve(false),
      onClose: () => resolve(false),
    })
  })
}
