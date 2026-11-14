<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import MarkdownIt from 'markdown-it'
import mdCss from './md.css?inline'

// 公告、文档由管理员编写，允许 HTML（与原项目一致）
const md = new MarkdownIt({ html: true, linkify: true, breaks: true })

// 以 HTML 块开头的内容按 HTML 原样显示：交给 Markdown 解析时，缩进的 HTML 行会被当成代码块
const htmlStart = /^\s*<(!--|!doctype|html|head|body|style|div|section|article|header|main|center|table|p|h[1-6]|ul|ol)\b/i

const props = defineProps<{ source: string | null | undefined }>()
const html = computed(() => {
  const s = props.source || ''
  return htmlStart.test(s) ? s : md.render(s)
})

// 渲染到 Shadow DOM，内容中的 <style> 不会影响页面其它部分
const host = ref<HTMLElement>()
let root: ShadowRoot | null = null
function paint() {
  if (!host.value) return
  root ??= host.value.attachShadow({ mode: 'open' })
  root.innerHTML = `<style>:host{display:block}${mdCss}</style><div class="md">${html.value}</div>`
  root.querySelectorAll('a[href]').forEach((a) => {
    if (/^https?:\/\//i.test(a.getAttribute('href') || '')) {
      a.setAttribute('target', '_blank')
      a.setAttribute('rel', 'noopener noreferrer')
    }
  })
}
onMounted(paint)
watch(html, paint)
</script>

<template>
  <div ref="host" />
</template>
