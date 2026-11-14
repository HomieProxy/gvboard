<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NForm, NFormItem, NInput, NButton, NCheckbox, NSelect, NAlert, NInputGroup } from 'naive-ui'
import { cfg, http, setToken } from '../api'
import { errText, toast } from '../ui'
import { clearSession } from '../store'
import type { AuthResult } from '../types'
import Captcha from '../components/Captcha.vue'
import EmailCodeButton from '../components/EmailCodeButton.vue'
import TextLink from '../components/TextLink.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const reg = computed(() => cfg.value.register)

const form = ref({ prefix: '', suffix: '', email: '', password: '', confirm: '', email_code: '', invite_code: '' })
const agree = ref(false)
const captcha = ref('')
const captchaRef = ref<InstanceType<typeof Captcha> | null>(null)
const loading = ref(false)
const fromLink = ref(false)

const suffixes = computed(() => reg.value.email_suffixes.map((s) => ({ label: `@${s}`, value: s })))
const email = computed(() => (suffixes.value.length ? `${form.value.prefix.trim()}@${form.value.suffix}` : form.value.email.trim()))

onMounted(() => {
  if (reg.value.email_suffixes.length) form.value.suffix = reg.value.email_suffixes[0]
  const code = String(route.query.code || '')
  if (code) {
    form.value.invite_code = code
    fromLink.value = true
    http.post('/invite/visit', { code }).catch(() => {})
  }
})

async function submit() {
  const f = form.value
  if (f.password.length < 8) return toast('warning', t('auth.passwordTooShort'))
  if (f.password !== f.confirm) return toast('warning', t('auth.passwordMismatch'))
  if (cfg.value.site.terms_url && !agree.value) return toast('warning', t('auth.mustAgree'))
  if (reg.value.captcha_key && !captcha.value) return toast('warning', t('auth.captchaRequired'))
  loading.value = true
  try {
    const r = await http.post<AuthResult>('/auth/register', {
      email: email.value,
      password: f.password,
      invite_code: f.invite_code.trim() || undefined,
      email_code: f.email_code.trim() || undefined,
      captcha: captcha.value || undefined,
    })
    clearSession()
    setToken(r.token)
    router.replace('/dashboard')
  } catch (e) {
    toast('error', errText(e))
    captchaRef.value?.reset()
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <h2 class="title">{{ t('auth.registerTitle') }}</h2>
  <NAlert v-if="reg.closed" type="warning">{{ t('auth.registerClosed') }}</NAlert>
  <NForm v-else @submit.prevent="submit">
    <NFormItem :label="t('auth.email')">
      <NInputGroup v-if="suffixes.length">
        <NInput v-model:value="form.prefix" />
        <NSelect v-model:value="form.suffix" :options="suffixes" style="width: 55%" />
      </NInputGroup>
      <NInput v-else v-model:value="form.email" :input-props="{ type: 'email', autocomplete: 'email' }" />
    </NFormItem>
    <NFormItem v-if="reg.email_code" :label="t('auth.emailCode')">
      <NInputGroup>
        <NInput v-model:value="form.email_code" maxlength="6" :input-props="{ inputmode: 'numeric', autocomplete: 'one-time-code' }" />
        <EmailCodeButton :email="email" scene="register" :captcha="captcha" :need-captcha="!!reg.captcha_key" @sent="captchaRef?.reset()" />
      </NInputGroup>
    </NFormItem>
    <NFormItem :label="t('auth.password')">
      <NInput v-model:value="form.password" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" />
    </NFormItem>
    <NFormItem :label="t('auth.passwordConfirm')">
      <NInput v-model:value="form.confirm" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" />
    </NFormItem>
    <NFormItem :label="reg.invite_required ? t('auth.inviteCode') : t('auth.inviteCodeOptional')">
      <NInput v-model:value="form.invite_code" :disabled="fromLink" />
    </NFormItem>
    <Captcha v-if="reg.captcha_key" ref="captchaRef" v-model="captcha" :site-key="reg.captcha_key" />
    <div v-if="cfg.site.terms_url" style="margin-bottom: 16px">
      <NCheckbox v-model:checked="agree">
        {{ t('auth.agree') }} <a :href="cfg.site.terms_url" target="_blank" rel="noopener" @click.stop>{{ t('auth.terms') }}</a>
      </NCheckbox>
    </div>
    <NButton type="primary" block size="large" :loading="loading" attr-type="submit">{{ t('auth.register') }}</NButton>
  </NForm>
  <div class="foot muted">
    {{ t('auth.hasAccount') }}
    <TextLink to="/login">{{ t('auth.toLogin') }}</TextLink>
  </div>
</template>

<style scoped>
.title { margin: 0 0 20px; font-size: 20px; }
.foot { text-align: center; margin-top: 18px; }
</style>
