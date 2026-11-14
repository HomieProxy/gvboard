<script setup lang="ts">
import { computed } from 'vue'
import { ChevronLeftIcon, ChevronRightIcon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'

const props = defineProps<{ total: number; pageSize: number }>()
const page = defineModel<number>('page', { required: true })
const emit = defineEmits<{ change: [p: number] }>()
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))

function go(p: number) {
  if (p < 1 || p > pages.value) return
  page.value = p
  emit('change', p)
}
</script>

<template>
  <div v-if="pages > 1" class="mt-4 flex items-center justify-center gap-2 text-sm">
    <Button variant="outline" size="icon" class="size-8" :disabled="page <= 1" @click="go(page - 1)"><ChevronLeftIcon class="rtl:rotate-180" /></Button>
    <span class="min-w-16 text-center tabular-nums">{{ page }} / {{ pages }}</span>
    <Button variant="outline" size="icon" class="size-8" :disabled="page >= pages" @click="go(page + 1)"><ChevronRightIcon class="rtl:rotate-180" /></Button>
  </div>
</template>
