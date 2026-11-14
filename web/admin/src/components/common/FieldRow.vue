<script setup lang="ts">
/** 表单行：左侧固定宽度的标签，右侧控件，说明文字在控件下方。 */
defineProps<{ label: string; help?: string }>()
</script>

<template>
  <div class="grid gap-x-8 gap-y-1.5 py-3 md:grid-cols-[12rem_minmax(0,1fr)]">
    <div class="text-sm font-medium md:pt-2 md:text-right">{{ label }}</div>
    <div class="grid min-w-0 justify-items-start gap-1.5">
      <slot />
      <p v-if="help" class="text-xs leading-relaxed text-muted-foreground">{{ help }}</p>
    </div>
  </div>
</template>
