import { createDiscreteApi, zhCN, dateZhCN, type GlobalThemeOverrides } from 'naive-ui'
import { ApiError } from '@gv/shared'

const primary: Record<string, string> = {
  default: '#2563eb',
  darkblue: '#1e3a8a',
  black: '#262626',
  green: '#16a34a',
}

export function themeOverrides(color: string): GlobalThemeOverrides {
  const c = primary[color] || primary.default
  return {
    common: { primaryColor: c, primaryColorHover: c, primaryColorPressed: c, primaryColorSuppl: c },
  }
}

export const locale = zhCN
export const dateLocale = dateZhCN

const api = createDiscreteApi(['message', 'dialog'], {
  configProviderProps: { locale: zhCN, dateLocale: dateZhCN },
})
export const message = api.message
export const dialog = api.dialog

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

/** 二次确认后执行。 */
export function confirm(content: string, fn: () => Promise<unknown>, ok = '操作成功'): Promise<boolean> {
  return new Promise((resolve) => {
    dialog.warning({
      title: '确认',
      content,
      positiveText: '确定',
      negativeText: '取消',
      onPositiveClick: async () => resolve(await run(fn, ok)),
      onNegativeClick: () => resolve(false),
      onClose: () => resolve(false),
    })
  })
}
