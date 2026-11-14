<script setup lang="ts">
import { h, onMounted, ref, watch } from 'vue'
import {
  NCard, NDataTable, NButton, NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NAlert, NSpin,
  type DataTableColumns,
} from 'naive-ui'
import { copyText, formatMoney } from '@gv/shared'
import { admin } from '../api'
import { errText, message, run } from '../ui'
import { useList } from '../paged'
import { actions, btn, confirmBtn, sw, toCents, toYuan } from '../helpers'
import type { Payment } from '../types'

type FormField = { type: 'input'; label: string; description: string; value?: unknown } | { type: 'alert'; content: string }

const { rows, loading, load } = useList(() => admin.get<Payment[]>('/payments'))
const gateways = ref<{ label: string; value: string }[]>([])

async function toggle(p: Payment) {
  if (await run(() => admin.post(`/payments/${p.id}/toggle`))) p.enable = p.enable ? 0 : 1
}
async function remove(p: Payment) {
  if (await run(() => admin.del(`/payments/${p.id}`), '已删除')) load()
}
async function move(i: number, d: number) {
  const list = [...rows.value]
  const j = i + d
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j], list[i]]
  rows.value = list
  if (!(await run(() => admin.post('/payments/sort', { ids: list.map((p) => p.id) })))) load()
}

function fee(p: Payment) {
  const out: string[] = []
  if (p.handling_fee_percent) out.push(`${p.handling_fee_percent}%`)
  if (p.handling_fee_fixed) out.push(formatMoney(p.handling_fee_fixed))
  return out.join(' + ') || '-'
}

const columns: DataTableColumns<Payment> = [
  {
    title: '排序',
    key: 'sort',
    width: 70,
    render: (_, i) => actions(btn('↑', () => move(i, -1), 'default'), btn('↓', () => move(i, 1), 'default')),
  },
  { title: 'ID', key: 'id', width: 60 },
  {
    title: '名称',
    key: 'name',
    render: (p) => h(NSpace, { size: 6, align: 'center' }, () => [p.icon ? h('img', { src: p.icon, style: 'height:18px' }) : null, p.name]),
  },
  { title: '网关', key: 'payment' },
  { title: '手续费', key: 'fee', render: fee },
  {
    title: '回调地址',
    key: 'notify_url',
    render: (p) =>
      h('a', { class: 'link mono', title: '点击复制', onClick: async () => message[(await copyText(p.notify_url)) ? 'success' : 'error']('已复制') }, p.notify_url),
  },
  { title: '启用', key: 'enable', width: 70, render: (p) => sw(!!p.enable, () => toggle(p)) },
  {
    title: '操作',
    key: 'op',
    width: 110,
    render: (p) => actions(btn('编辑', () => open(p)), confirmBtn('删除', '确定删除该支付方式？', () => remove(p))),
  },
]

// ---------- 表单 ----------
const show = ref(false)
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
watch(() => form.value.payment, loadForm)

function open(p: Payment | null) {
  editing.value = p
  form.value = {
    name: p?.name || '',
    payment: p?.payment || '',
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
  const body = {
    name: f.name,
    payment: f.payment,
    icon: f.icon || null,
    notify_domain: f.notify_domain || null,
    handling_fee_fixed: toCents(f.handling_fee_fixed),
    handling_fee_percent: f.handling_fee_percent,
    config: config.value,
  }
  const ok = await run(() => (editing.value ? admin.put(`/payments/${editing.value.id}`, body) : admin.post('/payments', body)), editing.value ? '已保存' : '已创建（默认停用）')
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
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NButton type="primary" @click="open(null)">添加支付方式</NButton>
        <NButton @click="load">刷新</NButton>
        <span class="muted">添加前需在系统配置中设置站点地址；把回调地址填到支付平台后台。</span>
      </div>
      <NDataTable size="small" :columns="columns" :data="rows" :loading="loading" :row-key="(p: Payment) => p.id" :scroll-x="1000" />
    </NCard>

    <NModal v-model:show="show" preset="card" :title="editing ? `编辑支付方式 #${editing.id}` : '添加支付方式'" style="width: 620px; max-width: 95vw">
      <NForm label-placement="left" label-width="120">
        <NFormItem label="显示名称" required><NInput v-model:value="form.name" /></NFormItem>
        <NFormItem label="图标 URL"><NInput v-model:value="form.icon" placeholder="可留空" /></NFormItem>
        <NFormItem label="自定义回调域名"><NInput v-model:value="form.notify_domain" placeholder="如 https://pay.example.com，留空使用站点地址" /></NFormItem>
        <NFormItem label="百分比手续费 %"><NInputNumber v-model:value="form.handling_fee_percent" :min="0.1" :max="100" clearable style="width: 100%" /></NFormItem>
        <NFormItem label="固定手续费（元）"><NInputNumber v-model:value="form.handling_fee_fixed" :min="0" :precision="2" clearable style="width: 100%" /></NFormItem>
        <NFormItem label="支付网关" required><NSelect v-model:value="form.payment" :options="gateways" filterable /></NFormItem>
        <NSpin :show="formLoading">
          <template v-for="[k, f] in fields" :key="k">
            <NAlert v-if="f.type === 'alert'" type="info" style="margin-bottom: 12px"><div v-html="f.content" /></NAlert>
            <NFormItem v-else :label="f.label">
              <NSpace vertical style="width: 100%" :size="2">
                <NInput v-model:value="config[k]" type="textarea" :autosize="{ minRows: 1, maxRows: 8 }" />
                <span v-if="f.description" class="muted">{{ f.description }}</span>
              </NSpace>
            </NFormItem>
          </template>
        </NSpin>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="show = false">取消</NButton><NButton type="primary" @click="submit">保存</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
