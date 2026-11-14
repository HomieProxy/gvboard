<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { SearchIcon, RefreshCwIcon, Loader2Icon, SendIcon, ExternalLinkIcon, XCircleIcon } from '@lucide/vue'
import { formatBytes, formatTime, nowUnix } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Tabs, TabsList, TabsTrigger } from '@gv/shared/ui/tabs'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@gv/shared/ui/sheet'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import { admin } from '../api'
import { confirm, errText, message, run } from '../ui'
import { usePaged } from '../paged'
import type { Tone } from '../helpers'
import type { Ticket, TicketMessage } from '../types'

const router = useRouter()
const view = ref('pending')
const email = ref('')

function query() {
  switch (view.value) {
    case 'pending':
      return { status: 0, reply_status: 0 }
    case 'replied':
      return { status: 0, reply_status: 1 }
    case 'closed':
      return { status: 1 }
    default:
      return {}
  }
}

const { rows, loading, pager, load, reload, setPage, setPageSize } = usePaged<Ticket>(async (current, page_size) => {
  const r = await admin.get<{ total: number; list: Ticket[] }>('/tickets', {
    current,
    page_size,
    ...query(),
    email: email.value.trim() || undefined,
  })
  return { rows: r.list || [], total: r.total }
})

const levels: Record<number, [string, Tone]> = { 0: ['低', 'default'], 1: ['中', 'warning'], 2: ['高', 'danger'] }
function statusOf(t: Ticket): [string, Tone] {
  if (t.status === 1) return ['已关闭', 'default']
  return t.reply_status === 0 ? ['待回复', 'warning'] : ['已回复', 'success']
}
function subLabel(t: Ticket) {
  const id = t.subscription_id ?? t.inferred_subscription_id
  if (!id) return '无订阅'
  return `${t.plan_name || '无套餐'} · #${id}${t.subscription_inferred ? '（主订阅）' : ''}`
}
function expireText(t: Ticket) {
  const e = t.subscription_expired_at
  if (!t.subscription_id && !t.inferred_subscription_id) return '-'
  if (e === null) return '长期有效'
  return e <= nowUnix() ? `已到期 ${formatTime(e, false)}` : formatTime(e)
}

const columns: Column[] = [
  { key: 'id', title: '#', class: 'w-16' },
  { key: 'subject', title: '主题' },
  { key: 'user', title: '用户 / 订阅' },
  { key: 'level', title: '工单级别', align: 'center' },
  { key: 'status', title: '工单状态' },
  { key: 'created_at', title: '创建时间' },
  { key: 'updated_at', title: '最后回复' },
]

// ---------- 详情 ----------
const sheet = ref(false)
const current = ref<(Ticket & { messages: TicketMessage[] }) | null>(null)
const detailLoading = ref(false)
const text = ref('')
const sending = ref(false)
const box = ref<HTMLElement | null>(null)

async function fetchDetail(id: number) {
  detailLoading.value = true
  try {
    current.value = await admin.get(`/tickets/${id}`)
    await nextTick()
    box.value?.scrollTo({ top: box.value.scrollHeight })
  } catch (e) {
    message.error(errText(e))
  } finally {
    detailLoading.value = false
  }
}
function open(t: Ticket) {
  current.value = null
  text.value = ''
  sheet.value = true
  fetchDetail(t.id)
}
async function send() {
  if (!current.value || !text.value.trim() || sending.value) return
  const id = current.value.id
  sending.value = true
  const ok = await run(() => admin.post(`/tickets/${id}/reply`, { message: text.value }), '回复成功')
  sending.value = false
  if (ok) {
    text.value = ''
    fetchDetail(id)
    load()
  }
}
async function close() {
  if (!current.value) return
  const id = current.value.id
  if (await confirm('确定关闭该工单？', () => admin.post(`/tickets/${id}/close`), '关闭成功')) {
    fetchDetail(id)
    load()
  }
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send()
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="工单管理" description="回复会重新打开已关闭的工单" />

    <div class="mb-3 flex flex-wrap items-center gap-2">
      <Tabs v-model="view" @update:model-value="reload">
        <TabsList>
          <TabsTrigger value="pending">待回复</TabsTrigger>
          <TabsTrigger value="replied">已回复</TabsTrigger>
          <TabsTrigger value="closed">已关闭</TabsTrigger>
          <TabsTrigger value="all">全部</TabsTrigger>
        </TabsList>
      </Tabs>
      <div class="relative w-full sm:w-64">
        <SearchIcon class="absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input v-model="email" class="pl-8" placeholder="用户邮箱（精确匹配）" @keyup.enter="reload" />
      </div>
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
    </div>

    <DataTable :columns="columns" :rows="rows" :loading="loading" clickable @row-click="open">
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-subject="{ row }"><span class="font-medium">{{ row.subject }}</span></template>
      <template #cell-user="{ row }">
        <div class="grid leading-tight">
          <span class="truncate">{{ row.email || `#${row.user_id}` }}</span>
          <span class="truncate text-xs text-muted-foreground">{{ subLabel(row) }}</span>
        </div>
      </template>
      <template #cell-level="{ row }"><StatusBadge :tone="levels[row.level]?.[1]">{{ levels[row.level]?.[0] || '-' }}</StatusBadge></template>
      <template #cell-status="{ row }"><StatusBadge :tone="statusOf(row)[1]" dot>{{ statusOf(row)[0] }}</StatusBadge></template>
      <template #cell-created_at="{ row }"><span class="whitespace-nowrap text-muted-foreground">{{ formatTime(row.created_at) }}</span></template>
      <template #cell-updated_at="{ row }"><span class="whitespace-nowrap text-muted-foreground">{{ formatTime(row.updated_at) }}</span></template>
    </DataTable>
    <Pager :pager="pager" @page="setPage" @size="setPageSize" />

    <Sheet v-model:open="sheet">
      <SheetContent class="flex w-full flex-col gap-0 p-0 sm:max-w-2xl">
        <SheetHeader class="border-b px-6 py-4">
          <SheetTitle class="flex items-center gap-2 pr-6">
            <span class="truncate">{{ current ? current.subject : '工单' }}</span>
            <StatusBadge v-if="current" :tone="statusOf(current)[1]" dot>{{ statusOf(current)[0] }}</StatusBadge>
          </SheetTitle>
          <SheetDescription v-if="current">#{{ current.id }} · 创建于 {{ formatTime(current.created_at) }}</SheetDescription>
        </SheetHeader>

        <div v-if="current" class="grid grid-cols-2 gap-x-6 gap-y-2 border-b bg-muted/30 px-6 py-3 text-sm sm:grid-cols-4">
          <div class="col-span-2 min-w-0">
            <div class="text-xs text-muted-foreground">用户</div>
            <button class="inline-flex max-w-full items-center gap-1 truncate hover:text-primary" @click="router.push({ path: '/users', query: { id: current.user_id } })">
              {{ current.email || `#${current.user_id}` }}<ExternalLinkIcon class="size-3 shrink-0" />
            </button>
          </div>
          <div class="col-span-2 min-w-0">
            <div class="text-xs text-muted-foreground">订阅{{ current.subscription_inferred ? '（未关联，显示主订阅）' : '' }}</div>
            <div class="truncate font-medium">{{ subLabel(current) }}</div>
          </div>
          <div class="col-span-2">
            <div class="text-xs text-muted-foreground">到期时间</div>
            <div>{{ expireText(current) }}</div>
          </div>
          <div class="col-span-2">
            <div class="text-xs text-muted-foreground">流量</div>
            <div v-if="current.subscription_total !== null">{{ formatBytes(current.subscription_used) }} / {{ formatBytes(current.subscription_total) }}</div>
            <div v-else>-</div>
          </div>
        </div>

        <div ref="box" class="flex-1 overflow-y-auto px-6 py-4">
          <div v-if="detailLoading && !current" class="flex h-40 items-center justify-center"><Loader2Icon class="size-6 animate-spin text-muted-foreground" /></div>
          <div class="flex flex-col gap-4">
            <div v-for="m in current?.messages || []" :key="m.id" :class="['flex max-w-[85%] flex-col gap-1', m.is_me ? 'items-end self-end' : 'items-start']">
              <div
                :class="[
                  'rounded-2xl px-4 py-2 text-sm whitespace-pre-wrap break-words',
                  m.is_me ? 'rounded-br-sm bg-primary text-primary-foreground' : 'rounded-bl-sm bg-muted',
                ]"
              >{{ m.message }}</div>
              <span class="text-xs text-muted-foreground">{{ m.is_me ? '客服' : '用户' }} · {{ formatTime(m.created_at) }}</span>
            </div>
          </div>
        </div>

        <div class="border-t p-4">
          <Textarea v-model="text" rows="3" placeholder="输入回复内容，Ctrl + Enter 发送" class="resize-none" @keydown="onKey" />
          <div class="mt-3 flex items-center justify-end gap-2">
            <Button v-if="current && current.status === 0" variant="outline" @click="close"><XCircleIcon />关闭工单</Button>
            <Button :disabled="!text.trim() || sending" @click="send">
              <Loader2Icon v-if="sending" class="animate-spin" />
              <SendIcon v-else />
              回复
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  </div>
</template>
