<script setup lang="ts">
import { computed } from 'vue'
import { Input } from '@gv/shared/ui/input'

/** 日期时间输入，v-model 为 Unix 秒（null 表示未设置）；dateOnly 时只选日期（当天 00:00）。 */
const props = withDefaults(defineProps<{ modelValue: number | null | undefined; dateOnly?: boolean; disabled?: boolean }>(), {
  dateOnly: false,
  disabled: false,
})
const emit = defineEmits<{ 'update:modelValue': [v: number | null] }>()

const pad = (n: number) => String(n).padStart(2, '0')
const text = computed(() => {
  if (!props.modelValue) return ''
  const d = new Date(props.modelValue * 1000)
  const day = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  return props.dateOnly ? day : `${day}T${pad(d.getHours())}:${pad(d.getMinutes())}`
})
function onInput(v: string | number) {
  const s = String(v)
  if (!s) return emit('update:modelValue', null)
  const t = new Date(props.dateOnly ? `${s}T00:00` : s).getTime()
  if (!Number.isNaN(t)) emit('update:modelValue', Math.floor(t / 1000))
}
</script>

<template>
  <Input :type="dateOnly ? 'date' : 'datetime-local'" :model-value="text" :disabled="disabled" @update:model-value="onInput" />
</template>
