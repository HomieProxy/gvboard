<script setup lang="ts">
import { computed } from 'vue'

export type Tone = 'default' | 'primary' | 'success' | 'warning' | 'danger' | 'info'

const props = withDefaults(defineProps<{ tone?: Tone; dot?: boolean }>(), { tone: 'default', dot: false })

const cls: Record<Tone, string> = {
  default: 'bg-muted text-muted-foreground',
  primary: 'bg-primary/10 text-primary',
  success: 'bg-success/15 text-success',
  warning: 'bg-warning/20 text-amber-700 dark:text-warning',
  danger: 'bg-destructive/10 text-destructive',
  info: 'bg-sky-500/10 text-sky-600 dark:text-sky-400',
}
const dotCls: Record<Tone, string> = {
  default: 'bg-muted-foreground',
  primary: 'bg-primary',
  success: 'bg-success',
  warning: 'bg-warning',
  danger: 'bg-destructive',
  info: 'bg-sky-500',
}
const klass = computed(() => cls[props.tone])
</script>

<template>
  <span :class="['inline-flex items-center gap-1.5 whitespace-nowrap rounded-md px-2 py-0.5 text-xs font-medium', klass]">
    <span v-if="dot" :class="['size-1.5 rounded-full', dotCls[tone]]" />
    <slot />
  </span>
</template>
