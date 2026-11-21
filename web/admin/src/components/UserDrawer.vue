<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  Loader2Icon, PlusIcon, MoreHorizontalIcon, PencilIcon, CopyIcon, StarIcon, RotateCcwIcon, Trash2Icon, ChevronDownIcon,
  ActivityIcon, MonitorSmartphoneIcon,
} from '@lucide/vue'
import { copyText, formatBytes, formatMoney, formatTime, nowUnix, daysLeft } from '@gv/shared'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@gv/shared/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@gv/shared/ui/tabs'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Progress } from '@gv/shared/ui/progress'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@gv/shared/ui/dropdown-menu'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@gv/shared/ui/collapsible'
import StatusBadge from './common/StatusBadge.vue'
import FormItem from './common/FormItem.vue'
import NumInput from './common/NumInput.vue'
import SelectBox from './common/SelectBox.vue'
import BoolSwitch from './common/BoolSwitch.vue'
import DataTable from './common/DataTable.vue'
import Pager from './common/Pager.vue'
import SubForm from './SubForm.vue'
import { admin } from '../api'
import { confirm, errText, message, run } from '../ui'
import { toCents, toYuan, type PageResult } from '../helpers'
import { groupName, loadGroups } from '../refs'
import { usePaged } from '../paged'
import type { Sub, UserDetail } from '../types'

const props = withDefaults(defineProps<{ userId: number | null; tab?: string }>(), { tab: 'subs' })
const show = defineModel<boolean>('show', { required: true })
const emit = defineEmits<{ changed: [] }>()

const user = ref<UserDetail | null>(null)
const loading = ref(false)
const tab = ref('subs')

interface Account {
  email: string
  password: string
  balance: number | null
  commission_balance: number | null
  commission_type: number
  commission_rate: number | null
  discount: number | null
  subscription_limit: number | null
  banned: boolean
  is_admin: boolean
  is_staff: boolean
  remarks: string
}
const acc = ref<Account | null>(null)
let accOrigin: Account | null = null
const inviter = ref('')
const savingAcc = ref(false)
const balanceRemark = ref('')
const balanceChanged = computed(() => !!acc.value && !!accOrigin && acc.value.balance !== accOrigin.balance)

async function load() {
  if (!props.userId) return
  loading.value = true
  try {
    const [u] = await Promise.all([admin.get<UserDetail>(`/users/${props.userId}`), loadGroups()])
    user.value = u
    acc.value = {
      email: u.email,
      password: '',
      balance: toYuan(u.balance),
      commission_balance: toYuan(u.commission_balance),
      commission_type: u.commission_type,
      commission_rate: u.commission_rate,
      discount: u.discount,
      subscription_limit: u.subscription_limit,
      banned: !!u.banned,
      is_admin: !!u.is_admin,
      is_staff: !!u.is_staff,
      remarks: u.remarks || '',
    }
    accOrigin = { ...acc.value }
    balanceRemark.value = ''
    inviter.value = u.invite_user?.email || ''
  } catch (e) {
    message.error(errText(e))
  } finally {
    loading.value = false
  }
}

watch(show, (v) => {
  if (v) {
    user.value = null
    tab.value = props.tab
    trafficSub.value = 0
    load()
    if (props.tab === 'traffic') traffic.reload()
    if (props.tab === 'balance') {
      balanceType.value = ''
      balanceLogs.reload()
    }
  }
})
watch(tab, (t) => {
  if (t === 'traffic' && show.value) traffic.reload()
  if (t === 'balance' && show.value) balanceLogs.reload()
})

async function saveAccount() {
  if (!acc.value || !accOrigin) return
  const a = acc.value
  const o = accOrigin
  const body: Record<string, unknown> = {}
  if (a.email !== o.email) body.email = a.email
  if (a.password) body.password = a.password
  if (a.balance !== o.balance) {
    body.balance = toCents(a.balance) ?? 0
    if (balanceRemark.value.trim()) body.balance_remark = balanceRemark.value.trim()
  }
  if (a.commission_balance !== o.commission_balance) body.commission_balance = toCents(a.commission_balance) ?? 0
  if (a.commission_type !== o.commission_type) body.commission_type = a.commission_type
  if (a.commission_rate !== o.commission_rate) body.commission_rate = a.commission_rate
  if (a.discount !== o.discount) body.discount = a.discount
  if (a.subscription_limit !== o.subscription_limit) body.subscription_limit = a.subscription_limit
  for (const k of ['banned', 'is_admin', 'is_staff'] as const) if (a[k] !== o[k]) body[k] = a[k] ? 1 : 0
  if (a.remarks !== o.remarks) body.remarks = a.remarks || null
  if (!Object.keys(body).length) {
    message.info('没有修改')
    return
  }
  savingAcc.value = true
  const ok = await run(() => admin.put(`/users/${props.userId}`, body), '保存成功')
  savingAcc.value = false
  if (ok) {
    await load()
    emit('changed')
  }
}

async function saveInviter() {
  if (await run(() => admin.post(`/users/${props.userId}/inviter`, { invite_user_email: inviter.value.trim() }), '保存成功')) {
    await load()
  }
}

async function unbindTelegram() {
  if (await confirm('确定解除该用户的 Telegram 绑定？', () => admin.put(`/users/${props.userId}`, { telegram_id: null }))) load()
}

async function removeUser() {
  const ok = await confirm(`确定要删除 ${user.value?.email} 的用户信息吗？其订阅、订单、工单等将一并删除，且不可恢复。`, () => admin.del(`/users/${props.userId}`), '删除成功')
  if (ok) {
    show.value = false
    emit('changed')
  }
}

// ---------- 订阅 ----------
const subForm = ref(false)
const editing = ref<Sub | null>(null)
function openSub(s: Sub | null) {
  editing.value = s
  subForm.value = true
}
async function onSubSaved() {
  await load()
  emit('changed')
}
async function copyUrl(s: Sub) {
  message[(await copyText(s.subscribe_url || '')) ? 'success' : 'error']('复制成功')
}
async function setPrimary(s: Sub) {
  if (await run(() => admin.post(`/subscriptions/${s.id}/primary`), '已设为主订阅')) onSubSaved()
}
async function resetSub(s: Sub) {
  if (await confirm(`确定要重置订阅 #${s.id} 的 UUID 及订阅 URL 吗？旧的订阅链接将立即失效。`, () => admin.post(`/subscriptions/${s.id}/reset-security`), '重置成功')) onSubSaved()
}
async function removeSub(s: Sub) {
  if (await confirm(`确定删除订阅 #${s.id}？`, () => admin.del(`/subscriptions/${s.id}`), '删除成功')) onSubSaved()
}
function showTraffic(s: Sub) {
  trafficSub.value = s.id
  tab.value = 'traffic'
  traffic.reload()
}

const pct = (s: Sub) => (s.transfer_enable ? Math.min(100, Math.round(((s.u + s.d) / s.transfer_enable) * 100)) : 0)
function expireText(s: Sub) {
  if (!s.plan_id || s.expired_at === 0) return '-'
  if (s.expired_at === null) return '长期有效'
  if (s.expired_at <= nowUnix()) return `已到期 ${formatTime(s.expired_at, false)}`
  return `${formatTime(s.expired_at)}（剩余 ${daysLeft(s.expired_at)} 天）`
}
const expired = (s: Sub) => !!s.plan_id && !!s.expired_at && s.expired_at <= nowUnix()

// ---------- 流量记录 ----------
interface StatRow {
  id: number
  subscription_id: number | null
  server_rate: number
  u: number
  d: number
  record_at: number
}
const trafficSub = ref(0)
const traffic = usePaged<StatRow>(async (current, page_size) => {
  if (!props.userId) return { rows: [], total: 0 }
  const q = { user_id: props.userId, current, page_size, subscription_id: trafficSub.value || undefined }
  const r = await admin.get<PageResult<StatRow>>('/stat/user', q)
  return { rows: r.data || [], total: r.total }
}, 20)
const trafficSubOptions = computed(() => [
  { label: '全部订阅', value: 0 },
  ...(user.value?.subscriptions || []).map((s) => ({ label: `#${s.id} ${s.name || s.plan_name || ''}`, value: s.id })),
])
const trafficCols = [
  { key: 'record_at', title: '记录时间' },
  { key: 'subscription_id', title: '订阅' },
  { key: 'u', title: '实际上行', align: 'right' as const },
  { key: 'd', title: '实际下行', align: 'right' as const },
  { key: 'server_rate', title: '扣费倍率', align: 'right' as const },
  { key: 'total', title: '合计', align: 'right' as const },
]

// ---------- 余额明细 ----------
interface BalanceLogRow {
  id: number
  amount: number
  balance: number
  type: string
  trade_no: string
  operator_id: number | null
  operator_email: string
  plugin: string
  remark: string
  created_at: number
}
const balanceTypeNames: Record<string, string> = {
  order_pay: '下单抵扣',
  order_cancel: '取消订单退回',
  order_restore: '补单扣回',
  renewal: '自动续费',
  deposit: '充值到账',
  plan_refund: '换套餐差额退回',
  giftcard: '礼品卡',
  commission: '邀请佣金',
  transfer: '佣金转余额',
  admin: '管理员修改',
  plugin: '插件',
}
const balanceTypeOptions = [{ label: '全部类型', value: '' }, ...Object.entries(balanceTypeNames).map(([value, label]) => ({ label, value }))]
const balanceType = ref('')
const balanceLogs = usePaged<BalanceLogRow>(async (current, page_size) => {
  if (!props.userId) return { rows: [], total: 0 }
  const q = { current, page_size, type: balanceType.value || undefined }
  const r = await admin.get<PageResult<BalanceLogRow>>(`/users/${props.userId}/balance-logs`, q)
  return { rows: r.data || [], total: r.total }
}, 20)
const balanceCols = [
  { key: 'created_at', title: '时间' },
  { key: 'type', title: '类型' },
  { key: 'amount', title: '变动', align: 'right' as const },
  { key: 'balance', title: '变动后余额', align: 'right' as const },
  { key: 'trade_no', title: '关联订单' },
  { key: 'remark', title: '说明' },
]
function balanceNote(r: BalanceLogRow) {
  const parts: string[] = []
  if (r.operator_id) parts.push(`管理员 ${r.operator_email || `#${r.operator_id}`}`)
  if (r.plugin) parts.push(`插件 ${r.plugin}`)
  if (r.remark) parts.push(r.remark)
  return parts.join(' · ') || '-'
}

const commissionTypes = [
  { label: '跟随系统设置', value: 0 },
  { label: '循环返利', value: 1 },
  { label: '首次返利', value: 2 },
]
</script>

<template>
  <Sheet v-model:open="show">
    <SheetContent class="w-full gap-0 overflow-y-auto p-0 sm:max-w-3xl">
      <SheetHeader class="border-b px-6 py-4">
        <SheetTitle class="flex items-center gap-2">
          {{ user ? user.email : '用户详情' }}
          <template v-if="user">
            <span class="text-sm font-normal text-muted-foreground">#{{ user.id }}</span>
            <StatusBadge v-if="user.banned" tone="danger">封禁</StatusBadge>
            <StatusBadge v-if="user.is_admin" tone="primary">管理员</StatusBadge>
            <StatusBadge v-if="user.is_staff" tone="info">客服</StatusBadge>
          </template>
        </SheetTitle>
        <SheetDescription v-if="user" class="flex flex-wrap gap-x-5 gap-y-1">
          <span>加入时间 {{ formatTime(user.created_at) }}</span>
          <span>最后登录 {{ formatTime(user.last_login_at) }}</span>
          <span>邀请人 {{ user.invite_user ? `${user.invite_user.email}（#${user.invite_user.id}）` : '无' }}</span>
        </SheetDescription>
      </SheetHeader>

      <div v-if="loading && !user" class="flex h-64 items-center justify-center"><Loader2Icon class="size-6 animate-spin text-muted-foreground" /></div>

      <Tabs v-else-if="user && acc" v-model="tab" class="px-6 py-4">
        <TabsList>
          <TabsTrigger value="subs">订阅（{{ user.subscriptions.length }}）</TabsTrigger>
          <TabsTrigger value="account">账号资料</TabsTrigger>
          <TabsTrigger value="traffic">流量记录</TabsTrigger>
          <TabsTrigger value="balance">余额明细</TabsTrigger>
        </TabsList>

        <TabsContent value="subs" class="mt-4 grid gap-3">
          <div class="flex items-center justify-between">
            <span class="text-xs text-muted-foreground">管理员新开订阅不受用户订阅数量上限限制</span>
            <Button size="sm" @click="openSub(null)"><PlusIcon />新开订阅</Button>
          </div>
          <div v-for="s in user.subscriptions" :key="s.id" class="rounded-lg border bg-card">
            <div class="flex items-start gap-3 p-4">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium">{{ s.plan_name || '无订阅' }}</span>
                  <span class="text-xs text-muted-foreground">#{{ s.id }}{{ s.name ? ` · ${s.name}` : '' }}</span>
                  <StatusBadge v-if="s.is_primary" tone="primary">主订阅</StatusBadge>
                  <StatusBadge v-if="s.banned" tone="danger">封禁</StatusBadge>
                  <StatusBadge v-if="expired(s)" tone="danger">已到期</StatusBadge>
                </div>
                <div class="mt-3 grid gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
                  <div>
                    <div class="text-xs text-muted-foreground">流量</div>
                    <div class="tabular-nums">{{ formatBytes(s.u + s.d) }} / {{ formatBytes(s.transfer_enable) }}</div>
                    <Progress :model-value="pct(s)" :class="['mt-1 h-1.5', pct(s) >= 90 ? '[&>div]:bg-destructive' : '']" />
                  </div>
                  <div>
                    <div class="text-xs text-muted-foreground">到期时间</div>
                    <div :class="expired(s) ? 'text-destructive' : ''">{{ expireText(s) }}</div>
                  </div>
                  <div>
                    <div class="text-xs text-muted-foreground">权限组 / 设备</div>
                    <div>{{ groupName(s.group_id) }} · 在线 {{ s.alive_ip ?? 0 }} / {{ s.device_limit ?? '不限' }}</div>
                  </div>
                </div>
              </div>
              <DropdownMenu>
                <DropdownMenuTrigger as-child>
                  <Button variant="ghost" size="icon" class="size-8"><MoreHorizontalIcon /></Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" class="w-48">
                  <DropdownMenuItem @click="openSub(s)"><PencilIcon />编辑</DropdownMenuItem>
                  <DropdownMenuItem @click="copyUrl(s)"><CopyIcon />复制订阅URL</DropdownMenuItem>
                  <DropdownMenuItem v-if="!s.is_primary" @click="setPrimary(s)"><StarIcon />设为主订阅</DropdownMenuItem>
                  <DropdownMenuItem @click="resetSub(s)"><RotateCcwIcon />重置UUID及订阅URL</DropdownMenuItem>
                  <DropdownMenuItem @click="showTraffic(s)"><ActivityIcon />流量记录</DropdownMenuItem>
                  <template v-if="!s.is_primary">
                    <DropdownMenuSeparator />
                    <DropdownMenuItem variant="destructive" @click="removeSub(s)"><Trash2Icon />删除订阅</DropdownMenuItem>
                  </template>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
            <Collapsible v-slot="{ open }" class="border-t">
              <CollapsibleTrigger class="flex w-full items-center gap-1 px-4 py-2 text-xs text-muted-foreground hover:text-foreground">
                <MonitorSmartphoneIcon class="size-3.5" />
                订阅信息与在线设备
                <ChevronDownIcon :class="['ml-auto size-3.5 transition-transform', open ? 'rotate-180' : '']" />
              </CollapsibleTrigger>
              <CollapsibleContent class="grid gap-2 px-4 pb-3 text-xs">
                <div class="grid grid-cols-[4rem_1fr] gap-x-3 gap-y-1">
                  <span class="text-muted-foreground">UUID</span><span class="break-all font-mono">{{ s.uuid }}</span>
                  <span class="text-muted-foreground">Token</span><span class="break-all font-mono">{{ s.token }}</span>
                  <span class="text-muted-foreground">订阅URL</span><span class="break-all font-mono">{{ s.subscribe_url }}</span>
                  <span class="text-muted-foreground">近期 IP</span><span>{{ s.share_ips ?? 0 }}</span>
                </div>
                <div v-if="s.devices?.length" class="flex flex-wrap gap-1.5">
                  <span v-for="d in s.devices" :key="d.ip + d.node" class="rounded bg-muted px-2 py-0.5">{{ d.ip }} · {{ d.node }} · {{ formatTime(d.update_at) }}</span>
                </div>
                <div v-else class="text-muted-foreground">当前没有在线设备</div>
              </CollapsibleContent>
            </Collapsible>
          </div>
          <div v-if="!user.subscriptions.length" class="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">该用户还没有订阅</div>
        </TabsContent>

        <TabsContent value="account" class="mt-4">
          <div class="grid gap-4 sm:grid-cols-2">
            <FormItem label="邮箱"><Input v-model="acc.email" /></FormItem>
            <FormItem label="密码"><Input v-model="acc.password" type="password" autocomplete="new-password" placeholder="如需修改密码请输入" /></FormItem>
            <FormItem label="余额"><NumInput v-model="acc.balance" :step="0.01" suffix="元" /></FormItem>
            <FormItem v-if="balanceChanged" label="余额调整原因" help="选填，记录在余额明细中" class="sm:col-span-2">
              <Input v-model="balanceRemark" maxlength="255" placeholder="如：补偿节点故障" />
            </FormItem>
            <FormItem label="推广佣金"><NumInput v-model="acc.commission_balance" :step="0.01" suffix="元" /></FormItem>
            <FormItem label="佣金类型"><SelectBox :model-value="acc.commission_type" :options="commissionTypes" @update:model-value="(v) => (acc!.commission_type = Number(v))" /></FormItem>
            <FormItem label="推荐返利比例"><NumInput v-model="acc.commission_rate" :min="0" :max="100" suffix="%" placeholder="跟随系统设置" /></FormItem>
            <FormItem label="专享折扣比例"><NumInput v-model="acc.discount" :min="0" :max="100" suffix="%" placeholder="无" /></FormItem>
            <FormItem label="订阅数量上限"><NumInput v-model="acc.subscription_limit" :min="0" :max="1000" placeholder="跟随系统设置" /></FormItem>
            <div class="divide-y rounded-lg border sm:col-span-2">
              <label class="flex items-center justify-between gap-4 px-3 py-2.5 text-sm">账户状态（封禁） <BoolSwitch v-model="acc.banned" /></label>
              <label class="flex items-center justify-between gap-4 px-3 py-2.5 text-sm">是否管理员 <BoolSwitch v-model="acc.is_admin" /></label>
              <label class="flex items-center justify-between gap-4 px-3 py-2.5 text-sm">是否员工 <BoolSwitch v-model="acc.is_staff" /></label>
            </div>
            <FormItem label="备注" class="sm:col-span-2"><Textarea v-model="acc.remarks" rows="3" /></FormItem>
            <div class="flex justify-end sm:col-span-2">
              <Button :disabled="savingAcc" @click="saveAccount">
                <Loader2Icon v-if="savingAcc" class="animate-spin" />
                保存
              </Button>
            </div>
          </div>

          <div class="mt-6 grid gap-4 border-t pt-5">
            <FormItem label="邀请人邮箱" help="留空并保存表示清除邀请人">
              <div class="flex gap-2">
                <Input v-model="inviter" placeholder="请输入邀请人邮箱" />
                <Button variant="outline" @click="saveInviter">设置邀请人</Button>
              </div>
            </FormItem>
            <FormItem label="Telegram">
              <div class="flex items-center gap-3 text-sm">
                <span>{{ user.telegram_id ? `已绑定 ${user.telegram_id}` : '未绑定' }}</span>
                <Button v-if="user.telegram_id" size="sm" variant="outline" @click="unbindTelegram">解除绑定</Button>
              </div>
            </FormItem>
            <div class="flex items-center justify-between rounded-lg border border-destructive/30 bg-destructive/5 p-4">
              <div>
                <div class="text-sm font-medium">删除用户</div>
                <div class="text-xs text-muted-foreground">订阅、订单、工单等将一并删除，且不可恢复。</div>
              </div>
              <Button variant="destructive" @click="removeUser"><Trash2Icon />删除用户</Button>
            </div>
          </div>
        </TabsContent>

        <TabsContent value="traffic" class="mt-4">
          <div class="mb-3 flex items-center gap-2">
            <SelectBox class="w-64" :model-value="trafficSub" :options="trafficSubOptions" @update:model-value="(v) => { trafficSub = Number(v); traffic.reload() }" />
          </div>
          <DataTable :columns="trafficCols" :rows="traffic.rows.value" :loading="traffic.loading.value" empty="暂无流量记录">
            <template #cell-record_at="{ row }">{{ formatTime(row.record_at, false) }}</template>
            <template #cell-subscription_id="{ row }">{{ row.subscription_id ? `#${row.subscription_id}` : '-' }}</template>
            <template #cell-u="{ row }">{{ formatBytes(row.u) }}</template>
            <template #cell-d="{ row }">{{ formatBytes(row.d) }}</template>
            <template #cell-server_rate="{ row }">{{ row.server_rate }} x</template>
            <template #cell-total="{ row }">{{ formatBytes((row.u + row.d) * row.server_rate) }}</template>
          </DataTable>
          <Pager :pager="traffic.pager" @page="traffic.setPage" @size="traffic.setPageSize" />
        </TabsContent>

        <TabsContent value="balance" class="mt-4">
          <div class="mb-3 flex items-center gap-2">
            <SelectBox class="w-48" :model-value="balanceType" :options="balanceTypeOptions" @update:model-value="(v) => { balanceType = String(v); balanceLogs.reload() }" />
            <span class="text-xs text-muted-foreground">当前余额 {{ formatMoney(user.balance) }} 元，超过保留天数的记录会自动清理</span>
          </div>
          <DataTable :columns="balanceCols" :rows="balanceLogs.rows.value" :loading="balanceLogs.loading.value" empty="暂无余额变动记录">
            <template #cell-created_at="{ row }">{{ formatTime(row.created_at) }}</template>
            <template #cell-type="{ row }">{{ balanceTypeNames[row.type] || row.type }}</template>
            <template #cell-amount="{ row }">
              <span :class="['tabular-nums', row.amount > 0 ? 'text-emerald-600' : 'text-destructive']">{{ row.amount > 0 ? '+' : '' }}{{ formatMoney(row.amount) }}</span>
            </template>
            <template #cell-balance="{ row }"><span class="tabular-nums">{{ formatMoney(row.balance) }}</span></template>
            <template #cell-trade_no="{ row }"><span class="font-mono text-xs">{{ row.trade_no || '-' }}</span></template>
            <template #cell-remark="{ row }"><span class="break-all text-xs">{{ balanceNote(row) }}</span></template>
          </DataTable>
          <Pager :pager="balanceLogs.pager" @page="balanceLogs.setPage" @size="balanceLogs.setPageSize" />
        </TabsContent>
      </Tabs>
    </SheetContent>
  </Sheet>
  <SubForm v-if="userId" v-model:show="subForm" :user-id="userId" :sub="editing" @saved="onSubSaved" />
</template>
