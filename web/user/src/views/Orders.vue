<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { http } from '../api'
import { confirm, errText, toast } from '../ui'
import { money } from '../store'
import type { Order } from '../types'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable from '../components/common/DataTable.vue'
import StatusBadge, { type Tone } from '../components/common/StatusBadge.vue'

const { t } = useI18n()
const router = useRouter()
const rows = ref<Order[]>([])
const loading = ref(true)

async function load() {
  loading.value = true
  try {
    rows.value = (await http.get<Order[]>('/user/orders')) || []
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
}

const statusTone: Record<number, Tone> = { 0: 'warning', 1: 'info', 2: 'default', 3: 'success', 4: 'default' }

function cancel(o: Order) {
  confirm(t('order.cancelConfirm'), () => http.post(`/user/orders/${o.trade_no}/cancel`), t('order.cancelled'), true).then((ok) => {
    if (ok) load()
  })
}

const productName = (o: Order) => (o.plan_id === 0 ? t('order.deposit') : o.plan?.name || `#${o.plan_id}`)

const columns = computed(() => [
  { key: 'trade_no', title: `# ${t('order.tradeNo')}` },
  { key: 'plan', title: t('order.plan') },
  { key: 'type', title: t('order.type') },
  { key: 'period', title: t('order.period') },
  { key: 'total_amount', title: t('order.amount'), align: 'right' as const },
  { key: 'status', title: t('common.status') },
  { key: 'created_at', title: t('order.createdAt') },
  { key: 'op', title: t('common.actions'), align: 'right' as const },
])

onMounted(load)
</script>

<template>
  <div>
    <PageHeader :title="t('nav.orders')" />
    <div class="overflow-x-auto">
      <DataTable
        :columns="columns"
        :rows="rows"
        :loading="loading"
        row-key="trade_no"
        :empty="t('order.empty')"
        clickable
        class="min-w-[860px]"
        @row-click="(o) => router.push(`/order/${o.trade_no}`)"
      >
        <template #cell-trade_no="{ row }"><span class="font-mono text-xs">{{ row.trade_no }}</span></template>
        <template #cell-plan="{ row }">{{ productName(row) }}</template>
        <template #cell-type="{ row }">{{ t(`order.types.${row.type}`) }}</template>
        <template #cell-period="{ row }">{{ t(`period.${row.period}`) }}</template>
        <template #cell-total_amount="{ row }"><span class="tabular-nums">{{ money(row.total_amount + (row.handling_amount || 0)) }}</span></template>
        <template #cell-status="{ row }">
          <StatusBadge :tone="statusTone[row.status]" dot>{{ t(`order.statuses.${row.status}`) }}</StatusBadge>
        </template>
        <template #cell-created_at="{ row }"><span class="text-muted-foreground">{{ formatTime(row.created_at) }}</span></template>
        <template #cell-op="{ row }">
          <div class="flex justify-end gap-1" @click.stop>
            <Button size="sm" :variant="row.status === 0 ? 'default' : 'ghost'" class="h-7" @click="router.push(`/order/${row.trade_no}`)">
              {{ row.status === 0 ? t('order.pay') : t('common.detail') }}
            </Button>
            <Button v-if="row.status === 0" size="sm" variant="ghost" class="h-7 text-destructive hover:text-destructive" @click="cancel(row)">
              {{ t('order.cancel') }}
            </Button>
          </div>
        </template>
      </DataTable>
    </div>
  </div>
</template>
