<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowLeftIcon, CopyIcon, QrCodeIcon, DownloadIcon, ChevronDownIcon, RotateCcwIcon, RefreshCwIcon, TriangleAlertIcon } from '@lucide/vue'
import { daysLeft, formatBytes, formatTime } from '@gv/shared'
import { Card, CardContent, CardFooter, CardHeader, CardTitle, CardAction } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Switch } from '@gv/shared/ui/switch'
import { Label } from '@gv/shared/ui/label'
import { Progress } from '@gv/shared/ui/progress'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@gv/shared/ui/dropdown-menu'
import { cn } from '@gv/shared/lib/utils'
import { cfg, http } from '../api'
import { confirm, copy, errText, run, toast } from '../ui'
import { biz, clientOnly, loadBiz, subAvailable, subName } from '../store'
import type { ServerView, Subscription } from '../types'
import QrCode from '../components/QrCode.vue'
import ClientDownloads from '../components/ClientDownloads.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import DataTable from '../components/common/DataTable.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const id = Number(route.params.id)
const sub = ref<Subscription | null>(null)
const servers = ref<ServerView[]>([])
const loading = ref(true)
const serversLoading = ref(false)
const missing = ref(false)
const qr = ref(false)
const saving = ref(false)
const settings = ref({ name: '', auto_renewal: false, remind_expire: false, remind_traffic: false })

async function load() {
  try {
    const s = await http.get<Subscription>(`/user/subscriptions/${id}`)
    sub.value = s
    settings.value = { name: s.name, auto_renewal: s.auto_renewal, remind_expire: s.remind_expire, remind_traffic: s.remind_traffic }
  } catch (e) {
    missing.value = true
    toast('error', errText(e))
  }
}

async function loadServers() {
  serversLoading.value = true
  try {
    servers.value = (await http.get<ServerView[]>(`/user/subscriptions/${id}/servers`)) || []
  } catch {
    servers.value = []
  } finally {
    serversLoading.value = false
  }
}

onMounted(async () => {
  await Promise.all([load(), loadServers(), loadBiz().catch(() => {})])
  loading.value = false
})

const used = computed(() => (sub.value ? sub.value.u + sub.value.d : 0))
const pct = computed(() => (sub.value?.transfer_enable ? Math.min(100, Math.round((used.value / sub.value.transfer_enable) * 100)) : 0))
const exhausted = computed(() => !!sub.value && used.value >= sub.value.transfer_enable)
const barCls = computed(() =>
  pct.value >= 90 ? '[&>[data-slot=progress-indicator]]:bg-destructive' : pct.value >= 70 ? '[&>[data-slot=progress-indicator]]:bg-warning' : '',
)
const infos = computed(() => {
  const s = sub.value
  if (!s) return []
  const out = [
    { label: t('sub.expire'), value: !s.plan ? '-' : s.expired_at === null ? t('sub.forever') : daysLeft(s.expired_at) ? formatTime(s.expired_at) : t('sub.expired') },
    { label: t('sub.devices'), value: `${s.alive_ip} / ${s.device_limit ?? '∞'}` },
    { label: t('sub.speedLimit'), value: s.speed_limit ? `${s.speed_limit} Mbps` : t('common.unlimited') },
  ]
  if (s.reset_day !== null) out.push({ label: t('sub.resetLabel'), value: s.reset_day === 0 ? t('sub.resetToday') : t('sub.resetIn', { n: s.reset_day }) })
  return out
})

const siteName = computed(() => cfg.value.site.name || 'Subscription')
const imports = computed(() => {
  const url = sub.value?.subscribe_url || ''
  const u = encodeURIComponent(url)
  const n = encodeURIComponent(siteName.value)
  const b64 = btoa(unescape(encodeURIComponent(url))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  return [
    { label: 'Clash / Clash Verge / Mihomo', key: `clash://install-config?url=${u}&name=${n}` },
    { label: 'Stash', key: `stash://install-config?url=${u}&name=${n}` },
    { label: 'Shadowrocket', key: `shadowrocket://add/sub://${b64}?remark=${n}` },
    {
      label: 'Quantumult X',
      key: `quantumult-x:///update-configuration?remote-resource=${encodeURIComponent(JSON.stringify({ server_remote: [`${url}, tag=${siteName.value}`] }))}`,
    },
    { label: 'Surge', key: `surge:///install-config?url=${u}` },
    { label: 'Loon', key: `loon://import?sub=${u}` },
    { label: 'sing-box', key: `sing-box://import-remote-profile?url=${u}#${n}` },
    { label: 'Hiddify', key: `hiddify://import/${url}#${n}` },
    { label: 'v2rayNG', key: `v2rayng://install-sub?url=${u}&name=${n}` },
  ]
})
function openImport(link: string) {
  window.location.href = link
}

function resetSecurity() {
  confirm(
    t('sub.resetSecurityConfirm'),
    async () => {
      const url = await http.post<string>(`/user/subscriptions/${id}/reset-security`)
      if (sub.value) sub.value.subscribe_url = url
    },
    t('sub.resetSecuritySuccess'),
    true,
  )
}

function newPeriod() {
  const s = sub.value
  if (!s) return
  confirm(t('sub.newPeriodConfirm', { n: s.reset_day || 30 }), () => http.post(`/user/subscriptions/${id}/new-period`), t('sub.newPeriodSuccess')).then((ok) => {
    if (ok) load()
  })
}

async function saveSettings() {
  const v = settings.value
  saving.value = true
  const ok = await run(
    () =>
      http.put(`/user/subscriptions/${id}`, {
        name: v.name.trim(),
        auto_renewal: v.auto_renewal ? 1 : 0,
        remind_expire: v.remind_expire ? 1 : 0,
        remind_traffic: v.remind_traffic ? 1 : 0,
      }),
    t('common.saved'),
  )
  saving.value = false
  if (ok) load()
}

const serverCols = computed(() => [
  { key: 'name', title: t('sub.nodeName') },
  { key: 'is_online', title: t('common.status'), class: 'w-24' },
  { key: 'rate', title: t('sub.rate'), class: 'w-20' },
  { key: 'tags', title: t('sub.tags') },
])
</script>

<template>
  <div>
    <div v-if="loading" class="grid gap-6 xl:grid-cols-2">
      <Skeleton v-for="i in 4" :key="i" class="h-56 rounded-xl" />
    </div>
    <div v-else-if="missing" class="flex flex-col items-center gap-4 py-20 text-center">
      <p class="text-lg font-medium">{{ t('sub.notFound') }}</p>
      <Button variant="outline" @click="router.push('/subscriptions')"><ArrowLeftIcon />{{ t('common.back') }}</Button>
    </div>
    <template v-else-if="sub">
      <div class="mb-5 flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="sm" @click="router.push('/subscriptions')"><ArrowLeftIcon class="rtl:rotate-180" />{{ t('common.back') }}</Button>
        <h1 class="text-xl font-semibold tracking-tight">{{ subName(sub) }}</h1>
        <StatusBadge v-if="sub.is_primary" tone="primary">{{ t('sub.primary') }}</StatusBadge>
        <StatusBadge v-if="sub.banned" tone="danger" dot>{{ t('sub.banned') }}</StatusBadge>
        <StatusBadge v-else :tone="subAvailable(sub) ? 'success' : 'warning'" dot>
          {{ subAvailable(sub) ? t('sub.available') : t('sub.unavailable') }}
        </StatusBadge>
      </div>

      <div class="grid gap-6 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>{{ sub.plan?.name || t('sub.noPlan') }}</CardTitle>
          </CardHeader>
          <CardContent class="space-y-4">
            <div class="space-y-2">
              <div class="flex flex-wrap justify-between gap-2 text-sm">
                <span>{{ t('sub.used', { used: formatBytes(used), total: formatBytes(sub.transfer_enable) }) }}</span>
                <span class="text-muted-foreground">{{ t('sub.remaining', { n: formatBytes(Math.max(0, sub.transfer_enable - used)) }) }}</span>
              </div>
              <Progress :model-value="pct" :class="cn('h-2.5', barCls)" />
            </div>
            <dl class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
              <div v-for="i in infos" :key="i.label">
                <dt class="text-muted-foreground">{{ i.label }}</dt>
                <dd class="mt-0.5 font-medium">{{ i.value }}</dd>
              </div>
            </dl>
          </CardContent>
          <CardFooter class="flex-wrap gap-2">
            <Button v-if="sub.plan" @click="router.push({ path: `/plans/${sub.plan.id}`, query: { sub: sub.id } })">{{ t('sub.renew') }}</Button>
            <Button v-if="!sub.plan || biz?.plan_change !== false" variant="outline" @click="router.push({ path: '/plans', query: { sub: sub.id } })">
              {{ sub.plan ? t('sub.changePlan') : t('dashboard.buyNow') }}
            </Button>
            <Button
              v-if="sub.plan && exhausted"
              variant="outline"
              @click="router.push({ path: `/plans/${sub.plan.id}`, query: { sub: sub.id, period: 'reset_price' } })"
            >
              {{ t('sub.buyReset') }}
            </Button>
            <Button v-if="sub.allow_new_period && sub.reset_day !== null && exhausted" variant="secondary" @click="newPeriod">
              <RotateCcwIcon />{{ t('sub.newPeriod') }}
            </Button>
          </CardFooter>
        </Card>

        <Card v-if="clientOnly">
          <CardHeader>
            <CardTitle>{{ t('client.title') }}</CardTitle>
          </CardHeader>
          <CardContent class="space-y-4">
            <p class="text-sm text-muted-foreground">{{ t('client.tip') }}</p>
            <ClientDownloads :clients="biz?.clients || []" />
          </CardContent>
          <CardFooter>
            <Button variant="ghost" class="text-destructive hover:text-destructive" @click="resetSecurity">{{ t('sub.resetSecurity') }}</Button>
          </CardFooter>
        </Card>
        <Card v-else>
          <CardHeader>
            <CardTitle>{{ t('sub.link') }}</CardTitle>
          </CardHeader>
          <CardContent class="space-y-3">
            <Alert class="border-warning/50 bg-warning/10">
              <TriangleAlertIcon class="text-warning" />
              <AlertDescription class="text-foreground">{{ t('sub.linkWarning') }}</AlertDescription>
            </Alert>
            <div class="break-all rounded-md bg-muted px-3 py-2.5 font-mono text-xs">{{ sub.subscribe_url }}</div>
          </CardContent>
          <CardFooter class="flex-wrap gap-2">
            <Button @click="copy(sub.subscribe_url)"><CopyIcon />{{ t('sub.copyLink') }}</Button>
            <Button variant="outline" @click="qr = true"><QrCodeIcon />{{ t('sub.qrcode') }}</Button>
            <DropdownMenu>
              <DropdownMenuTrigger as-child>
                <Button variant="outline"><DownloadIcon />{{ t('sub.oneClick') }}<ChevronDownIcon /></Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start">
                <DropdownMenuItem v-for="i in imports" :key="i.label" @click="openImport(i.key)">{{ i.label }}</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <Button variant="ghost" class="text-destructive hover:text-destructive" @click="resetSecurity">{{ t('sub.resetSecurity') }}</Button>
          </CardFooter>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{{ t('sub.settings') }}</CardTitle>
          </CardHeader>
          <CardContent class="space-y-4">
            <div class="grid gap-1.5">
              <Label for="sub-name">{{ t('sub.name') }}</Label>
              <Input id="sub-name" v-model="settings.name" maxlength="64" :placeholder="t('sub.namePlaceholder')" />
            </div>
            <label class="flex items-center justify-between gap-4 text-sm">
              <span>{{ t('sub.autoRenewal') }}</span>
              <Switch v-model="settings.auto_renewal" />
            </label>
            <label class="flex items-center justify-between gap-4 text-sm">
              <span>{{ t('sub.remindExpire') }}</span>
              <Switch v-model="settings.remind_expire" />
            </label>
            <label class="flex items-center justify-between gap-4 text-sm">
              <span>{{ t('sub.remindTraffic') }}</span>
              <Switch v-model="settings.remind_traffic" />
            </label>
          </CardContent>
          <CardFooter>
            <Button :disabled="saving" @click="saveSettings">{{ t('common.save') }}</Button>
          </CardFooter>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{{ t('sub.nodes') }}</CardTitle>
            <CardAction>
              <Button variant="ghost" size="icon" class="size-8" :title="t('common.refresh')" @click="loadServers">
                <RefreshCwIcon :class="serversLoading ? 'animate-spin' : ''" />
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            <div class="max-h-96 overflow-y-auto">
              <DataTable :columns="serverCols" :rows="servers" :row-key="(s) => `${s.type}-${s.id}`" :empty="t('sub.noNodes')">
                <template #cell-is_online="{ row }">
                  <StatusBadge :tone="row.is_online ? 'success' : 'default'" dot>{{ row.is_online ? t('sub.online') : t('sub.offline') }}</StatusBadge>
                </template>
                <template #cell-rate="{ row }">{{ Number(row.rate) }}x</template>
                <template #cell-tags="{ row }">
                  <div class="flex flex-wrap gap-1">
                    <StatusBadge v-for="tg in row.tags || []" :key="tg">{{ tg }}</StatusBadge>
                  </div>
                </template>
              </DataTable>
            </div>
          </CardContent>
        </Card>
      </div>
    </template>

    <Dialog v-model:open="qr">
      <DialogContent class="sm:max-w-xs">
        <DialogHeader>
          <DialogTitle>{{ t('sub.qrcode') }}</DialogTitle>
          <DialogDescription>{{ t('sub.scanTip') }}</DialogDescription>
        </DialogHeader>
        <div class="flex justify-center"><QrCode :text="sub?.subscribe_url || ''" :size="240" /></div>
      </DialogContent>
    </Dialog>
  </div>
</template>
