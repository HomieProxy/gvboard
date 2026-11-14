<script setup lang="ts">
import { computed } from 'vue'
import MarkdownIt from 'markdown-it'

// 公告、文档、套餐描述由管理员编写，允许 HTML（与原项目一致）
const md = new MarkdownIt({ html: true, linkify: true, breaks: true })
const defaultLink = md.renderer.rules.link_open || ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options))
md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  const href = tokens[idx].attrGet('href') || ''
  if (/^https?:\/\//i.test(href)) {
    tokens[idx].attrSet('target', '_blank')
    tokens[idx].attrSet('rel', 'noopener noreferrer')
  }
  return defaultLink(tokens, idx, options, env, self)
}

const props = defineProps<{ source: string | null | undefined }>()
const html = computed(() => md.render(props.source || ''))
</script>

<template>
  <div class="md" v-html="html" />
</template>
