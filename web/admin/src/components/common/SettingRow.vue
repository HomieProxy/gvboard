<script setup lang="ts">
/** 设置行：左侧标题与说明，右侧控件（系统配置、插件配置等页面使用）。 */
withDefaults(defineProps<{ label: string; description?: string; wide?: boolean }>(), { wide: false })
</script>

<template>
  <div class="flex flex-col gap-3 border-b py-4 last:border-b-0 md:flex-row md:items-center md:justify-between md:gap-8">
    <div class="min-w-0 md:max-w-[50%]">
      <div class="text-sm font-medium">{{ label }}</div>
      <p v-if="description" class="mt-1 text-xs leading-relaxed text-muted-foreground">{{ description }}</p>
      <slot name="extra" />
    </div>
    <div :class="['w-full shrink-0', wide ? 'md:w-[28rem]' : 'md:w-80', 'flex md:justify-end']">
      <slot />
    </div>
  </div>
</template>
