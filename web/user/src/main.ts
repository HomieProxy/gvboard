import { createApp } from 'vue'
import { loadRawConfig } from '@gv/shared'
import App from './App.vue'
import { cfg, http, setToken, setLogoutHandler, type UserConfig } from './api'
import { i18n, initLocale } from './i18n'
import { injectCustomHTML } from './theme'
import { clearSession } from './store'
import { router } from './router'
import './style.css'

async function boot() {
  let raw: Record<string, any>
  try {
    raw = await loadRawConfig()
  } catch {
    document.body.textContent = 'Missing runtime config (config.js)'
    return
  }
  cfg.value = {
    ...cfg.value,
    ...raw,
    theme: { ...cfg.value.theme, ...(raw.theme || {}), config: { ...(raw.theme?.config || {}) } },
  } as UserConfig
  await initLocale()

  // 分离部署时页面没有注入站点配置，从接口获取
  if (!raw.site) {
    try {
      const site = await http.get<Pick<UserConfig, 'site' | 'register'>>('/site')
      cfg.value.site = site.site
      cfg.value.register = site.register
    } catch {
      /* 接口不可用时使用默认值 */
    }
  }
  if (cfg.value.site.name) document.title = cfg.value.site.name
  if (cfg.value.site.description) {
    let meta = document.querySelector('meta[name="description"]')
    if (!meta) {
      meta = document.createElement('meta')
      meta.setAttribute('name', 'description')
      document.head.appendChild(meta)
    }
    meta.setAttribute('content', cfg.value.site.description)
  }

  setLogoutHandler(() => {
    setToken('')
    clearSession()
    const cur = router.currentRoute.value
    if (!cur.meta.public) router.replace({ path: '/login', query: { redirect: cur.fullPath } })
  })

  createApp(App).use(i18n).use(router).mount('#app')
  injectCustomHTML(String(cfg.value.theme.config.custom_html || ''))
}

boot()
