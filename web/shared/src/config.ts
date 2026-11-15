// 运行配置：不分离部署时由服务器注入 window.APP_CONFIG；
// 分离部署时页面没有注入，从站点目录下的 config.js 读取（由部署者编写）。

export interface SiteInfo {
  name: string
  description: string
  url: string
  logo: string
  terms_url: string
  /** 访客访问根路径时是否显示首页 */
  home?: boolean
}

export interface RegisterInfo {
  closed: boolean
  email_code: boolean
  invite_required: boolean
  email_suffixes: string[]
  captcha_key: string
}

export interface BaseConfig {
  /** 原生 API 前缀，可以是完整地址（分离部署跨域时），例如 https://api.example.com/api/k3f9x2 */
  api: string
}

declare global {
  interface Window {
    APP_CONFIG?: Record<string, any>
  }
}

function loadScript(src: string): Promise<void> {
  return new Promise((resolve) => {
    const s = document.createElement('script')
    s.src = src
    s.onload = () => resolve()
    s.onerror = () => resolve()
    document.head.appendChild(s)
  })
}

/** 读取运行配置，必要时加载 config.js。返回原始对象，由各端自行取字段。 */
export async function loadRawConfig(): Promise<Record<string, any>> {
  if (!window.APP_CONFIG) {
    await loadScript(`config.js?t=${Date.now()}`)
  }
  const cfg = window.APP_CONFIG ?? {}
  if (typeof cfg.api !== 'string' || cfg.api === '') {
    throw new Error('missing api')
  }
  cfg.api = cfg.api.replace(/\/+$/, '')
  return cfg
}
