<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NCard, NDataTable, NButton, NInput, NModal, NForm, NFormItem, NInputNumber, NSelect, NDatePicker, NSpace,
  NDropdown, NCheckbox, type DataTableColumns, type DataTableSortState,
} from 'naive-ui'
import { formatBytes, formatMoney, formatTime, nowUnix } from '@gv/shared'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { usePaged } from '../paged'
import { actions, btn, tag, toSec, type Filter, type PageResult } from '../helpers'
import { loadPlans, planOptions } from '../refs'
import type { UserRow } from '../types'
import FilterEditor from '../components/FilterEditor.vue'
import UserDrawer from '../components/UserDrawer.vue'

const route = useRoute()
const router = useRouter()
const filterFields = [
  { label: '用户 ID', value: 'id' },
  { label: '邮箱', value: 'email' },
  { label: '邀请人 ID', value: 'invite_user_id' },
  { label: '邀请人邮箱', value: 'invite_by_email' },
  { label: '账号封禁(0/1)', value: 'banned' },
  { label: '备注', value: 'remarks' },
  { label: '管理员(0/1)', value: 'is_admin' },
  { label: '客服(0/1)', value: 'is_staff' },
  { label: 'Telegram ID', value: 'telegram_id' },
  { label: '余额(分)', value: 'balance' },
  { label: '佣金余额(分)', value: 'commission_balance' },
  { label: '订阅·总流量(GB)', value: 'transfer_enable' },
  { label: '订阅·上行(GB)', value: 'u' },
  { label: '订阅·下行(GB)', value: 'd' },
  { label: '订阅·设备数限制', value: 'device_limit' },
  { label: '订阅·到期时间(Unix 秒)', value: 'expired_at' },
  { label: '订阅·UUID', value: 'uuid' },
  { label: '订阅·Token', value: 'token' },
  { label: '订阅·套餐 ID（null 为无套餐）', value: 'plan_id' },
  { label: '订阅·ID', value: 'subscription_id' },
  { label: '订阅·封禁(0/1)', value: 'subscription_banned' },
]

const filter = ref<Filter[]>([])
const keyword = ref('')
const sort = ref<{ sort?: string; sort_type?: string }>({})

function filters(): Filter[] {
  const f = [...filter.value]
  if (keyword.value.trim()) f.push({ key: 'email', condition: '模糊', value: keyword.value.trim() })
  return f
}

const { rows, loading, pagination, load, reload } = usePaged<UserRow>(async (current, page_size) => {
  const r = await admin.post<PageResult<UserRow>>('/users/query', { current, page_size, filter: filters(), ...sort.value })
  return { rows: r.data || [], total: r.total }
})

function onSort(s: DataTableSortState | null) {
  sort.value = s && s.order ? { sort: String(s.columnKey), sort_type: s.order === 'ascend' ? 'ASC' : 'DESC' } : {}
  reload()
}

// ---------- 详情 ----------
const drawer = ref(false)
const current = ref<number | null>(null)
function open(id: number) {
  current.value = id
  drawer.value = true
}

function primaryInfo(r: UserRow) {
  const p = r.primary
  if (!p) return '-'
  const expired = p.expired_at !== null && p.expired_at <= nowUnix()
  return h(NSpace, { vertical: true, size: 0 }, () => [
    h('span', null, p.plan_name || '无套餐'),
    h('span', { class: 'muted' }, `${formatBytes(p.u + p.d)} / ${formatBytes(p.transfer_enable)}`),
    h(
      'span',
      { class: 'muted', style: expired ? 'color:#d03050' : '' },
      p.expired_at === null ? '长期有效' : `${expired ? '已到期 ' : ''}${formatTime(p.expired_at, false)}`,
    ),
  ])
}

const columns: DataTableColumns<UserRow> = [
  { title: 'ID', key: 'id', width: 80, sorter: true },
  {
    title: '邮箱',
    key: 'email',
    sorter: true,
    render: (r) =>
      h(NSpace, { size: 4, vertical: true }, () => [
        h('a', { class: 'link', onClick: () => open(r.id) }, r.email),
        h(NSpace, { size: 4 }, () => [
          r.banned ? tag('封禁', 'error') : null,
          r.is_admin ? tag('管理员', 'info') : null,
          r.is_staff ? tag('客服', 'info') : null,
        ]),
      ]),
  },
  { title: '主订阅', key: 'primary', render: primaryInfo },
  { title: '订阅数', key: 'subscription_count', width: 80 },
  { title: '在线', key: 'alive_ip', width: 70 },
  { title: '余额', key: 'balance', sorter: true, render: (r) => formatMoney(r.balance) },
  { title: '佣金', key: 'commission_balance', sorter: true, render: (r) => formatMoney(r.commission_balance) },
  { title: '注册时间', key: 'created_at', sorter: true, render: (r) => formatTime(r.created_at) },
  {
    title: '操作',
    key: 'op',
    width: 120,
    render: (r) =>
      actions(
        btn('详情', () => open(r.id)),
        btn('订单', () => router.push({ path: '/orders', query: { user_id: r.id } }), 'info'),
      ),
  },
]

// ---------- 生成用户 ----------
const gen = ref(false)
const genForm = ref({ email_prefix: '', email_suffix: '', password: '', plan_id: null as number | null, expired_at: null as number | null, generate_count: 10 })
function openGen() {
  loadPlans()
  gen.value = true
}
async function submitGen() {
  const f = genForm.value
  const body = {
    email_prefix: f.email_prefix.trim(),
    email_suffix: f.email_suffix.trim(),
    password: f.password,
    plan_id: f.plan_id || 0,
    expired_at: toSec(f.expired_at),
    generate_count: f.generate_count,
  }
  const ok = await run(async () => {
    if (body.email_prefix) await admin.post('/users/generate', body)
    else await admin.download('POST', '/users/generate', body, 'users.csv')
  }, '已生成')
  if (ok) {
    gen.value = false
    reload()
  }
}

// ---------- 批量操作 ----------
const mail = ref(false)
const mailForm = ref({ subject: '', content: '', all: false })
async function submitMail() {
  const f = filters()
  if (!f.length && !mailForm.value.all) {
    message.warning('当前没有筛选条件，如需发送给全部用户请勾选“发送给全部用户”')
    return
  }
  const ok = await run(async () => {
    const r = await admin.post<{ count: number }>('/users/mail', { filter: f, subject: mailForm.value.subject, content: mailForm.value.content })
    message.success(`已加入发送队列：${r.count} 封`)
  })
  if (ok) mail.value = false
}

function batch(key: string) {
  const f = filters()
  if (key === 'export') {
    run(() => admin.download('POST', '/users/export', { filter: f }, 'users.csv'))
    return
  }
  if (key === 'mail') {
    mail.value = true
    return
  }
  if (!f.length) {
    message.warning('请先设置筛选条件')
    return
  }
  const isBan = key === 'ban'
  confirm(
    isBan ? '确定封禁所有符合当前筛选条件的用户？' : '确定删除所有符合当前筛选条件的用户？其订阅、订单、工单等将一并删除，且不可恢复。',
    async () => {
      const r = await admin.post<{ count: number }>(isBan ? '/users/ban' : '/users/batch-delete', { filter: f })
      message.success(`${isBan ? '已封禁' : '已删除'} ${r.count} 个用户`)
      reload()
    },
    '',
  )
}
const batchOptions = [
  { label: '导出 CSV（每个订阅一行）', key: 'export' },
  { label: '群发邮件', key: 'mail' },
  { label: '批量封禁', key: 'ban' },
  { label: '批量删除', key: 'delete' },
]

onMounted(() => {
  const uid = route.query.id
  if (uid) filter.value = [{ key: 'id', condition: '=', value: String(uid) }]
  load()
})
</script>

<template>
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NInput v-model:value="keyword" placeholder="搜索邮箱" clearable style="width: 220px" @keyup.enter="reload" @clear="reload" />
        <FilterEditor v-model="filter" :fields="filterFields" @apply="reload" />
        <NButton @click="reload">刷新</NButton>
        <span class="spacer" />
        <NDropdown :options="batchOptions" trigger="click" @select="batch">
          <NButton>批量操作（按当前筛选）</NButton>
        </NDropdown>
        <NButton type="primary" @click="openGen">创建用户</NButton>
      </div>
      <NDataTable
        remote
        size="small"
        :columns="columns"
        :data="rows"
        :loading="loading"
        :pagination="pagination"
        :row-key="(r: UserRow) => r.id"
        :scroll-x="1000"
        @update:sorter="onSort"
      />
    </NCard>

    <UserDrawer v-model:show="drawer" :user-id="current" @changed="load" />

    <NModal v-model:show="gen" preset="card" title="创建用户" style="width: 520px; max-width: 95vw">
      <NForm label-placement="left" label-width="96">
        <NFormItem label="邮箱">
          <NSpace :wrap="false" align="center">
            <NInput v-model:value="genForm.email_prefix" placeholder="留空则批量生成" />
            <span>@</span>
            <NInput v-model:value="genForm.email_suffix" placeholder="example.com" />
          </NSpace>
        </NFormItem>
        <NFormItem v-if="!genForm.email_prefix" label="生成数量"><NInputNumber v-model:value="genForm.generate_count" :min="1" :max="500" /></NFormItem>
        <NFormItem label="密码"><NInput v-model:value="genForm.password" placeholder="留空则与邮箱相同" /></NFormItem>
        <NFormItem label="套餐"><NSelect v-model:value="genForm.plan_id" :options="planOptions" clearable placeholder="不开通套餐" /></NFormItem>
        <NFormItem label="到期时间"><NDatePicker v-model:value="genForm.expired_at" type="datetime" clearable placeholder="有套餐时留空为长期有效" /></NFormItem>
        <div class="muted">批量生成会直接下载包含账号、密码与订阅地址的 CSV 文件，请妥善保管。</div>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="gen = false">取消</NButton><NButton type="primary" @click="submitGen">生成</NButton></NSpace>
      </template>
    </NModal>

    <NModal v-model:show="mail" preset="card" title="群发邮件（按当前筛选条件）" style="width: 640px; max-width: 95vw">
      <NForm label-placement="top">
        <NFormItem label="标题"><NInput v-model:value="mailForm.subject" /></NFormItem>
        <NFormItem label="内容（HTML）"><NInput v-model:value="mailForm.content" type="textarea" :autosize="{ minRows: 6, maxRows: 16 }" /></NFormItem>
        <NCheckbox v-model:checked="mailForm.all">没有筛选条件时发送给全部用户</NCheckbox>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="mail = false">取消</NButton><NButton type="primary" @click="submitMail">发送</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>