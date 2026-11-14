<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NCard, NDataTable, NButton, NSpace, NTag, type DataTableColumns } from 'naive-ui'
import { formatTime } from '@gv/shared'
import { http } from '../api'
import { confirm, errText, toast } from '../ui'
import { money } from '../store'
import type { Order } from '../types'

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

const statusType: Record<number, 'default' | 'warning' | 'info' | 'success' | 'error'> = { 0: 'warning', 1: 'info', 2: 'default', 3: 'success', 4: 'default' }

function cancel(o: Order) {
  confirm(t('order.cancelConfirm'), () => http.post(`/user/orders/${o.trade_no}/cancel`), t('order.cancelled')).then((ok) => {
    if (ok) load()
  })
}

const productName = (o: Order) => (o.plan_id === 0 ? t('order.deposit') : o.plan?.name || `#${o.plan_id}`)

const columns = computed<DataTableColumns<Order>>(() => [
  { title: t('order.tradeNo'), key: 'trade_no', render: (o) => h('span', { class: 'mono' }, o.trade_no) },
  { title: t('order.plan'), key: 'plan', render: productName },
  { title: t('order.type'), key: 'type', render: (o) => t(`order.types.${o.type}`) },
  { title: t('order.period'), key: 'period', render: (o) => t(`period.${o.period}`) },
  { title: t('order.amount'), key: 'total_amount', render: (o) => money(o.total_amount + (o.handling_amount || 0)) },
  {
    title: t('common.status'),
    key: 'status',
    render: (o) => h(NTag, { size: 'small', bordered: false, type: statusType[o.status] }, () => t(`order.statuses.${o.status}`)),
  },
  { title: t('order.createdAt'), key: 'created_at', render: (o) => formatTime(o.created_at) },
  {
    title: t('common.actions'),
    key: 'op',
    render: (o) =>
      h(NSpace, { size: 8, wrap: false }, () => [
        h(NButton, { size: 'small', text: true, type: 'primary', onClick: () => router.push(`/order/${o.trade_no}`) }, () =>
          o.status === 0 ? t('order.pay') : t('common.detail'),
        ),
        o.status === 0 ? h(NButton, { size: 'small', text: true, type: 'error', onClick: () => cancel(o) }, () => t('order.cancel')) : null,
      ]),
  },
])

onMounted(load)
</script>

<template>
  <div class="page">
    <h2 class="page-title">{{ t('nav.orders') }}</h2>
    <NCard size="small">
      <NDataTable :columns="columns" :data="rows" :loading="loading" :row-key="(o: Order) => o.trade_no" :scroll-x="900">
        <template #empty>{{ t('order.empty') }}</template>
      </NDataTable>
    </NCard>
  </div>
</template>
