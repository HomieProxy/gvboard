<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NCard, NDataTable, NButton, NModal, NForm, NFormItem, NInput, NSelect, NSpace, type DataTableColumns } from 'naive-ui'
import { admin } from '../api'
import { run } from '../ui'
import { useList } from '../paged'
import { actions, btn, confirmBtn, ellipsis } from '../helpers'
import { invalidate } from '../refs'
import type { ServerRoute } from '../types'

const { rows, loading, load } = useList(() => admin.get<ServerRoute[]>('/servers/routes'))

const actionOptions = [
  { label: '禁止访问（域名）', value: 'block' },
  { label: '禁止访问（IP）', value: 'block_ip' },
  { label: '禁止访问（端口）', value: 'block_port' },
  { label: '禁止协议', value: 'protocol' },
  { label: '指定 DNS 解析', value: 'dns' },
  { label: '指定出站（域名）', value: 'route' },
  { label: '指定出站（IP）', value: 'route_ip' },
  { label: '默认出站', value: 'default_out' },
]
const actionName = (a: string) => actionOptions.find((o) => o.value === a)?.label || a

const show = ref(false)
const editing = ref<ServerRoute | null>(null)
const form = ref({ remarks: '', action: 'block', match: '', action_value: '' })

function open(r: ServerRoute | null) {
  editing.value = r
  form.value = r
    ? { remarks: r.remarks, action: r.action, match: Array.isArray(r.match) ? r.match.join('\n') : r.match || '', action_value: r.action_value || '' }
    : { remarks: '', action: 'block', match: '', action_value: '' }
  show.value = true
}

async function submit() {
  const f = form.value
  const body = {
    remarks: f.remarks.trim(),
    action: f.action,
    match: f.match.split('\n').map((s) => s.trim()).filter(Boolean),
    action_value: f.action_value.trim() || null,
  }
  const ok = await run(() => (editing.value ? admin.put(`/servers/routes/${editing.value.id}`, body) : admin.post('/servers/routes', body)), '已保存')
  if (ok) {
    show.value = false
    invalidate('routes')
    load()
  }
}

async function remove(r: ServerRoute) {
  if (await run(() => admin.del(`/servers/routes/${r.id}`), '已删除')) {
    invalidate('routes')
    load()
  }
}

const columns: DataTableColumns<ServerRoute> = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '备注', key: 'remarks' },
  { title: '动作', key: 'action', render: (r) => actionName(r.action) },
  { title: '匹配', key: 'match', render: (r) => ellipsis(Array.isArray(r.match) ? r.match.join(', ') : String(r.match ?? ''), 320) },
  { title: '动作值', key: 'action_value', render: (r) => r.action_value || '-' },
  {
    title: '操作',
    key: 'op',
    width: 120,
    render: (r) => actions(btn('编辑', () => open(r)), confirmBtn('删除', '确定删除该路由？', () => remove(r))),
  },
]

onMounted(load)
</script>

<template>
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NButton type="primary" @click="open(null)">新建路由</NButton>
        <NButton @click="load">刷新</NButton>
      </div>
      <NDataTable size="small" :columns="columns" :data="rows" :loading="loading" :row-key="(r: ServerRoute) => r.id" />
    </NCard>
    <NModal v-model:show="show" preset="card" :title="editing ? '编辑路由' : '新建路由'" style="width: 560px; max-width: 95vw">
      <NForm label-placement="left" label-width="80">
        <NFormItem label="备注" required><NInput v-model:value="form.remarks" /></NFormItem>
        <NFormItem label="动作"><NSelect v-model:value="form.action" :options="actionOptions" /></NFormItem>
        <NFormItem v-if="form.action !== 'default_out'" label="匹配值" required>
          <NInput v-model:value="form.match" type="textarea" :autosize="{ minRows: 4, maxRows: 14 }" placeholder="每行一个，如 domain:example.com、geosite:cn、1.1.1.0/24" />
        </NFormItem>
        <NFormItem v-if="['dns', 'route', 'route_ip', 'default_out'].includes(form.action)" label="动作值">
          <NInput v-model:value="form.action_value" type="textarea" :autosize="{ minRows: 2, maxRows: 10 }" placeholder="如 DNS 服务器地址或出站配置" />
        </NFormItem>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="show = false">取消</NButton><NButton type="primary" @click="submit">保存</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
