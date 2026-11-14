<script setup lang="ts">
import { Label } from '@gv/shared/ui/label'

/** 表单项（弹窗 / 抽屉中纵向排列）：标题、控件、说明。 */
withDefaults(defineProps<{ label: string; help?: string; required?: boolean; class?: string }>(), { required: false, class: '' })
</script>

<template>
  <div :class="['grid gap-1.5', $props.class]">
    <Label class="text-sm">
      {{ label }}<span v-if="required" class="text-destructive">*</span>
      <slot name="extra" />
    </Label>
    <slot />
    <p v-if="help" class="text-xs text-muted-foreground">{{ help }}</p>
  </div>
</template>
