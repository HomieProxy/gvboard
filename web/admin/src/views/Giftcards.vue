<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { PlusIcon, RefreshCwIcon, PencilIcon, Trash2Icon, Loader2Icon, CopyIcon } from '@lucide/vue'
import { copyText, formatMoney, formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column, type SortState } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import NumInput from '../components/common/NumInput.vue'
import DateTimeInput from '../components/common/DateTimeInput.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { usePaged } from '../paged'
import { giftcardTypeOptions, giftcardTypes, toCents, toYuan, type PageResult } from '../helpers'
import { loadPlans, planName, planOptions } from '../refs'
import type { Giftcard } from '../types'

const sort = ref<SortState | null>(null)
const { rows, loading, pager, load, reload, setPage, setPageSize } = usePaged<Giftcard>(async (current, page_size) => {
  const q: Record<string, string | number> = { current, page_size }
  if (sort.value) Object.assign(q, { sort: sort.value.key, sort_type: sort.value.order })
  const r = await admin.get<PageResult<Giftcard>>('/giftcards', q)
  return { rows: r.data || [], total: r.total }
})
function onSort(s: SortState | null) {
  sort.value = s
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
      return `${planName(g.plan_id)} · ${g.value ? `${g.value} 天` : '一次性'}`
    default:
      return '-'
  }
}

const columns: Column[] = [
  { key: 'id', title: 'ID', class: 'w-16', sortable: true },
  { key: 'name', title: '名称', sortable: true },
  { key: 'type', title: '类型', sortable: true },
  { key: 'value', title: '数值', sortable: true },
  { key: 'code', title: '卡密', sortable: true },
  { key: 'limit_use', title: '剩余次数', align: 'center', sortable: true },
  { key: 'ended_at', title: '有效期', sortable: true },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]

async function remove(g: Giftcard) {
  if (await confirm(`确定要删除礼品卡「${g.name}」吗？`, () => admin.del(`/giftcards/${g.id}`), '删除成功')) load()
}
async function copyCode(g: Giftcard) {
  const ok = await copyText(g.code)
  message[ok ? 'success' : 'error'](ok ? '复制成功' : '复制失败')
}

// ---------- 表单 ----------
interface GiftForm {
  name: string
  code: string
  type: number
  value: number | null
  plan_id: number | null
  limit_use: number | null
  started_at: number | null
  ended_at: number | null
  generate_count: number | null
}
const show = ref(false)
const saving = ref(false)
const editing = ref<Giftcard | null>(null)
const form = ref<GiftForm>(blank())

function blank(): GiftForm {
  return { name: '', code: '', type: 1, value: null, plan_id: null, limit_use: null, started_at: null, ended_at: null, generate_count: null }
}
const valueSuffix = computed(() => ({ 1: '元', 2: '天', 3: 'GB', 5: '天' })[form.value.type as 1] || '')

function openForm(g: Giftcard | null) {
  loadPlans()
  editing.value = g
  form.value = g
    ? {
        name: g.name,
        code: g.code,
        type: g.type,
        value: g.type === 1 ? toYuan(g.value) : g.value,
        plan_id: g.plan_id,
        limit_use: g.limit_use,
        started_at: g.started_at || null,
        ended_at: g.ended_at || null,
        generate_count: null,
      }
    : blank()
  show.value = true
}

async function submit() {
  const f = form.value
  if (!f.name.trim()) {
    message.warning('请输入礼品卡名称')
    return
  }
  if (f.type !== 4 && f.value === null) {
    message.warning('请输入值')
    return
  }
  if (f.type === 5 && !f.plan_id) {
    message.warning('请选择指定订阅')
    return
  }
  const body: Record<string, unknown> = {
    name: f.name.trim(),
    code: f.code.trim(),
    type: f.type,
    value: f.type === 4 ? null : f.type === 1 ? toCents(f.value) : f.value,
    plan_id: f.type === 5 ? f.plan_id : null,
    limit_use: f.limit_use,
    started_at: f.started_at ?? 0,
    ended_at: f.ended_at ?? 0,
  }
  saving.value = true
  let ok: boolean
  if (!editing.value && f.generate_count) {
    ok = await run(() => admin.download('POST', '/giftcards/batch', { ...body, count: f.generate_count }, 'giftcards.csv'), '已生成并下载')
  } else if (editing.value) {
    ok = await run(() => admin.put(`/giftcards/${editing.value!.id}`, body), '保存成功')
  } else {
    ok = await run(() => admin.post('/giftcards', body), '保存成功')
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
    <PageHeader title="礼品卡管理" description="用户在个人中心兑换礼品卡。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <Button @click="openForm(null)"><PlusIcon />添加礼品卡</Button>
    </PageHeader>

    <DataTable :columns="columns" :rows="rows" :loading="loading" :sort="sort" @update:sort="onSort">
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-name="{ row }"><span class="font-medium">{{ row.name }}</span></template>
      <template #cell-type="{ row }"><span class="rounded bg-muted px-1.5 py-0.5 text-xs">{{ giftcardTypes[row.type] }}</span></template>
      <template #cell-value="{ row }"><span class="tabular-nums">{{ valueText(row) }}</span></template>
      <template #cell-code="{ row }">
        <button type="button" class="inline-flex items-center gap-1 font-mono text-xs hover:text-primary" @click="copyCode(row)">
          {{ row.code }}<CopyIcon class="size-3" />
        </button>
      </template>
      <template #cell-limit_use="{ row }">
        <span class="tabular-nums">{{ row.limit_use ?? '无限' }}</span>
      </template>
      <template #cell-ended_at="{ row }">
        <span class="whitespace-nowrap text-xs">
          {{ row.started_at ? formatTime(row.started_at, false) : '不限' }} ~ {{ row.ended_at ? formatTime(row.ended_at, false) : '不限' }}
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
        <DialogHeader>
          <DialogTitle>{{ editing ? '编辑礼品卡' : '添加礼品卡' }}</DialogTitle>
          <DialogDescription>时长、流量、重置类作用于用户兑换时选择的订阅（默认主订阅）；套餐类未选择订阅时为用户新开订阅。</DialogDescription>
        </DialogHeader>
        <div class="grid gap-4">
          <FormItem label="名称">
            <Input v-model="form.name" maxlength="255" placeholder="请输入礼品卡名称" />
          </FormItem>
          <FormItem label="自定义礼品卡卡密">
            <Input v-model="form.code" placeholder="自定义礼品卡卡密(留空随机生成)" :disabled="!!form.generate_count" />
          </FormItem>
          <FormItem label="礼品卡类型">
            <div class="flex gap-2">
              <SelectBox class="w-40" :model-value="form.type" :options="giftcardTypeOptions" @update:model-value="(v) => (form.type = Number(v))" />
              <NumInput
                v-if="form.type !== 4"
                v-model="form.value"
                class="flex-1"
                :min="0"
                :step="form.type === 1 ? 0.01 : 1"
                :suffix="valueSuffix"
                :placeholder="form.type === 5 ? '一次性套餐输入0' : '请输入值'"
              />
            </div>
          </FormItem>
          <FormItem v-if="form.type === 5" label="指定订阅">
            <SelectBox :model-value="form.plan_id" :options="planOptions" placeholder="指定订阅" @update:model-value="(v) => (form.plan_id = v as number)" />
          </FormItem>
          <FormItem label="礼品卡有效期" help="留空则不限制">
            <div class="flex items-center gap-2">
              <DateTimeInput v-model="form.started_at" />
              <span class="text-muted-foreground">~</span>
              <DateTimeInput v-model="form.ended_at" />
            </div>
          </FormItem>
          <FormItem label="最大使用次数" help="限制最大使用次数，用完则无法使用(为空则不限制)">
            <NumInput v-model="form.limit_use" :min="0" />
          </FormItem>
          <FormItem v-if="!editing" label="生成数量" help="输入数量批量生成，生成后自动下载 CSV（批量生成时卡密随机）">
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
