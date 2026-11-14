<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NCard, NDataTable, NButton, NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NDatePicker, NRadioGroup, NRadio,
  type DataTableColumns, type DataTableSortState,
} from 'naive-ui'
import { formatMoney, formatTime, nowUnix } from '@gv/shared'
import { admin } from '../api'
import { message, run } from '../ui'
import { usePaged } from '../paged'
import { actions, btn, confirmBtn, ellipsis, periodLabels, periodOptions, sw, toCents, toMs, toSec, toYuan, type PageResult } from '../helpers'
import { loadPlans, planName, planOptions } from '../refs'
import type { Coupon } from '../types'

const sort = ref<{ sort?: string; sort_type?: string }>({})
const { rows, loading, pagination, load, reload } = usePaged<Coupon>(async (current, page_size) => {
  const r = await admin.get<PageResult<Coupon>>('/coupons', { current, page_size, ...sort.value })
  return { rows: r.data || [], total: r.total }
})
function onSort(s: DataTableSortState | null) {
  sort.value = s && s.order ? { sort: String(s.columnKey), sort_type: s.order === 'ascend' ? 'ASC' : 'DESC' } : {}
  reload()
}

async function toggle(c: Coupon) {
  if (await run(() => admin.post(`/coupons/${c.id}/toggle`))) c.show = c.show ? 0 : 1
}
async function remove(c: Coupon) {
  if (await run(() => admin.del(`/coupons/${c.id}`), '已删除')) load()
}

function limits(c: Coupon) {
  const out: string[] = []
  if (c.limit_plan_ids?.length) out.push(`套餐：${c.limit_plan_ids.map(planName).join('、')}`)
  if (c.limit_period?.length) out.push(`周期：${c.limit_period.map((p) => periodLabels[p] || p).join('、')}`)
  if (c.limit_use_with_user) out.push(`每人 ${c.limit_use_with_user} 次`)
  return out.length ? ellipsis(out.join('；'), 220) : '-'
}

const columns: DataTableColumns<Coupon> = [
  { title: 'ID', key: 'id', width: 70, sorter: true },
  { title: '名称', key: 'name', sorter: true },
  { title: '券码', key: 'code', sorter: true, render: (c) => ellipsis(c.code, 140) },
  { title: '优惠', key: 'value', render: (c) => (c.type === 1 ? `减 ${formatMoney(c.value)}` : `${c.value}% 折扣`) },
  { title: '启用', key: 'show', width: 70, sorter: true, render: (c) => sw(!!c.show, () => toggle(c)) },
  { title: '剩余次数', key: 'limit_use', sorter: true, render: (c) => c.limit_use ?? '不限' },
  { title: '限制', key: 'limits', render: limits },
  {
    title: '有效期',
    key: 'ended_at',
    sorter: true,
    render: (c) => `${formatTime(c.started_at, false)} ~ ${formatTime(c.ended_at, false)}${c.ended_at < nowUnix() ? '（已过期）' : ''}`,
  },
  {
    title: '操作',
    key: 'op',
    width: 110,
    render: (c) => actions(btn('编辑', () => openForm(c)), confirmBtn('删除', '确定删除该优惠券？', () => remove(c))),
  },
]

// ---------- 表单 ----------
interface CouponForm {
  name: string
  code: string
  type: 1 | 2
  value: number | null
  range: [number, number] | null
  limit_use: number | null
  limit_use_with_user: number | null
  limit_plan_ids: number[]
  limit_period: string[]
  count: number
}
const show = ref(false)
const batch = ref(false)
const editing = ref<Coupon | null>(null)
const form = ref<CouponForm>(blank())

function blank(): CouponForm {
  const now = Date.now()
  return {
    name: '',
    code: '',
    type: 1,
    value: null,
    range: [now, now + 30 * 86400000],
    limit_use: null,
    limit_use_with_user: null,
    limit_plan_ids: [],
    limit_period: [],
    count: 10,
  }
}

function openForm(c: Coupon | null, isBatch = false) {
  loadPlans()
  editing.value = c
  batch.value = isBatch
  form.value = c
    ? {
        name: c.name,
        code: c.code,
        type: c.type,
        value: c.type === 1 ? toYuan(c.value) : c.value,
        range: [toMs(c.started_at)!, toMs(c.ended_at)!],
        limit_use: c.limit_use,
        limit_use_with_user: c.limit_use_with_user,
        limit_plan_ids: c.limit_plan_ids || [],
        limit_period: c.limit_period || [],
        count: 10,
      }
    : blank()
  show.value = true
}

async function submit() {
  const f = form.value
  if (!f.range || f.value === null) {
    message.warning('请填写优惠值与有效期')
    return
  }
  if (f.type === 2 && (f.value < 1 || f.value > 100)) {
    message.warning('比例优惠须在 1-100 之间')
    return
  }
  const body: Record<string, unknown> = {
    name: f.name,
    code: f.code.trim(),
    type: f.type,
    value: f.type === 1 ? toCents(f.value) : Math.round(f.value),
    started_at: toSec(f.range[0]),
    ended_at: toSec(f.range[1]),
    limit_use: f.limit_use,
    limit_use_with_user: f.limit_use_with_user,
    limit_plan_ids: f.limit_plan_ids.length ? f.limit_plan_ids : null,
    limit_period: f.limit_period.length ? f.limit_period : null,
  }
  let ok: boolean
  if (batch.value) {
    ok = await run(() => admin.download('POST', '/coupons/batch', { ...body, count: f.count }, 'coupons.csv'), '已生成并下载')
  } else if (editing.value) {
    ok = await run(() => admin.put(`/coupons/${editing.value!.id}`, body), '已保存')
  } else {
    ok = await run(() => admin.post('/coupons', body), '已创建（默认未启用）')
  }
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
        <NButton type="primary" @click="openForm(null)">新建优惠券</NButton>
        <NButton @click="openForm(null, true)">批量生成</NButton>
        <NButton @click="load">刷新</NButton>
      </div>
      <NDataTable remote size="small" :columns="columns" :data="rows" :loading="loading" :pagination="pagination" :row-key="(c: Coupon) => c.id" :scroll-x="1000" @update:sorter="onSort" />
    </NCard>

    <NModal v-model:show="show" preset="card" :title="batch ? '批量生成优惠券' : editing ? '编辑优惠券' : '新建优惠券'" style="width: 560px; max-width: 95vw">
      <NForm label-placement="left" label-width="110">
        <NFormItem label="名称" required><NInput v-model:value="form.name" /></NFormItem>
        <NFormItem v-if="!batch" label="券码"><NInput v-model:value="form.code" placeholder="留空自动生成" /></NFormItem>
        <NFormItem v-else label="生成数量"><NInputNumber v-model:value="form.count" :min="1" :max="500" /></NFormItem>
        <NFormItem label="类型">
          <NRadioGroup v-model:value="form.type">
            <NRadio :value="1">按金额</NRadio>
            <NRadio :value="2">按比例</NRadio>
          </NRadioGroup>
        </NFormItem>
        <NFormItem :label="form.type === 1 ? '减免金额（元）' : '折扣比例（%）'" required>
          <NInputNumber v-model:value="form.value" :min="0" :max="form.type === 2 ? 100 : undefined" :precision="form.type === 1 ? 2 : 0" style="width: 100%" />
        </NFormItem>
        <NFormItem label="有效期" required><NDatePicker v-model:value="form.range" type="datetimerange" /></NFormItem>
        <NFormItem label="总可用次数"><NInputNumber v-model:value="form.limit_use" :min="0" clearable placeholder="不限" style="width: 100%" /></NFormItem>
        <NFormItem label="每人可用次数"><NInputNumber v-model:value="form.limit_use_with_user" :min="0" clearable placeholder="不限" style="width: 100%" /></NFormItem>
        <NFormItem label="限定套餐"><NSelect v-model:value="form.limit_plan_ids" multiple :options="planOptions" placeholder="不限" /></NFormItem>
        <NFormItem label="限定周期"><NSelect v-model:value="form.limit_period" multiple :options="periodOptions" placeholder="不限" /></NFormItem>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="show = false">取消</NButton><NButton type="primary" @click="submit">{{ batch ? '生成并下载' : '保存' }}</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
