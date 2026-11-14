<script setup lang="ts">
import { computed } from 'vue'
import { TagsInput, TagsInputInput, TagsInputItem, TagsInputItemDelete, TagsInputItemText } from '@gv/shared/ui/tags-input'

/** 标签输入：回车或逗号添加，v-model 为字符串数组。 */
const props = withDefaults(defineProps<{ modelValue: string[] | null | undefined; placeholder?: string }>(), { placeholder: '输入后回车添加' })
const emit = defineEmits<{ 'update:modelValue': [v: string[]] }>()
const value = computed(() => props.modelValue || [])
</script>

<template>
  <TagsInput
    :model-value="value"
    :delimiter="/[,，]/"
    add-on-paste
    add-on-blur
    class="w-full"
    @update:model-value="(v) => emit('update:modelValue', (v as string[]).map((x) => x.trim()).filter(Boolean))"
  >
    <TagsInputItem v-for="t in value" :key="t" :value="t">
      <TagsInputItemText />
      <TagsInputItemDelete />
    </TagsInputItem>
    <TagsInputInput :placeholder="placeholder" />
  </TagsInput>
</template>
