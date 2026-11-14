<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ActivityIcon, InfoIcon } from '@lucide/vue'
import { formatBytes, formatTime } from '@gv/shared'
import { Card, CardContent } from '@gv/shared/ui/card'
import { http } from '../api'
import { errText, toast } from '../ui'
import { loadSubs, subName, subOptions, subs } from '../store'
import type { TrafficLog } from '../types'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable from '../components/common/DataTable.vue'
import SelectBox from '../components/common/SelectBox.vue'

const { t } = useI18n()
const rows = ref<TrafficLog[]>([])
const loading = ref(true)
const sub = ref<number | null>(null)
const subChoices = computed(() => [{ label: t('traffic.allSubs'), value: null }, ...subOptions()])

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

function subLabel(r: TrafficLog) {
  const s = subs.value.find((x) => x.id === r.subscription_id)
  return s ? subName(s) : r.subscription_id ? `#${r.subscription_id}` : '-'
}

const columns = computed(() => [
  { key: 'record_at', title: t('traffic.date') },
  { key: 'subscription_id', title: t('traffic.subscription') },
  { key: 'u', title: t('traffic.upload'), align: 'right' as const },
  { key: 'd', title: t('traffic.download'), align: 'right' as const },
  { key: 'server_rate', title: t('traffic.rate'), align: 'right' as const },
  { key: 'total', title: t('traffic.total'), align: 'right' as const },
])

watch(sub, load)
onMounted(() => {
  load()
  loadSubs().catch(() => {})
})
</script>

<template>
  <div>
    <PageHeader :title="t('nav.traffic')">
      <SelectBox class="w-60" :model-value="sub" :options="subChoices" @update:model-value="(v) => (sub = v === null ? null : Number(v))" />
    </PageHeader>
    <Card class="mb-4 py-4">
      <CardContent class="flex flex-wrap items-center gap-x-8 gap-y-3 px-4">
        <div class="flex items-center gap-3">
          <div class="flex size-10 items-center justify-center rounded-full bg-primary/10 text-primary"><ActivityIcon class="size-5" /></div>
          <div>
            <div class="text-sm text-muted-foreground">{{ t('traffic.monthTotal') }}</div>
            <div class="text-2xl font-semibold tabular-nums">{{ formatBytes(sum) }}</div>
          </div>
        </div>
        <p class="flex items-start gap-1.5 text-sm text-muted-foreground"><InfoIcon class="mt-0.5 size-4 shrink-0" />{{ t('traffic.tip') }}</p>
      </CardContent>
    </Card>
    <div class="overflow-x-auto">
      <DataTable :columns="columns" :rows="rows" :loading="loading" :row-key="(r) => `${r.record_at}-${r.subscription_id}-${r.server_rate}`" class="min-w-[680px]">
        <template #cell-record_at="{ row }">{{ formatTime(row.record_at, false) }}</template>
        <template #cell-subscription_id="{ row }">{{ subLabel(row) }}</template>
        <template #cell-u="{ row }"><span class="tabular-nums">{{ formatBytes(row.u) }}</span></template>
        <template #cell-d="{ row }"><span class="tabular-nums">{{ formatBytes(row.d) }}</span></template>
        <template #cell-server_rate="{ row }">{{ Number(row.server_rate) }}x</template>
        <template #cell-total="{ row }"><span class="font-medium tabular-nums">{{ formatBytes(charged(row)) }}</span></template>
      </DataTable>
    </div>
  </div>
</template>
