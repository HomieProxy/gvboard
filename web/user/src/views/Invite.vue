<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NGrid, NGi, NCard, NButton, NStatistic, NDataTable, NModal, NForm, NFormItem, NSelect, NInput, NAlert, type DataTableColumns,
} from 'naive-ui'
import { formatTime } from '@gv/shared'
import { http } from '../api'
import { copy, errText, toast } from '../ui'
import { biz, loadBiz, loadMe, money } from '../store'
import type { CommissionDetail, InviteCode, InviteOverview } from '../types'

const { t } = useI18n()
const data = ref<InviteOverview | null>(null)
const creating = ref(false)

const link = (code: string) => `${location.origin}${location.pathname}#/register?code=${encodeURIComponent(code)}`

async function load() {
  try {
    data.value = await http.get<InviteOverview>('/user/invite')
  } catch (e) {
    toast('error', errText(e))
  }
}

async function create() {
  creating.value = true
  try {
    await http.post('/user/invite/codes')
    await load()
  } catch (e) {
    toast('error', errText(e))
  } finally {
    creating.value = false
  }
}

const codeColumns = computed<DataTableColumns<InviteCode>>(() => [
  { title: t('invite.code'), key: 'code', render: (c) => h('span', { class: 'mono' }, c.code) },
  { title: t('invite.pvTitle'), key: 'pv', width: 100 },
  { title: t('invite.createdAt'), key: 'created_at', render: (c) => formatTime(c.created_at) },
  {
    title: t('common.actions'),
    key: 'op',
    render: (c) => h(NButton, { size: 'small', text: true, type: 'primary', onClick: () => copy(link(c.code)) }, () => t('invite.copyLink')),
  },
])

// 佣金明细
const rows = ref<CommissionDetail[]>([])
const loading = ref(false)
const pagination = reactive({
  page: 1,
  pageSize: 10,
  itemCount: 0,
  onChange: (p: number) => {
    pagination.page = p
    loadCommissions()
  },
})
async function loadCommissions() {
  loading.value = true
  try {
    const r = await http.get<{ total: number; data: CommissionDetail[] }>('/user/invite/commissions', {
      current: pagination.page,
      page_size: pagination.pageSize,
    })
    rows.value = r.data || []
    pagination.itemCount = r.total
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
}
const commissionColumns = computed<DataTableColumns<CommissionDetail>>(() => [
  { title: t('order.tradeNo'), key: 'trade_no', render: (c) => h('span', { class: 'mono' }, c.trade_no) },
  { title: t('invite.orderAmount'), key: 'order_amount', render: (c) => money(c.order_amount) },
  { title: t('invite.getAmount'), key: 'get_amount', render: (c) => money(c.get_amount) },
  { title: t('invite.createdAt'), key: 'created_at', render: (c) => formatTime(c.created_at) },
])

// 提现
const withdrawShow = ref(false)
const withdraw = reactive({ method: null as string | null, account: '' })
const withdrawing = ref(false)
const methodOptions = computed(() => (biz.value?.withdraw.methods || []).map((m) => ({ label: m, value: m })))
async function submitWithdraw() {
  if (!withdraw.method || !withdraw.account.trim()) return
  withdrawing.value = true
  try {
    await http.post('/user/withdrawals', { withdraw_method: withdraw.method, withdraw_account: withdraw.account.trim() })
    toast('success', t('invite.withdrawOk'))
    withdrawShow.value = false
    withdraw.account = ''
  } catch (e) {
    toast('error', errText(e))
  } finally {
    withdrawing.value = false
  }
}

const levels = computed(() => (biz.value?.distribution.enabled ? biz.value.distribution.levels : []))

onMounted(() => {
  load()
  loadCommissions()
  loadBiz().catch(() => {})
  loadMe().catch(() => {})
})
</script>

<template>
  <div class="page">
    <h2 class="page-title">{{ t('nav.invite') }}</h2>
    <NGrid cols="2 m:5" responsive="screen" :x-gap="16" :y-gap="16">
      <NGi><NCard size="small"><NStatistic :label="t('invite.invitees')" :value="data?.stat.invitees ?? 0" /></NCard></NGi>
      <NGi><NCard size="small"><NStatistic :label="t('invite.rate')" :value="`${data?.stat.commission_rate ?? 0}%`" /></NCard></NGi>
      <NGi><NCard size="small"><NStatistic :label="t('invite.total')" :value="money(data?.stat.total_commission ?? 0)" /></NCard></NGi>
      <NGi><NCard size="small"><NStatistic :label="t('invite.pending')" :value="money(Math.trunc(data?.stat.pending_commission ?? 0))" /></NCard></NGi>
      <NGi>
        <NCard size="small">
          <NStatistic :label="t('invite.available')" :value="money(data?.stat.commission_balance ?? 0)" />
          <NButton size="tiny" style="margin-top: 6px" :disabled="biz?.withdraw.closed" @click="withdrawShow = true">{{ t('invite.withdraw') }}</NButton>
        </NCard>
      </NGi>
    </NGrid>

    <NAlert v-if="biz?.withdraw.closed" type="info" style="margin-top: 16px">{{ t('invite.withdrawClosed') }}</NAlert>
    <NAlert v-if="levels.length" type="info" style="margin-top: 16px">
      {{ t('invite.distribution', { l1: levels[0] ?? '-', l2: levels[1] ?? '-', l3: levels[2] ?? '-' }) }}
    </NAlert>

    <NCard size="small" :title="t('invite.codes')" style="margin-top: 16px">
      <template #header-extra>
        <NButton size="small" type="primary" :loading="creating" @click="create">{{ t('invite.generate') }}</NButton>
      </template>
      <NDataTable :columns="codeColumns" :data="data?.codes || []" :row-key="(c: InviteCode) => c.id" :scroll-x="600">
        <template #empty>{{ t('invite.noCodes') }}</template>
      </NDataTable>
    </NCard>

    <NCard size="small" :title="t('invite.details')" style="margin-top: 16px">
      <NDataTable
        remote
        :columns="commissionColumns"
        :data="rows"
        :loading="loading"
        :pagination="pagination"
        :row-key="(c: CommissionDetail) => c.id"
        :scroll-x="600"
      />
    </NCard>

    <NModal v-model:show="withdrawShow" preset="card" :title="t('invite.withdraw')" style="width: 420px; max-width: 92vw">
      <p class="muted">{{ t('invite.withdrawMin', { n: money((biz?.withdraw.min || 0) * 100) }) }}</p>
      <NForm @submit.prevent="submitWithdraw">
        <NFormItem :label="t('invite.withdrawMethod')">
          <NSelect v-model:value="withdraw.method" :options="methodOptions" />
        </NFormItem>
        <NFormItem :label="t('invite.withdrawAccount')">
          <NInput v-model:value="withdraw.account" :maxlength="255" />
        </NFormItem>
        <NButton type="primary" block attr-type="submit" :loading="withdrawing" :disabled="!withdraw.method || !withdraw.account.trim()">
          {{ t('common.submit') }}
        </NButton>
      </NForm>
    </NModal>
  </div>
</template>
