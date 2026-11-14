<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { UsersIcon, PercentIcon, HourglassIcon, CoinsIcon, PlusIcon, CopyIcon, InfoIcon, Loader2Icon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Card, CardContent, CardHeader, CardTitle, CardAction } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { http } from '../api'
import { copy, errText, toast } from '../ui'
import { biz, loadBiz, loadMe, money } from '../store'
import type { CommissionDetail, InviteOverview } from '../types'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable from '../components/common/DataTable.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import SimplePager from '../components/common/SimplePager.vue'

const { t } = useI18n()
const router = useRouter()
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

const stats = computed(() => [
  { label: t('invite.invitees'), value: data.value?.stat.invitees ?? 0, icon: UsersIcon },
  { label: t('invite.rate'), value: `${data.value?.stat.commission_rate ?? 0}%`, icon: PercentIcon },
  { label: t('invite.pending'), value: money(Math.trunc(data.value?.stat.pending_commission ?? 0)), icon: HourglassIcon, tip: t('invite.pendingTip') },
  { label: t('invite.total'), value: money(data.value?.stat.total_commission ?? 0), icon: CoinsIcon },
])

const codeColumns = computed(() => [
  { key: 'code', title: t('invite.code') },
  { key: 'pv', title: t('invite.pvTitle'), class: 'w-24' },
  { key: 'created_at', title: t('invite.createdAt') },
  { key: 'op', title: t('common.actions'), align: 'right' as const },
])

// 佣金明细
const rows = ref<CommissionDetail[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = 10
const total = ref(0)
async function loadCommissions() {
  loading.value = true
  try {
    const r = await http.get<{ total: number; data: CommissionDetail[] }>('/user/invite/commissions', { current: page.value, page_size: pageSize })
    rows.value = r.data || []
    total.value = r.total
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
}
const commissionColumns = computed(() => [
  { key: 'created_at', title: t('invite.issuedAt') },
  { key: 'trade_no', title: t('order.tradeNo') },
  { key: 'order_amount', title: t('invite.orderAmount'), align: 'right' as const },
  { key: 'get_amount', title: t('invite.getAmount'), align: 'right' as const },
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
  <PageHeader :title="t('nav.invite')" />
  <div class="space-y-6">

    <Card class="bg-gradient-to-br from-primary to-primary/80 text-primary-foreground">
      <CardContent class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <div class="text-sm opacity-80">{{ t('invite.available') }}</div>
          <div class="mt-2 text-3xl font-semibold tabular-nums">{{ money(data?.stat.commission_balance ?? 0) }}</div>
        </div>
        <div class="flex flex-wrap gap-2">
          <Button variant="secondary" @click="router.push('/wallet')">{{ t('invite.transfer') }}</Button>
          <Button variant="secondary" :disabled="biz?.withdraw.closed" @click="withdrawShow = true">{{ t('invite.withdraw') }}</Button>
        </div>
      </CardContent>
    </Card>

    <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <Card v-for="s in stats" :key="s.label" class="gap-2 py-4">
        <CardContent class="px-4">
          <div class="flex items-center gap-1.5 text-sm text-muted-foreground">
            <component :is="s.icon" class="size-4" />{{ s.label }}
          </div>
          <div class="mt-2 text-2xl font-semibold tabular-nums">{{ s.value }}</div>
          <p v-if="s.tip" class="mt-1 text-xs text-muted-foreground">{{ s.tip }}</p>
        </CardContent>
      </Card>
    </div>

    <Alert v-if="biz?.withdraw.closed">
      <InfoIcon />
      <AlertDescription>{{ t('invite.withdrawClosed') }}</AlertDescription>
    </Alert>
    <Alert v-if="levels.length">
      <InfoIcon />
      <AlertDescription>{{ t('invite.distribution', { l1: levels[0] ?? '-', l2: levels[1] ?? '-', l3: levels[2] ?? '-' }) }}</AlertDescription>
    </Alert>

    <Card>
      <CardHeader>
        <CardTitle class="text-base">{{ t('invite.codes') }}</CardTitle>
        <CardAction>
          <Button size="sm" :disabled="creating" @click="create">
            <Loader2Icon v-if="creating" class="animate-spin" /><PlusIcon v-else />{{ t('invite.generate') }}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent class="overflow-x-auto">
        <DataTable :columns="codeColumns" :rows="data?.codes || []" :empty="t('invite.noCodes')" class="min-w-[560px]">
          <template #cell-code="{ row }"><span class="font-mono">{{ row.code }}</span></template>
          <template #cell-created_at="{ row }"><span class="text-muted-foreground">{{ formatTime(row.created_at) }}</span></template>
          <template #cell-op="{ row }">
            <Button size="sm" variant="ghost" class="h-7" @click="copy(link(row.code))"><CopyIcon />{{ t('invite.copyLink') }}</Button>
          </template>
        </DataTable>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle class="text-base">{{ t('invite.details') }}</CardTitle>
      </CardHeader>
      <CardContent class="overflow-x-auto">
        <DataTable :columns="commissionColumns" :rows="rows" :loading="loading" class="min-w-[560px]">
          <template #cell-created_at="{ row }"><span class="text-muted-foreground">{{ formatTime(row.created_at) }}</span></template>
          <template #cell-trade_no="{ row }"><span class="font-mono text-xs">{{ row.trade_no }}</span></template>
          <template #cell-order_amount="{ row }"><span class="tabular-nums">{{ money(row.order_amount) }}</span></template>
          <template #cell-get_amount="{ row }"><span class="font-medium tabular-nums text-success">{{ money(row.get_amount) }}</span></template>
        </DataTable>
        <SimplePager v-model:page="page" :total="total" :page-size="pageSize" @change="loadCommissions" />
      </CardContent>
    </Card>

    <Dialog v-model:open="withdrawShow">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ t('invite.withdraw') }}</DialogTitle>
          <DialogDescription>{{ t('invite.withdrawMin', { n: money((biz?.withdraw.min || 0) * 100) }) }}</DialogDescription>
        </DialogHeader>
        <form class="space-y-4" @submit.prevent="submitWithdraw">
          <FormItem :label="t('invite.withdrawMethod')">
            <SelectBox :model-value="withdraw.method" :options="methodOptions" @update:model-value="(v) => (withdraw.method = v === null ? null : String(v))" />
          </FormItem>
          <FormItem :label="t('invite.withdrawAccount')">
            <Input v-model="withdraw.account" maxlength="255" />
          </FormItem>
          <DialogFooter>
            <Button type="button" variant="outline" @click="withdrawShow = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="withdrawing || !withdraw.method || !withdraw.account.trim()">
              <Loader2Icon v-if="withdrawing" class="animate-spin" />{{ t('common.submit') }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </div>
</template>
