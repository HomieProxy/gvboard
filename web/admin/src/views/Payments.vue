<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { PlusIcon, RefreshCwIcon, ArrowUpIcon, ArrowDownIcon, PencilIcon, Trash2Icon, Loader2Icon, CopyIcon } from '@lucide/vue'
import { copyText, formatMoney } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Switch } from '@gv/shared/ui/switch'
import { Tooltip, TooltipContent, TooltipTrigger } from '@gv/shared/ui/tooltip'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import NumInput from '../components/common/NumInput.vue'
import { admin } from '../api'
import { confirm, errText, message, run } from '../ui'
import { useList } from '../paged'
import { toCents, toYuan } from '../helpers'
import type { Payment } from '../types'

type FormField = { type: 'input'; label: string; description: string; value?: unknown } | { type: 'alert'; content: string }

const { rows, loading, load } = useList(() => admin.get<Payment[]>('/payments'))
const gateways = ref<{ label: string; value: string }[]>([])

async function toggle(p: Payment) {
  if (await run(() => admin.post(`/payments/${p.id}/toggle`))) p.enable = p.enable ? 0 : 1
}
async function remove(p: Payment) {
  if (await confirm(`确定要删除支付方式「${p.name}」吗？`, () => admin.del(`/payments/${p.id}`), '删除成功')) load()
}
async function move(i: number, d: number) {
  const list = [...rows.value]
  const j = i + d
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j], list[i]]
  rows.value = list
  if (!(await run(() => admin.post('/payments/sort', { ids: list.map((p) => p.id) })))) load()
}
async function copyNotify(p: Payment) {
  const ok = await copyText(p.notify_url)
  message[ok ? 'success' : 'error'](ok ? '复制成功' : '复制失败')
}

function fee(p: Payment) {
  const out: string[] = []
  if (p.handling_fee_percent) out.push(`${p.handling_fee_percent}%`)
  if (p.handling_fee_fixed) out.push(formatMoney(p.handling_fee_fixed))
  return out.join(' + ')
}

const columns: Column[] = [
  { key: 'sort', title: '排序', class: 'w-20' },
  { key: 'id', title: 'ID', class: 'w-14' },
  { key: 'enable', title: '启用', align: 'center', class: 'w-16' },
  { key: 'name', title: '显示名称' },
  { key: 'payment', title: '支付接口' },
  { key: 'notify_url', title: '通知地址' },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]

// ---------- 表单 ----------
const show = ref(false)
const saving = ref(false)
const editing = ref<Payment | null>(null)
const form = ref({
  name: '',
  payment: '',
  icon: '',
  notify_domain: '',
  handling_fee_fixed: null as number | null,
  handling_fee_percent: null as number | null,
})
const fields = ref<[string, FormField][]>([])
const config = ref<Record<string, string>>({})
const formLoading = ref(false)

async function loadForm() {
  if (!form.value.payment) {
    fields.value = []
    return
  }
  formLoading.value = true
  try {
    const query: Record<string, string | number> = { payment: form.value.payment }
    if (editing.value && editing.value.payment === form.value.payment) query.id = editing.value.id
    const r = await admin.get<Record<string, FormField>>('/payments/form', query)
    fields.value = Object.entries(r || {})
    const c: Record<string, string> = {}
    for (const [k, f] of fields.value) if (f.type === 'input') c[k] = f.value === undefined || f.value === null ? '' : String(f.value)
    config.value = c
  } catch (e) {
    message.error(errText(e))
    fields.value = []
  } finally {
    formLoading.value = false
  }
}
watch(() => form.value.payment, () => show.value && loadForm())

function open(p: Payment | null) {
  editing.value = p
  form.value = {
    name: p?.name || '',
    payment: p?.payment || gateways.value[0]?.value || '',
    icon: p?.icon || '',
    notify_domain: p?.notify_domain || '',
    handling_fee_fixed: toYuan(p?.handling_fee_fixed),
    handling_fee_percent: p?.handling_fee_percent ?? null,
  }
  show.value = true
  loadForm()
}

async function submit() {
  const f = form.value
  if (!f.name.trim() || !f.payment) {
    message.warning('请填写显示名称并选择接口文件')
    return
  }
  const body = {
    name: f.name,
    payment: f.payment,
    icon: f.icon || null,
    notify_domain: f.notify_domain || null,
    handling_fee_fixed: toCents(f.handling_fee_fixed),
    handling_fee_percent: f.handling_fee_percent,
    config: config.value,
  }
  saving.value = true
  const ok = await run(() => (editing.value ? admin.put(`/payments/${editing.value.id}`, body) : admin.post('/payments', body)), '保存成功')
  saving.value = false
  if (ok) {
    show.value = false
    load()
  }
}

onMounted(async () => {
  load()
  try {
    gateways.value = ((await admin.get<string[]>('/payments/gateways')) || []).map((g) => ({ label: g, value: g }))
  } catch (e) {
    message.error(errText(e))
  }
})
</script>

<template>
  <div>
    <PageHeader title="支付配置" description="新添加的支付方式默认停用。请把通知地址填到支付平台后台，并在防火墙放行。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <Button @click="open(null)"><PlusIcon />添加支付方式</Button>
    </PageHeader>

    <DataTable :columns="columns" :rows="rows" :loading="loading">
      <template #cell-sort="{ index }">
        <div class="flex gap-0.5">
          <Button variant="ghost" size="icon" class="size-7" :disabled="index === 0" @click="move(index, -1)"><ArrowUpIcon /></Button>
          <Button variant="ghost" size="icon" class="size-7" :disabled="index === rows.length - 1" @click="move(index, 1)"><ArrowDownIcon /></Button>
        </div>
      </template>
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-enable="{ row }"><Switch :model-value="!!row.enable" @update:model-value="toggle(row)" /></template>
      <template #cell-name="{ row }">
        <div class="flex items-center gap-2">
          <img v-if="row.icon" :src="row.icon" alt="" class="size-5 object-contain" />
          <span class="font-medium">{{ row.name }}</span>
          <span v-if="fee(row)" class="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">手续费 {{ fee(row) }}</span>
        </div>
      </template>
      <template #cell-payment="{ row }"><span class="font-mono text-xs">{{ row.payment }}</span></template>
      <template #cell-notify_url="{ row }">
        <Tooltip>
          <TooltipTrigger as-child>
            <button type="button" class="inline-flex max-w-md items-center gap-1 truncate font-mono text-xs hover:text-primary" @click="copyNotify(row)">
              <span class="truncate">{{ row.notify_url }}</span><CopyIcon class="size-3 shrink-0" />
            </button>
          </TooltipTrigger>
          <TooltipContent>支付网关将会把数据通知到本地址，请通过防火墙放行本地址。</TooltipContent>
        </Tooltip>
      </template>
      <template #cell-op="{ row }">
        <div class="flex justify-end gap-0.5">
          <Button variant="ghost" size="icon" class="size-8" @click="open(row)"><PencilIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" @click="remove(row)"><Trash2Icon /></Button>
        </div>
      </template>
    </DataTable>

    <Dialog v-model:open="show">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader><DialogTitle>{{ editing ? '编辑支付方式' : '添加支付方式' }}</DialogTitle></DialogHeader>
        <div class="grid gap-4">
          <FormItem label="显示名称" help="用于前端显示使用">
            <Input v-model="form.name" />
          </FormItem>
          <FormItem label="图标URL(选填)" help="用于前端显示使用(https://x.com/icon.svg)">
            <Input v-model="form.icon" />
          </FormItem>
          <FormItem label="自定义通知域名(选填)" help="网关的通知将会发送到该域名(https://x.com)">
            <Input v-model="form.notify_domain" />
          </FormItem>
          <div class="grid grid-cols-2 gap-4">
            <FormItem label="百分比手续费(选填)" help="在订单金额基础上附加手续费">
              <NumInput v-model="form.handling_fee_percent" :min="0.1" :max="100" :step="0.1" suffix="%" />
            </FormItem>
            <FormItem label="固定手续费(选填)" help="在订单金额基础上附加手续费">
              <NumInput v-model="form.handling_fee_fixed" :min="0" :step="0.01" suffix="元" />
            </FormItem>
          </div>
          <FormItem label="接口文件">
            <SelectBox :model-value="form.payment" :options="gateways" @update:model-value="(v) => (form.payment = String(v))" />
          </FormItem>
          <div v-if="formLoading" class="flex justify-center py-4"><Loader2Icon class="size-5 animate-spin text-muted-foreground" /></div>
          <template v-else>
            <template v-for="[k, f] in fields" :key="k">
              <!-- 提示内容来自后端内置网关定义 -->
              <div v-if="f.type === 'alert'" class="rounded-md border bg-muted/50 px-3 py-2 text-sm" v-html="f.content" />
              <FormItem v-else :label="f.label" :help="f.description">
                <Textarea v-model="config[k]" rows="1" class="min-h-9 font-mono text-xs" />
              </FormItem>
            </template>
          </template>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="show = false">取消</Button>
          <Button :disabled="saving" @click="submit"><Loader2Icon v-if="saving" class="animate-spin" />{{ editing ? '保存' : '添加' }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
