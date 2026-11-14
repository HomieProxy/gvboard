import { createHttp, createStore } from '@gv/shared'
import { ref } from 'vue'

export interface AdminConfig {
  api: string
  secure_path: string
  site: { name: string; logo: string }
  theme: { sidebar: string; header: string; color: string; background_url: string }
}

export const cfg = ref<AdminConfig>({
  api: '',
  secure_path: '',
  site: { name: '', logo: '' },
  theme: { sidebar: 'light', header: 'dark', color: 'default', background_url: '' },
})

export const store = createStore('gva')
export const token = ref(store.get('token'))

export function setToken(t: string) {
  token.value = t
  store.set('token', t)
}

let onLogout: () => void = () => {}
export function setLogoutHandler(f: () => void) {
  onLogout = f
}

const common = {
  token: () => token.value,
  locale: () => 'zh-CN',
  networkError: () => '网络错误或服务器无响应，请稍后再试',
}

/** 访客 / 用户接口（{api}），用于登录 */
export const http = createHttp({ ...common, base: () => cfg.value.api })

/** 管理端接口（{api}/{安全路径}）。非管理员或登录失效时返回 404 / 401，回到登录页。 */
export const admin = createHttp({
  ...common,
  base: () => `${cfg.value.api}/${cfg.value.secure_path}`,
  onUnauthorized: () => onLogout(),
  hiddenAsUnauthorized: true,
})
