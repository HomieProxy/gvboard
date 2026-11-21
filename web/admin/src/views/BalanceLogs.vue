<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { SearchIcon } from '@lucide/vue'
import { formatMoney, formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Card, CardContent } from '@gv/shared/ui/card'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import SelectBox from '../components/common/SelectBox.vue'
import DateTimeInput from '../components/common/DateTimeInput.vue'
import { admin } from '../api'
import { usePaged } from '../paged'
import { balanceNote, balanceTypeNames, balanceTypeOptions, type BalanceLogPage, type BalanceLogRow } from '../balance'

const router = useRouter()

const blank = () => ({ email: '', type: '', since: null as number | null, until: null as number | null })
const q = ref(blank())
const sum = ref({ income: 0, expense: 0 })

const { rows, loading, pager, load, reload, setPage, setPageSize } = usePaged<BalanceLogRow>(async (current, page_size) => {
  const f = q.value
  const r = await admin.get<BalanceLogPage>('/balance-logs', {
    current,
    page_size,
    email: f.email.trim() || undefined,
    type: f.type || undefined,
    since: f.since ?? undefined,
    until: f.until ?? undefined,
  })
  sum.value = { income: r.income, expense: r.expense }
  return { rows: r.data || [], total: r.total }
}, 20)

function reset() {
  q.value = blank()
  reload()
}
function viewUser(id: number) {
  router.push({ path: '/users', query: { id, tab: 'balance' } })
}

const columns: Column[] = [
  { key: 'created_at', title: '时间' },
  { key: 'email', title: '用户' },
  { key: 'type', title: '类型' },
  { key: 'amount', title: '变动', align: 'right' },
  { key: 'balance', title: '变动后余额', align: 'right' },
  { key: 'trade_no', title: '关联订单' },
  { key: 'remark', title: '说明' },
]

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="余额明细" description="全站用户的余额变动记录，超过保留天数的记录会自动清理（保留天数在系统配置中设置）。" />

    <Card class="mb-3 py-3">
      <CardContent class="grid grid-cols-2 gap-2 px-3 md:grid-cols-4 xl:grid-cols-6">
        <Input v-model="q.email" placeholder="用户邮箱（模糊）" @keydown.enter="reload" />
        <SelectBox :model-value="q.type" :options="balanceTypeOptions" @update:model-value="(v) => (q.type = String(v ?? ''))" />
        <DateTimeInput v-model="q.since" title="开始时间" />
        <DateTimeInput v-model="q.until" title="结束时间" />
        <div class="col-span-2 flex gap-2">
          <Button @click="reload"><SearchIcon />查询</Button>
          <Button variant="outline" @click="reset">重置</Button>
        </div>
      </CardContent>
    </Card>

    <div class="mb-3 flex flex-wrap gap-x-6 gap-y-1 text-sm">
      <span>共 <span class="font-medium tabular-nums">{{ pager.total }}</span> 条</span>
      <span>增加合计 <span class="font-medium tabular-nums text-emerald-600">+{{ formatMoney(sum.income) }}</span> 元</span>
      <span>减少合计 <span class="font-medium tabular-nums text-destructive">{{ formatMoney(sum.expense) }}</span> 元</span>
      <span>净变动 <span class="font-medium tabular-nums">{{ formatMoney(sum.income + sum.expense) }}</span> 元</span>
    </div>

    <DataTable :columns="columns" :rows="rows" :loading="loading" empty="暂无余额变动记录">
      <template #cell-created_at="{ row }"><span class="whitespace-nowrap text-xs">{{ formatTime(row.created_at) }}</span></template>
      <template #cell-email="{ row }">
        <button type="button" class="block max-w-56 truncate text-left text-primary hover:underline" :title="`${row.email || '#' + row.user_id}（查看该用户的余额明细）`" @click="viewUser(row.user_id)">
          {{ row.email || `#${row.user_id}` }}
        </button>
      </template>
      <template #cell-type="{ row }">{{ balanceTypeNames[row.type] || row.type }}</template>
      <template #cell-amount="{ row }">
        <span :class="['tabular-nums', row.amount > 0 ? 'text-emerald-600' : 'text-destructive']">{{ row.amount > 0 ? '+' : '' }}{{ formatMoney(row.amount) }}</span>
      </template>
      <template #cell-balance="{ row }"><span class="tabular-nums">{{ formatMoney(row.balance) }}</span></template>
      <template #cell-trade_no="{ row }"><span class="font-mono text-xs">{{ row.trade_no || '-' }}</span></template>
      <template #cell-remark="{ row }"><span class="break-all text-xs">{{ balanceNote(row) }}</span></template>
    </DataTable>
    <Pager :pager="pager" @page="setPage" @size="setPageSize" />
  </div>
</template>
