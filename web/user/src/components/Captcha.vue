<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

// Google reCAPTCHA v2。验证结果一次有效，使用后需调用 reset 重新验证。
const props = defineProps<{ siteKey: string }>()
const token = defineModel<string>({ required: true })
const { locale } = useI18n()
const box = ref<HTMLElement | null>(null)
let widget: number | null = null

interface Grecaptcha {
  render: (el: HTMLElement, opt: Record<string, unknown>) => number
  reset: (id?: number) => void
}
declare global {
  interface Window {
    grecaptcha?: Grecaptcha
    __gvCaptchaReady?: () => void
  }
}

let loading: Promise<void> | null = null
function loadScript(): Promise<void> {
  if (window.grecaptcha?.render) return Promise.resolve()
  if (loading) return loading
  loading = new Promise((resolve) => {
    window.__gvCaptchaReady = () => resolve()
    const s = document.createElement('script')
    s.src = `https://www.recaptcha.net/recaptcha/api.js?onload=__gvCaptchaReady&render=explicit&hl=${encodeURIComponent(locale.value)}`
    s.async = true
    document.head.appendChild(s)
  })
  return loading
}

onMounted(async () => {
  await loadScript()
  if (!box.value || !window.grecaptcha) return
  widget = window.grecaptcha.render(box.value, {
    sitekey: props.siteKey,
    callback: (v: string) => (token.value = v),
    'expired-callback': () => (token.value = ''),
  })
})

function reset() {
  token.value = ''
  if (widget !== null) window.grecaptcha?.reset(widget)
}
defineExpose({ reset })
</script>

<template>
  <div ref="box" class="captcha" />
</template>

<style scoped>
.captcha { min-height: 78px; margin-bottom: 12px; }
</style>
