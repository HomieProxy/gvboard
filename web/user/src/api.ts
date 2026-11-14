import { createHttp, createStore, type SiteInfo, type RegisterInfo } from '@gv/shared'
import { ref } from 'vue'
import { i18n } from './i18n'

export interface ThemeConfig {
  theme_color?: string
  dark_mode?: string
  background_url?: string
  custom_html?: string
  [k: string]: unknown
}

export interface UserConfig {
  api: string
  site: SiteInfo
  register: RegisterInfo
  theme: { id: string; config: ThemeConfig }
}

export const cfg = ref<UserConfig>({
  api: '',
  site: { name: '', description: '', url: '', logo: '', terms_url: '' },
  register: { closed: false, email_code: false, invite_required: false, email_suffixes: [], captcha_key: '' },
  theme: { id: 'default', config: {} },
})

export const store = createStore('gvu')
export const token = ref(store.get('token'))

export function setToken(t: string) {
  token.value = t
  store.set('token', t)
}

let onLogout: () => void = () => {}
export function setLogoutHandler(f: () => void) {
  onLogout = f
}

export const http = createHttp({
  base: () => cfg.value.api,
  token: () => token.value,
  // 后端只翻译简体中文与英文，其它语言的错误信息使用英文（繁体中文使用简体）
  locale: () => (i18n.global.locale.value.startsWith('zh') ? 'zh-CN' : 'en-US'),
  onUnauthorized: () => onLogout(),
  networkError: () => i18n.global.t('common.networkError'),
})
