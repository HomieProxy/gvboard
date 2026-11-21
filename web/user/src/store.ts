import { computed, ref } from 'vue'
import { formatMoney, nowUnix } from '@gv/shared'
import { http } from './api'
import { i18n } from './i18n'
import type { BizConfig, PluginPage, Subscription, UserInfo, UserStat } from './types'

// 登录后共享的数据：用户信息、业务设置、订阅列表。

export const me = ref<UserInfo | null>(null)
export const biz = ref<BizConfig | null>(null)
export const stat = ref<UserStat | null>(null)
export const subs = ref<Subscription[]>([])

export async function loadMe() {
  me.value = await http.get<UserInfo>('/user/info')
  return me.value
}

export async function loadBiz() {
  if (!biz.value) biz.value = await http.get<BizConfig>('/user/config')
  return biz.value
}

/** 只允许使用客户端（决策 D67）；设置未加载时按开启处理，避免订阅链接闪现 */
export const clientOnly = computed(() => biz.value?.client_only ?? true)

export async function loadStat() {
  stat.value = await http.get<UserStat>('/user/stat')
  return stat.value
}

export async function loadSubs() {
  subs.value = (await http.get<Subscription[]>('/user/subscriptions')) || []
  return subs.value
}

/** 已启用插件的用户端页面；null 表示尚未加载 */
export const pluginPages = ref<PluginPage[] | null>(null)
const pagePattern = /^\/[A-Za-z0-9_/-]{0,127}$/
const codePattern = /^[a-z][a-z0-9_-]{1,31}$/
const keyPattern = /^[a-z][a-z0-9_]{0,31}$/

export async function loadPluginPages() {
  try {
    const list = await http.get<PluginPage[]>('/user/plugin-pages')
    pluginPages.value = (Array.isArray(list) ? list : []).filter(
      (p) => codePattern.test(p.plugin) && keyPattern.test(p.key) && pagePattern.test(p.content) && typeof p.title === 'string',
    )
  } catch {
    pluginPages.value = []
  }
  return pluginPages.value
}

/** 插件页面在当前语言下的标题 */
export function pageTitle(p: PluginPage): string {
  const s = p.titles?.[String(i18n.global.locale.value)] || p.title
  return [...s].slice(0, 24).join('')
}

export function clearSession() {
  me.value = null
  stat.value = null
  subs.value = []
  pluginPages.value = null
}

/** 金额（分）加货币符号 */
export function money(cents: number | null | undefined): string {
  return `${biz.value?.currency_symbol ?? '¥'}${formatMoney(cents)}`
}

export function subName(s: Pick<Subscription, 'id' | 'name'>): string {
  return s.name || i18n.global.t('sub.unnamed', { id: s.id })
}

/** 与后端一致的“可用”判断 */
export function subAvailable(s: Subscription): boolean {
  return !s.banned && s.transfer_enable !== 0 && (s.expired_at === null || s.expired_at > nowUnix())
}

export function subOptions(list: Subscription[] = subs.value) {
  return list.map((s) => ({
    label: `${subName(s)}${s.plan ? ` · ${s.plan.name}` : ''}${s.is_primary ? ` (${i18n.global.t('sub.primary')})` : ''}`,
    value: s.id,
  }))
}
