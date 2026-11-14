import { computed, ref, watchEffect } from 'vue'
import { cfg, store } from './api'

/** 主题色（对应主题配置中的 theme_color） */
const primary: Record<string, string> = {
  default: '#0665d0',
  green: '#319795',
  black: '#343a40',
  darkblue: '#3b5998',
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

/** 把外观与主题色同步到页面根节点 */
export function applyTheme() {
  watchEffect(() => {
    const root = document.documentElement
    root.classList.toggle('dark', isDark.value)
    const name = String(cfg.value.theme.config.theme_color || 'default')
    const c = primary[name] || (name.startsWith('#') ? name : primary.default)
    root.style.setProperty('--primary', c)
    root.style.setProperty('--ring', c)
    root.style.setProperty('--sidebar-primary', c)
    // 主题色均为深色，深色模式下前景色也必须保持白色
    root.style.setProperty('--primary-foreground', '#fff')
    root.style.setProperty('--sidebar-primary-foreground', '#fff')
  })
}

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
