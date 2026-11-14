<script setup lang="ts">
import { computed } from 'vue'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@gv/shared/ui/select'
import { cn } from '@gv/shared/lib/utils'

export type OptionValue = string | number | null
export interface Option {
  label: string
  value: OptionValue
}

// 内部统一转成字符串（null 与空字符串用占位值，选项值不能为空字符串），对外保持原始类型
const NULL = '__null__'
const EMPTY = '__empty__'
const props = withDefaults(
  defineProps<{ modelValue: OptionValue | undefined; options: Option[]; placeholder?: string; disabled?: boolean; class?: string }>(),
  { placeholder: '', disabled: false, class: '' },
)
const emit = defineEmits<{ 'update:modelValue': [v: OptionValue] }>()

const enc = (v: OptionValue | undefined) => (v === null || v === undefined ? NULL : v === '' ? EMPTY : String(v))
const inner = computed(() => {
  const k = enc(props.modelValue)
  return props.options.some((o) => enc(o.value) === k) ? k : undefined
})
function onUpdate(k: unknown) {
  const o = props.options.find((x) => enc(x.value) === k)
  if (o) emit('update:modelValue', o.value)
}
</script>

<template>
  <Select :model-value="inner" :disabled="disabled" @update:model-value="onUpdate">
    <SelectTrigger :class="cn('w-full', props.class)">
      <SelectValue :placeholder="placeholder" />
    </SelectTrigger>
    <SelectContent>
      <SelectItem v-for="o in options" :key="enc(o.value)" :value="enc(o.value)">{{ o.label }}</SelectItem>
    </SelectContent>
  </Select>
</template>
