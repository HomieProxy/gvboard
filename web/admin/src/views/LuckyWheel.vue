<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { PlusIcon, RefreshCwIcon, SearchIcon, Trash2Icon, Loader2Icon } from '@lucide/vue'
import { formatBytes, formatMoney, formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Card, CardContent, CardHeader, CardTitle } from '@gv/shared/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@gv/shared/ui/tabs'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import NumInput from '../components/common/NumInput.vue'
import BoolSwitch from '../components/common/BoolSwitch.vue'
import { admin } from '../api'
import { errText, message, run } from '../ui'
import { usePaged } from '../paged'
import { toCents, toYuan, type PageResult } from '../helpers'

type PrizeType = 'balance' | 'days' | 'traffic' | 'none'
interface Prize {
  name: string
  type: PrizeType
  value: number
  weight: number
}
interface Settings {
  free_daily: number
  balance_enable: boolean
  balance_cost: number
  traffic_enable: boolean
  traffic_cost: number
  paid_daily: number
  retention_days: number
  prizes: Prize[]
}
interface WheelLog {
  id: number
  user_id: number
  email: string
  subscription_id: number
  subscription_name: string
  prize_name: string
  prize_type: PrizeType
  prize_value: number
  cost_type: 'free' | 'balance' | 'traffic'
  cost: number
  created_at: number
}

const base = '/plugin/lucky_wheel'
const MAX_PRIZES = 12
const tab = ref('settings')

// ---------- 规则与奖品（金额在表单中以元编辑） ----------
interface PrizeRow {
  name: string
  type: PrizeType
  value: number | null
  weight: number | null
}
const loadingSettings = ref(true)
const saving = ref(false)
const form = ref({
  free_daily: 1 as number | null,
  balance_enable: false,
  balance_cost: 1 as number | null,
  traffic_enable: false,
  traffic_cost: 1024 as number | null,
  paid_daily: 0 as number | null,
  retention_days: 90 as number | null,
})
const prizes = ref<PrizeRow[]>([])

const typeOptions = [
  { label: '余额', value: 'balance' },
  { label: '到期时间', value: 'days' },
  { label: '流量', value: 'traffic' },
  { label: '谢谢惠顾', value: 'none' },
]
const suffix: Record<PrizeType, string> = { balance: '元', days: '天', traffic: 'MB', none: '' }

const totalWeight = computed(() => prizes.value.reduce((n, p) => n + Math.max(p.weight || 0, 0), 0))
function percent(p: PrizeRow) {
  if (!totalWeight.value || !p.weight) return '0%'
  return `${Number(((p.weight / totalWeight.value) * 100).toFixed(2))}%`
}

async function loadSettings() {
  loadingSettings.value = true
  try {
    const s = await admin.get<Settings>(`${base}/settings`)
    form.value = {
      free_daily: s.free_daily,
      balance_enable: s.balance_enable,
      balance_cost: toYuan(s.balance_cost),
      traffic_enable: s.traffic_enable,
      traffic_cost: s.traffic_cost,
      paid_daily: s.paid_daily,
      retention_days: s.retention_days,
    }
    prizes.value = (s.prizes || []).map((p) => ({ ...p, value: p.type === 'balance' ? toYuan(p.value) : p.value }))
  } catch (e) {
    message.error(errText(e))
  } finally {
    loadingSettings.value = false
  }
}

function addPrize() {
  prizes.value.push({ name: '', type: 'none', value: null, weight: 1 })
}
function setType(p: PrizeRow, v: unknown) {
  p.type = v as PrizeType
  if (p.type === 'none') p.value = null
}

async function save() {
  const f = form.value
  const body: Settings = {
    free_daily: f.free_daily ?? 0,
    balance_enable: f.balance_enable,
    balance_cost: toCents(f.balance_cost) ?? 0,
    traffic_enable: f.traffic_enable,
    traffic_cost: f.traffic_cost ?? 0,
    paid_daily: f.paid_daily ?? 0,
    retention_days: f.retention_days ?? 0,
    prizes: prizes.value.map((p) => ({
      name: p.name.trim(),
      type: p.type,
      value: p.type === 'none' ? 0 : p.type === 'balance' ? (toCents(p.value) ?? 0) : Math.round(p.value ?? 0),
      weight: Math.round(p.weight ?? 0),
    })),
  }
  saving.value = true
  if (await run(() => admin.post(`${base}/settings`, body), '保存成功')) await loadSettings()
  saving.value = false
}

// ---------- 抽奖记录 ----------
const email = ref('')
const { rows, loading, pager, load, reload, setPage, setPageSize } = usePaged<WheelLog>(async (current, page_size) => {
  const r = await admin.get<PageResult<WheelLog>>(`${base}/records`, { current, page_size, email: email.value.trim() || undefined })
  return { rows: r.data || [], total: r.total }
})
const columns: Column[] = [
  { key: 'created_at', title: '时间', class: 'w-40' },
  { key: 'email', title: '用户' },
  { key: 'subscription_id', title: '订阅' },
  { key: 'prize', title: '奖品' },
  { key: 'cost', title: '消耗', align: 'right' },
]
const subLabel = (l: WheelLog) => (l.subscription_name ? `${l.subscription_name}（#${l.subscription_id}）` : `#${l.subscription_id}`)
function prizeText(l: WheelLog) {
  switch (l.prize_type) {
    case 'balance':
      return `余额 ${formatMoney(l.prize_value)} 元`
    case 'days':
      return `时长 ${l.prize_value} 天`
    case 'traffic':
      return `本周期流量 ${formatBytes(l.prize_value)}`
  }
  return ''
}
function costText(l: WheelLog) {
  if (l.cost_type === 'balance') return `余额 ${formatMoney(l.cost)} 元`
  if (l.cost_type === 'traffic') return `流量 ${formatBytes(l.cost)}`
  return '免费'
}

onMounted(() => {
  loadSettings()
  load()
})
</script>

<template>
  <div>
    <PageHeader title="幸运转盘" description="内置插件「幸运大转盘」：用户在仪表盘点击「去参加」进入转盘页抽奖。">
      <Button variant="ghost" size="icon" :disabled="loading || loadingSettings" @click="tab === 'settings' ? loadSettings() : load()">
        <RefreshCwIcon :class="loading || loadingSettings ? 'animate-spin' : ''" />
      </Button>
    </PageHeader>

    <Tabs v-model="tab">
      <TabsList class="mb-3">
        <TabsTrigger value="settings">规则与奖品</TabsTrigger>
        <TabsTrigger value="records">抽奖记录</TabsTrigger>
      </TabsList>

      <TabsContent value="settings" class="space-y-4">
        <Card class="gap-4 py-5">
          <CardHeader class="px-5"><CardTitle class="text-base">抽奖规则</CardTitle></CardHeader>
          <CardContent class="grid gap-4 px-5 md:grid-cols-2">
            <FormItem label="每日免费次数" help="每个用户每天可免费抽奖的次数，0 表示没有免费次数">
              <NumInput v-model="form.free_daily" :min="0" :max="100" suffix="次" />
            </FormItem>
            <FormItem label="每日付费次数上限" help="免费次数用完后，每天最多付费抽奖的次数，0 表示不限">
              <NumInput v-model="form.paid_daily" :min="0" :max="1000" suffix="次" />
            </FormItem>
            <FormItem label="消耗余额抽奖" help="免费次数用完后，可扣除余额抽奖一次（记入余额明细）">
              <div class="flex items-center gap-3">
                <BoolSwitch v-model="form.balance_enable" />
                <NumInput v-model="form.balance_cost" class="flex-1" :min="0.01" :step="0.01" suffix="元 / 次" :disabled="!form.balance_enable" />
              </div>
            </FormItem>
            <FormItem label="消耗流量抽奖" help="免费次数用完后，可从所选订阅扣除流量抽奖一次（计入本周期已用流量）">
              <div class="flex items-center gap-3">
                <BoolSwitch v-model="form.traffic_enable" />
                <NumInput v-model="form.traffic_cost" class="flex-1" :min="1" suffix="MB / 次" :disabled="!form.traffic_enable" />
              </div>
            </FormItem>
            <FormItem label="记录保留天数" help="超过天数的抽奖记录每天自动清理">
              <NumInput v-model="form.retention_days" :min="1" :max="3650" suffix="天" />
            </FormItem>
          </CardContent>
        </Card>

        <Card class="gap-4 py-5">
          <CardHeader class="flex flex-row items-center justify-between px-5">
            <CardTitle class="text-base">奖品（{{ prizes.length }} / {{ MAX_PRIZES }}）</CardTitle>
            <Button size="sm" variant="outline" :disabled="prizes.length >= MAX_PRIZES" @click="addPrize"><PlusIcon />添加奖品</Button>
          </CardHeader>
          <CardContent class="space-y-3 px-5">
            <p class="text-sm text-muted-foreground">
              概率按权重换算。流量奖品只在当前流量周期内有效，重置流量、续费或更换套餐时收回；到期时间奖品对长期有效 / 一次性订阅不可抽，此时按其余奖品的权重重新换算概率。
            </p>
            <div class="overflow-x-auto">
              <table class="w-full min-w-[720px] text-sm">
                <thead class="text-muted-foreground">
                  <tr class="border-b">
                    <th class="py-2 pr-2 text-left font-medium">名称</th>
                    <th class="w-36 py-2 pr-2 text-left font-medium">类型</th>
                    <th class="w-44 py-2 pr-2 text-left font-medium">数值</th>
                    <th class="w-32 py-2 pr-2 text-left font-medium">权重</th>
                    <th class="w-20 py-2 pr-2 text-right font-medium">概率</th>
                    <th class="w-12 py-2" />
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(p, i) in prizes" :key="i" class="border-b last:border-0">
                    <td class="py-2 pr-2"><Input v-model="p.name" maxlength="16" placeholder="显示在转盘上，最多 16 字" /></td>
                    <td class="py-2 pr-2"><SelectBox :model-value="p.type" :options="typeOptions" @update:model-value="(v) => setType(p, v)" /></td>
                    <td class="py-2 pr-2">
                      <NumInput
                        v-if="p.type !== 'none'"
                        v-model="p.value"
                        :min="p.type === 'balance' ? 0.01 : 1"
                        :step="p.type === 'balance' ? 0.01 : 1"
                        :suffix="suffix[p.type]"
                      />
                      <span v-else class="text-muted-foreground">-</span>
                    </td>
                    <td class="py-2 pr-2"><NumInput v-model="p.weight" :min="0" :max="1000000" /></td>
                    <td class="py-2 pr-2 text-right tabular-nums">{{ percent(p) }}</td>
                    <td class="py-2 text-right">
                      <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" :disabled="prizes.length <= 2" @click="prizes.splice(i, 1)">
                        <Trash2Icon />
                      </Button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>

        <div class="flex justify-end">
          <Button :disabled="saving || loadingSettings" @click="save"><Loader2Icon v-if="saving" class="animate-spin" />保存</Button>
        </div>
      </TabsContent>

      <TabsContent value="records">
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <Input v-model="email" class="w-64" placeholder="用户邮箱（精确匹配）" @keyup.enter="reload" />
          <Button variant="outline" @click="reload"><SearchIcon />查询</Button>
        </div>
        <DataTable :columns="columns" :rows="rows" :loading="loading">
          <template #cell-created_at="{ row }"><span class="whitespace-nowrap text-xs">{{ formatTime(row.created_at) }}</span></template>
          <template #cell-email="{ row }">{{ row.email || `#${row.user_id}` }}</template>
          <template #cell-subscription_id="{ row }">
            <span class="block max-w-48 truncate" :title="subLabel(row)">{{ subLabel(row) }}</span>
          </template>
          <template #cell-prize="{ row }">
            <span class="font-medium">{{ row.prize_name }}</span>
            <span v-if="row.prize_type !== 'none'" class="text-muted-foreground"> · {{ prizeText(row) }}</span>
          </template>
          <template #cell-cost="{ row }"><span class="whitespace-nowrap">{{ costText(row) }}</span></template>
        </DataTable>
        <Pager :pager="pager" @page="setPage" @size="setPageSize" />
      </TabsContent>
    </Tabs>
  </div>
</template>
