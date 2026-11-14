<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NForm, NFormItem, NInput, NButton, NInputGroup } from 'naive-ui'
import { cfg, http } from '../api'
import { run, toast } from '../ui'
import Captcha from '../components/Captcha.vue'
import EmailCodeButton from '../components/EmailCodeButton.vue'
import TextLink from '../components/TextLink.vue'

const { t } = useI18n()
const router = useRouter()
const form = ref({ email: '', email_code: '', password: '', confirm: '' })
const captcha = ref('')
const captchaRef = ref<InstanceType<typeof Captcha> | null>(null)
const loading = ref(false)

async function submit() {
  const f = form.value
  if (f.password.length < 8) return toast('warning', t('auth.passwordTooShort'))
  if (f.password !== f.confirm) return toast('warning', t('auth.passwordMismatch'))
  loading.value = true
  const ok = await run(
    () => http.post('/auth/reset-password', { email: f.email.trim(), email_code: f.email_code.trim(), password: f.password }),
    t('auth.resetSuccess'),
  )
  loading.value = false
  if (ok) router.replace('/login')
}
</script>

<template>
  <h2 class="title">{{ t('auth.resetPassword') }}</h2>
  <NForm @submit.prevent="submit">
    <NFormItem :label="t('auth.email')">
      <NInput v-model:value="form.email" :input-props="{ type: 'email', autocomplete: 'email' }" />
    </NFormItem>
    <Captcha v-if="cfg.register.captcha_key" ref="captchaRef" v-model="captcha" :site-key="cfg.register.captcha_key" />
    <NFormItem :label="t('auth.emailCode')">
      <NInputGroup>
        <NInput v-model:value="form.email_code" maxlength="6" :input-props="{ inputmode: 'numeric', autocomplete: 'one-time-code' }" />
        <EmailCodeButton :email="form.email" scene="reset" :captcha="captcha" :need-captcha="!!cfg.register.captcha_key" @sent="captchaRef?.reset()" />
      </NInputGroup>
    </NFormItem>
    <NFormItem :label="t('auth.newPassword')">
      <NInput v-model:value="form.password" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" />
    </NFormItem>
    <NFormItem :label="t('auth.passwordConfirm')">
      <NInput v-model:value="form.confirm" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" />
    </NFormItem>
    <NButton type="primary" block size="large" :loading="loading" attr-type="submit">{{ t('auth.resetPassword') }}</NButton>
  </NForm>
  <div class="foot"><TextLink to="/login">{{ t('auth.toLogin') }}</TextLink></div>
</template>

<style scoped>
.title { margin: 0 0 20px; font-size: 20px; }
.foot { text-align: center; margin-top: 18px; }
</style>
