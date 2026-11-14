<script setup lang="ts">
import { Input } from '@gv/shared/ui/input'

/** 数字输入：清空时为 null；suffix 显示单位。 */
const props = withDefaults(
  defineProps<{ modelValue: number | null | undefined; min?: number; max?: number; step?: number; placeholder?: string; suffix?: string; class?: string; disabled?: boolean }>(),
  { step: 1, placeholder: '', class: '', disabled: false },
)
const emit = defineEmits<{ 'update:modelValue': [v: number | null] }>()

function onInput(v: string | number) {
  const s = String(v).trim()
  if (s === '') return emit('update:modelValue', null)
  const n = Number(s)
  if (!Number.isNaN(n)) emit('update:modelValue', n)
}
</script>

<template>
  <div :class="['relative', props.class]">
    <Input
      type="number"
      :model-value="modelValue ?? ''"
      :min="min"
      :max="max"
      :step="step"
      :placeholder="placeholder"
      :disabled="disabled"
      :class="suffix ? 'pe-12' : ''"
      @update:model-value="onInput"
    />
    <span v-if="suffix" class="pointer-events-none absolute inset-y-0 end-3 flex items-center text-xs text-muted-foreground">{{ suffix }}</span>
  </div>
</template>
