<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import MarkdownIt from 'markdown-it'
import mdCss from './md.css?inline'
import { copyText } from '../format'

// 公告、文档由管理员编写，允许 HTML（与原项目一致）
const md = new MarkdownIt({ html: true, linkify: true, breaks: true })

// 以 HTML 块开头的内容按 HTML 原样显示：交给 Markdown 解析时，缩进的 HTML 行会被当成代码块
const htmlStart = /^\s*<(!--|!doctype|html|head|body|style|div|section|article|header|main|center|table|p|h[1-6]|ul|ol)\b/i

// 完整 HTML 文档里 html / body 的样式在 Shadow DOM 中没有对应元素，改为作用于内容容器
const docSelector = /(^|[\s,{}])(html|body)(?=\s*[,{.:#[>~+\s])/g

const props = defineProps<{ source: string | null | undefined }>()
const emit = defineEmits<{ copied: [ok: boolean] }>()

// 内容中带 data-copy 属性的元素点击即复制属性值（不需要在内容中写脚本），复制后 1.5 秒内带 data-copied 属性
async function onClick(e: Event) {
  const el = (e.target as Element | null)?.closest?.('[data-copy]')
  if (!el) return
  e.preventDefault()
  const ok = await copyText(el.getAttribute('data-copy') || '')
  if (ok) {
    el.setAttribute('data-copied', '')
    setTimeout(() => el.removeAttribute('data-copied'), 1500)
  }
  emit('copied', ok)
}
const html = computed(() => {
  const s = props.source || ''
  return htmlStart.test(s) ? s : md.render(s)
})

// 渲染到 Shadow DOM，内容中的 <style> 不会影响页面其它部分；
// 默认排版放在 @layer 中，内容自带的样式无论选择器优先级高低都会覆盖默认排版
const host = ref<HTMLElement>()
let root: ShadowRoot | null = null
function paint() {
  if (!host.value) return
  if (!root) {
    root = host.value.attachShadow({ mode: 'open' })
    root.addEventListener('click', onClick)
  }
  root.innerHTML = `<style>:host{display:block}@layer md{${mdCss}}</style><div class="md">${html.value}</div>`
  root.querySelectorAll('.md style').forEach((s) => {
    s.textContent = (s.textContent || '').replace(docSelector, '$1.md')
  })
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
