import { useColorMode } from '@vueuse/core'

/** 管理端主题色（对应「个性化」中的主题色选项） */
const colors: Record<string, string> = {
  default: '#0665d0',
  darkblue: '#3b5998',
  black: '#343a40',
  green: '#319795',
}

export function applyThemeColor(name: string) {
  const c = colors[name] || (name?.startsWith('#') ? name : colors.default)
  const root = document.documentElement.style
  root.setProperty('--primary', c)
  root.setProperty('--ring', c)
  root.setProperty('--sidebar-primary', c)
  // 主题色均为深色，深色模式下前景色也必须保持白色
  root.setProperty('--primary-foreground', '#fff')
  root.setProperty('--sidebar-primary-foreground', '#fff')
}

export const colorMode = useColorMode({ storageKey: 'gv-admin-color-mode', initialValue: 'light' })
