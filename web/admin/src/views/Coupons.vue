<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { PlusIcon, RefreshCwIcon, PencilIcon, Trash2Icon, Loader2Icon, CopyIcon } from '@lucide/vue'
import { copyText, formatMoney, formatTime, nowUnix } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Switch } from '@gv/shared/ui/switch'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column, type SortState } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import MultiSelect from '../components/common/MultiSelect.vue'
import NumInput from '../components/common/NumInput.vue'
import DateTimeInput from '../components/common/DateTimeInput.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { usePaged } from '../paged'
import { periodOptions, toCents, toYuan, type PageResult } from '../helpers'
import { loadPlans, planOptions } from '../refs'
import type { Coupon } from '../types'

const sort = ref<SortState | null>(null)
const { rows, loading, pager, load, reload, setPage, setPageSize } = usePaged<Coupon>(async (current, page_size) => {
  const q: Record<string, string | number> = { current, page_size }
  if (sort.value) Object.assign(q, { sort: sort.value.key, sort_type: sort.value.order })
  const r = await admin.get<PageResult<Coupon>>('/coupons', q)
  return { rows: r.data || [], total: r.total }
})
function onSort(s: SortState | null) {
  sort.value = s
  reload()
}

const columns: Column[] = [
  { key: 'id', title: 'ID', class: 'w-16', sortable: true },
  { key: 'show', title: '启用', align: 'center', class: 'w-16', sortable: true },
  { key: 'name', title: '券名称', sortable: true },
  { key: 'type', title: '类型', sortable: true },
  { key: 'code', title: '券码', sortable: true },
  { key: 'limit_use', title: '剩余次数', align: 'center', sortable: true },
  { key: 'ended_at', title: '有效期', sortable: true },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]

async function toggle(c: Coupon) {
  if (await run(() => admin.post(`/coupons/${c.id}/toggle`))) c.show = c.show ? 0 : 1
}
async function remove(c: Coupon) {
  if (await confirm(`确定要删除优惠券「${c.name}」吗？`, () => admin.del(`/coupons/${c.id}`), '删除成功')) load()
}
async function copyCode(c: Coupon) {
  const ok = await copyText(c.code)
  message[ok ? 'success' : 'error'](ok ? '复制成功' : '复制失败')
}

// ---------- 表单 ----------
interface CouponForm {
  name: string
  code: string
  type: 1 | 2
  value: number | null
  started_at: number | null
  ended_at: number | null
  limit_use: number | null
  limit_use_with_user: number | null
  limit_plan_ids: number[]
  limit_period: string[]
  generate_count: number | null
}
const show = ref(false)
const saving = ref(false)
const editing = ref<Coupon | null>(null)
const form = ref<CouponForm>(blank())

function blank(): CouponForm {
  const now = nowUnix()
  return {
    name: '',
    code: '',
    type: 1,
    value: null,
    started_at: now,
    ended_at: now + 30 * 86400,
    limit_use: null,
    limit_use_with_user: null,
    limit_plan_ids: [],
    limit_period: [],
    generate_count: null,
  }
}

function openForm(c: Coupon | null) {
  loadPlans()
  editing.value = c
  form.value = c
    ? {
        name: c.name,
        code: c.code,
        type: c.type,
        value: c.type === 1 ? toYuan(c.value) : c.value,
        started_at: c.started_at,
        ended_at: c.ended_at,
        limit_use: c.limit_use,
        limit_use_with_user: c.limit_use_with_user,
        limit_plan_ids: c.limit_plan_ids || [],
        limit_period: c.limit_period || [],
        generate_count: null,
      }
    : blank()
  show.value = true
}

async function submit() {
  const f = form.value
  if (!f.name.trim() || f.value === null || !f.started_at || !f.ended_at) {
    message.warning('请填写名称、优惠信息与优惠券有效期')
    return
  }
  if (f.type === 2 && (f.value < 1 || f.value > 100)) {
    message.warning('比例优惠须在 1-100 之间')
    return
  }
  const body: Record<string, unknown> = {
    name: f.name.trim(),
    code: f.code.trim(),
    type: f.type,
    value: f.type === 1 ? toCents(f.value) : Math.round(f.value),
    started_at: f.started_at,
    ended_at: f.ended_at,
    limit_use: f.limit_use,
    limit_use_with_user: f.limit_use_with_user,
    limit_plan_ids: f.limit_plan_ids.length ? f.limit_plan_ids : null,
    limit_period: f.limit_period.length ? f.limit_period : null,
  }
  saving.value = true
  let ok: boolean
  if (!editing.value && f.generate_count) {
    ok = await run(() => admin.download('POST', '/coupons/batch', { ...body, count: f.generate_count }, 'coupons.csv'), '已生成并下载')
  } else if (editing.value) {
    ok = await run(() => admin.put(`/coupons/${editing.value!.id}`, body), '保存成功')
  } else {
    ok = await run(() => admin.post('/coupons', body), '保存成功')
  }
  saving.value = false
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
  <div>
    <PageHeader title="优惠券管理" description="新添加的优惠券默认不启用。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <Button @click="openForm(null)"><PlusIcon />添加优惠券</Button>
    </PageHeader>

    <DataTable :columns="columns" :rows="rows" :loading="loading" :sort="sort" @update:sort="onSort">
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-show="{ row }"><Switch :model-value="!!row.show" @update:model-value="toggle(row)" /></template>
      <template #cell-name="{ row }"><span class="font-medium">{{ row.name }}</span></template>
      <template #cell-type="{ row }">
        <span class="rounded bg-muted px-1.5 py-0.5 text-xs">{{ row.type === 1 ? '金额' : '比例' }}</span>
        <span class="ml-1.5 tabular-nums">{{ row.type === 1 ? formatMoney(row.value) : `${row.value}%` }}</span>
      </template>
      <template #cell-code="{ row }">
        <button type="button" class="inline-flex items-center gap-1 font-mono text-xs hover:text-primary" @click="copyCode(row)">
          {{ row.code }}<CopyIcon class="size-3" />
        </button>
      </template>
      <template #cell-limit_use="{ row }">
        <span class="tabular-nums">{{ row.limit_use ?? '无限' }}</span>
      </template>
      <template #cell-ended_at="{ row }">
        <span class="whitespace-nowrap text-xs" :class="row.ended_at < nowUnix() ? 'text-destructive' : ''">
          {{ formatTime(row.started_at, false) }} ~ {{ formatTime(row.ended_at, false) }}
        </span>
      </template>
      <template #cell-op="{ row }">
        <div class="flex justify-end gap-0.5">
          <Button variant="ghost" size="icon" class="size-8" @click="openForm(row)"><PencilIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" @click="remove(row)"><Trash2Icon /></Button>
        </div>
      </template>
    </DataTable>
    <Pager :pager="pager" @page="setPage" @size="setPageSize" />

    <Dialog v-model:open="show">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader><DialogTitle>{{ editing ? '编辑优惠券' : '添加优惠券' }}</DialogTitle></DialogHeader>
        <div class="grid gap-4">
          <FormItem label="名称">
            <Input v-model="form.name" placeholder="请输入优惠券名称" />
          </FormItem>
          <FormItem label="自定义优惠券码">
            <Input v-model="form.code" placeholder="自定义优惠券码(留空随机生成)" :disabled="!!form.generate_count" />
          </FormItem>
          <FormItem label="优惠信息">
            <div class="flex gap-2">
              <SelectBox
                class="w-36"
                :model-value="form.type"
                :options="[{ value: 1, label: '按金额优惠' }, { value: 2, label: '按比例优惠' }]"
                @update:model-value="(v) => (form.type = v as 1 | 2)"
              />
              <NumInput v-model="form.value" class="flex-1" :min="0" :max="form.type === 2 ? 100 : undefined" :step="form.type === 1 ? 0.01 : 1" :suffix="form.type === 1 ? '元' : '%'" placeholder="请输入值" />
            </div>
          </FormItem>
          <FormItem label="优惠券有效期">
            <div class="flex items-center gap-2">
              <DateTimeInput v-model="form.started_at" />
              <span class="text-muted-foreground">~</span>
              <DateTimeInput v-model="form.ended_at" />
            </div>
          </FormItem>
          <FormItem label="最大使用次数" help="限制最大使用次数，用完则无法使用(为空则不限制)">
            <NumInput v-model="form.limit_use" :min="0" />
          </FormItem>
          <FormItem label="每个用户可使用次数" help="限制每个用户可使用次数(为空则不限制)">
            <NumInput v-model="form.limit_use_with_user" :min="0" />
          </FormItem>
          <FormItem label="指定订阅" help="限制指定订阅可以使用优惠(为空则不限制)">
            <MultiSelect v-model="form.limit_plan_ids" :options="planOptions" placeholder="不限制" />
          </FormItem>
          <FormItem label="指定周期" help="限制指定周期可以使用优惠(为空则不限制)">
            <MultiSelect v-model="form.limit_period" :options="periodOptions" placeholder="不限制" />
          </FormItem>
          <FormItem v-if="!editing" label="生成数量" help="输入数量批量生成，生成后自动下载 CSV（批量生成时券码随机）">
            <NumInput v-model="form.generate_count" :min="1" :max="500" placeholder="留空只添加一张" />
          </FormItem>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="show = false">取消</Button>
          <Button :disabled="saving" @click="submit"><Loader2Icon v-if="saving" class="animate-spin" />提交</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
