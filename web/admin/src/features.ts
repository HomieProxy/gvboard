import { ref } from 'vue'
import { admin } from './api'
import type { PluginView } from './types'

// 由系统配置开关或插件启用状态控制显示的页面（路由 meta.feature）。
export const features = ref<Record<string, boolean>>({})

export async function loadFeatures() {
  const [cfg, plugins] = await Promise.allSettled([
    admin.get<Record<string, Record<string, unknown>>>('/config', { group: 'server' }),
    admin.get<PluginView[]>('/plugins'),
  ])
  const next = { ...features.value }
  if (cfg.status === 'fulfilled') next.node_machine = !!Number(cfg.value?.server?.node_machine_enable)
  if (plugins.status === 'fulfilled') next.lucky_wheel = !!plugins.value?.some((p) => p.code === 'lucky_wheel' && p.enabled)
  features.value = next
}
