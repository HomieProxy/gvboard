import { createApp } from 'vue'
import { loadRawConfig } from '@gv/shared'
import App from './App.vue'
import { cfg, setToken, setLogoutHandler, type AdminConfig } from './api'
import { router } from './router'
import './style.css'

async function boot() {
  let raw: Record<string, any>
  try {
    raw = await loadRawConfig()
  } catch {
    document.body.textContent = '缺少运行配置（config.js）'
    return
  }
  cfg.value = {
    ...cfg.value,
    ...raw,
    site: { ...cfg.value.site, ...(raw.site || {}) },
    theme: { ...cfg.value.theme, ...(raw.theme || {}) },
  } as AdminConfig
  if (cfg.value.site.name) document.title = cfg.value.site.name

  setLogoutHandler(() => {
    setToken('')
    if (router.currentRoute.value.path !== '/login') {
      router.replace({ path: '/login', query: { redirect: router.currentRoute.value.fullPath } })
    }
  })
  createApp(App).use(router).mount('#app')
}

boot()
