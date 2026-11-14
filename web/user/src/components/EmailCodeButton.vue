<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { NButton } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { http } from '../api'
import { run, toast } from '../ui'

// 发送邮箱验证码，成功后倒计时 60 秒
const props = defineProps<{ email: string; scene: 'register' | 'reset'; captcha?: string; needCaptcha?: boolean }>()
const emit = defineEmits<{ sent: [] }>()
const { t } = useI18n()
const left = ref(0)
const sending = ref(false)
let timer = 0

async function send() {
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(props.email.trim())) {
    toast('warning', t('auth.emailInvalid'))
    return
  }
  if (props.needCaptcha && !props.captcha) {
    toast('warning', t('auth.captchaRequired'))
    return
  }
  sending.value = true
  const ok = await run(
    () => http.post('/auth/email-code', { email: props.email.trim(), scene: props.scene, captcha: props.captcha || undefined }),
    t('auth.codeSent'),
  )
  sending.value = false
  emit('sent')
  if (!ok) return
  left.value = 60
  timer = window.setInterval(() => {
    left.value--
    if (left.value <= 0) clearInterval(timer)
  }, 1000)
}
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <NButton :disabled="left > 0" :loading="sending" @click="send">
    {{ left > 0 ? t('auth.resend', { n: left }) : t('auth.sendCode') }}
  </NButton>
</template>
