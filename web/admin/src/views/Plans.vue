<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import {
  NCard, NDataTable, NButton, NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NCheckbox, NGrid, NGi,
  type DataTableColumns,
} from 'naive-ui'
import { formatMoney } from '@gv/shared'
import { admin } from '../api'
import { run } from '../ui'
import { useList } from '../paged'
import { actions, btn, confirmBtn, periodLabels, planPeriods, resetMethods, sw, toCents, toYuan } from '../helpers'
import { groupName, groupOptions, invalidate, loadGroups } from '../refs'
import type { Plan } from '../types'

const { rows, loading, load } = useList(() => admin.get<Plan[]>('/plans'))

async function patch(p: Plan, body: Record<string, number>) {
  if (await run(() => admin.patch(`/plans/${p.id}`, body))) {
    Object.assign(p, body)
    invalidate('plans')
  }
}

async function move(i: number, d: number) {
  const list = [...rows.value]
  const j = i + d
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j], list[i]]
  rows.value = list
  if (await run(() => admin.post('/plans/sort', { ids: list.map((p) => p.id) }))) invalidate('plans')
  else load()
}

async function remove(p: Plan) {
  if (await run(() => admin.del(`/plans/${p.id}`), '已删除')) {
    invalidate('plans')
    load()
  }
}

function prices(p: Plan) {
  const out = planPeriods
    .filter((k) => p[k as keyof Plan] !== null && p[k as keyof Plan] !== undefined)
    .map((k) => `${periodLabels[k]} ${formatMoney(p[k as keyof Plan] as number)}`)
  return out.length ? h('div', { class: 'muted', style: 'max-width:260px' }, out.join('　')) : '-'
}

const columns: DataTableColumns<Plan> = [
  {
    title: '排序',
    key: 'sort',
    width: 80,
    render: (_, i) => actions(btn('↑', () => move(i, -1), 'default'), btn('↓', () => move(i, 1), 'default')),
  },
  { title: 'ID', key: 'id', width: 60 },
  { title: '名称', key: 'name' },
  { title: '销售', key: 'show', width: 70, render: (p) => sw(!!p.show, (v) => patch(p, { show: v ? 1 : 0 })) },
  { title: '续费', key: 'renew', width: 70, render: (p) => sw(!!p.renew, (v) => patch(p, { renew: v ? 1 : 0 })) },
  { title: '权限组', key: 'group_id', render: (p) => groupName(p.group_id) },
  { title: '流量', key: 'transfer_enable', render: (p) => `${p.transfer_enable} GB` },
  { title: '价格', key: 'prices', render: prices },
  { title: '有效订阅 / 容量', key: 'count', render: (p) => `${p.count ?? 0} / ${p.capacity_limit ?? '∞'}` },
  {
    title: '操作',
    key: 'op',
    width: 110,
    render: (p) => actions(btn('编辑', () => openForm(p)), confirmBtn('删除', '确定删除该套餐？', () => remove(p))),
  },
]

// ---------- 表单 ----------
interface PlanForm {
  name: string
  group_id: number | null
  transfer_enable: number | null
  device_limit: number | null
  speed_limit: number | null
  capacity_limit: number | null
  reset_traffic_method: number
  content: string
  prices: Record<string, number | null>
  force_update: boolean
}
const show = ref(false)
const editing = ref<Plan | null>(null)
const form = ref<PlanForm>(blank())

function blank(): PlanForm {
  return {
    name: '',
    group_id: null,
    transfer_enable: null,
    device_limit: null,
    speed_limit: null,
    capacity_limit: null,
    reset_traffic_method: -1,
    content: '',
    prices: Object.fromEntries(planPeriods.map((k) => [k, null])),
    force_update: false,
  }
}

function openForm(p: Plan | null) {
  editing.value = p
  form.value = p
    ? {
        name: p.name,
        group_id: p.group_id,
        transfer_enable: p.transfer_enable,
        device_limit: p.device_limit,
        speed_limit: p.speed_limit,
        capacity_limit: p.capacity_limit,
        reset_traffic_method: p.reset_traffic_method ?? -1,
        content: p.content || '',
        prices: Object.fromEntries(planPeriods.map((k) => [k, toYuan(p[k as keyof Plan] as number | null)])),
        force_update: false,
      }
    : blank()
  show.value = true
  loadGroups().catch(() => {})
}

async function submit() {
  const f = form.value
  const body: Record<string, unknown> = {
    name: f.name,
    group_id: f.group_id,
    transfer_enable: f.transfer_enable,
    device_limit: f.device_limit,
    speed_limit: f.speed_limit,
    capacity_limit: f.capacity_limit,
    reset_traffic_method: f.reset_traffic_method < 0 ? null : f.reset_traffic_method,
    content: f.content || null,
  }
  for (const k of planPeriods) body[k] = toCents(f.prices[k])
  if (editing.value) body.force_update = f.force_update
  const ok = await run(() => (editing.value ? admin.put(`/plans/${editing.value.id}`, body) : admin.post('/plans', body)), '已保存')
  if (ok) {
    show.value = false
    invalidate('plans')
    load()
  }
}

onMounted(() => {
  loadGroups().catch(() => {})
  load()
})
</script>

<template>
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NButton type="primary" @click="openForm(null)">新建套餐</NButton>
        <NButton @click="load">刷新</NButton>
        <span class="muted">新建的套餐默认不销售，需手动打开“销售”开关。</span>
      </div>
      <NDataTable size="small" :columns="columns" :data="rows" :loading="loading" :row-key="(p: Plan) => p.id" :scroll-x="1000" />
    </NCard>

    <NModal v-model:show="show" preset="card" :title="editing ? `编辑套餐 #${editing.id}` : '新建套餐'" style="width: 760px; max-width: 95vw">
      <NForm label-placement="top">
        <NGrid cols="1 s:2" responsive="screen" :x-gap="12">
          <NGi><NFormItem label="名称" required><NInput v-model:value="form.name" /></NFormItem></NGi>
          <NGi><NFormItem label="权限组" required><NSelect v-model:value="form.group_id" :options="groupOptions" /></NFormItem></NGi>
          <NGi><NFormItem label="流量（GB）" required><NInputNumber v-model:value="form.transfer_enable" :min="0" style="width: 100%" /></NFormItem></NGi>
          <NGi><NFormItem label="流量重置方式"><NSelect v-model:value="form.reset_traffic_method" :options="resetMethods" /></NFormItem></NGi>
          <NGi><NFormItem label="设备数限制"><NInputNumber v-model:value="form.device_limit" :min="0" clearable placeholder="不限制" style="width: 100%" /></NFormItem></NGi>
          <NGi><NFormItem label="限速（Mbps）"><NInputNumber v-model:value="form.speed_limit" :min="0" clearable placeholder="不限速" style="width: 100%" /></NFormItem></NGi>
          <NGi><NFormItem label="最大有效订阅数（容量）"><NInputNumber v-model:value="form.capacity_limit" :min="0" clearable placeholder="不限制" style="width: 100%" /></NFormItem></NGi>
        </NGrid>
        <NFormItem label="价格（元，留空表示不提供该周期）">
          <NGrid cols="2 s:4" responsive="screen" :x-gap="8" :y-gap="8" style="width: 100%">
            <NGi v-for="k in planPeriods" :key="k">
              <NInputNumber v-model:value="form.prices[k]" :min="0" :precision="2" clearable :placeholder="periodLabels[k]">
                <template #prefix><span class="muted">{{ periodLabels[k] }}</span></template>
              </NInputNumber>
            </NGi>
          </NGrid>
        </NFormItem>
        <NFormItem label="描述（HTML / Markdown）">
          <NInput v-model:value="form.content" type="textarea" :autosize="{ minRows: 4, maxRows: 14 }" />
        </NFormItem>
        <NCheckbox v-if="editing" v-model:checked="form.force_update">
          同步到正在使用该套餐的全部订阅（权限组、流量、设备数、限速）
        </NCheckbox>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="show = false">取消</NButton><NButton type="primary" @click="submit">保存</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
