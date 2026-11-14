<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Loader2Icon, TriangleAlertIcon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Checkbox } from '@gv/shared/ui/checkbox'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import { cfg, http, setToken } from '../api'
import { errText, toast } from '../ui'
import { clearSession } from '../store'
import type { AuthResult } from '../types'
import Captcha from '../components/Captcha.vue'
import EmailCodeButton from '../components/EmailCodeButton.vue'
import TextLink from '../components/TextLink.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'

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
  <h2 class="mb-6 text-xl font-semibold">{{ t('auth.registerTitle') }}</h2>
  <Alert v-if="reg.closed" class="border-warning/50 bg-warning/10">
    <TriangleAlertIcon class="text-warning" />
    <AlertDescription class="text-foreground">{{ t('auth.registerClosed') }}</AlertDescription>
  </Alert>
  <form v-else class="space-y-4" @submit.prevent="submit">
    <FormItem :label="t('auth.email')">
      <div v-if="suffixes.length" class="flex gap-2">
        <Input v-model="form.prefix" class="h-10 flex-1" autocomplete="email" />
        <SelectBox class="h-10! w-[48%]" :model-value="form.suffix" :options="suffixes" @update:model-value="(v) => (form.suffix = String(v))" />
      </div>
      <Input v-else v-model="form.email" type="email" autocomplete="email" class="h-10" />
    </FormItem>
    <FormItem v-if="reg.email_code" :label="t('auth.emailCode')">
      <div class="flex gap-2">
        <Input v-model="form.email_code" maxlength="6" inputmode="numeric" autocomplete="one-time-code" class="h-10 flex-1" />
        <EmailCodeButton class="h-10" :email="email" scene="register" :captcha="captcha" :need-captcha="!!reg.captcha_key" @sent="captchaRef?.reset()" />
      </div>
    </FormItem>
    <FormItem :label="t('auth.password')">
      <Input v-model="form.password" type="password" autocomplete="new-password" class="h-10" />
    </FormItem>
    <FormItem :label="t('auth.passwordConfirm')">
      <Input v-model="form.confirm" type="password" autocomplete="new-password" class="h-10" />
    </FormItem>
    <FormItem :label="reg.invite_required ? t('auth.inviteCode') : t('auth.inviteCodeOptional')">
      <Input v-model="form.invite_code" :disabled="fromLink" class="h-10" />
    </FormItem>
    <Captcha v-if="reg.captcha_key" ref="captchaRef" v-model="captcha" :site-key="reg.captcha_key" />
    <label v-if="cfg.site.terms_url" class="flex items-center gap-2 text-sm">
      <Checkbox v-model="agree" />
      <span>
        {{ t('auth.agree') }}
        <a :href="cfg.site.terms_url" target="_blank" rel="noopener" class="text-primary hover:underline" @click.stop>{{ t('auth.terms') }}</a>
      </span>
    </label>
    <Button type="submit" size="lg" class="w-full" :disabled="loading">
      <Loader2Icon v-if="loading" class="animate-spin" />{{ t('auth.register') }}
    </Button>
  </form>
  <p class="mt-6 text-center text-sm text-muted-foreground">
    {{ t('auth.hasAccount') }}
    <TextLink to="/login">{{ t('auth.toLogin') }}</TextLink>
  </p>
</template>
