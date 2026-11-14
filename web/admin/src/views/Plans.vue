<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { PlusIcon, RefreshCwIcon, PencilIcon, Trash2Icon, Loader2Icon } from '@lucide/vue'
import { formatMoney } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Checkbox } from '@gv/shared/ui/checkbox'
import { Label } from '@gv/shared/ui/label'
import { Switch } from '@gv/shared/ui/switch'
import { Tooltip, TooltipContent, TooltipTrigger } from '@gv/shared/ui/tooltip'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import NumInput from '../components/common/NumInput.vue'
import PlanContentEditor from '../components/PlanContentEditor.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { useList } from '../paged'
import { moveItem, planPeriods, resetMethods, toCents, toYuan } from '../helpers'
import { groupName, groupOptions, invalidate, loadGroups } from '../refs'
import type { Plan } from '../types'

/** 套餐表单中的周期名称（与原版一致） */
const priceLabels: Record<string, string> = {
  month_price: '月付',
  quarter_price: '季付',
  half_year_price: '半年',
  year_price: '年付',
  two_year_price: '两年付',
  three_year_price: '三年付',
  onetime_price: '一次性',
  reset_price: '重置包',
}

const { rows, loading, load } = useList(() => admin.get<Plan[]>('/plans'))

async function patch(p: Plan, body: Record<string, number>) {
  if (await run(() => admin.patch(`/plans/${p.id}`, body))) {
    Object.assign(p, body)
    invalidate('plans')
  }
}

async function reorder(from: number, to: number) {
  const list = moveItem(rows.value, from, to)
  rows.value = list
  if (await run(() => admin.post('/plans/sort', { ids: list.map((p) => p.id) }))) invalidate('plans')
  else load()
}

async function remove(p: Plan) {
  if (await confirm(`确定要删除订阅「${p.name}」吗？`, () => admin.del(`/plans/${p.id}`), '删除成功')) {
    invalidate('plans')
    load()
  }
}

const prices = (p: Plan) =>
  planPeriods.filter((k) => p[k as keyof Plan] !== null && p[k as keyof Plan] !== undefined).map((k) => ({ label: priceLabels[k], value: formatMoney(p[k as keyof Plan] as number) }))

const columns: Column[] = [
  { key: 'id', title: 'ID', class: 'w-14' },
  { key: 'show', title: '销售状态', align: 'center' },
  { key: 'renew', title: '续费', align: 'center' },
  { key: 'name', title: '名称' },
  { key: 'count', title: '统计', align: 'center' },
  { key: 'transfer_enable', title: '流量', align: 'right' },
  { key: 'device_limit', title: '设备数限制', align: 'center' },
  { key: 'prices', title: '售价' },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
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
const saving = ref(false)
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

// ---------- 快速添加权限组 ----------
const newGroup = ref('')
const addingGroup = ref(false)
async function addGroup() {
  const name = newGroup.value.trim()
  if (!name) return
  addingGroup.value = true
  const ok = await run(() => admin.post('/servers/groups', { name }), '添加成功')
  addingGroup.value = false
  if (ok) {
    newGroup.value = ''
    invalidate('groups')
    const list = await loadGroups(true)
    form.value.group_id = list.find((g) => g.name === name)?.id ?? form.value.group_id
  }
}

async function submit() {
  const f = form.value
  if (!f.name.trim() || !f.group_id || f.transfer_enable === null) {
    message.warning('请填写套餐名称、套餐流量并选择权限组')
    return
  }
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
  saving.value = true
  const ok = await run(() => (editing.value ? admin.put(`/plans/${editing.value.id}`, body) : admin.post('/plans', body)), '保存成功')
  saving.value = false
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
  <div>
    <PageHeader title="订阅管理" description="新建的订阅默认不销售，需手动打开「销售状态」。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <Button @click="openForm(null)"><PlusIcon />添加订阅</Button>
    </PageHeader>

    <DataTable :columns="columns" :rows="rows" :loading="loading" draggable @reorder="reorder">
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-show="{ row }"><Switch :model-value="!!row.show" @update:model-value="(v: boolean) => patch(row, { show: v ? 1 : 0 })" /></template>
      <template #cell-renew="{ row }">
        <Tooltip>
          <TooltipTrigger as-child>
            <span><Switch :model-value="!!row.renew" @update:model-value="(v: boolean) => patch(row, { renew: v ? 1 : 0 })" /></span>
          </TooltipTrigger>
          <TooltipContent>在订阅停止销售时，已购用户是否可以续费</TooltipContent>
        </Tooltip>
      </template>
      <template #cell-name="{ row }">
        <div class="grid leading-tight">
          <span class="font-medium">{{ row.name }}</span>
          <span class="text-xs text-muted-foreground">{{ groupName(row.group_id) }}</span>
        </div>
      </template>
      <template #cell-count="{ row }">
        <Tooltip>
          <TooltipTrigger as-child>
            <span class="tabular-nums">{{ row.count ?? 0 }}<span v-if="row.capacity_limit" class="text-muted-foreground"> / {{ row.capacity_limit }}</span></span>
          </TooltipTrigger>
          <TooltipContent>有效订阅数{{ row.capacity_limit ? ' / 最大容纳用户量' : '' }}</TooltipContent>
        </Tooltip>
      </template>
      <template #cell-transfer_enable="{ row }"><span class="whitespace-nowrap tabular-nums">{{ row.transfer_enable }} GB</span></template>
      <template #cell-device_limit="{ row }">{{ row.device_limit ?? '-' }}</template>
      <template #cell-prices="{ row }">
        <div class="flex max-w-md flex-wrap gap-1">
          <span v-for="p in prices(row)" :key="p.label" class="rounded bg-muted px-1.5 py-0.5 text-xs whitespace-nowrap">
            <span class="text-muted-foreground">{{ p.label }}</span> {{ p.value }}
          </span>
          <span v-if="!prices(row).length" class="text-muted-foreground">-</span>
        </div>
      </template>
      <template #cell-op="{ row }">
        <div class="flex justify-end gap-0.5">
          <Button variant="ghost" size="icon" class="size-8" @click="openForm(row)"><PencilIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" @click="remove(row)"><Trash2Icon /></Button>
        </div>
      </template>
    </DataTable>

    <Dialog v-model:open="show">
      <DialogContent class="max-h-[92svh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{{ editing ? '编辑订阅' : '添加订阅' }}</DialogTitle>
        </DialogHeader>
        <div class="grid gap-4 sm:grid-cols-2">
          <FormItem label="套餐名称" required class="sm:col-span-2"><Input v-model="form.name" placeholder="请输入套餐名称" /></FormItem>
          <FormItem label="套餐描述" class="sm:col-span-2"><PlanContentEditor v-model="form.content" /></FormItem>
          <FormItem label="售价设置" help="将金额留空则不会进行出售" class="sm:col-span-2">
            <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <div v-for="k in planPeriods" :key="k" class="relative">
                <span class="pointer-events-none absolute inset-y-0 left-3 z-10 flex items-center text-xs text-muted-foreground">{{ priceLabels[k] }}</span>
                <NumInput v-model="form.prices[k]" :min="0" :step="0.01" class="[&_input]:pl-14" />
              </div>
            </div>
          </FormItem>
          <FormItem label="套餐流量" required><NumInput v-model="form.transfer_enable" :min="0" suffix="GB" placeholder="请输入套餐流量" /></FormItem>
          <FormItem label="设备数限制"><NumInput v-model="form.device_limit" :min="0" placeholder="留空则不限制" /></FormItem>
          <FormItem label="权限组" required class="sm:col-span-2">
            <div class="flex gap-2">
              <SelectBox class="flex-1" :model-value="form.group_id" :options="groupOptions" placeholder="请选择权限组" @update:model-value="(v) => (form.group_id = v as number)" />
              <Input v-model="newGroup" class="w-40" placeholder="新权限组名称" @keyup.enter="addGroup" />
              <Button variant="outline" :disabled="!newGroup.trim() || addingGroup" @click="addGroup"><PlusIcon />添加权限组</Button>
            </div>
          </FormItem>
          <FormItem label="流量重置方式">
            <SelectBox :model-value="form.reset_traffic_method" :options="resetMethods" @update:model-value="(v) => (form.reset_traffic_method = Number(v))" />
          </FormItem>
          <FormItem label="最大容纳用户量"><NumInput v-model="form.capacity_limit" :min="0" placeholder="留空则不限制" /></FormItem>
          <FormItem label="限速"><NumInput v-model="form.speed_limit" :min="0" suffix="Mbps" placeholder="留空则不限制" /></FormItem>
        </div>
        <DialogFooter class="items-center sm:justify-between">
          <div v-if="editing" class="flex items-center gap-2">
            <Checkbox id="plan-force" :model-value="form.force_update" @update:model-value="(v) => (form.force_update = v === true)" />
            <Label for="plan-force" class="text-sm font-normal">强制更新到用户<span class="ml-1 text-xs text-muted-foreground">（变更的流量、限速、权限组将应用到该套餐下的用户）</span></Label>
          </div>
          <span v-else />
          <div class="flex gap-2">
            <Button variant="outline" @click="show = false">取消</Button>
            <Button :disabled="saving" @click="submit"><Loader2Icon v-if="saving" class="animate-spin" />提交</Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
