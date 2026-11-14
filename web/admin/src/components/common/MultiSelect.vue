<script setup lang="ts">
import { computed, ref } from 'vue'
import { CheckIcon, ChevronsUpDownIcon, XIcon } from '@lucide/vue'
import { Popover, PopoverContent, PopoverTrigger } from '@gv/shared/ui/popover'
import { Input } from '@gv/shared/ui/input'
import { cn } from '@gv/shared/lib/utils'

type V = string | number
const props = withDefaults(
  defineProps<{ modelValue: V[] | null | undefined; options: { label: string; value: V }[]; placeholder?: string; class?: string }>(),
  { placeholder: '请选择', class: '' },
)
const emit = defineEmits<{ 'update:modelValue': [v: V[]] }>()

const open = ref(false)
const kw = ref('')
const value = computed(() => props.modelValue || [])
const shown = computed(() => {
  const k = kw.value.trim().toLowerCase()
  return k ? props.options.filter((o) => o.label.toLowerCase().includes(k)) : props.options
})
const labelOf = (v: V) => props.options.find((o) => o.value === v)?.label ?? String(v)

function toggle(v: V) {
  emit('update:modelValue', value.value.includes(v) ? value.value.filter((x) => x !== v) : [...value.value, v])
}
function remove(v: V) {
  emit('update:modelValue', value.value.filter((x) => x !== v))
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <button
        type="button"
        :class="cn('flex min-h-9 w-full items-center gap-1 rounded-md border border-input bg-transparent px-2 py-1 text-left text-sm shadow-xs outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-input/30', props.class)"
      >
        <span v-if="value.length === 0" class="px-1 text-muted-foreground">{{ placeholder }}</span>
        <span class="flex flex-1 flex-wrap gap-1">
          <span v-for="v in value" :key="v" class="inline-flex items-center gap-1 rounded bg-secondary px-1.5 py-0.5 text-xs">
            {{ labelOf(v) }}
            <XIcon class="size-3 cursor-pointer opacity-60 hover:opacity-100" @click.stop="remove(v)" />
          </span>
        </span>
        <ChevronsUpDownIcon class="size-4 shrink-0 opacity-50" />
      </button>
    </PopoverTrigger>
    <PopoverContent class="w-(--reka-popover-trigger-width) p-1" align="start">
      <Input v-if="options.length > 8" v-model="kw" placeholder="搜索" class="mb-1 h-8" />
      <div class="max-h-64 overflow-y-auto">
        <div
          v-for="o in shown"
          :key="o.value"
          class="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent"
          @click="toggle(o.value)"
        >
          <span class="flex size-4 items-center justify-center rounded-sm border" :class="value.includes(o.value) ? 'border-primary bg-primary text-primary-foreground' : ''">
            <CheckIcon v-if="value.includes(o.value)" class="size-3" />
          </span>
          <span class="truncate">{{ o.label }}</span>
        </div>
        <div v-if="shown.length === 0" class="px-2 py-4 text-center text-sm text-muted-foreground">无匹配项</div>
      </div>
    </PopoverContent>
  </Popover>
</template>
