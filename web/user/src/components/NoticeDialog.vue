<script setup lang="ts">
import { formatTime } from '@gv/shared'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import Markdown from '@gv/shared/content/Markdown.vue'
import type { Notice } from '../types'

defineProps<{ notice: Notice | null }>()
const open = defineModel<boolean>('open', { required: true })
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
      <DialogHeader>
        <DialogTitle>{{ notice?.title }}</DialogTitle>
        <DialogDescription v-if="notice">{{ formatTime(notice.created_at) }}</DialogDescription>
      </DialogHeader>
      <img v-if="notice?.img_url" :src="notice.img_url" alt="" class="w-full rounded-lg" />
      <Markdown v-if="notice" :source="notice.content" />
    </DialogContent>
  </Dialog>
</template>
