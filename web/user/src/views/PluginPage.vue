<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, reactive, ref, type Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { CopyIcon, EyeIcon, EyeOffIcon, InfoIcon, CircleCheckIcon, TriangleAlertIcon, CircleAlertIcon } from '@lucide/vue'
import Markdown from '@gv/shared/content/Markdown.vue'
import { Card, CardContent, CardHeader, CardTitle } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { http } from '../api'
import { confirm, copied, copy, errText, toast } from '../ui'
import { loadMe, loadPluginPages, pageTitle, pluginPages } from '../store'
import PageHeader from '../components/common/PageHeader.vue'
import type { PluginAction, PluginBlock, PluginPage } from '../types'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

/** 主题为插件页面提供的专门界面（{插件}/{页面}） */
const native: Record<string, Component> = {
  'lucky_wheel/wheel': defineAsyncComponent(() => import('./LuckyWheel.vue')),
}

const plugin = String(route.params.plugin)
const key = String(route.params.key)
const page = ref<PluginPage | null>(null)
const ready = ref(false)
const view = computed(() => native[`${plugin}/${key}`])

const blocks = ref<PluginBlock[]>([])
const loading = ref(false)
const failed = ref('')
const busy = ref('')
const shown = reactive(new Set<string>())

// 插件只能请求自己路由前缀下的路径、跳转到站内页面
const pathPattern = /^\/[A-Za-z0-9_/-]{0,127}$/
const valid = (p: unknown): p is string => typeof p === 'string' && pathPattern.test(p)
const clip = (s: unknown, n: number) => (typeof s === 'string' ? [...s].slice(0, n).join('') : '')
const base = `/plugin/${encodeURIComponent(plugin)}`
const tones = ['info', 'success', 'warning', 'danger'] as const

function actions(list: unknown, max: number): PluginAction[] {
  return (Array.isArray(list) ? list : [])
    .filter((a) => a && (a.link ? valid(a.link) : valid(a.path)))
    .slice(0, max)
    .map((a) => ({
      label: clip(a.label, 16) || t('common.confirm'),
      path: a.link ? undefined : a.path,
      link: a.link || undefined,
      disabled: a.disabled === true,
      confirm: clip(a.confirm, 200),
    }))
}

function normalize(b: PluginBlock): PluginBlock | null {
  if (!b || typeof b !== 'object') return null
  const title = clip(b.title, 32)
  switch (b.type) {
    case 'markdown':
      return { type: 'markdown', title, text: clip(b.text, 20000) }
    case 'alert':
      return { type: 'alert', title, text: clip(b.text, 500), tone: tones.includes(b.tone as never) ? b.tone : 'info' }
    case 'fields':
      return {
        type: 'fields',
        title,
        fields: (Array.isArray(b.fields) ? b.fields : []).slice(0, 30).map((f) => ({
          label: clip(f?.label, 32),
          value: clip(f?.value, 500),
          copy: f?.copy === true,
          secret: f?.secret === true,
        })),
      }
    case 'table': {
      const columns = (Array.isArray(b.columns) ? b.columns : []).slice(0, 10).map((c) => clip(c, 32))
      const rows = (Array.isArray(b.rows) ? b.rows : [])
        .slice(0, 200)
        .map((r) => columns.map((_, i) => clip(Array.isArray(r) ? r[i] : '', 200)))
      return columns.length ? { type: 'table', title, columns, rows } : null
    }
    case 'actions':
      return { type: 'actions', title, actions: actions(b.actions, 4) }
  }
  return null
}

async function load() {
  if (!page.value || view.value) return
  loading.value = true
  try {
    const r = await http.get<{ blocks?: PluginBlock[] }>(base + page.value.content)
    blocks.value = (Array.isArray(r?.blocks) ? r.blocks : [])
      .slice(0, 20)
      .map(normalize)
      .filter((b): b is PluginBlock => !!b)
    failed.value = ''
  } catch (e) {
    failed.value = errText(e)
  } finally {
    loading.value = false
  }
}

async function act(a: PluginAction) {
  if (a.link) {
    router.push(a.link)
    return
  }
  const run = async () => {
    const r = await http.post<{ message?: unknown } | null>(base + a.path)
    const msg = r && typeof r === 'object' ? clip(r.message, 200) : ''
    toast('success', msg || t('common.success'))
    await Promise.all([load(), loadMe().catch(() => {})])
  }
  if (a.confirm) {
    await confirm(a.confirm, run)
    return
  }
  busy.value = a.path || ''
  try {
    await run()
  } catch (e) {
    toast('error', errText(e))
  } finally {
    busy.value = ''
  }
}

const toneClass: Record<string, string> = {
  info: 'border-sky-200 bg-sky-50 text-sky-800 dark:border-sky-900 dark:bg-sky-950/50 dark:text-sky-200',
  success: 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/50 dark:text-emerald-200',
  warning: 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/50 dark:text-amber-200',
  danger: 'border-red-200 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950/50 dark:text-red-200',
}
const toneIcon: Record<string, Component> = { info: InfoIcon, success: CircleCheckIcon, warning: TriangleAlertIcon, danger: CircleAlertIcon }

function toggle(id: string) {
  if (shown.has(id)) shown.delete(id)
  else shown.add(id)
}

onMounted(async () => {
  const list = pluginPages.value ?? (await loadPluginPages())
  page.value = list.find((p) => p.plugin === plugin && p.key === key) || null
  ready.value = true
  await load()
})
</script>

<template>
  <div>
    <div v-if="!ready" class="space-y-4">
      <Skeleton class="h-8 w-48" />
      <Skeleton class="h-40 w-full" />
    </div>
    <Card v-else-if="!page" class="py-10">
      <CardContent class="text-center text-muted-foreground">{{ t('pluginPage.notFound') }}</CardContent>
    </Card>
    <component :is="view" v-else-if="view" />
    <template v-else>
      <PageHeader :title="pageTitle(page)" />
      <div v-if="loading && !blocks.length" class="space-y-4">
        <Skeleton class="h-24 w-full" />
        <Skeleton class="h-40 w-full" />
      </div>
      <Card v-else-if="failed" class="py-10">
        <CardContent class="space-y-3 text-center text-muted-foreground">
          <p>{{ failed }}</p>
          <Button size="sm" variant="outline" @click="load">{{ t('common.refresh') }}</Button>
        </CardContent>
      </Card>
      <div v-else class="space-y-4">
        <template v-for="(b, bi) in blocks" :key="bi">
          <div v-if="b.type === 'alert'" class="flex gap-2.5 rounded-lg border px-4 py-3 text-sm" :class="toneClass[b.tone || 'info']">
            <component :is="toneIcon[b.tone || 'info']" class="mt-0.5 size-4 shrink-0" />
            <div class="min-w-0 space-y-0.5">
              <div v-if="b.title" class="font-medium">{{ b.title }}</div>
              <p class="break-words whitespace-pre-line">{{ b.text }}</p>
            </div>
          </div>

          <Card v-else-if="b.type === 'actions'" class="py-4">
            <CardContent class="flex flex-wrap items-center gap-2 px-4">
              <span v-if="b.title" class="me-2 text-sm font-medium">{{ b.title }}</span>
              <Button
                v-for="(a, i) in b.actions"
                :key="i"
                :variant="i === 0 ? 'default' : 'outline'"
                :disabled="a.disabled || !!busy || loading"
                @click="act(a)"
              >
                {{ a.label }}
              </Button>
            </CardContent>
          </Card>

          <Card v-else class="gap-3 py-4">
            <CardHeader v-if="b.title" class="px-4"><CardTitle class="text-base">{{ b.title }}</CardTitle></CardHeader>
            <CardContent class="px-4">
              <Markdown v-if="b.type === 'markdown'" :source="b.text" safe @copied="copied" />

              <dl v-else-if="b.type === 'fields'" class="divide-y rounded-lg border">
                <div v-for="(f, fi) in b.fields" :key="fi" class="flex flex-wrap items-center gap-x-4 gap-y-1 px-3 py-2.5 text-sm">
                  <dt class="w-32 shrink-0 text-muted-foreground">{{ f.label }}</dt>
                  <dd class="flex min-w-0 flex-1 items-center gap-1">
                    <span class="min-w-0 flex-1 font-medium break-all">{{ f.secret && !shown.has(`${bi}:${fi}`) ? '••••••••' : f.value }}</span>
                    <Button v-if="f.secret" variant="ghost" size="icon" class="size-7 shrink-0" :title="shown.has(`${bi}:${fi}`) ? t('pluginPage.hide') : t('pluginPage.show')" @click="toggle(`${bi}:${fi}`)">
                      <EyeOffIcon v-if="shown.has(`${bi}:${fi}`)" class="size-3.5" />
                      <EyeIcon v-else class="size-3.5" />
                    </Button>
                    <Button v-if="f.copy" variant="ghost" size="icon" class="size-7 shrink-0" :title="t('common.copy')" @click="copy(f.value)">
                      <CopyIcon class="size-3.5" />
                    </Button>
                  </dd>
                </div>
              </dl>

              <div v-else-if="b.type === 'table'" class="overflow-x-auto rounded-lg border">
                <table class="w-full text-sm">
                  <thead class="bg-muted/50 text-muted-foreground">
                    <tr>
                      <th v-for="(c, ci) in b.columns" :key="ci" class="px-3 py-2 text-start font-medium whitespace-nowrap">{{ c }}</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y">
                    <tr v-for="(r, ri) in b.rows" :key="ri">
                      <td v-for="(cell, ci) in r" :key="ci" class="px-3 py-2 break-words">{{ cell }}</td>
                    </tr>
                    <tr v-if="!b.rows?.length">
                      <td :colspan="b.columns?.length" class="px-3 py-6 text-center text-muted-foreground">{{ t('common.empty') }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </CardContent>
          </Card>
        </template>
      </div>
    </template>
  </div>
</template>
