<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import {
  PlusIcon, RefreshCwIcon, SearchIcon, PencilIcon, CopyIcon, Trash2Icon, TerminalIcon, EllipsisIcon, GripVerticalIcon, ArrowDownUpIcon, Loader2Icon,
} from '@lucide/vue'
import { copyText } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Switch } from '@gv/shared/ui/switch'
import { Textarea } from '@gv/shared/ui/textarea'
import { Tooltip, TooltipContent, TooltipTrigger } from '@gv/shared/ui/tooltip'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@gv/shared/ui/dropdown-menu'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import ServerForm from '../components/ServerForm.vue'
import { admin } from '../api'
import { ask, confirm, message, run } from '../ui'
import { useList } from '../paged'
import { groupName, loadGroups } from '../refs'
import { serverTypes, typeLabel } from '../serverSpec'
import type { ServerRow } from '../types'

const { rows, loading, load } = useList(() => admin.get<ServerRow[]>('/servers'))
const keyword = ref('')

const data = computed(() => {
  const k = keyword.value.trim().toLowerCase()
  if (!k) return rows.value
  return rows.value.filter((s) =>
    [s.id, s.name, s.host, s.port, s.type, typeLabel(s.type), ...(s.tags || [])].some((x) => String(x).toLowerCase().includes(k)),
  )
})

const typeColor: Record<string, string> = {
  shadowsocks: 'bg-sky-500/15 text-sky-700 dark:text-sky-300',
  vmess: 'bg-violet-500/15 text-violet-700 dark:text-violet-300',
  trojan: 'bg-rose-500/15 text-rose-700 dark:text-rose-300',
  tuic: 'bg-amber-500/15 text-amber-700 dark:text-amber-300',
  hysteria: 'bg-orange-500/15 text-orange-700 dark:text-orange-300',
  vless: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300',
  anytls: 'bg-cyan-500/15 text-cyan-700 dark:text-cyan-300',
  v2node: 'bg-indigo-500/15 text-indigo-700 dark:text-indigo-300',
}
const status: Record<number, [string, string]> = {
  0: ['未运行', 'bg-red-500'],
  1: ['无人使用或服务端上报异常', 'bg-amber-500'],
  2: ['运行正常', 'bg-emerald-500'],
}

const columns: Column[] = [
  { key: 'id', title: '节点ID', class: 'w-36' },
  { key: 'show', title: '显隐', align: 'center', class: 'w-16' },
  { key: 'name', title: '节点' },
  { key: 'host', title: '地址' },
  { key: 'online', title: '人数', align: 'center', class: 'w-20' },
  { key: 'rate', title: '倍率', align: 'center', class: 'w-20' },
  { key: 'group_id', title: '权限组' },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]

async function setShow(s: ServerRow, v: boolean) {
  if (await run(() => admin.post(`/servers/${s.type}/${s.id}/show`, { show: v ? 1 : 0 }))) s.show = v ? 1 : 0
}
async function copyAddr(s: ServerRow) {
  const ok = await copyText(`${s.host}:${s.port}`)
  message[ok ? 'success' : 'error'](ok ? '复制成功' : '复制失败')
}
async function duplicate(s: ServerRow) {
  if (await run(() => admin.post(`/servers/${s.type}/${s.id}/copy`), '复制成功')) load()
}
async function remove(s: ServerRow) {
  if (await confirm(`确定要删除节点「${s.name}」吗？`, () => admin.del(`/servers/${s.type}/${s.id}`), '删除成功')) load()
}

// ---------- 编辑 ----------
const formShow = ref(false)
const editing = ref<ServerRow | null>(null)
const newType = ref('vless')
function openForm(s: ServerRow | null, type?: string) {
  editing.value = s
  if (type) newType.value = type
  formShow.value = true
}

// ---------- 安装命令 ----------
const install = ref({ open: false, text: '' })
async function copyInstall() {
  const ok = await copyText(install.value.text)
  message[ok ? 'success' : 'error'](ok ? '复制成功' : '复制失败')
}

// ---------- 排序 ----------
const sorting = ref(false)
const sortList = ref<ServerRow[]>([])
const dirty = ref(false)
const savingSort = ref(false)
let dragFrom = -1

function startSort() {
  keyword.value = ''
  sortList.value = [...rows.value]
  dirty.value = false
  sorting.value = true
}
function onDrop(to: number) {
  if (dragFrom < 0 || dragFrom === to) return
  const list = [...sortList.value]
  const [item] = list.splice(dragFrom, 1)
  list.splice(to, 0, item)
  sortList.value = list
  dirty.value = true
  dragFrom = -1
}
async function saveSort() {
  const body: Record<string, Record<string, number>> = {}
  sortList.value.forEach((s, i) => {
    body[s.type] = body[s.type] || {}
    body[s.type][String(s.id)] = i + 1
  })
  savingSort.value = true
  const ok = await run(() => admin.post('/servers/sort', body), '保存成功')
  savingSort.value = false
  if (ok) {
    sorting.value = false
    dirty.value = false
    load()
  }
}
onBeforeRouteLeave(async () => (sorting.value && dirty.value ? await ask('提示', '节点排序还没有保存，是否离开') : true))

onMounted(() => {
  loadGroups().catch(() => {})
  load()
})
</script>

<template>
  <div>
    <PageHeader title="节点管理" description="节点状态根据服务端拉取配置与上报流量的时间判断。">
      <template v-if="!sorting">
        <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
        <Button variant="outline" @click="startSort"><ArrowDownUpIcon />编辑排序</Button>
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button><PlusIcon />添加节点</Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem v-for="t in serverTypes" :key="t.value" @click="openForm(null, t.value)">
              <span class="size-2 rounded-full" :class="typeColor[t.value]?.split(' ')[0]" />
              {{ t.label }}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </template>
      <template v-else>
        <Button variant="outline" @click="sorting = false">取消</Button>
        <Button :disabled="savingSort" @click="saveSort"><Loader2Icon v-if="savingSort" class="animate-spin" />保存排序</Button>
      </template>
    </PageHeader>

    <template v-if="!sorting">
      <div class="mb-3 flex items-center gap-2">
        <div class="relative w-full max-w-xs">
          <SearchIcon class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input v-model="keyword" placeholder="输入任意关键字搜索" class="pl-8" />
        </div>
        <span class="text-sm text-muted-foreground">共 {{ data.length }} 个节点</span>
      </div>

      <DataTable :columns="columns" :rows="data" :loading="loading" :row-key="(s: ServerRow) => `${s.type}-${s.id}`">
        <template #cell-id="{ row }">
          <div class="flex items-center gap-1.5">
            <span class="rounded px-1.5 py-0.5 text-xs font-medium" :class="typeColor[row.type]">{{ typeLabel(row.type) }}</span>
            <span class="tabular-nums">{{ row.id }}</span>
            <span v-if="row.parent_id" class="text-xs text-muted-foreground">→ {{ row.parent_id }}</span>
          </div>
        </template>
        <template #cell-show="{ row }">
          <Switch :model-value="!!row.show" @update:model-value="(v: boolean) => setShow(row, v)" />
        </template>
        <template #cell-name="{ row }">
          <div class="flex items-center gap-2">
            <Tooltip>
              <TooltipTrigger as-child>
                <span class="size-2 shrink-0 rounded-full" :class="status[row.available_status]?.[1] || 'bg-red-500'" />
              </TooltipTrigger>
              <TooltipContent>{{ status[row.available_status]?.[0] || '未运行' }}</TooltipContent>
            </Tooltip>
            <span class="font-medium">{{ row.name }}</span>
            <span v-for="tg in row.tags || []" :key="tg" class="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{{ tg }}</span>
          </div>
        </template>
        <template #cell-host="{ row }">
          <button type="button" class="font-mono text-xs hover:text-primary" title="点击复制" @click="copyAddr(row)">{{ row.host }}:{{ row.port }}</button>
        </template>
        <template #cell-online="{ row }">
          <Tooltip>
            <TooltipTrigger as-child><span class="tabular-nums">{{ row.online ?? 0 }}</span></TooltipTrigger>
            <TooltipContent>根据服务端上报频率而定</TooltipContent>
          </Tooltip>
        </template>
        <template #cell-rate="{ row }">
          <Tooltip>
            <TooltipTrigger as-child><span class="rounded bg-muted px-1.5 py-0.5 text-xs tabular-nums">{{ Number(row.rate) }} x</span></TooltipTrigger>
            <TooltipContent>使用的流量将乘以倍率进行扣除</TooltipContent>
          </Tooltip>
        </template>
        <template #cell-group_id="{ row }">
          <div class="flex max-w-xs flex-wrap gap-1">
            <span v-for="g in row.group_id || []" :key="g" class="rounded border px-1.5 py-0.5 text-xs">{{ groupName(Number(g)) }}</span>
          </div>
        </template>
        <template #cell-op="{ row }">
          <div class="flex justify-end gap-0.5">
            <Button variant="ghost" size="icon" class="size-8" @click="openForm(row)"><PencilIcon /></Button>
            <DropdownMenu>
              <DropdownMenuTrigger as-child>
                <Button variant="ghost" size="icon" class="size-8"><EllipsisIcon /></Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem @click="openForm(row)"><PencilIcon />编辑</DropdownMenuItem>
                <DropdownMenuItem @click="duplicate(row)"><CopyIcon />复制</DropdownMenuItem>
                <DropdownMenuItem v-if="row.install_command" @click="install = { open: true, text: String(row.install_command) }"><TerminalIcon />一键安装指令</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" @click="remove(row)"><Trash2Icon />删除</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </template>
      </DataTable>
    </template>

    <div v-else class="rounded-lg border bg-card">
      <p class="border-b px-4 py-2 text-sm text-muted-foreground">拖动排序，完成后点击「保存排序」。</p>
      <div
        v-for="(s, i) in sortList"
        :key="`${s.type}-${s.id}`"
        draggable="true"
        class="flex cursor-grab items-center gap-3 border-b px-4 py-2 last:border-b-0 hover:bg-muted/50"
        @dragstart="dragFrom = i"
        @dragover.prevent
        @drop="onDrop(i)"
      >
        <GripVerticalIcon class="size-4 text-muted-foreground" />
        <span class="w-8 text-xs text-muted-foreground tabular-nums">{{ i + 1 }}</span>
        <span class="rounded px-1.5 py-0.5 text-xs font-medium" :class="typeColor[s.type]">{{ typeLabel(s.type) }}</span>
        <span class="text-sm">{{ s.name }}</span>
        <span class="ml-auto font-mono text-xs text-muted-foreground">{{ s.host }}:{{ s.port }}</span>
      </div>
    </div>

    <ServerForm v-model:show="formShow" :server="editing" :type="newType" :parents="rows" @saved="load" />

    <Dialog v-model:open="install.open">
      <DialogContent class="sm:max-w-2xl">
        <DialogHeader><DialogTitle>一键安装指令</DialogTitle></DialogHeader>
        <Textarea :model-value="install.text" readonly rows="5" class="bg-muted font-mono text-xs" />
        <DialogFooter><Button @click="copyInstall"><CopyIcon />复制</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
