<script setup lang="ts">
import { ref } from 'vue'
import { FilterIcon, PlusIcon, Trash2Icon } from '@lucide/vue'
import { Popover, PopoverContent, PopoverTrigger } from '@gv/shared/ui/popover'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import SelectBox from './common/SelectBox.vue'
import { conditions, type Filter } from '../helpers'

export interface FilterField {
  label: string
  value: string
  /** 可选值（如 是 / 否），提供时值输入改为下拉 */
  options?: { label: string; value: string }[]
  /** 默认条件 */
  condition?: string
}

const props = defineProps<{ fields: FilterField[] }>()
const model = defineModel<Filter[]>({ required: true })
const emit = defineEmits<{ apply: [] }>()
const open = ref(false)
const draft = ref<Filter[]>([])

const fieldOf = (k: string) => props.fields.find((f) => f.value === k)

function onOpen(v: boolean) {
  open.value = v
  if (v) {
    draft.value = model.value.map((f) => ({ ...f }))
    if (!draft.value.length) add()
  }
}
function add() {
  const f = props.fields[0]
  draft.value.push({ key: f?.value || '', condition: f?.condition || '=', value: '' })
}
function setKey(i: number, k: string) {
  const f = fieldOf(k)
  draft.value[i] = { key: k, condition: f?.condition || '=', value: '' }
}
function apply() {
  model.value = draft.value.filter((f) => f.key && f.value !== '')
  open.value = false
  emit('apply')
}
function clear() {
  model.value = []
  open.value = false
  emit('apply')
}
</script>

<template>
  <Popover :open="open" @update:open="onOpen">
    <PopoverTrigger as-child>
      <Button :variant="model.length ? 'secondary' : 'outline'">
        <FilterIcon />
        过滤器
        <span v-if="model.length" class="rounded bg-primary px-1.5 text-[11px] text-primary-foreground">{{ model.length }}</span>
      </Button>
    </PopoverTrigger>
    <PopoverContent align="start" class="w-[min(40rem,calc(100vw-2rem))]">
      <div class="mb-3 text-sm font-medium">过滤条件<span class="ml-2 text-xs font-normal text-muted-foreground">所有条件之间为「并且」关系</span></div>
      <div class="grid gap-2">
        <div v-for="(f, i) in draft" :key="i" class="flex items-center gap-2">
          <SelectBox class="w-44 shrink-0" :model-value="f.key" :options="fields" @update:model-value="(v) => setKey(i, String(v))" />
          <SelectBox
            v-if="!fieldOf(f.key)?.options"
            class="w-20 shrink-0"
            :model-value="f.condition"
            :options="conditions"
            @update:model-value="(v) => (f.condition = String(v))"
          />
          <SelectBox
            v-if="fieldOf(f.key)?.options"
            class="flex-1"
            :model-value="f.value"
            :options="fieldOf(f.key)!.options!"
            @update:model-value="(v) => (f.value = String(v))"
          />
          <Input v-else v-model="f.value" class="flex-1" placeholder="值" @keyup.enter="apply" />
          <Button variant="ghost" size="icon" class="shrink-0 text-muted-foreground hover:text-destructive" @click="draft.splice(i, 1)">
            <Trash2Icon />
          </Button>
        </div>
      </div>
      <div class="mt-3 flex items-center gap-2">
        <Button variant="outline" size="sm" @click="add"><PlusIcon />添加条件</Button>
        <div class="ml-auto flex gap-2">
          <Button variant="ghost" size="sm" @click="clear">清空</Button>
          <Button size="sm" @click="apply">检索</Button>
        </div>
      </div>
    </PopoverContent>
  </Popover>
</template>
