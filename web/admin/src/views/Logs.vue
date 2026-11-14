<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { NCard, NDataTable, NButton, NInput, NSelect, type DataTableColumns } from 'naive-ui'
import { formatTime } from '@gv/shared'
import { admin } from '../api'
import { usePaged } from '../paged'
import { ellipsis, tag, type PageResult } from '../helpers'

interface SysLog {
  id: number
  title: string
  level: string | null
  host: string | null
  uri: string
  method: string
  data: string | null
  ip: string | null
  context: string | null
  created_at: number
}

const level = ref<string | null>(null)
const keyword = ref('')
const { rows, loading, pagination, load, reload } = usePaged<SysLog>(async (current, page_size) => {
  const r = await admin.get<PageResult<SysLog>>('/system/logs', {
    current,
    page_size,
    level: level.value ?? undefined,
    keyword: keyword.value.trim() || undefined,
  })
  return { rows: r.data || [], total: r.total }
})

const levels = ['DEBUG', 'INFO', 'NOTICE', 'WARNING', 'ERROR', 'CRITICAL', 'ALERT', 'EMERGENCY'].map((l) => ({ label: l, value: l }))
const levelType = (l: string | null) => {
  const v = (l || '').toUpperCase()
  if (['ERROR', 'CRITICAL', 'ALERT', 'EMERGENCY'].includes(v)) return 'error'
  if (v === 'WARNING') return 'warning'
  return 'default'
}

const columns: DataTableColumns<SysLog> = [
  {
    type: 'expand',
    renderExpand: (l) =>
      h('div', null, [
        h('div', { class: 'muted' }, `${l.method} ${l.host || ''}${l.uri}　IP ${l.ip || '-'}`),
        l.data ? h('pre', { class: 'code', style: 'margin-top:6px' }, l.data) : null,
        l.context ? h('pre', { class: 'code', style: 'margin-top:6px' }, l.context) : null,
      ]),
  },
  { title: '时间', key: 'created_at', width: 140, render: (l) => formatTime(l.created_at) },
  { title: '级别', key: 'level', width: 100, render: (l) => tag(l.level || '-', levelType(l.level)) },
  { title: '标题', key: 'title', render: (l) => ellipsis(l.title, 480) },
  { title: '请求', key: 'uri', render: (l) => ellipsis(`${l.method} ${l.uri}`, 260) },
]

onMounted(load)
</script>

<template>
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NSelect v-model:value="level" :options="levels" clearable placeholder="全部级别" style="width: 140px" @update:value="reload" />
        <NInput v-model:value="keyword" placeholder="标题关键字" clearable style="width: 220px" @keyup.enter="reload" @clear="reload" />
        <NButton @click="reload">查询</NButton>
      </div>
      <NDataTable remote size="small" :columns="columns" :data="rows" :loading="loading" :pagination="pagination" :row-key="(l: SysLog) => l.id" :scroll-x="900" />
    </NCard>
  </div>
</template>
