<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { SearchIcon, RefreshCwIcon, PlusIcon, ChevronDownIcon, XIcon, CopyIcon, Loader2Icon, RotateCcwIcon } from '@lucide/vue'
import { copyText, formatMoney, formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Switch } from '@gv/shared/ui/switch'
import { Label } from '@gv/shared/ui/label'
import { Separator } from '@gv/shared/ui/separator'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@gv/shared/ui/dropdown-menu'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import FormItem from '../components/common/FormItem.vue'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import FilterEditor, { type FilterField } from '../components/FilterEditor.vue'
import AssignOrder from '../components/AssignOrder.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { usePaged } from '../paged'
import { commissionStatus, orderStatus, orderType, periodLabels, type Filter, type PageResult } from '../helpers'
import { loadPlans, planOptions } from '../refs'
import type { Order, OrderDetail } from '../types'

const route = useRoute()
const router = useRouter()

const filterFields = computed<FilterField[]>(() => [
  { label: '订单号', value: 'trade_no', condition: '模糊' },
  { label: '用户邮箱', value: 'email', condition: '模糊' },
  {
    label: '订单状态',
    value: 'status',
    options: [
      { label: '未支付', value: '0' },
      { label: '开通中', value: '1' },
      { label: '已取消', value: '2' },
      { label: '已完成', value: '3' },
      { label: '已折抵', value: '4' },
    ],
  },
  {
    label: '佣金状态',
    value: 'commission_status',
    options: [
      { label: '待确认', value: '0' },
      { label: '发放中', value: '1' },
      { label: '已发放', value: '2' },
      { label: '无效', value: '3' },
    ],
  },
  { label: '订阅计划', value: 'plan_id', options: planOptions.value.map((p) => ({ label: p.label, value: String(p.value) })) },
  { label: '用户ID', value: 'user_id' },
  { label: '邀请人ID', value: 'invite_user_id' },
  { label: '订阅ID', value: 'subscription_id' },
  { label: '回调单号', value: 'callback_no', condition: '模糊' },
  { label: '佣金金额(分)', value: 'commission_balance' },
])
const filter = ref<Filter[]>([])
const keyword = ref('')
const commissionOnly = ref(false)

const { rows, loading, pager, load, reload, setPage, setPageSize } = usePaged<Order>(async (current, page_size) => {
  const f = [...filter.value]
  if (keyword.value.trim()) f.push({ key: 'trade_no', condition: '模糊', value: keyword.value.trim() })
  const r = await admin.post<PageResult<Order>>('/orders/query', { current, page_size, filter: f, is_commission: commissionOnly.value })
  return { rows: r.data || [], total: r.total }
})

const fieldLabel = (k: string) => filterFields.value.find((f) => f.value === k)?.label || k
function valueLabel(f: Filter) {
  return filterFields.value.find((x) => x.value === f.key)?.options?.find((x) => x.value === f.value)?.label ?? f.value
}
function removeFilter(i: number) {
  filter.value = filter.value.filter((_, j) => j !== i)
  reload()
}

const columns: Column[] = [
  { key: 'trade_no', title: '# 订单号' },
  { key: 'email', title: '用户' },
  { key: 'type', title: '类型' },
  { key: 'plan', title: '订阅计划' },
  { key: 'period', title: '周期' },
  { key: 'total_amount', title: '支付金额', align: 'right' },
  { key: 'status', title: '订单状态' },
  { key: 'commission_balance', title: '佣金金额', align: 'right' },
  { key: 'commission_status', title: '佣金状态' },
  { key: 'created_at', title: '创建时间' },
]

const paid = (o: Order) => o.total_amount + (o.handling_amount || 0)
const planLabel = (o: Order) => (o.plan_id === 0 ? '充值' : o.plan_name || `#${o.plan_id}`)
const subLabel = (o: Order) => (o.subscription_name ? `${o.subscription_name}（#${o.subscription_id}）` : `订阅 #${o.subscription_id}`)

// ---------- 状态操作 ----------
async function markPaid(o: Order) {
  if (await confirm(`确定将订单 ${o.trade_no} 标记为已支付？\n标记为[已支付]后将会由系统进行开通后并完成。`, () => admin.post(`/orders/${o.trade_no}/paid`), '操作成功', false)) load()
}
async function cancel(o: Order) {
  if (await confirm(`确定取消订单 ${o.trade_no}？`, () => admin.post(`/orders/${o.trade_no}/cancel`), '操作成功')) load()
}
async function setCommission(o: Order, status: number) {
  if (await run(() => admin.put(`/orders/${o.trade_no}`, { commission_status: status }), '操作成功')) load()
}
// 已取消订单补单：支付方掉单（取消后才收到付款）时使用，会重新扣回下单时抵扣的余额
const restoring = ref<Order | null>(null)
const restoreShow = ref(false)
const restoreNo = ref('')
const restoreBusy = ref(false)
function restore(o: Order) {
  restoring.value = o
  restoreNo.value = ''
  restoreShow.value = true
}
async function submitRestore() {
  const o = restoring.value
  if (!o) return
  restoreBusy.value = true
  const ok = await run(() => admin.post(`/orders/${o.trade_no}/restore`, { callback_no: restoreNo.value.trim() }), '补单成功，系统将自动开通')
  restoreBusy.value = false
  if (!ok) return
  restoreShow.value = false
  load()
  if (sheet.value && detail.value?.trade_no === o.trade_no) open(o)
}

const commissionMarks = [
  { label: '待确认', value: 0 },
  { label: '有效', value: 1 },
  { label: '无效', value: 3 },
]

// ---------- 详情 ----------
const detail = ref<OrderDetail | null>(null)
const detailRow = ref<Order | null>(null)
const sheet = ref(false)
async function open(o: Order) {
  detailRow.value = o
  await run(async () => {
    detail.value = await admin.get<OrderDetail>(`/orders/${o.trade_no}`)
    sheet.value = true
  })
}
async function copy(s: string) {
  message[(await copyText(s)) ? 'success' : 'error']('复制成功')
}

// ---------- 添加订单 ----------
const assign = ref(false)

onMounted(() => {
  const uid = route.query.user_id
  if (uid) filter.value = [{ key: 'user_id', condition: '=', value: String(uid) }]
  loadPlans()
  load()
})
</script>

<template>
  <div>
    <PageHeader title="订单管理">
      <Button @click="assign = true"><PlusIcon />添加订单</Button>
    </PageHeader>

    <div class="mb-3 flex flex-wrap items-center gap-2">
      <div class="relative w-full sm:w-64">
        <SearchIcon class="absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input v-model="keyword" class="pl-8" placeholder="搜索订单号" @keyup.enter="reload" />
      </div>
      <FilterEditor v-model="filter" :fields="filterFields" @apply="reload" />
      <div class="flex items-center gap-2 px-2">
        <Switch id="commission-only" :model-value="commissionOnly" @update:model-value="(v: boolean) => { commissionOnly = v; reload() }" />
        <Label for="commission-only" class="font-normal">只看佣金订单</Label>
      </div>
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <div v-if="filter.length" class="flex flex-wrap items-center gap-1.5">
        <span v-for="(f, i) in filter" :key="i" class="inline-flex items-center gap-1 rounded-md border bg-muted/50 px-2 py-1 text-xs">
          {{ fieldLabel(f.key) }} {{ f.condition }} {{ valueLabel(f) }}
          <XIcon class="size-3 cursor-pointer opacity-60 hover:opacity-100" @click="removeFilter(i)" />
        </span>
      </div>
    </div>

    <DataTable :columns="columns" :rows="rows" :loading="loading">
      <template #cell-trade_no="{ row }">
        <button class="font-mono text-xs hover:text-primary hover:underline" @click="open(row)">{{ row.trade_no }}</button>
      </template>
      <template #cell-email="{ row }">
        <button class="max-w-56 truncate text-left hover:text-primary hover:underline" @click="router.push({ path: '/users', query: { id: row.user_id } })">
          {{ row.email || `#${row.user_id}` }}
        </button>
      </template>
      <template #cell-type="{ row }"><StatusBadge :tone="row.type === 1 ? 'primary' : row.type === 9 ? 'success' : 'default'">{{ orderType[row.type] || row.type }}</StatusBadge></template>
      <template #cell-plan="{ row }">
        <div class="grid leading-tight">
          <span>{{ planLabel(row) }}</span>
          <span v-if="row.subscription_id" class="max-w-48 truncate text-xs text-muted-foreground">{{ subLabel(row) }}</span>
        </div>
      </template>
      <template #cell-period="{ row }"><span class="whitespace-nowrap">{{ periodLabels[row.period] || row.period }}</span></template>
      <template #cell-total_amount="{ row }"><span class="tabular-nums">{{ formatMoney(paid(row)) }}</span></template>
      <template #cell-status="{ row }">
        <DropdownMenu v-if="row.status === 0">
          <DropdownMenuTrigger as-child>
            <button class="inline-flex items-center gap-0.5">
              <StatusBadge :tone="orderStatus[row.status]?.[1]" dot>{{ orderStatus[row.status]?.[0] }}</StatusBadge>
              <ChevronDownIcon class="size-3.5 text-muted-foreground" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuItem @click="markPaid(row)">标记为 已支付</DropdownMenuItem>
            <DropdownMenuItem variant="destructive" @click="cancel(row)">标记为 取消</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <DropdownMenu v-else-if="row.status === 2">
          <DropdownMenuTrigger as-child>
            <button class="inline-flex items-center gap-0.5">
              <StatusBadge :tone="orderStatus[row.status]?.[1]" dot>{{ orderStatus[row.status]?.[0] }}</StatusBadge>
              <ChevronDownIcon class="size-3.5 text-muted-foreground" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuItem @click="restore(row)">补单（已收到付款）</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <StatusBadge v-else :tone="orderStatus[row.status]?.[1]" dot>{{ orderStatus[row.status]?.[0] }}</StatusBadge>
      </template>
      <template #cell-commission_balance="{ row }">
        <span class="tabular-nums" :class="row.commission_balance > 0 ? '' : 'text-muted-foreground'">{{ row.commission_balance > 0 ? formatMoney(row.commission_balance) : '-' }}</span>
      </template>
      <template #cell-commission_status="{ row }">
        <template v-if="row.commission_balance > 0">
          <DropdownMenu v-if="row.commission_status !== 2">
            <DropdownMenuTrigger as-child>
              <button class="inline-flex items-center gap-0.5">
                <StatusBadge :tone="commissionStatus[row.commission_status]?.[1]">{{ commissionStatus[row.commission_status]?.[0] }}</StatusBadge>
                <ChevronDownIcon class="size-3.5 text-muted-foreground" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" class="w-64">
              <DropdownMenuLabel class="text-xs font-normal text-muted-foreground">标记为[有效]后将会由系统处理后发放到用户并完成</DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuItem v-for="m in commissionMarks" :key="m.value" :disabled="m.value === row.commission_status" @click="setCommission(row, m.value)">
                标记为 {{ m.label }}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <StatusBadge v-else :tone="commissionStatus[row.commission_status]?.[1]">{{ commissionStatus[row.commission_status]?.[0] }}</StatusBadge>
        </template>
        <span v-else class="text-muted-foreground">-</span>
      </template>
      <template #cell-created_at="{ row }"><span class="whitespace-nowrap text-muted-foreground">{{ formatTime(row.created_at) }}</span></template>
    </DataTable>
    <Pager :pager="pager" @page="setPage" @size="setPageSize" />

    <AssignOrder v-model:show="assign" @done="reload" />

    <Dialog v-model:open="sheet">
      <DialogContent class="max-h-[90vh] gap-0 overflow-y-auto p-0 sm:max-w-xl">
        <DialogHeader class="border-b px-6 py-4">
          <DialogTitle>订单信息</DialogTitle>
          <DialogDescription v-if="detail" class="flex items-center gap-2">
            <span class="font-mono">{{ detail.trade_no }}</span>
            <CopyIcon class="size-3.5 cursor-pointer hover:text-foreground" @click="copy(detail.trade_no)" />
          </DialogDescription>
        </DialogHeader>
        <div v-if="detail" class="grid gap-5 px-6 py-5 text-sm">
          <div class="flex items-center gap-2">
            <StatusBadge :tone="orderStatus[detail.status]?.[1]" dot>{{ orderStatus[detail.status]?.[0] }}</StatusBadge>
            <StatusBadge>{{ orderType[detail.type] }}</StatusBadge>
            <StatusBadge v-if="detail.immediate" tone="warning">立即生效</StatusBadge>
            <Button v-if="detail.status === 2" size="sm" variant="outline" class="ml-auto" @click="restore(detail)"><RotateCcwIcon />补单</Button>
          </div>
          <dl class="grid grid-cols-[6rem_1fr] gap-x-4 gap-y-2.5">
            <dt class="text-muted-foreground">邮箱</dt>
            <dd>
              <button class="hover:text-primary hover:underline" @click="router.push({ path: '/users', query: { id: detail.user_id } })">
                {{ detail.email || detailRow?.email || `#${detail.user_id}` }}
              </button>
            </dd>
            <dt class="text-muted-foreground">订阅计划</dt>
            <dd>{{ planLabel(detail) }} · {{ periodLabels[detail.period] || detail.period }}</dd>
            <template v-if="detail.subscription_id">
              <dt class="text-muted-foreground">订阅</dt>
              <dd>{{ subLabel(detail) }}</dd>
            </template>
            <dt class="text-muted-foreground">回调单号</dt>
            <dd class="break-all font-mono text-xs">{{ detail.callback_no || '-' }}</dd>
            <dt class="text-muted-foreground">支付时间</dt>
            <dd>{{ formatTime(detail.paid_at) }}</dd>
            <dt class="text-muted-foreground">创建时间</dt>
            <dd>{{ formatTime(detail.created_at) }}</dd>
            <dt class="text-muted-foreground">更新时间</dt>
            <dd>{{ formatTime(detail.updated_at) }}</dd>
          </dl>
          <Separator />
          <dl class="grid grid-cols-[6rem_1fr] gap-x-4 gap-y-2.5">
            <dt class="text-muted-foreground">支付金额</dt>
            <dd class="font-medium tabular-nums">{{ formatMoney(paid(detail)) }}<span v-if="detail.handling_amount" class="text-xs text-muted-foreground">（含手续费 {{ formatMoney(detail.handling_amount) }}）</span></dd>
            <dt class="text-muted-foreground">余额支付</dt>
            <dd class="tabular-nums">{{ formatMoney(detail.balance_amount) }}</dd>
            <dt class="text-muted-foreground">优惠金额</dt>
            <dd class="tabular-nums">{{ formatMoney(detail.discount_amount) }}</dd>
            <dt class="text-muted-foreground">退回金额</dt>
            <dd class="tabular-nums">{{ formatMoney(detail.refund_amount) }}</dd>
            <dt class="text-muted-foreground">折抵金额</dt>
            <dd class="tabular-nums">{{ formatMoney(detail.surplus_amount) }}</dd>
          </dl>
          <template v-if="detail.invite_user_id">
            <Separator />
            <dl class="grid grid-cols-[6rem_1fr] gap-x-4 gap-y-2.5">
              <dt class="text-muted-foreground">邀请人</dt>
              <dd>
                <button class="hover:text-primary hover:underline" @click="router.push({ path: '/users', query: { id: detail.invite_user_id } })">#{{ detail.invite_user_id }}</button>
                <button class="ml-3 text-xs text-muted-foreground hover:text-primary" @click="router.push({ path: '/users', query: { invite_user_id: detail.invite_user_id } })">查看TA邀请的人</button>
              </dd>
              <dt class="text-muted-foreground">佣金金额</dt>
              <dd class="tabular-nums">{{ formatMoney(detail.commission_balance) }}</dd>
              <dt class="text-muted-foreground">实际发放</dt>
              <dd class="tabular-nums">{{ formatMoney(detail.actual_commission_balance) }}</dd>
              <dt class="text-muted-foreground">佣金状态</dt>
              <dd><StatusBadge :tone="commissionStatus[detail.commission_status]?.[1]">{{ commissionStatus[detail.commission_status]?.[0] }}</StatusBadge></dd>
            </dl>
          </template>
          <template v-if="detail.adjustments?.length">
            <Separator />
            <div>
              <div class="mb-2 font-medium">插件调整明细<span class="ml-2 text-xs font-normal text-muted-foreground">减免已计入优惠金额</span></div>
              <div v-for="(a, i) in detail.adjustments" :key="i" class="flex justify-between py-1 text-xs text-muted-foreground">
                <span>{{ a.label }}<span class="ml-2 font-mono">{{ a.plugin }}</span></span>
                <span class="tabular-nums text-foreground">{{ (a.amount < 0 ? '-' : '+') + formatMoney(Math.abs(a.amount)) }}</span>
              </div>
            </div>
          </template>
          <template v-if="detail.commission_log.length">
            <Separator />
            <div>
              <div class="mb-2 font-medium">佣金发放记录</div>
              <div v-for="l in detail.commission_log" :key="l.id" class="flex justify-between py-1 text-xs text-muted-foreground">
                <span>{{ formatTime(l.created_at) }} · 邀请人 #{{ l.invite_user_id }}</span>
                <span class="tabular-nums text-foreground">{{ formatMoney(l.get_amount) }}</span>
              </div>
            </div>
          </template>
          <template v-if="detail.surplus_orders.length">
            <Separator />
            <div>
              <div class="mb-2 font-medium">被折抵的订单</div>
              <div v-for="s in detail.surplus_orders" :key="s.id" class="flex justify-between py-1 text-xs text-muted-foreground">
                <span class="font-mono">{{ s.trade_no }}</span>
                <span class="tabular-nums">{{ formatMoney(s.total_amount) }} · {{ formatTime(s.created_at) }}</span>
              </div>
            </div>
          </template>
        </div>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="restoreShow">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>已取消订单补单</DialogTitle>
          <DialogDescription v-if="restoring" class="font-mono">{{ restoring.trade_no }}</DialogDescription>
        </DialogHeader>
        <Alert>
          <AlertDescription class="text-xs leading-relaxed">
            用于支付方掉单：订单超时取消后才收到用户付款。补单后订单变为已支付并由系统自动开通。
            下单时使用了余额抵扣的，会重新扣回取消时退回的余额；用户余额不足时无法补单。请先在支付平台核实已到账。
          </AlertDescription>
        </Alert>
        <FormItem label="支付方单号" help="可选，留空记为 manual_restore">
          <Input v-model="restoreNo" placeholder="支付平台的交易号" maxlength="255" />
        </FormItem>
        <DialogFooter>
          <Button variant="outline" @click="restoreShow = false">取消</Button>
          <Button :disabled="restoreBusy" @click="submitRestore">
            <Loader2Icon v-if="restoreBusy" class="animate-spin" />
            确认补单
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
