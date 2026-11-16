<script setup lang="ts">
import { computed, onMounted, ref, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CalendarCheckIcon, CoinsIcon, GiftIcon, MegaphoneIcon, PuzzleIcon, SparklesIcon, StarIcon, TicketIcon, TrophyIcon,
} from '@lucide/vue'
import { Card, CardContent } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { http } from '../api'
import { confirm, errText, toast } from '../ui'
import { loadMe } from '../store'
import type { PluginWidget, PluginWidgetState } from '../types'

const props = defineProps<{ position: string }>()
const { t } = useI18n()

const icons: Record<string, Component> = {
  puzzle: PuzzleIcon,
  gift: GiftIcon,
  'calendar-check': CalendarCheckIcon,
  star: StarIcon,
  trophy: TrophyIcon,
  ticket: TicketIcon,
  sparkles: SparklesIcon,
  coins: CoinsIcon,
  megaphone: MegaphoneIcon,
}
// 插件只能请求自己路由前缀下的路径
const pathPattern = /^\/[A-Za-z0-9_/-]{0,127}$/

interface Item {
  w: PluginWidget
  id: string
  state: PluginWidgetState | null
  loading: boolean
  failed: boolean
  busy: string
}

const items = ref<Item[]>([])
const visible = computed(() => items.value.filter((i) => !i.failed && !i.state?.hidden))

const clip = (s: unknown, n: number) => (typeof s === 'string' ? [...s].slice(0, n).join('') : '')
const base = (w: PluginWidget) => `/plugin/${encodeURIComponent(w.plugin)}`

function normalize(s: PluginWidgetState | null): PluginWidgetState {
  const v = s && typeof s === 'object' ? s : {}
  return {
    hidden: v.hidden === true,
    text: clip(v.text, 200),
    value: clip(v.value, 32),
    actions: (Array.isArray(v.actions) ? v.actions : [])
      .filter((a) => a && typeof a.path === 'string' && pathPattern.test(a.path))
      .slice(0, 2)
      .map((a) => ({ label: clip(a.label, 16) || t('common.confirm'), path: a.path, disabled: a.disabled === true, confirm: clip(a.confirm, 200) })),
  }
}

async function refresh(it: Item) {
  it.loading = true
  try {
    it.state = normalize(await http.get<PluginWidgetState>(base(it.w) + it.w.state))
    it.failed = false
  } catch {
    it.failed = true
  } finally {
    it.loading = false
  }
}

async function act(it: Item, a: NonNullable<PluginWidgetState['actions']>[number]) {
  const run = async () => {
    const r = await http.post<{ message?: unknown } | null>(base(it.w) + a.path)
    const msg = r && typeof r === 'object' ? clip(r.message, 200) : ''
    toast('success', msg || t('common.success'))
    await Promise.all([refresh(it), loadMe().catch(() => {})])
  }
  if (a.confirm) {
    await confirm(a.confirm, run)
    return
  }
  it.busy = a.path
  try {
    await run()
  } catch (e) {
    toast('error', errText(e))
  } finally {
    it.busy = ''
  }
}

onMounted(async () => {
  let list: PluginWidget[] = []
  try {
    list = await http.get<PluginWidget[]>('/user/plugin-widgets', { slot: props.position })
  } catch {
    return
  }
  items.value = (list || [])
    .filter((w) => pathPattern.test(w.state))
    .map((w) => ({ w, id: `${w.plugin}:${w.key}`, state: null, loading: true, failed: false, busy: '' }))
  await Promise.all(items.value.map(refresh))
})
</script>

<template>
  <section v-if="visible.length">
    <h2 class="mb-3 text-base font-semibold">{{ t('dashboard.activities') }}</h2>
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      <Card v-for="it in visible" :key="it.id" class="gap-0 py-4">
        <CardContent class="flex items-start gap-4 px-4">
          <div class="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <component :is="icons[it.w.icon] || PuzzleIcon" class="size-4.5" />
          </div>
          <div class="min-w-0 flex-1 space-y-1">
            <div class="flex items-baseline justify-between gap-2">
              <span class="truncate font-medium">{{ it.w.title }}</span>
              <span v-if="it.state?.value" class="shrink-0 font-semibold text-primary tabular-nums">{{ it.state.value }}</span>
            </div>
            <Skeleton v-if="it.loading && !it.state" class="h-4 w-3/4" />
            <p v-else-if="it.state?.text" class="text-sm break-words text-muted-foreground">{{ it.state.text }}</p>
            <div v-if="it.state?.actions?.length" class="flex flex-wrap gap-2 pt-2">
              <Button
                v-for="(a, i) in it.state.actions"
                :key="a.path + i"
                size="sm"
                :variant="i === 0 ? 'default' : 'outline'"
                :disabled="a.disabled || !!it.busy || it.loading"
                @click="act(it, a)"
              >
                {{ a.label }}
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  </section>
</template>
