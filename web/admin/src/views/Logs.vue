<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { SearchIcon, RefreshCwIcon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import SelectBox from '../components/common/SelectBox.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import { admin } from '../api'
import { usePaged } from '../paged'
import type { PageResult, Tone } from '../helpers'

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
const { rows, loading, pager, load, reload, setPage, setPageSize } = usePaged<SysLog>(async (current, page_size) => {
  const r = await admin.get<PageResult<SysLog>>('/system/logs', {
    current,
    page_size,
    level: level.value ?? undefined,
    keyword: keyword.value.trim() || undefined,
  })
  return { rows: r.data || [], total: r.total }
})

const levels = [{ label: '全部级别', value: null as string | null }, ...['DEBUG', 'INFO', 'NOTICE', 'WARNING', 'ERROR', 'CRITICAL', 'ALERT', 'EMERGENCY'].map((l) => ({ label: l, value: l as string | null }))]
function levelTone(l: string | null): Tone {
  const v = (l || '').toUpperCase()
  if (['ERROR', 'CRITICAL', 'ALERT', 'EMERGENCY'].includes(v)) return 'danger'
  if (v === 'WARNING') return 'warning'
  if (v === 'INFO' || v === 'NOTICE') return 'info'
  return 'default'
}

const columns: Column[] = [
  { key: 'created_at', title: '时间', class: 'w-40' },
  { key: 'level', title: '级别', class: 'w-24' },
  { key: 'title', title: '标题' },
  { key: 'uri', title: '请求' },
]

const detail = ref<SysLog | null>(null)
const detailShow = ref(false)
function openDetail(l: SysLog) {
  detail.value = l
  detailShow.value = true
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="系统日志" description="点击一行查看请求详情。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
    </PageHeader>

    <div class="mb-3 flex flex-wrap items-center gap-2">
      <SelectBox class="w-36" :model-value="level" :options="levels" @update:model-value="(v) => ((level = v as string | null), reload())" />
      <Input v-model="keyword" class="w-60" placeholder="标题关键字" @keyup.enter="reload" />
      <Button variant="outline" @click="reload"><SearchIcon />查询</Button>
    </div>

    <DataTable :columns="columns" :rows="rows" :loading="loading" clickable @row-click="openDetail">
      <template #cell-created_at="{ row }"><span class="whitespace-nowrap text-xs">{{ formatTime(row.created_at) }}</span></template>
      <template #cell-level="{ row }"><StatusBadge :tone="levelTone(row.level)">{{ row.level || '-' }}</StatusBadge></template>
      <template #cell-title="{ row }"><span class="line-clamp-2 max-w-xl">{{ row.title }}</span></template>
      <template #cell-uri="{ row }"><span class="line-clamp-1 max-w-sm font-mono text-xs">{{ row.method }} {{ row.uri }}</span></template>
    </DataTable>
    <Pager :pager="pager" @page="setPage" @size="setPageSize" />

    <Dialog v-model:open="detailShow">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle class="pr-6">{{ detail?.title }}</DialogTitle>
          <DialogDescription v-if="detail">{{ formatTime(detail.created_at) }} · {{ detail.level || '-' }} · IP {{ detail.ip || '-' }}</DialogDescription>
        </DialogHeader>
        <template v-if="detail">
          <div class="rounded-md bg-muted px-3 py-2 font-mono text-xs break-all">{{ detail.method }} {{ detail.host || '' }}{{ detail.uri }}</div>
          <div v-if="detail.data" class="grid gap-1">
            <div class="text-sm font-medium">请求数据</div>
            <pre class="max-h-60 overflow-auto rounded-md bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{{ detail.data }}</pre>
          </div>
          <div v-if="detail.context" class="grid gap-1">
            <div class="text-sm font-medium">上下文</div>
            <pre class="max-h-80 overflow-auto rounded-md bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{{ detail.context }}</pre>
          </div>
        </template>
      </DialogContent>
    </Dialog>
  </div>
</template>
