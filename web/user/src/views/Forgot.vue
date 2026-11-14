<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Loader2Icon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { cfg, http } from '../api'
import { run, toast } from '../ui'
import Captcha from '../components/Captcha.vue'
import EmailCodeButton from '../components/EmailCodeButton.vue'
import TextLink from '../components/TextLink.vue'
import FormItem from '../components/common/FormItem.vue'

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
  <h2 class="mb-6 text-xl font-semibold">{{ t('auth.resetPassword') }}</h2>
  <form class="space-y-4" @submit.prevent="submit">
    <FormItem :label="t('auth.email')">
      <Input v-model="form.email" type="email" autocomplete="email" class="h-10" />
    </FormItem>
    <Captcha v-if="cfg.register.captcha_key" ref="captchaRef" v-model="captcha" :site-key="cfg.register.captcha_key" />
    <FormItem :label="t('auth.emailCode')">
      <div class="flex gap-2">
        <Input v-model="form.email_code" maxlength="6" inputmode="numeric" autocomplete="one-time-code" class="h-10 flex-1" />
        <EmailCodeButton
          class="h-10"
          :email="form.email"
          scene="reset"
          :captcha="captcha"
          :need-captcha="!!cfg.register.captcha_key"
          @sent="captchaRef?.reset()"
        />
      </div>
    </FormItem>
    <FormItem :label="t('auth.newPassword')">
      <Input v-model="form.password" type="password" autocomplete="new-password" class="h-10" />
    </FormItem>
    <FormItem :label="t('auth.passwordConfirm')">
      <Input v-model="form.confirm" type="password" autocomplete="new-password" class="h-10" />
    </FormItem>
    <Button type="submit" size="lg" class="w-full" :disabled="loading">
      <Loader2Icon v-if="loading" class="animate-spin" />{{ t('auth.resetPassword') }}
    </Button>
  </form>
  <p class="mt-6 text-center text-sm"><TextLink to="/login">{{ t('auth.toLogin') }}</TextLink></p>
</template>
