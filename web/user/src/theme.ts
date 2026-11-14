import { computed, ref } from 'vue'
import {
  darkTheme, zhCN, zhTW, enUS, jaJP, koKR, viVN, faIR,
  dateZhCN, dateZhTW, dateEnUS, dateJaJP, dateKoKR, dateViVN, dateFaIR,
  type GlobalThemeOverrides,
} from 'naive-ui'
import { cfg, store } from './api'
import { i18n } from './i18n'

const primary: Record<string, string> = {
  default: '#2563eb',
  green: '#10b981',
  black: '#404040',
  darkblue: '#1e40af',
}

const media = window.matchMedia('(prefers-color-scheme: dark)')
const systemDark = ref(media.matches)
media.addEventListener?.('change', (e) => (systemDark.value = e.matches))

/** 用户手动选择的外观：auto / light / dark，未选择时取主题配置 */
export const appearance = ref(store.get('appearance') || '')

export function setAppearance(v: string) {
  appearance.value = v
  store.set('appearance', v)
}

export const isDark = computed(() => {
  const mode = appearance.value || String(cfg.value.theme.config.dark_mode || 'auto')
  return mode === 'dark' || (mode === 'auto' && systemDark.value)
})

export const naiveTheme = computed(() => (isDark.value ? darkTheme : null))

export const overrides = computed<GlobalThemeOverrides>(() => {
  const c = primary[String(cfg.value.theme.config.theme_color || 'default')] || primary.default
  return {
    common: { primaryColor: c, primaryColorHover: c, primaryColorPressed: c, primaryColorSuppl: c, borderRadius: '6px' },
  }
})

const naiveLocales: Record<string, [typeof zhCN, typeof dateZhCN]> = {
  'zh-CN': [zhCN, dateZhCN],
  'zh-TW': [zhTW, dateZhTW],
  'en-US': [enUS, dateEnUS],
  'ja-JP': [jaJP, dateJaJP],
  'ko-KR': [koKR, dateKoKR],
  'vi-VN': [viVN, dateViVN],
  'fa-IR': [faIR, dateFaIR],
}

export const naiveLocale = computed(() => (naiveLocales[i18n.global.locale.value] || naiveLocales['zh-CN'])[0])
export const naiveDateLocale = computed(() => (naiveLocales[i18n.global.locale.value] || naiveLocales['zh-CN'])[1])

/** 插入管理员配置的自定义 HTML（客服脚本等），其中的脚本需要重新创建才会执行。 */
export function injectCustomHTML(html: string) {
  if (!html) return
  const box = document.createElement('div')
  box.innerHTML = html
  for (const old of Array.from(box.querySelectorAll('script'))) {
    const s = document.createElement('script')
    for (const a of Array.from(old.attributes)) s.setAttribute(a.name, a.value)
    s.text = old.text
    old.replaceWith(s)
  }
  document.body.appendChild(box)
}
