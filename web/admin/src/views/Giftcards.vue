<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  NCard, NDataTable, NButton, NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NDatePicker,
  type DataTableColumns, type DataTableSortState,
} from 'naive-ui'
import { formatMoney, formatTime } from '@gv/shared'
import { admin } from '../api'
import { message, run } from '../ui'
import { usePaged } from '../paged'
import { actions, btn, confirmBtn, ellipsis, giftcardTypes, toCents, toMs, toSec, toYuan, type PageResult } from '../helpers'
import { loadPlans, planName, planOptions } from '../refs'
import type { Giftcard } from '../types'

const sort = ref<{ sort?: string; sort_type?: string }>({})
const { rows, loading, pagination, load, reload } = usePaged<Giftcard>(async (current, page_size) => {
  const r = await admin.get<PageResult<Giftcard>>('/giftcards', { current, page_size, ...sort.value })
  return { rows: r.data || [], total: r.total }
})
function onSort(s: DataTableSortState | null) {
  sort.value = s && s.order ? { sort: String(s.columnKey), sort_type: s.order === 'ascend' ? 'ASC' : 'DESC' } : {}
  reload()
}

function valueText(g: Giftcard) {
  switch (g.type) {
    case 1:
      return formatMoney(g.value)
    case 2:
      return `${g.value} 天`
    case 3:
      return `${g.value} GB`
    case 5:
      return `${planName(g.plan_id)}，${g.value ? `${g.value} 天` : '长期有效'}`
    default:
      return '-'
  }
}

async function remove(g: Giftcard) {
  if (await run(() => admin.del(`/giftcards/${g.id}`), '已删除')) load()
}

const columns: DataTableColumns<Giftcard> = [
  { title: 'ID', key: 'id', width: 70, sorter: true },
  { title: '名称', key: 'name', sorter: true },
  { title: '卡密', key: 'code', sorter: true, render: (g) => ellipsis(g.code, 160) },
  { title: '类型', key: 'type', sorter: true, render: (g) => giftcardTypes[g.type] },
  { title: '数值', key: 'value', render: valueText },
  { title: '已用 / 次数', key: 'limit_use', render: (g) => `${g.used_user_ids?.length ?? 0} / ${g.limit_use ?? '不限'}` },
  {
    title: '有效期',
    key: 'ended_at',
    sorter: true,
    render: (g) => `${g.started_at ? formatTime(g.started_at, false) : '不限'} ~ ${g.ended_at ? formatTime(g.ended_at, false) : '不限'}`,
  },
  {
    title: '操作',
    key: 'op',
    width: 110,
    render: (g) => actions(btn('编辑', () => openForm(g)), confirmBtn('删除', '确定删除该礼品卡？', () => remove(g))),
  },
]

interface GiftForm {
  name: string
  code: string
  type: number
  value: number | null
  plan_id: number | null
  limit_use: number | null
  started_at: number | null
  ended_at: number | null
  count: number
}
const show = ref(false)
const batch = ref(false)
const editing = ref<Giftcard | null>(null)
const form = ref<GiftForm>(blank())
const typeOptions = Object.entries(giftcardTypes).map(([v, l]) => ({ label: l, value: Number(v) }))
const valueLabel = computed(() => ({ 1: '金额（元）', 2: '延长天数', 3: '增加流量（GB）', 4: '', 5: '有效天数（0 为长期）' })[form.value.type as 1])

function blank(): GiftForm {
  return { name: '', code: '', type: 1, value: null, plan_id: null, limit_use: 1, started_at: null, ended_at: null, count: 10 }
}

function openForm(g: Giftcard | null, isBatch = false) {
  loadPlans()
  editing.value = g
  batch.value = isBatch
  form.value = g
    ? {
        name: g.name,
        code: g.code,
        type: g.type,
        value: g.type === 1 ? toYuan(g.value) : g.value,
        plan_id: g.plan_id,
        limit_use: g.limit_use,
        started_at: toMs(g.started_at),
        ended_at: toMs(g.ended_at),
        count: 10,
      }
    : blank()
  show.value = true
}

async function submit() {
  const f = form.value
  if (f.type !== 4 && f.value === null) {
    message.warning('请填写数值')
    return
  }
  const body: Record<string, unknown> = {
    name: f.name,
    code: f.code.trim(),
    type: f.type,
    value: f.type === 4 ? null : f.type === 1 ? toCents(f.value) : f.value,
    plan_id: f.type === 5 ? f.plan_id : null,
    limit_use: f.limit_use,
    started_at: toSec(f.started_at) ?? 0,
    ended_at: toSec(f.ended_at) ?? 0,
  }
  let ok: boolean
  if (batch.value) ok = await run(() => admin.download('POST', '/giftcards/batch', { ...body, count: f.count }, 'giftcards.csv'), '已生成并下载')
  else if (editing.value) ok = await run(() => admin.put(`/giftcards/${editing.value!.id}`, body), '已保存')
  else ok = await run(() => admin.post('/giftcards', body), '已创建')
  if (ok) {
    show.value = false
    load()
  }
}

onMounted(() => {
  loadPlans().catch(() => {})
  load()
})
</script>

<template>
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NButton type="primary" @click="openForm(null)">新建礼品卡</NButton>
        <NButton @click="openForm(null, true)">批量生成</NButton>
        <NButton @click="load">刷新</NButton>
      </div>
      <NDataTable remote size="small" :columns="columns" :data="rows" :loading="loading" :pagination="pagination" :row-key="(g: Giftcard) => g.id" :scroll-x="900" @update:sorter="onSort" />
    </NCard>

    <NModal v-model:show="show" preset="card" :title="batch ? '批量生成礼品卡' : editing ? '编辑礼品卡' : '新建礼品卡'" style="width: 520px; max-width: 95vw">
      <NForm label-placement="left" label-width="120">
        <NFormItem label="名称" required><NInput v-model:value="form.name" maxlength="255" /></NFormItem>
        <NFormItem v-if="!batch" label="卡密"><NInput v-model:value="form.code" placeholder="留空自动生成" /></NFormItem>
        <NFormItem v-else label="生成数量"><NInputNumber v-model:value="form.count" :min="1" :max="500" /></NFormItem>
        <NFormItem label="类型"><NSelect v-model:value="form.type" :options="typeOptions" /></NFormItem>
        <NFormItem v-if="form.type === 5" label="套餐" required><NSelect v-model:value="form.plan_id" :options="planOptions" /></NFormItem>
        <NFormItem v-if="form.type !== 4" :label="valueLabel" required>
          <NInputNumber v-model:value="form.value" :min="0" :precision="form.type === 1 ? 2 : 0" style="width: 100%" />
        </NFormItem>
        <NFormItem label="可用次数"><NInputNumber v-model:value="form.limit_use" :min="0" clearable placeholder="不限" style="width: 100%" /></NFormItem>
        <NFormItem label="开始时间"><NDatePicker v-model:value="form.started_at" type="datetime" clearable placeholder="不限" /></NFormItem>
        <NFormItem label="结束时间"><NDatePicker v-model:value="form.ended_at" type="datetime" clearable placeholder="不限" /></NFormItem>
        <div class="muted">时长、流量、重置类作用于用户兑换时选择的订阅（默认主订阅）；套餐类不选订阅时为用户新开订阅。</div>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="show = false">取消</NButton><NButton type="primary" @click="submit">{{ batch ? '生成并下载' : '保存' }}</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
