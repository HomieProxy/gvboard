<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  NCard, NGrid, NGi, NButton, NSpace, NInput, NSwitch, NForm, NFormItem, NTag, NAlert, NDropdown, NModal, NSpin, NDataTable,
  NProgress, NDescriptions, NDescriptionsItem, NResult, type DataTableColumns,
} from 'naive-ui'
import { daysLeft, formatBytes, formatTime } from '@gv/shared'
import { cfg, http } from '../api'
import { confirm, copy, errText, run, toast } from '../ui'
import { biz, loadBiz, subAvailable, subName } from '../store'
import type { ServerView, Subscription } from '../types'
import QrCode from '../components/QrCode.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const id = Number(route.params.id)
const sub = ref<Subscription | null>(null)
const servers = ref<ServerView[]>([])
const loading = ref(true)
const missing = ref(false)
const qr = ref(false)
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
  try {
    servers.value = (await http.get<ServerView[]>(`/user/subscriptions/${id}/servers`)) || []
  } catch {
    servers.value = []
  }
}

onMounted(async () => {
  await Promise.all([load(), loadServers(), loadBiz().catch(() => {})])
  loading.value = false
})

const used = computed(() => (sub.value ? sub.value.u + sub.value.d : 0))
const pct = computed(() => (sub.value?.transfer_enable ? Math.min(100, Math.round((used.value / sub.value.transfer_enable) * 100)) : 0))
const exhausted = computed(() => !!sub.value && used.value >= sub.value.transfer_enable)

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
  confirm(t('sub.resetSecurityConfirm'), async () => {
    const url = await http.post<string>(`/user/subscriptions/${id}/reset-security`)
    if (sub.value) sub.value.subscribe_url = url
  }, t('sub.resetSecuritySuccess'))
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
  if (ok) load()
}

const serverCols = computed<DataTableColumns<ServerView>>(() => [
  { title: t('common.status'), key: 'is_online', width: 80, render: (s) => (s.is_online ? '🟢' : '⚪') },
  { title: t('sub.nodeName'), key: 'name', render: (s) => s.name },
  { title: t('sub.rate'), key: 'rate', width: 80, render: (s) => `${Number(s.rate)}x` },
  { title: t('sub.tags'), key: 'tags', render: (s) => (s.tags || []).join(' · ') },
])
</script>

<template>
  <div class="page">
    <NSpin :show="loading">
      <NResult v-if="missing" status="404" :title="t('sub.notFound')">
        <template #footer><NButton @click="router.push('/subscriptions')">{{ t('common.back') }}</NButton></template>
      </NResult>
      <template v-else-if="sub">
        <div class="row" style="margin-bottom: 16px">
          <NButton quaternary size="small" @click="router.push('/subscriptions')">← {{ t('common.back') }}</NButton>
          <h2 class="page-title" style="margin: 0">{{ subName(sub) }}</h2>
          <NTag v-if="sub.is_primary" size="small" type="info" :bordered="false">{{ t('sub.primary') }}</NTag>
          <NTag v-if="sub.banned" size="small" type="error" :bordered="false">{{ t('sub.banned') }}</NTag>
          <NTag v-else size="small" :type="subAvailable(sub) ? 'success' : 'warning'" :bordered="false">
            {{ subAvailable(sub) ? t('sub.available') : t('sub.unavailable') }}
          </NTag>
        </div>

        <NGrid cols="1 l:2" responsive="screen" :x-gap="16" :y-gap="16">
          <NGi>
            <NCard size="small" :title="sub.plan?.name || t('sub.noPlan')">
              <div class="row">
                <span>{{ t('sub.used', { used: formatBytes(used), total: formatBytes(sub.transfer_enable) }) }}</span>
                <span class="spacer" />
                <span class="muted">{{ t('sub.remaining', { n: formatBytes(Math.max(0, sub.transfer_enable - used)) }) }}</span>
              </div>
              <NProgress type="line" :percentage="pct" :status="pct >= 90 ? 'error' : pct >= 70 ? 'warning' : 'success'" style="margin: 8px 0 12px" />
              <NDescriptions :column="2" size="small" label-placement="left">
                <NDescriptionsItem :label="t('sub.expire')">
                  {{ sub.expired_at === null ? t('sub.forever') : daysLeft(sub.expired_at) ? formatTime(sub.expired_at) : t('sub.expired') }}
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('sub.devices')">{{ sub.alive_ip }} / {{ sub.device_limit ?? '∞' }}</NDescriptionsItem>
                <NDescriptionsItem :label="t('sub.speedLimit')">{{ sub.speed_limit ? `${sub.speed_limit} Mbps` : t('common.unlimited') }}</NDescriptionsItem>
                <NDescriptionsItem v-if="sub.reset_day !== null" :label="t('sub.resetLabel')">
                  {{ sub.reset_day === 0 ? t('sub.resetToday') : t('sub.resetIn', { n: sub.reset_day }) }}
                </NDescriptionsItem>
              </NDescriptions>
              <template #action>
                <NSpace>
                  <NButton v-if="sub.plan" type="primary" @click="router.push({ path: `/plans/${sub.plan.id}`, query: { sub: sub.id } })">{{ t('sub.renew') }}</NButton>
                  <NButton v-if="!sub.plan || biz?.plan_change !== false" @click="router.push({ path: '/plans', query: { sub: sub.id } })">
                    {{ sub.plan ? t('sub.changePlan') : t('dashboard.buyNow') }}
                  </NButton>
                  <NButton v-if="sub.plan && exhausted" @click="router.push({ path: `/plans/${sub.plan.id}`, query: { sub: sub.id, period: 'reset_price' } })">
                    {{ t('sub.buyReset') }}
                  </NButton>
                  <NButton v-if="sub.allow_new_period && sub.reset_day !== null && exhausted" type="warning" secondary @click="newPeriod">
                    {{ t('sub.newPeriod') }}
                  </NButton>
                </NSpace>
              </template>
            </NCard>
          </NGi>

          <NGi>
            <NCard size="small" :title="t('sub.link')">
              <NAlert type="warning" :show-icon="false" style="margin-bottom: 10px">{{ t('sub.linkWarning') }}</NAlert>
              <div class="mono link-box">{{ sub.subscribe_url }}</div>
              <NSpace style="margin-top: 12px">
                <NButton type="primary" @click="copy(sub.subscribe_url)">{{ t('sub.copyLink') }}</NButton>
                <NButton @click="qr = true">{{ t('sub.qrcode') }}</NButton>
                <NDropdown :options="imports" trigger="click" @select="openImport">
                  <NButton>{{ t('sub.oneClick') }} ▾</NButton>
                </NDropdown>
                <NButton type="error" secondary @click="resetSecurity">{{ t('sub.resetSecurity') }}</NButton>
              </NSpace>
            </NCard>
          </NGi>

          <NGi>
            <NCard size="small" :title="t('sub.settings')">
              <NForm label-placement="left" label-width="auto">
                <NFormItem :label="t('sub.name')">
                  <NInput v-model:value="settings.name" maxlength="64" :placeholder="t('sub.namePlaceholder')" />
                </NFormItem>
                <NFormItem :label="t('sub.autoRenewal')"><NSwitch v-model:value="settings.auto_renewal" /></NFormItem>
                <NFormItem :label="t('sub.remindExpire')"><NSwitch v-model:value="settings.remind_expire" /></NFormItem>
                <NFormItem :label="t('sub.remindTraffic')"><NSwitch v-model:value="settings.remind_traffic" /></NFormItem>
              </NForm>
              <NButton type="primary" @click="saveSettings">{{ t('common.save') }}</NButton>
            </NCard>
          </NGi>

          <NGi>
            <NCard size="small" :title="t('sub.nodes')">
              <template #header-extra><NButton text @click="loadServers">{{ t('common.refresh') }}</NButton></template>
              <NDataTable size="small" :columns="serverCols" :data="servers" :max-height="360" :bordered="false">
                <template #empty>{{ t('sub.noNodes') }}</template>
              </NDataTable>
            </NCard>
          </NGi>
        </NGrid>
      </template>
    </NSpin>
    <NModal v-model:show="qr" preset="card" :title="t('sub.qrcode')" style="width: 300px">
      <div style="display: flex; justify-content: center"><QrCode :text="sub?.subscribe_url || ''" :size="240" /></div>
      <p class="muted" style="text-align: center">{{ t('sub.scanTip') }}</p>
    </NModal>
  </div>
</template>

<style scoped>
.link-box { padding: 10px; border-radius: 6px; background: rgba(127, 127, 127, .1); font-size: 13px; }
</style>
