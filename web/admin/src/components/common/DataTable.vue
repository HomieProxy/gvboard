<script setup lang="ts" generic="T extends Record<string, any>">
import { computed, ref } from 'vue'
import { Loader2Icon, InboxIcon, ArrowDownIcon, ArrowUpIcon, ArrowUpDownIcon, GripVerticalIcon } from '@lucide/vue'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@gv/shared/ui/table'
import { Checkbox } from '@gv/shared/ui/checkbox'
import { cn } from '@gv/shared/lib/utils'

export interface Column {
  key: string
  title: string
  /** 单元格附加类名，如 w-24、text-right */
  class?: string
  align?: 'left' | 'center' | 'right'
  sortable?: boolean
}
export interface SortState {
  key: string
  order: 'ASC' | 'DESC'
}

const props = withDefaults(
  defineProps<{
    columns: Column[]
    rows: T[]
    loading?: boolean
    rowKey?: string | ((row: T) => string | number)
    empty?: string
    selectable?: boolean
    selected?: (string | number)[]
    rowClass?: (row: T) => string
    clickable?: boolean
    sort?: SortState | null
    /** 显示拖动手柄，拖动行后触发 reorder */
    draggable?: boolean
  }>(),
  { loading: false, rowKey: 'id', empty: '暂无数据', selectable: false, selected: () => [], clickable: false, sort: null, draggable: false },
)
const emit = defineEmits<{
  'update:selected': [ids: (string | number)[]]
  rowClick: [row: T]
  'update:sort': [s: SortState | null]
  reorder: [from: number, to: number]
}>()

// 只有按住手柄时行才可拖动，避免与行内开关、按钮、文字选择冲突
const armed = ref(-1)
const dragFrom = ref(-1)
const dragOver = ref(-1)
function onDragStart(i: number, e: DragEvent) {
  dragFrom.value = i
  e.dataTransfer?.setData('text/plain', String(i))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function onDrop(i: number) {
  const from = dragFrom.value
  resetDrag()
  if (from >= 0 && from !== i) emit('reorder', from, i)
}
function resetDrag() {
  armed.value = dragFrom.value = dragOver.value = -1
}
function dropCls(i: number) {
  if (dragFrom.value < 0 || dragOver.value !== i || dragFrom.value === i) return ''
  return dragFrom.value < i ? 'shadow-[inset_0_-2px_0_var(--primary)]' : 'shadow-[inset_0_2px_0_var(--primary)]'
}
const extraCols = computed(() => (props.selectable ? 1 : 0) + (props.draggable ? 1 : 0))

function toggleSort(c: Column) {
  if (!c.sortable) return
  const s = props.sort
  if (!s || s.key !== c.key) emit('update:sort', { key: c.key, order: 'DESC' })
  else if (s.order === 'DESC') emit('update:sort', { key: c.key, order: 'ASC' })
  else emit('update:sort', null)
}

const alignCls = (a?: string) => (a === 'right' ? 'text-right' : a === 'center' ? 'text-center' : '')
const keyOf = (r: T) => (typeof props.rowKey === 'function' ? props.rowKey(r) : (r[props.rowKey] as string | number))
const allChecked = computed(() => props.rows.length > 0 && props.rows.every((r) => props.selected.includes(keyOf(r))))
const someChecked = computed(() => !allChecked.value && props.rows.some((r) => props.selected.includes(keyOf(r))))

function toggleAll(v: boolean | 'indeterminate') {
  const ids = props.rows.map(keyOf)
  emit('update:selected', v === true ? Array.from(new Set([...props.selected, ...ids])) : props.selected.filter((i) => !ids.includes(i)))
}
function toggle(r: T, v: boolean | 'indeterminate') {
  const id = keyOf(r)
  emit('update:selected', v === true ? [...props.selected, id] : props.selected.filter((i) => i !== id))
}
</script>

<template>
  <div class="relative overflow-hidden rounded-lg border bg-card">
    <Table>
      <TableHeader class="bg-muted/40">
        <TableRow class="hover:bg-transparent">
          <TableHead v-if="draggable" class="w-8" />
          <TableHead v-if="selectable" class="w-10">
            <Checkbox :model-value="allChecked ? true : someChecked ? 'indeterminate' : false" @update:model-value="toggleAll" />
          </TableHead>
          <TableHead
            v-for="c in columns"
            :key="c.key"
            :class="cn('h-10 whitespace-nowrap text-xs font-medium text-muted-foreground', alignCls(c.align), c.class, c.sortable ? 'cursor-pointer select-none hover:text-foreground' : '')"
            @click="toggleSort(c)"
          >
            <span class="inline-flex items-center gap-1">
              {{ c.title }}
              <template v-if="c.sortable">
                <ArrowDownIcon v-if="sort?.key === c.key && sort.order === 'DESC'" class="size-3.5 text-foreground" />
                <ArrowUpIcon v-else-if="sort?.key === c.key && sort.order === 'ASC'" class="size-3.5 text-foreground" />
                <ArrowUpDownIcon v-else class="size-3.5 opacity-40" />
              </template>
            </span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow
          v-for="(r, i) in rows"
          :key="keyOf(r) ?? i"
          :class="cn(rowClass?.(r), clickable ? 'cursor-pointer' : '', dragFrom === i ? 'opacity-40' : '', dropCls(i))"
          :data-state="selected.includes(keyOf(r)) ? 'selected' : undefined"
          :draggable="draggable && armed === i"
          @click="emit('rowClick', r)"
          @dragstart="onDragStart(i, $event)"
          @dragover.prevent="dragOver = i"
          @drop.prevent="onDrop(i)"
          @dragend="resetDrag"
        >
          <TableCell v-if="draggable" class="w-8 pr-0" @click.stop>
            <span
              class="flex cursor-grab items-center text-muted-foreground hover:text-foreground active:cursor-grabbing"
              title="拖动排序"
              @mousedown="armed = i"
              @mouseup="armed = -1"
            >
              <GripVerticalIcon class="size-4" />
            </span>
          </TableCell>
          <TableCell v-if="selectable" class="w-10" @click.stop>
            <Checkbox :model-value="selected.includes(keyOf(r))" @update:model-value="(v) => toggle(r, v)" />
          </TableCell>
          <TableCell v-for="c in columns" :key="c.key" :class="cn('py-2.5', alignCls(c.align), c.class)">
            <slot :name="`cell-${c.key}`" :row="r" :index="i">
              {{ r[c.key] ?? '-' }}
            </slot>
          </TableCell>
        </TableRow>
        <TableRow v-if="!loading && rows.length === 0" class="hover:bg-transparent">
          <TableCell :colspan="columns.length + extraCols" class="h-40 text-center text-muted-foreground">
            <div class="flex flex-col items-center gap-2">
              <InboxIcon class="size-8 opacity-40" />
              <span class="text-sm">{{ empty }}</span>
            </div>
          </TableCell>
        </TableRow>
        <TableRow v-if="loading && rows.length === 0" class="hover:bg-transparent">
          <TableCell :colspan="columns.length + extraCols" class="h-40" />
        </TableRow>
      </TableBody>
    </Table>
    <div v-if="loading" class="absolute inset-0 flex items-center justify-center bg-background/50">
      <Loader2Icon class="size-6 animate-spin text-muted-foreground" />
    </div>
  </div>
</template>
