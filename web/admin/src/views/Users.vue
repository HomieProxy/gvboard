<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  SearchIcon, RefreshCwIcon, PlusIcon, ChevronDownIcon, MoreHorizontalIcon, PencilIcon, ReceiptIcon, CopyIcon, RotateCcwIcon,
  ListOrderedIcon, UsersIcon, ActivityIcon, Trash2Icon, DownloadIcon, MailIcon, BanIcon, XIcon, Loader2Icon, InfoIcon,
} from '@lucide/vue'
import { copyText, formatMoney, formatTime, nowUnix, daysLeft } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Checkbox } from '@gv/shared/ui/checkbox'
import { Label } from '@gv/shared/ui/label'
import { Tooltip, TooltipContent, TooltipTrigger } from '@gv/shared/ui/tooltip'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@gv/shared/ui/dropdown-menu'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column, type SortState } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import NumInput from '../components/common/NumInput.vue'
import DateTimeInput from '../components/common/DateTimeInput.vue'
import FilterEditor, { type FilterField } from '../components/FilterEditor.vue'
import UserDrawer from '../components/UserDrawer.vue'
import AssignOrder from '../components/AssignOrder.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { usePaged } from '../paged'
import type { Filter, PageResult } from '../helpers'
import { groupName, loadGroups, loadPlans, planOptions } from '../refs'
import type { UserRow } from '../types'

const route = useRoute()
const router = useRouter()

const yesNo = [
  { label: '是', value: '1' },
  { label: '否', value: '0' },
]
const filterFields = computed<FilterField[]>(() => [
  { label: '邮箱', value: 'email', condition: '模糊' },
  { label: '用户ID', value: 'id' },
  { label: '订阅', value: 'plan_id', options: [{ label: '无订阅', value: 'null' }, ...planOptions.value.map((p) => ({ label: p.label, value: String(p.value) }))] },
  { label: '流量(GB)', value: 'transfer_enable' },
  { label: '下行(GB)', value: 'd' },
  { label: '上行(GB)', value: 'u' },
  { label: '到期时间(Unix 秒)', value: 'expired_at' },
  { label: '账号状态', value: 'banned', options: [{ label: '正常', value: '0' }, { label: '封禁', value: '1' }] },
  { label: '邀请人邮箱', value: 'invite_by_email', condition: '模糊' },
  { label: '邀请人ID', value: 'invite_user_id' },
  { label: '备注', value: 'remarks', condition: '模糊' },
  { label: '管理员', value: 'is_admin', options: yesNo },
  { label: '员工', value: 'is_staff', options: yesNo },
  { label: 'Telegram ID', value: 'telegram_id' },
  { label: '余额(分)', value: 'balance' },
  { label: '佣金余额(分)', value: 'commission_balance' },
  { label: '订阅·设备数限制', value: 'device_limit' },
  { label: '订阅·UUID', value: 'uuid' },
  { label: '订阅·Token', value: 'token' },
  { label: '订阅·ID', value: 'subscription_id' },
  { label: '订阅·封禁', value: 'subscription_banned', options: yesNo },
])

const filter = ref<Filter[]>([])
const keyword = ref('')
const sort = ref<SortState | null>(null)

function filters(): Filter[] {
  const f = [...filter.value]
  if (keyword.value.trim()) f.push({ key: 'email', condition: '模糊', value: keyword.value.trim() })
  return f
}

const { rows, loading, pager, load, reload, setPage, setPageSize } = usePaged<UserRow>(async (current, page_size) => {
  const s = sort.value ? { sort: sort.value.key, sort_type: sort.value.order } : {}
  const r = await admin.post<PageResult<UserRow>>('/users/query', { current, page_size, filter: filters(), ...s })
  return { rows: r.data || [], total: r.total }
})

function onSort(s: SortState | null) {
  sort.value = s
  reload()
}

const fieldLabel = (k: string) => filterFields.value.find((f) => f.value === k)?.label || k
function valueLabel(f: Filter) {
  const o = filterFields.value.find((x) => x.value === f.key)?.options?.find((x) => x.value === f.value)
  return o ? o.label : f.value
}
function removeFilter(i: number) {
  filter.value = filter.value.filter((_, j) => j !== i)
  reload()
}

const columns: Column[] = [
  { key: 'id', title: 'ID', sortable: true, class: 'w-16' },
  { key: 'email', title: '邮箱', sortable: true },
  { key: 'banned', title: '状态' },
  { key: 'plan', title: '订阅' },
  { key: 'group', title: '权限组' },
  { key: 'used', title: '已用(G)', align: 'right' },
  { key: 'transfer', title: '流量(G)', align: 'right' },
  { key: 'device', title: '设备数', align: 'center' },
  { key: 'expired_at', title: '到期时间' },
  { key: 'balance', title: '余额', sortable: true, align: 'right' },
  { key: 'commission_balance', title: '佣金', sortable: true, align: 'right' },
  { key: 'created_at', title: '加入时间', sortable: true },
  { key: 'op', title: '操作', align: 'right', class: 'w-16' },
]

const gb = (n: number) => (n / 1024 ** 3).toFixed(2)
function online(r: UserRow) {
  const t = r.primary?.t
  return !!t && nowUnix() - t < 600
}
function lastOnline(r: UserRow) {
  const t = r.primary?.t
  return t ? `最后在线 ${formatTime(t)}` : '从未在线'
}
function expireInfo(r: UserRow): { text: string; tone: string } {
  const p = r.primary
  if (!p) return { text: '-', tone: 'text-muted-foreground' }
  if (p.expired_at === null) return { text: '长期有效', tone: 'text-success' }
  const left = daysLeft(p.expired_at) ?? 0
  if (p.expired_at <= nowUnix()) return { text: `已到期 ${formatTime(p.expired_at, false)}`, tone: 'text-destructive' }
  return { text: `${formatTime(p.expired_at, false)}（${left} 天）`, tone: left <= 7 ? 'text-amber-600' : '' }
}

// ---------- 详情 ----------
const drawer = ref(false)
const drawerTab = ref('subs')
const current = ref<number | null>(null)
function open(id: number, tab = 'subs') {
  current.value = id
  drawerTab.value = tab
  drawer.value = true
}

// ---------- 行操作 ----------
const assign = ref(false)
const assignUser = ref<UserRow | null>(null)
function openAssign(r: UserRow) {
  assignUser.value = r
  assign.value = true
}
async function copyUrl(r: UserRow) {
  if (!r.primary?.subscribe_url) return message.warning('该用户没有订阅')
  message[(await copyText(r.primary.subscribe_url)) ? 'success' : 'error']('复制成功')
}
async function resetSecurity(r: UserRow) {
  if (!r.primary) return message.warning('该用户没有订阅')
  if (await confirm(`确定要重置 ${r.email} 的安全信息吗？主订阅的 UUID 及订阅 URL 将立即变更。`, () => admin.post(`/subscriptions/${r.primary!.id}/reset-security`), '重置成功')) load()
}
function showInvites(r: UserRow) {
  filter.value = [{ key: 'invite_user_id', condition: '=', value: String(r.id) }]
  keyword.value = ''
  reload()
}
async function removeUser(r: UserRow) {
  if (await confirm(`确定要删除 ${r.email} 的用户信息吗？其订阅、订单、工单等将一并删除，且不可恢复。`, () => admin.del(`/users/${r.id}`), '删除成功')) load()
}

// ---------- 创建用户 ----------
const gen = ref(false)
const genBusy = ref(false)
const genForm = ref({ email_prefix: '', email_suffix: '', password: '', plan_id: null as number | null, expired_at: null as number | null, generate_count: 10 as number | null })
function openGen() {
  loadPlans()
  gen.value = true
}
async function submitGen() {
  const f = genForm.value
  if (!f.email_suffix.trim()) return message.warning('请输入邮箱后缀')
  const body = {
    email_prefix: f.email_prefix.trim(),
    email_suffix: f.email_suffix.trim(),
    password: f.password,
    plan_id: f.plan_id || 0,
    expired_at: f.expired_at,
    generate_count: f.generate_count || 1,
  }
  genBusy.value = true
  const ok = await run(async () => {
    if (body.email_prefix) await admin.post('/users/generate', body)
    else await admin.download('POST', '/users/generate', body, 'users.csv')
  }, '生成成功')
  genBusy.value = false
  if (ok) {
    gen.value = false
    reload()
  }
}

// ---------- 批量操作 ----------
const mail = ref(false)
const mailBusy = ref(false)
const mailForm = ref({ subject: '', content: '', all: false })
async function submitMail() {
  const f = filters()
  if (!f.length && !mailForm.value.all) return message.warning('当前没有过滤条件，如需发送给全部用户请勾选「发送给全部用户」')
  if (!mailForm.value.subject.trim() || !mailForm.value.content.trim()) return message.warning('请填写主题与内容')
  mailBusy.value = true
  const ok = await run(async () => {
    const r = await admin.post<{ count: number }>('/users/mail', { filter: f, subject: mailForm.value.subject, content: mailForm.value.content })
    message.success(`已加入发送队列：${r.count} 封`)
  })
  mailBusy.value = false
  if (ok) mail.value = false
}
function exportCsv() {
  run(() => admin.download('POST', '/users/export', { filter: filters() }, 'users.csv'))
}
async function batchBan() {
  const f = filters()
  if (!f.length) return message.warning('请先使用过滤器过滤用户')
  await confirm('确定要封禁所有符合当前过滤条件的用户吗？', async () => {
    const r = await admin.post<{ count: number }>('/users/ban', { filter: f })
    message.success(`已封禁 ${r.count} 个用户`)
    reload()
  }, '')
}
async function batchDelete() {
  const f = filters()
  if (!f.length) return message.warning('请先使用过滤器过滤用户')
  await confirm('确定要删除所有符合当前过滤条件的用户吗？其订阅、订单、工单等将一并删除，且不可恢复。', async () => {
    const r = await admin.post<{ count: number }>('/users/batch-delete', { filter: f })
    message.success(`已删除 ${r.count} 个用户`)
    reload()
  }, '')
}

onMounted(() => {
  const uid = route.query.id
  if (uid) {
    filter.value = [{ key: 'id', condition: '=', value: String(uid) }]
    open(Number(uid))
  }
  const inviter = route.query.invite_user_id
  if (inviter) filter.value = [{ key: 'invite_user_id', condition: '=', value: String(inviter) }]
  loadPlans()
  loadGroups()
  load()
})
</script>

<template>
  <div>
    <PageHeader title="用户管理" description="可以使用过滤器过滤后再使用操作对过滤的用户进行操作。">
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button variant="outline">操作<ChevronDownIcon /></Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" class="w-48">
          <DropdownMenuLabel class="text-xs font-normal text-muted-foreground">对当前过滤结果操作</DropdownMenuLabel>
          <DropdownMenuItem @click="exportCsv"><DownloadIcon />导出CSV</DropdownMenuItem>
          <DropdownMenuItem @click="mail = true"><MailIcon />发送邮件</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem @click="batchBan"><BanIcon />批量封禁</DropdownMenuItem>
          <DropdownMenuItem variant="destructive" @click="batchDelete"><Trash2Icon />批量删除</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Button @click="openGen"><PlusIcon />创建用户</Button>
    </PageHeader>

    <div class="mb-3 flex flex-wrap items-center gap-2">
      <div class="relative w-full sm:w-64">
        <SearchIcon class="absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input v-model="keyword" class="pl-8" placeholder="搜索邮箱" @keyup.enter="reload" />
      </div>
      <FilterEditor v-model="filter" :fields="filterFields" @apply="reload" />
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <div v-if="filter.length" class="flex flex-wrap items-center gap-1.5">
        <span
          v-for="(f, i) in filter"
          :key="i"
          class="inline-flex items-center gap-1 rounded-md border bg-muted/50 px-2 py-1 text-xs"
        >
          {{ fieldLabel(f.key) }} {{ f.condition === '=' ? '=' : f.condition }} {{ valueLabel(f) }}
          <XIcon class="size-3 cursor-pointer opacity-60 hover:opacity-100" @click="removeFilter(i)" />
        </span>
      </div>
    </div>

    <DataTable :columns="columns" :rows="rows" :loading="loading" :sort="sort" @update:sort="onSort">
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-email="{ row }">
        <div class="flex items-center gap-2">
          <Tooltip>
            <TooltipTrigger as-child>
              <span :class="['size-2 shrink-0 rounded-full', online(row) ? 'bg-success' : 'bg-muted-foreground/30']" />
            </TooltipTrigger>
            <TooltipContent>{{ lastOnline(row) }}</TooltipContent>
          </Tooltip>
          <button class="max-w-64 truncate text-left font-medium hover:text-primary hover:underline" @click="open(row.id)">{{ row.email }}</button>
          <StatusBadge v-if="row.is_admin" tone="primary">管理员</StatusBadge>
          <StatusBadge v-if="row.is_staff" tone="info">员工</StatusBadge>
        </div>
      </template>
      <template #cell-banned="{ row }">
        <StatusBadge :tone="row.banned ? 'danger' : 'success'" dot>{{ row.banned ? '封禁' : '正常' }}</StatusBadge>
      </template>
      <template #cell-plan="{ row }">
        <div class="flex items-center gap-1.5">
          <span :class="row.primary?.plan_name ? '' : 'text-muted-foreground'">{{ row.primary?.plan_name || '无订阅' }}</span>
          <Tooltip v-if="row.subscription_count > 1">
            <TooltipTrigger as-child>
              <button class="rounded bg-muted px-1.5 text-[11px] text-muted-foreground hover:bg-accent" @click="open(row.id)">+{{ row.subscription_count - 1 }}</button>
            </TooltipTrigger>
            <TooltipContent>共 {{ row.subscription_count }} 个订阅，点击查看</TooltipContent>
          </Tooltip>
        </div>
      </template>
      <template #cell-group="{ row }">{{ row.primary?.group_id ? groupName(row.primary.group_id) : '-' }}</template>
      <template #cell-used="{ row }"><span class="tabular-nums">{{ row.primary ? gb(row.primary.u + row.primary.d) : '-' }}</span></template>
      <template #cell-transfer="{ row }"><span class="tabular-nums">{{ row.primary ? gb(row.primary.transfer_enable) : '-' }}</span></template>
      <template #cell-device="{ row }">
        <span class="tabular-nums">{{ row.alive_ip }} / {{ row.primary?.device_limit ?? '∞' }}</span>
      </template>
      <template #cell-expired_at="{ row }">
        <span :class="['whitespace-nowrap', expireInfo(row).tone]">{{ expireInfo(row).text }}</span>
      </template>
      <template #cell-balance="{ row }"><span class="tabular-nums">{{ formatMoney(row.balance) }}</span></template>
      <template #cell-commission_balance="{ row }"><span class="tabular-nums">{{ formatMoney(row.commission_balance) }}</span></template>
      <template #cell-created_at="{ row }"><span class="whitespace-nowrap text-muted-foreground">{{ formatTime(row.created_at) }}</span></template>
      <template #cell-op="{ row }">
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button variant="ghost" size="icon" class="size-8"><MoreHorizontalIcon /></Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" class="w-52">
            <DropdownMenuItem @click="open(row.id, 'account')"><PencilIcon />编辑</DropdownMenuItem>
            <DropdownMenuItem @click="openAssign(row)"><ReceiptIcon />分配订单</DropdownMenuItem>
            <DropdownMenuItem @click="copyUrl(row)"><CopyIcon />复制订阅URL</DropdownMenuItem>
            <DropdownMenuItem @click="resetSecurity(row)"><RotateCcwIcon />重置UUID及订阅URL</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem @click="router.push({ path: '/orders', query: { user_id: row.id } })"><ListOrderedIcon />TA的订单</DropdownMenuItem>
            <DropdownMenuItem @click="showInvites(row)"><UsersIcon />TA的邀请</DropdownMenuItem>
            <DropdownMenuItem @click="open(row.id, 'traffic')"><ActivityIcon />TA的流量记录</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" @click="removeUser(row)"><Trash2Icon />删除用户</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </template>
    </DataTable>
    <Pager :pager="pager" @page="setPage" @size="setPageSize" />

    <UserDrawer v-model:show="drawer" :user-id="current" :tab="drawerTab" @changed="load" />
    <AssignOrder v-model:show="assign" :email="assignUser?.email" :user-id="assignUser?.id" @done="load" />

    <Dialog v-model:open="gen">
      <DialogContent class="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>创建用户</DialogTitle>
          <DialogDescription>邮箱前缀留空时批量生成，并直接下载包含账号、密码与订阅地址的 CSV 文件，请妥善保管。</DialogDescription>
        </DialogHeader>
        <div class="grid gap-4">
          <FormItem label="邮箱" required>
            <div class="flex items-center gap-2">
              <Input v-model="genForm.email_prefix" placeholder="账号（批量生成请留空）" />
              <span class="text-muted-foreground">@</span>
              <Input v-model="genForm.email_suffix" placeholder="域名" />
            </div>
          </FormItem>
          <FormItem v-if="!genForm.email_prefix" label="生成数量"><NumInput v-model="genForm.generate_count" :min="1" :max="500" /></FormItem>
          <FormItem label="密码"><Input v-model="genForm.password" placeholder="留空则密码与邮箱相同" /></FormItem>
          <FormItem label="订阅计划">
            <SelectBox :model-value="genForm.plan_id" :options="[{ label: '无', value: null }, ...planOptions]" placeholder="无" @update:model-value="(v) => (genForm.plan_id = v as number | null)" />
          </FormItem>
          <FormItem label="到期时间" help="有订阅计划时留空为长期有效"><DateTimeInput v-model="genForm.expired_at" /></FormItem>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="gen = false">取消</Button>
          <Button :disabled="genBusy" @click="submitGen"><Loader2Icon v-if="genBusy" class="animate-spin" />生成</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="mail">
      <DialogContent class="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>发送邮件</DialogTitle>
          <DialogDescription>邮件将发送给符合当前过滤条件的用户。</DialogDescription>
        </DialogHeader>
        <div class="grid gap-4">
          <div v-if="filters().length" class="flex items-start gap-2 rounded-md bg-muted/60 p-3 text-xs">
            <InfoIcon class="mt-0.5 size-3.5 shrink-0" />
            <span>当前过滤：{{ filters().map((f) => `${fieldLabel(f.key)} ${f.condition} ${valueLabel(f)}`).join('；') }}</span>
          </div>
          <FormItem label="主题" required><Input v-model="mailForm.subject" placeholder="请输入邮件主题" /></FormItem>
          <FormItem label="发送内容" required help="支持 HTML"><Textarea v-model="mailForm.content" rows="8" placeholder="请输入邮件内容" /></FormItem>
          <div v-if="!filters().length" class="flex items-center gap-2">
            <Checkbox id="mail-all" :model-value="mailForm.all" @update:model-value="(v) => (mailForm.all = v === true)" />
            <Label for="mail-all" class="font-normal">没有过滤条件，发送给全部用户</Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="mail = false">取消</Button>
          <Button :disabled="mailBusy" @click="submitMail"><Loader2Icon v-if="mailBusy" class="animate-spin" />发送</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
