<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Loader2Icon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Label } from '@gv/shared/ui/label'
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
  <div v-if="quick" class="flex flex-col items-center gap-3 py-10 text-muted-foreground">
    <Loader2Icon class="size-6 animate-spin" />
    <span class="text-sm">{{ t('auth.quickLogging') }}</span>
  </div>
  <div v-else>
    <h2 class="mb-6 text-xl font-semibold">{{ t('auth.welcome') }}</h2>
    <form class="space-y-4" @submit.prevent="submit">
      <div class="grid gap-1.5">
        <Label for="email">{{ t('auth.email') }}</Label>
        <Input id="email" v-model="email" type="email" autocomplete="username" class="h-10" />
      </div>
      <div class="grid gap-1.5">
        <div class="flex items-center justify-between">
          <Label for="password">{{ t('auth.password') }}</Label>
          <TextLink to="/forgot" class="text-sm font-normal">{{ t('auth.forgot') }}</TextLink>
        </div>
        <Input id="password" v-model="password" type="password" autocomplete="current-password" class="h-10" />
      </div>
      <Button type="submit" size="lg" class="w-full" :disabled="loading">
        <Loader2Icon v-if="loading" class="animate-spin" />{{ t('auth.login') }}
      </Button>
    </form>
    <p class="mt-6 text-center text-sm text-muted-foreground">
      {{ t('auth.noAccount') }}
      <TextLink to="/register">{{ t('auth.toRegister') }}</TextLink>
    </p>
  </div>
</template>
