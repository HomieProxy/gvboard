<script setup lang="ts">
import { h, nextTick, onMounted, ref } from 'vue'
import { NCard, NDataTable, NButton, NInput, NSelect, NDrawer, NDrawerContent, NSpace, NSpin, type DataTableColumns } from 'naive-ui'
import { formatTime } from '@gv/shared'
import { admin } from '../api'
import { confirm, errText, message, run } from '../ui'
import { usePaged } from '../paged'
import { actions, btn, tag } from '../helpers'
import type { Ticket, TicketMessage } from '../types'

const status = ref<number | null>(0)
const reply = ref<number | null>(0)
const email = ref('')

const { rows, loading, pagination, load, reload } = usePaged<Ticket>(async (current, page_size) => {
  const r = await admin.get<{ total: number; list: Ticket[] }>('/tickets', {
    current,
    page_size,
    status: status.value ?? undefined,
    reply_status: reply.value ?? undefined,
    email: email.value.trim() || undefined,
  })
  return { rows: r.list || [], total: r.total }
})

const levels: Record<number, [string, 'default' | 'warning' | 'error']> = { 0: ['低', 'default'], 1: ['中', 'warning'], 2: ['高', 'error'] }

const columns: DataTableColumns<Ticket> = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '主题', key: 'subject', render: (t) => h('a', { class: 'link', onClick: () => open(t) }, t.subject) },
  { title: '用户', key: 'email', render: (t) => `${t.email || '-'}（#${t.user_id}）` },
  { title: '关联订阅', key: 'subscription_id', render: (t) => (t.subscription_id ? `#${t.subscription_id} ${t.subscription_name || ''}` : '-') },
  { title: '优先级', key: 'level', width: 80, render: (t) => tag(levels[t.level]?.[0] || '?', levels[t.level]?.[1]) },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render: (t) => (t.status === 1 ? tag('已关闭') : t.reply_status === 0 ? tag('待回复', 'warning') : tag('已回复', 'success')),
  },
  { title: '更新时间', key: 'updated_at', render: (t) => formatTime(t.updated_at) },
  { title: '操作', key: 'op', width: 80, render: (t) => actions(btn('查看', () => open(t))) },
]

// ---------- 详情 ----------
const drawer = ref(false)
const current = ref<(Ticket & { messages: TicketMessage[] }) | null>(null)
const detailLoading = ref(false)
const text = ref('')
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
  drawer.value = true
  fetchDetail(t.id)
}
async function send() {
  if (!current.value || !text.value.trim()) return
  const id = current.value.id
  if (await run(() => admin.post(`/tickets/${id}/reply`, { message: text.value }), '已回复')) {
    text.value = ''
    fetchDetail(id)
    load()
  }
}
function close() {
  if (!current.value) return
  const id = current.value.id
  confirm('确定关闭该工单？', () => admin.post(`/tickets/${id}/close`), '已关闭').then((ok) => {
    if (ok) {
      fetchDetail(id)
      load()
    }
  })
}

const statusOptions = [
  { label: '开启中', value: 0 },
  { label: '已关闭', value: 1 },
]
const replyOptions = [
  { label: '待回复', value: 0 },
  { label: '已回复', value: 1 },
]

onMounted(load)
</script>

<template>
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NSelect v-model:value="status" :options="statusOptions" clearable placeholder="全部状态" style="width: 120px" @update:value="reload" />
        <NSelect v-model:value="reply" :options="replyOptions" clearable placeholder="全部回复状态" style="width: 140px" @update:value="reload" />
        <NInput v-model:value="email" placeholder="用户邮箱（精确）" clearable style="width: 220px" @keyup.enter="reload" @clear="reload" />
        <NButton @click="reload">刷新</NButton>
      </div>
      <NDataTable remote size="small" :columns="columns" :data="rows" :loading="loading" :pagination="pagination" :row-key="(t: Ticket) => t.id" :scroll-x="900" />
    </NCard>

    <NDrawer v-model:show="drawer" :width="640" style="max-width: 100vw">
      <NDrawerContent :title="current ? `#${current.id} ${current.subject}` : '工单'" closable :native-scrollbar="false">
        <NSpin :show="detailLoading">
          <div v-if="current" class="meta muted">
            {{ current.email }}（#{{ current.user_id }}）
            <template v-if="current.subscription_id">· 订阅 #{{ current.subscription_id }} {{ current.subscription_name }}</template>
            · {{ formatTime(current.created_at) }}
          </div>
          <div ref="box" class="msgs">
            <div v-for="m in current?.messages || []" :key="m.id" class="msg" :class="{ me: m.is_me }">
              <div class="bubble">{{ m.message }}</div>
              <div class="muted">{{ m.is_me ? '客服' : '用户' }} · {{ formatTime(m.created_at) }}</div>
            </div>
          </div>
        </NSpin>
        <template #footer>
          <NSpace vertical style="width: 100%">
            <NInput v-model:value="text" type="textarea" :autosize="{ minRows: 3, maxRows: 8 }" placeholder="回复内容（回复会重新打开已关闭的工单）" />
            <NSpace justify="end">
              <NButton v-if="current && current.status === 0" @click="close">关闭工单</NButton>
              <NButton type="primary" :disabled="!text.trim()" @click="send">回复</NButton>
            </NSpace>
          </NSpace>
        </template>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>

<style scoped>
.meta { margin-bottom: 12px; }
.msgs { display: flex; flex-direction: column; gap: 12px; }
.msg { max-width: 85%; }
.msg.me { align-self: flex-end; text-align: right; }
.bubble { display: inline-block; text-align: left; white-space: pre-wrap; word-break: break-word; padding: 8px 12px; border-radius: 8px; background: #f2f3f5; }
.msg.me .bubble { background: #dbeafe; }
</style>
