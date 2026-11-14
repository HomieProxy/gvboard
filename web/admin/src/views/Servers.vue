<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { NCard, NDataTable, NButton, NInput, NSelect, NSpace, NBadge, NModal, type DataTableColumns } from 'naive-ui'
import { copyText, formatTime } from '@gv/shared'
import { admin } from '../api'
import { message, run } from '../ui'
import { useList } from '../paged'
import { actions, btn, confirmBtn, sw, tag, tip } from '../helpers'
import { groupName, loadGroups } from '../refs'
import { serverTypes, typeLabel } from '../serverSpec'
import type { ServerRow } from '../types'
import ServerForm from '../components/ServerForm.vue'

const { rows, loading, load } = useList(() => admin.get<ServerRow[]>('/servers'))
const keyword = ref('')
const typeFilter = ref<string | null>(null)

const data = computed(() => {
  const k = keyword.value.trim().toLowerCase()
  return rows.value.filter(
    (s) => (!typeFilter.value || s.type === typeFilter.value) && (!k || s.name.toLowerCase().includes(k) || s.host.toLowerCase().includes(k) || String(s.id) === k),
  )
})
const sortable = computed(() => !keyword.value.trim() && !typeFilter.value)

const statusInfo: Record<number, [string, 'error' | 'warning' | 'success']> = {
  0: ['离线（5 分钟内未拉取配置）', 'error'],
  1: ['在线，但 5 分钟内无流量上报', 'warning'],
  2: ['正常', 'success'],
}

async function setShow(s: ServerRow, v: boolean) {
  if (await run(() => admin.post(`/servers/${s.type}/${s.id}/show`, { show: v ? 1 : 0 }))) s.show = v ? 1 : 0
}

async function move(i: number, d: number) {
  const list = [...rows.value]
  const j = i + d
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j], list[i]]
  rows.value = list
  const body: Record<string, Record<string, number>> = {}
  list.forEach((s, idx) => {
    body[s.type] = body[s.type] || {}
    body[s.type][String(s.id)] = idx + 1
  })
  if (!(await run(() => admin.post('/servers/sort', body)))) load()
}

async function copy(s: ServerRow) {
  if (await run(() => admin.post(`/servers/${s.type}/${s.id}/copy`), '已复制（新节点默认隐藏）')) load()
}
async function remove(s: ServerRow) {
  if (await run(() => admin.del(`/servers/${s.type}/${s.id}`), '已删除')) load()
}

const installCmd = ref('')
const installShow = ref(false)
function showInstall(s: ServerRow) {
  installCmd.value = s.install_command || ''
  installShow.value = true
}

const columns = computed<DataTableColumns<ServerRow>>(() => [
  ...(sortable.value
    ? [
        {
          title: '排序',
          key: 'sort',
          width: 70,
          render: (_: ServerRow, i: number) => actions(btn('↑', () => move(i, -1), 'default'), btn('↓', () => move(i, 1), 'default')),
        },
      ]
    : []),
  {
    title: '节点',
    key: 'name',
    render: (s) =>
      h(NSpace, { size: 6, align: 'center', wrap: false }, () => [
        tip(h(NBadge, { dot: true, type: statusInfo[s.available_status]?.[1] || 'error' }), statusInfo[s.available_status]?.[0] || ''),
        tag(typeLabel(s.type), 'info'),
        h('span', null, `#${s.id} ${s.name}`),
        s.parent_id ? h('span', { class: 'muted' }, `（父节点 #${s.parent_id}）`) : null,
      ]),
  },
  { title: '地址', key: 'host', render: (s) => h('span', { class: 'mono' }, `${s.host}:${s.port}`) },
  { title: '在线', key: 'online', width: 70, render: (s) => s.online ?? 0 },
  { title: '倍率', key: 'rate', width: 70, render: (s) => `${Number(s.rate)}x` },
  { title: '权限组', key: 'group_id', render: (s) => (s.group_id || []).map((g) => groupName(Number(g))).join('、') },
  {
    title: '最后通讯',
    key: 'last_check_at',
    render: (s) => h('span', { class: 'muted' }, `拉取 ${formatTime(s.last_check_at)} / 上报 ${formatTime(s.last_push_at)}`),
  },
  { title: '显示', key: 'show', width: 70, render: (s) => sw(!!s.show, (v) => setShow(s, v)) },
  {
    title: '操作',
    key: 'op',
    width: 200,
    render: (s) =>
      actions(
        btn('编辑', () => open(s)),
        btn('复制', () => copy(s), 'info'),
        s.install_command ? btn('安装命令', () => showInstall(s), 'success') : null,
        confirmBtn('删除', '确定删除该节点？', () => remove(s)),
      ),
  },
])

const formShow = ref(false)
const editing = ref<ServerRow | null>(null)
function open(s: ServerRow | null) {
  editing.value = s
  formShow.value = true
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
        <NButton type="primary" @click="open(null)">新建节点</NButton>
        <NSelect v-model:value="typeFilter" :options="serverTypes" clearable placeholder="全部类型" style="width: 150px" />
        <NInput v-model:value="keyword" placeholder="搜索名称 / 地址 / ID" clearable style="width: 220px" />
        <NButton @click="load">刷新</NButton>
        <span class="muted">清空筛选后可调整排序</span>
      </div>
      <NDataTable size="small" :columns="columns" :data="data" :loading="loading" :row-key="(s: ServerRow) => `${s.type}-${s.id}`" :scroll-x="1100" />
    </NCard>
    <ServerForm v-model:show="formShow" :server="editing" :type="typeFilter || 'vless'" :parents="rows" @saved="load" />
    <NModal v-model:show="installShow" preset="card" title="一键安装命令" style="width: 680px; max-width: 95vw">
      <pre class="code">{{ installCmd }}</pre>
      <template #footer>
        <NSpace justify="end">
          <NButton type="primary" @click="copyText(installCmd).then((ok) => message[ok ? 'success' : 'error'](ok ? '已复制' : '复制失败'))">复制</NButton>
        </NSpace>
      </template>
    </NModal>
  </div>
</template>
