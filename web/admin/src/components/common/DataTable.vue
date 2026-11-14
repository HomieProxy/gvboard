<script setup lang="ts" generic="T extends Record<string, any>">
import { computed } from 'vue'
import { Loader2Icon, InboxIcon, ArrowDownIcon, ArrowUpIcon, ArrowUpDownIcon } from '@lucide/vue'
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
  }>(),
  { loading: false, rowKey: 'id', empty: '暂无数据', selectable: false, selected: () => [], clickable: false, sort: null },
)
const emit = defineEmits<{ 'update:selected': [ids: (string | number)[]]; rowClick: [row: T]; 'update:sort': [s: SortState | null] }>()

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
          :class="cn(rowClass?.(r), clickable ? 'cursor-pointer' : '')"
          :data-state="selected.includes(keyOf(r)) ? 'selected' : undefined"
          @click="emit('rowClick', r)"
        >
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
          <TableCell :colspan="columns.length + (selectable ? 1 : 0)" class="h-40 text-center text-muted-foreground">
            <div class="flex flex-col items-center gap-2">
              <InboxIcon class="size-8 opacity-40" />
              <span class="text-sm">{{ empty }}</span>
            </div>
          </TableCell>
        </TableRow>
        <TableRow v-if="loading && rows.length === 0" class="hover:bg-transparent">
          <TableCell :colspan="columns.length + (selectable ? 1 : 0)" class="h-40" />
        </TableRow>
      </TableBody>
    </Table>
    <div v-if="loading" class="absolute inset-0 flex items-center justify-center bg-background/50">
      <Loader2Icon class="size-6 animate-spin text-muted-foreground" />
    </div>
  </div>
</template>
