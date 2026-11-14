<script setup lang="ts">
import { computed } from 'vue'
import DOMPurify from 'dompurify'
import { CheckIcon, XIcon } from '@lucide/vue'

// 套餐描述：JSON 数组 [{feature, support}] 显示为功能列表，否则按 HTML 显示
const props = defineProps<{ content: string | null | undefined }>()

interface Feature {
  feature: string
  support: boolean
}

const features = computed<Feature[] | null>(() => {
  const s = (props.content || '').trim()
  if (!s.startsWith('[')) return null
  try {
    const v = JSON.parse(s)
    if (!Array.isArray(v)) return null
    return v
      .filter((x) => x && typeof x === 'object' && 'feature' in x)
      .map((x) => ({ feature: String(x.feature), support: !!x.support }))
  } catch {
    return null
  }
})
const html = computed(() => DOMPurify.sanitize(props.content || '', { ADD_ATTR: ['target'] }))
</script>

<template>
  <ul v-if="features" class="space-y-2 text-sm">
    <li v-for="(f, i) in features" :key="i" class="flex items-start gap-2" :class="f.support ? '' : 'opacity-30'">
      <CheckIcon v-if="f.support" class="mt-0.5 size-4 shrink-0 text-primary" />
      <XIcon v-else class="mt-0.5 size-4 shrink-0" />
      <span>{{ f.feature }}</span>
    </li>
  </ul>
  <div v-else class="md text-sm" v-html="html" />
</template>
