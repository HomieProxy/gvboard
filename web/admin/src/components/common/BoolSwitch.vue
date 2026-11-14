<script setup lang="ts">
import { Switch } from '@gv/shared/ui/switch'

/** 开关：兼容接口中的 0 / 1 与 boolean，输出类型与输入一致（未设置时输出 0 / 1）。 */
const props = withDefaults(defineProps<{ modelValue: number | boolean | null | undefined; disabled?: boolean }>(), { disabled: false })
const emit = defineEmits<{ 'update:modelValue': [v: number | boolean] }>()

function onUpdate(v: boolean) {
  emit('update:modelValue', typeof props.modelValue === 'boolean' ? v : v ? 1 : 0)
}
</script>

<template>
  <Switch :model-value="!!modelValue" :disabled="disabled" @update:model-value="onUpdate" />
</template>
