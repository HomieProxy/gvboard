<script setup lang="ts">
import { computed } from 'vue'
import { ChevronLeftIcon, ChevronRightIcon, ChevronsLeftIcon, ChevronsRightIcon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import SelectBox from './SelectBox.vue'
import type { Pager } from '@/paged'

const props = withDefaults(defineProps<{ pager: Pager; sizes?: number[] }>(), { sizes: () => [10, 20, 50, 100] })
const emit = defineEmits<{ page: [p: number]; size: [s: number] }>()

const pages = computed(() => Math.max(1, Math.ceil(props.pager.total / props.pager.pageSize)))
const sizeOptions = computed(() => props.sizes.map((s) => ({ label: `${s} 条/页`, value: s })))
const go = (p: number) => p >= 1 && p <= pages.value && p !== props.pager.page && emit('page', p)
</script>

<template>
  <div class="flex flex-wrap items-center justify-between gap-3 px-1 pt-3 text-sm text-muted-foreground">
    <span>共 {{ pager.total }} 条</span>
    <div class="flex items-center gap-2">
      <SelectBox
        class="h-8 w-28"
        :model-value="pager.pageSize"
        :options="sizeOptions"
        @update:model-value="(v) => emit('size', Number(v))"
      />
      <span class="whitespace-nowrap">第 {{ pager.page }} / {{ pages }} 页</span>
      <div class="flex items-center gap-1">
        <Button variant="outline" size="icon" class="size-8" :disabled="pager.page <= 1" @click="go(1)"><ChevronsLeftIcon /></Button>
        <Button variant="outline" size="icon" class="size-8" :disabled="pager.page <= 1" @click="go(pager.page - 1)"><ChevronLeftIcon /></Button>
        <Button variant="outline" size="icon" class="size-8" :disabled="pager.page >= pages" @click="go(pager.page + 1)"><ChevronRightIcon /></Button>
        <Button variant="outline" size="icon" class="size-8" :disabled="pager.page >= pages" @click="go(pages)"><ChevronsRightIcon /></Button>
      </div>
    </div>
  </div>
</template>
