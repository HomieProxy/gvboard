<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NDataTable, NSelect, NStatistic, NSpace, type DataTableColumns } from 'naive-ui'
import { formatBytes, formatTime } from '@gv/shared'
import { http } from '../api'
import { errText, toast } from '../ui'
import { loadSubs, subName, subOptions, subs } from '../store'
import type { TrafficLog } from '../types'

const { t } = useI18n()
const rows = ref<TrafficLog[]>([])
const loading = ref(true)
const sub = ref<number | null>(null)

const charged = (r: TrafficLog) => (r.u + r.d) * Number(r.server_rate)
const sum = computed(() => rows.value.reduce((n, r) => n + charged(r), 0))

async function load() {
  loading.value = true
  try {
    rows.value = (await http.get<TrafficLog[]>('/user/traffic-log', { subscription_id: sub.value || undefined })) || []
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
}

const columns = computed<DataTableColumns<TrafficLog>>(() => [
  { title: t('traffic.date'), key: 'record_at', render: (r) => formatTime(r.record_at, false) },
  {
    title: t('traffic.subscription'),
    key: 'subscription_id',
    render: (r) => {
      const s = subs.value.find((x) => x.id === r.subscription_id)
      return s ? subName(s) : r.subscription_id ? `#${r.subscription_id}` : '-'
    },
  },
  { title: t('traffic.upload'), key: 'u', render: (r) => formatBytes(r.u) },
  { title: t('traffic.download'), key: 'd', render: (r) => formatBytes(r.d) },
  { title: t('traffic.rate'), key: 'server_rate', render: (r) => `${Number(r.server_rate)}x` },
  { title: t('traffic.total'), key: 'total', render: (r) => h('strong', formatBytes(charged(r))) },
])

watch(sub, load)
onMounted(() => {
  load()
  loadSubs().catch(() => {})
})
</script>

<template>
  <div class="page">
    <div class="row" style="justify-content: space-between; margin-bottom: 16px; flex-wrap: wrap">
      <h2 class="page-title" style="margin: 0">{{ t('nav.traffic') }}</h2>
      <NSelect v-model:value="sub" :options="subOptions()" clearable :placeholder="t('traffic.allSubs')" style="width: 240px" />
    </div>
    <NCard size="small" style="margin-bottom: 16px">
      <NSpace :size="48">
        <NStatistic :label="t('traffic.monthTotal')" :value="formatBytes(sum)" />
      </NSpace>
      <p class="muted">{{ t('traffic.tip') }}</p>
    </NCard>
    <NCard size="small">
      <NDataTable :columns="columns" :data="rows" :loading="loading" :scroll-x="700">
        <template #empty>{{ t('common.empty') }}</template>
      </NDataTable>
    </NCard>
  </div>
</template>
