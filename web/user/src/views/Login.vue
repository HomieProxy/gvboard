<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NForm, NFormItem, NInput, NButton, NSpin } from 'naive-ui'
import { http, setToken } from '../api'
import { errText, toast } from '../ui'
import { clearSession } from '../store'
import type { AuthResult } from '../types'
import TextLink from '../components/TextLink.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const email = ref('')
const password = ref('')
const loading = ref(false)
const quick = ref(false)

function target(): string {
  const r = String(route.query.redirect || '')
  if (!r) return '/dashboard'
  return r.startsWith('/') ? r : `/${r}`
}

function done(r: AuthResult) {
  clearSession()
  setToken(r.token)
  router.replace(target())
}

async function submit() {
  if (!email.value.trim() || !password.value) return
  loading.value = true
  try {
    done(await http.post<AuthResult>('/auth/login', { email: email.value.trim(), password: password.value }))
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
}

// 快捷登录链接：/#/login?verify={code}&redirect={页面}
onMounted(async () => {
  const code = String(route.query.verify || '')
  if (!code) return
  quick.value = true
  try {
    done(await http.post<AuthResult>('/auth/code-login', { code }))
  } catch (e) {
    toast('error', errText(e) || t('auth.quickLoginFailed'))
    quick.value = false
    router.replace('/login')
  }
})
</script>

<template>
  <NSpin :show="quick" :description="t('auth.quickLogging')">
    <h2 class="title">{{ t('auth.welcome') }}</h2>
    <NForm @submit.prevent="submit">
      <NFormItem :label="t('auth.email')" :show-feedback="false" style="margin-bottom: 16px">
        <NInput v-model:value="email" :input-props="{ autocomplete: 'username', type: 'email' }" size="large" />
      </NFormItem>
      <NFormItem :label="t('auth.password')" :show-feedback="false" style="margin-bottom: 8px">
        <NInput
          v-model:value="password"
          type="password"
          show-password-on="click"
          size="large"
          :input-props="{ autocomplete: 'current-password' }"
        />
      </NFormItem>
      <div class="row" style="justify-content: flex-end; margin-bottom: 16px">
        <TextLink to="/forgot">{{ t('auth.forgot') }}</TextLink>
      </div>
      <NButton type="primary" block size="large" :loading="loading" attr-type="submit">{{ t('auth.login') }}</NButton>
    </NForm>
    <div class="foot muted">
      {{ t('auth.noAccount') }}
      <TextLink to="/register">{{ t('auth.toRegister') }}</TextLink>
    </div>
  </NSpin>
</template>

<style scoped>
.title { margin: 0 0 20px; font-size: 20px; }
.foot { text-align: center; margin-top: 18px; }
</style>
