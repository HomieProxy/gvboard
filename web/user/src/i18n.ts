import { ref } from 'vue'
import { createI18n } from 'vue-i18n'
import { createStore } from '@gv/shared'
import zhCN from './locales/zh-CN'

export const locales = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁體中文' },
  { value: 'en-US', label: 'English' },
  { value: 'ja-JP', label: '日本語' },
  { value: 'ko-KR', label: '한국어' },
  { value: 'vi-VN', label: 'Tiếng Việt' },
  { value: 'fa-IR', label: 'فارسی' },
] as const

export type Locale = (typeof locales)[number]['value']
export type Messages = typeof zhCN

const prefs = createStore('gvu')

/** 当前语言是否从右往左书写（侧边栏随之放到右侧）。 */
export const rtl = ref(false)

export const i18n = createI18n({
  legacy: false,
  locale: 'zh-CN',
  fallbackLocale: 'zh-CN',
  messages: { 'zh-CN': zhCN } as Record<string, Messages>,
  missingWarn: false,
  fallbackWarn: false,
})

const loaders = import.meta.glob<{ default: Messages }>(['./locales/*.ts', '!./locales/zh-CN.ts'])

function detect(): Locale {
  const saved = prefs.get('locale')
  if (locales.some((l) => l.value === saved)) return saved as Locale
  for (const lang of navigator.languages || [navigator.language]) {
    const l = lang.toLowerCase()
    if (l.startsWith('zh')) return l.includes('tw') || l.includes('hk') || l.includes('hant') ? 'zh-TW' : 'zh-CN'
    const hit = locales.find((x) => x.value.slice(0, 2) === l.slice(0, 2))
    if (hit) return hit.value
  }
  return 'zh-CN'
}

export async function setLocale(l: Locale, save = true) {
  if (!i18n.global.availableLocales.includes(l)) {
    const load = loaders[`./locales/${l}.ts`]
    if (load) i18n.global.setLocaleMessage(l, (await load()).default)
  }
  i18n.global.locale.value = l
  document.documentElement.lang = l
  rtl.value = l === 'fa-IR'
  document.documentElement.dir = rtl.value ? 'rtl' : 'ltr'
  if (save) prefs.set('locale', l)
}

export function initLocale() {
  return setLocale(detect(), false)
}
