<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  NCard, NDataTable, NButton, NInput, NModal, NForm, NFormItem, NInputNumber, NSelect, NSpace, NCheckbox,
  NDescriptions, NDescriptionsItem, NDropdown, type DataTableColumns,
} from 'naive-ui'
import { formatMoney, formatTime } from '@gv/shared'
import { admin } from '../api'
import { confirm, run } from '../ui'
import { usePaged } from '../paged'
import { actions, btn, commissionStatus, enumTag, orderStatus, orderType, periodLabels, periodOptions, toCents, type Filter, type PageResult } from '../helpers'
import { loadPlans, planOptions } from '../refs'
import type { Order, OrderDetail } from '../types'
import FilterEditor from '../components/FilterEditor.vue'

const route = useRoute()
const filterFields = [
  { label: '用户邮箱（包含）', value: 'email' },
  { label: '订单号', value: 'trade_no' },
  { label: '支付流水号', value: 'callback_no' },
  { label: '状态(0-4)', value: 'status' },
  { label: '佣金状态(0-3)', value: 'commission_status' },
  { label: '用户 ID', value: 'user_id' },
  { label: '邀请人 ID', value: 'invite_user_id' },
  { label: '佣金(分)', value: 'commission_balance' },
  { label: '订阅 ID', value: 'subscription_id' },
  { label: '套餐 ID', value: 'plan_id' },
]
const filter = ref<Filter[]>([])
const keyword = ref('')
const commissionOnly = ref(false)

const { rows, loading, pagination, load, reload } = usePaged<Order>(async (current, page_size) => {
  const f = [...filter.value]
  if (keyword.value.trim()) f.push({ key: 'trade_no', condition: '模糊', value: keyword.value.trim() })
  const r = await admin.post<PageResult<Order>>('/orders/query', { current, page_size, filter: f, is_commission: commissionOnly.value })
  return { rows: r.data || [], total: r.total }
})

// ---------- 详情 ----------
const detail = ref<OrderDetail | null>(null)
const detailShow = ref(false)
async function open(o: Order) {
  await run(async () => {
    detail.value = await admin.get<OrderDetail>(`/orders/${o.trade_no}`)
    detailShow.value = true
  })
}

function act(o: Order, path: string, text: string) {
  confirm(text, () => admin.post(`/orders/${o.trade_no}/${path}`)).then((ok) => {
    if (ok) load()
  })
}
function setCommission(o: Order, status: number) {
  run(() => admin.put(`/orders/${o.trade_no}`, { commission_status: status }), '已修改').then((ok) => {
    if (ok) load()
  })
}
const commissionOptions = [0, 1, 3].map((v) => ({ label: `设为${commissionStatus[v][0]}`, key: v }))

const columns: DataTableColumns<Order> = [
  { title: '订单号', key: 'trade_no', render: (o) => h('a', { class: 'link mono', onClick: () => open(o) }, o.trade_no) },
  { title: '用户', key: 'user_id', width: 80, render: (o) => `#${o.user_id}` },
  {
    title: '套餐 / 周期',
    key: 'plan',
    render: (o) => `${o.plan_id === 0 ? '充值' : o.plan_name || `#${o.plan_id}`} / ${periodLabels[o.period] || o.period}`,
  },
  { title: '类型', key: 'type', width: 90, render: (o) => orderType[o.type] || o.type },
  { title: '订阅', key: 'subscription_id', width: 80, render: (o) => (o.subscription_id ? `#${o.subscription_id}` : '-') },
  { title: '金额', key: 'total_amount', render: (o) => formatMoney(o.total_amount + (o.handling_amount || 0)) },
  { title: '状态', key: 'status', width: 90, render: (o) => enumTag(orderStatus, o.status) },
  {
    title: '佣金',
    key: 'commission_balance',
    render: (o) =>
      o.commission_balance > 0
        ? h(NSpace, { size: 4 }, () => [formatMoney(o.commission_balance), enumTag(commissionStatus, o.commission_status)])
        : '-',
  },
  { title: '创建时间', key: 'created_at', render: (o) => formatTime(o.created_at) },
  {
    title: '操作',
    key: 'op',
    render: (o) =>
      actions(
        btn('详情', () => open(o)),
        o.status === 0 ? btn('标记已支付', () => act(o, 'paid', '确定将该订单标记为已支付并开通？'), 'success') : null,
        o.status === 0 ? btn('取消', () => act(o, 'cancel', '确定取消该订单？'), 'error') : null,
        o.commission_balance > 0 && o.commission_status !== 2
          ? h(
              NDropdown,
              { options: commissionOptions, trigger: 'click', onSelect: (k: number) => setCommission(o, k) },
              { default: () => h(NButton, { size: 'small', text: true, type: 'warning' }, { default: () => '佣金状态' }) },
            )
          : null,
      ),
  },
]

// ---------- 分配订单 ----------
const assign = ref(false)
const assignForm = ref({ email: '', plan_id: null as number | null, period: 'month_price', total_amount: 0 as number | null, subscription_id: null as number | null })
function openAssign() {
  loadPlans()
  assign.value = true
}
async function submitAssign() {
  const f = assignForm.value
  const ok = await run(
    () =>
      admin.post('/orders/assign', {
        email: f.email.trim(),
        plan_id: f.plan_id || 0,
        period: f.period,
        total_amount: toCents(f.total_amount) ?? 0,
        subscription_id: f.subscription_id || 0,
      }),
    '已创建待支付订单',
  )
  if (ok) {
    assign.value = false
    reload()
  }
}

onMounted(() => {
  const uid = route.query.user_id
  if (uid) filter.value = [{ key: 'user_id', condition: '=', value: String(uid) }]
  load()
})
</script>

<template>
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NInput v-model:value="keyword" placeholder="搜索订单号" clearable style="width: 220px" @keyup.enter="reload" @clear="reload" />
        <FilterEditor v-model="filter" :fields="filterFields" @apply="reload" />
        <NCheckbox v-model:checked="commissionOnly" @update:checked="reload">只看有佣金的订单</NCheckbox>
        <NButton @click="reload">刷新</NButton>
        <span class="spacer" />
        <NButton type="primary" @click="openAssign">分配订单</NButton>
      </div>
      <NDataTable remote size="small" :columns="columns" :data="rows" :loading="loading" :pagination="pagination" :row-key="(o: Order) => o.id" :scroll-x="1100" />
    </NCard>

    <NModal v-model:show="detailShow" preset="card" title="订单详情" style="width: 720px; max-width: 95vw">
      <template v-if="detail">
        <NDescriptions :column="2" size="small" label-placement="left" bordered>
          <NDescriptionsItem label="订单号"><span class="mono">{{ detail.trade_no }}</span></NDescriptionsItem>
          <NDescriptionsItem label="状态">{{ orderStatus[detail.status]?.[0] }}</NDescriptionsItem>
          <NDescriptionsItem label="用户 ID">{{ detail.user_id }}</NDescriptionsItem>
          <NDescriptionsItem label="订阅 ID">{{ detail.subscription_id ?? '-' }}</NDescriptionsItem>
          <NDescriptionsItem label="类型">{{ orderType[detail.type] }}</NDescriptionsItem>
          <NDescriptionsItem label="周期">{{ periodLabels[detail.period] || detail.period }}</NDescriptionsItem>
          <NDescriptionsItem label="应付金额">{{ formatMoney(detail.total_amount) }}</NDescriptionsItem>
          <NDescriptionsItem label="手续费">{{ formatMoney(detail.handling_amount) }}</NDescriptionsItem>
          <NDescriptionsItem label="优惠金额">{{ formatMoney(detail.discount_amount) }}</NDescriptionsItem>
          <NDescriptionsItem label="余额抵扣">{{ formatMoney(detail.balance_amount) }}</NDescriptionsItem>
          <NDescriptionsItem label="折抵金额">{{ formatMoney(detail.surplus_amount) }}</NDescriptionsItem>
          <NDescriptionsItem label="退回金额">{{ formatMoney(detail.refund_amount) }}</NDescriptionsItem>
          <NDescriptionsItem label="优惠券 ID">{{ detail.coupon_id ?? '-' }}</NDescriptionsItem>
          <NDescriptionsItem label="支付方式 ID">{{ detail.payment_id ?? '-' }}</NDescriptionsItem>
          <NDescriptionsItem label="支付流水号"><span class="mono">{{ detail.callback_no || '-' }}</span></NDescriptionsItem>
          <NDescriptionsItem label="支付时间">{{ formatTime(detail.paid_at) }}</NDescriptionsItem>
          <NDescriptionsItem label="邀请人 ID">{{ detail.invite_user_id ?? '-' }}</NDescriptionsItem>
          <NDescriptionsItem label="佣金">
            {{ formatMoney(detail.commission_balance) }}（{{ commissionStatus[detail.commission_status]?.[0] }}，实际发放 {{ formatMoney(detail.actual_commission_balance) }}）
          </NDescriptionsItem>
          <NDescriptionsItem label="创建时间">{{ formatTime(detail.created_at) }}</NDescriptionsItem>
          <NDescriptionsItem label="更新时间">{{ formatTime(detail.updated_at) }}</NDescriptionsItem>
        </NDescriptions>
        <template v-if="detail.commission_log.length">
          <h4>佣金发放记录</h4>
          <div v-for="l in detail.commission_log" :key="l.id" class="muted">
            {{ formatTime(l.created_at) }}　邀请人 #{{ l.invite_user_id }}　获得 {{ formatMoney(l.get_amount) }}
          </div>
        </template>
        <template v-if="detail.surplus_orders.length">
          <h4>被折抵的订单</h4>
          <div v-for="s in detail.surplus_orders" :key="s.id" class="muted mono">
            {{ s.trade_no }}　{{ formatMoney(s.total_amount) }}　{{ formatTime(s.created_at) }}
          </div>
        </template>
      </template>
    </NModal>

    <NModal v-model:show="assign" preset="card" title="分配订单" style="width: 520px; max-width: 95vw">
      <NForm label-placement="left" label-width="96">
        <NFormItem label="用户邮箱"><NInput v-model:value="assignForm.email" /></NFormItem>
        <NFormItem label="套餐"><NSelect v-model:value="assignForm.plan_id" :options="planOptions" /></NFormItem>
        <NFormItem label="周期"><NSelect v-model:value="assignForm.period" :options="periodOptions" /></NFormItem>
        <NFormItem label="金额（元）"><NInputNumber v-model:value="assignForm.total_amount" :min="0" :precision="2" style="width: 100%" /></NFormItem>
        <NFormItem label="订阅 ID"><NInputNumber v-model:value="assignForm.subscription_id" :min="0" clearable placeholder="留空则新开订阅" style="width: 100%" /></NFormItem>
        <div class="muted">分配后生成待支付订单，可在列表中“标记已支付”直接开通。流量重置包必须指定订阅。</div>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="assign = false">取消</NButton><NButton type="primary" @click="submitAssign">创建</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
