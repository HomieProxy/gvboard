<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { XIcon } from '@lucide/vue'
import { nowUnix } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { http, store } from '../api'
import { confirm, errText, toast } from '../ui'
import { loadMe, me } from '../store'
import { pluginIcon } from '../pluginIcons'
import type { PluginAction, PluginBanner, PluginBannerState } from '../types'

const { t } = useI18n()
const router = useRouter()

// 插件只能请求自己路由前缀下的路径、跳转到站内页面
const pathPattern = /^\/[A-Za-z0-9_/-]{0,127}$/
const valid = (p: unknown): p is string => typeof p === 'string' && pathPattern.test(p)
const tones = ['info', 'success', 'warning', 'danger'] as const
const dismissKey = 'plugin_banner_dismissed'

interface Item {
  b: PluginBanner
  id: string
  state: PluginBannerState | null
  busy: boolean
}

const items = ref<Item[]>([])
const now = ref(nowUnix())
const dismissed = ref<Record<string, string>>(readDismissed())
let timer: ReturnType<typeof setInterval> | undefined

const clip = (s: unknown, n: number) => (typeof s === 'string' ? [...s].slice(0, n).join('') : '')
const base = (b: PluginBanner) => `/plugin/${encodeURIComponent(b.plugin)}`
const dismissId = (it: Item) => `${me.value?.id ?? 0}:${it.id}`

function readDismissed(): Record<string, string> {
  try {
    const v = JSON.parse(store.get(dismissKey) || '{}')
    return v && typeof v === 'object' ? v : {}
  } catch {
    return {}
  }
}

const visible = computed(() =>
  items.value.filter((it) => {
    const s = it.state
    if (!s || s.hidden || !s.text) return false
    if (s.ends_at && s.ends_at <= now.value) return false
    return !(s.dismissible && dismissed.value[dismissId(it)] === s.text)
  }),
)

function normalize(s: PluginBannerState | null): PluginBannerState {
  const v = s && typeof s === 'object' ? s : {}
  const a = v.action && typeof v.action === 'object' && (v.action.link ? valid(v.action.link) : valid(v.action.path)) ? v.action : null
  return {
    hidden: v.hidden === true,
    text: clip(v.text, 200),
    tone: tones.includes(v.tone as never) ? v.tone : 'info',
    ends_at: typeof v.ends_at === 'number' && v.ends_at > 0 ? Math.floor(v.ends_at) : 0,
    dismissible: v.dismissible === true,
    action: a && {
      label: clip(a.label, 16) || t('common.confirm'),
      path: a.link ? undefined : a.path,
      link: a.link || undefined,
      disabled: a.disabled === true,
      confirm: clip(a.confirm, 200),
    },
  }
}

async function refresh(it: Item) {
  try {
    it.state = normalize(await http.get<PluginBannerState>(base(it.b) + it.b.state))
  } catch {
    it.state = null
  }
}

function left(endsAt: number) {
  const s = Math.max(0, endsAt - now.value)
  const d = Math.floor(s / 86400)
  const hms = [Math.floor((s % 86400) / 3600), Math.floor((s % 3600) / 60), s % 60].map((n) => String(n).padStart(2, '0')).join(':')
  return d > 0 ? `${t('dashboard.bannerDays', { n: d })} ${hms}` : hms
}

function dismiss(it: Item) {
  dismissed.value = { ...dismissed.value, [dismissId(it)]: it.state?.text || '' }
  store.set(dismissKey, JSON.stringify(dismissed.value))
}

async function act(it: Item, a: PluginAction) {
  if (a.link) {
    router.push(a.link)
    return
  }
  const run = async () => {
    const r = await http.post<{ message?: unknown } | null>(base(it.b) + a.path)
    const msg = r && typeof r === 'object' ? clip(r.message, 200) : ''
    toast('success', msg || t('common.success'))
    await Promise.all([refresh(it), loadMe().catch(() => {})])
  }
  if (a.confirm) {
    await confirm(a.confirm, run)
    return
  }
  it.busy = true
  try {
    await run()
  } catch (e) {
    toast('error', errText(e))
  } finally {
    it.busy = false
  }
}

const toneClass: Record<string, string> = {
  info: 'border-sky-300 bg-sky-50 text-sky-900 dark:border-sky-800 dark:bg-sky-950/60 dark:text-sky-100',
  success: 'border-emerald-300 bg-emerald-50 text-emerald-900 dark:border-emerald-800 dark:bg-emerald-950/60 dark:text-emerald-100',
  warning: 'border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-800 dark:bg-amber-950/60 dark:text-amber-100',
  danger: 'border-red-300 bg-red-50 text-red-900 dark:border-red-800 dark:bg-red-950/60 dark:text-red-100',
}
const iconClass: Record<string, string> = {
  info: 'bg-sky-500 text-white',
  success: 'bg-emerald-500 text-white',
  warning: 'bg-amber-500 text-white',
  danger: 'bg-red-500 text-white',
}

onMounted(async () => {
  let list: PluginBanner[] = []
  try {
    list = await http.get<PluginBanner[]>('/user/plugin-banners')
  } catch {
    return
  }
  items.value = (Array.isArray(list) ? list : [])
    .filter((b) => pathPattern.test(b.state))
    .map((b) => ({ b, id: `${b.plugin}:${b.key}`, state: null, busy: false }))
  await Promise.all(items.value.map(refresh))
  if (items.value.some((it) => it.state?.ends_at)) timer = setInterval(() => (now.value = nowUnix()), 1000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div v-if="visible.length" class="space-y-3">
    <div
      v-for="it in visible"
      :key="it.id"
      class="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-xl border-2 px-4 py-3 shadow-sm"
      :class="toneClass[it.state!.tone || 'info']"
    >
      <div class="flex size-9 shrink-0 items-center justify-center rounded-lg" :class="iconClass[it.state!.tone || 'info']">
        <component :is="pluginIcon(it.b.icon)" class="size-4.5" />
      </div>
      <div class="min-w-0 flex-1">
        <div v-if="it.b.title" class="font-semibold">{{ clip(it.b.title, 32) }}</div>
        <p class="text-sm break-words">{{ it.state!.text }}</p>
      </div>
      <div class="flex items-center gap-3">
        <div v-if="it.state!.ends_at" class="text-end">
          <div class="text-xs opacity-80">{{ t('dashboard.bannerLeft') }}</div>
          <div class="font-mono text-lg font-bold tabular-nums">{{ left(it.state!.ends_at) }}</div>
        </div>
        <Button v-if="it.state!.action" size="sm" :disabled="it.state!.action.disabled || it.busy" @click="act(it, it.state!.action)">
          {{ it.state!.action.label }}
        </Button>
        <button
          v-if="it.state!.dismissible"
          type="button"
          class="rounded-md p-1 opacity-70 transition-opacity hover:opacity-100"
          :aria-label="t('common.close')"
          @click="dismiss(it)"
        >
          <XIcon class="size-4" />
        </button>
      </div>
    </div>
  </div>
</template>
