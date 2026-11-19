import { ref } from 'vue'
import { admin } from './api'

// 由系统配置开关控制显示的页面（路由 meta.feature）。
export const features = ref<Record<string, boolean>>({})

export async function loadFeatures() {
  try {
    const r = await admin.get<Record<string, Record<string, unknown>>>('/config', { group: 'server' })
    features.value = { node_machine: !!Number(r?.server?.node_machine_enable) }
  } catch {
    // 保留现有状态
  }
}
