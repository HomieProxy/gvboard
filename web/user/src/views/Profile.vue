<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  NGrid, NGi, NCard, NButton, NForm, NFormItem, NInput, NDataTable, NTag, NSwitch, NSpace, NDescriptions, NDescriptionsItem, NModal,
  type DataTableColumns,
} from 'naive-ui'
import { formatTime } from '@gv/shared'
import { http, setToken } from '../api'
import { confirm, errText, toast } from '../ui'
import { clearSession, loadMe, me, money } from '../store'
import type { Session, TelegramStatus } from '../types'
import QrCode from '../components/QrCode.vue'

const { t } = useI18n()
const router = useRouter()

// 修改密码
const pwd = reactive({ old: '', next: '', again: '' })
const changing = ref(false)
const pwdError = computed(() => {
  if (pwd.next && pwd.next.length < 8) return t('auth.passwordTooShort')
  if (pwd.again && pwd.again !== pwd.next) return t('auth.passwordMismatch')
  return ''
})
async function changePassword() {
  if (!pwd.old || !pwd.next || pwdError.value || pwd.next !== pwd.again) return
  changing.value = true
  try {
    await http.post('/user/password', { old_password: pwd.old, new_password: pwd.next })
    toast('success', t('profile.passwordChanged'))
    setToken('')
    clearSession()
    router.replace('/login')
  } catch (e) {
    toast('error', errText(e))
  } finally {
    changing.value = false
  }
}

// 会话
const sessions = ref<Session[]>([])
async function loadSessions() {
  try {
    sessions.value = (await http.get<Session[]>('/user/sessions')) || []
  } catch (e) {
    toast('error', errText(e))
  }
}
function removeSession(s: Session) {
  confirm(t('profile.removeSessionConfirm'), () => http.del(`/user/sessions/${encodeURIComponent(s.id)}`), t('common.success')).then((ok) => {
    if (!ok) return
    if (s.current) {
      setToken('')
      clearSession()
      router.replace('/login')
    } else loadSessions()
  })
}
const sessionColumns = computed<DataTableColumns<Session>>(() => [
  { title: 'IP', key: 'ip', width: 150 },
  { title: t('profile.device'), key: 'ua', ellipsis: { tooltip: true } },
  { title: t('profile.loginAt'), key: 'login_at', width: 170, render: (s) => formatTime(s.login_at) },
  {
    title: t('common.actions'),
    key: 'op',
    width: 120,
    render: (s) =>
      h(NSpace, { size: 8, wrap: false, align: 'center' }, () => [
        s.current ? h(NTag, { size: 'small', bordered: false, type: 'success' }, () => t('profile.current')) : null,
        h(NButton, { size: 'small', text: true, type: 'error', onClick: () => removeSession(s) }, () => t('profile.removeSession')),
      ]),
  },
])

// Telegram
const tg = ref<TelegramStatus | null>(null)
let tgTimer = 0
async function loadTelegram() {
  try {
    tg.value = await http.get<TelegramStatus>('/user/telegram')
  } catch {
    tg.value = null
  }
}
async function bindTelegram() {
  try {
    const r = await http.post<{ url: string; expires_in: number }>('/user/telegram/bind')
    window.open(r.url, '_blank', 'noopener')
    const until = Date.now() + r.expires_in * 1000
    clearInterval(tgTimer)
    tgTimer = window.setInterval(async () => {
      await loadTelegram()
      if (tg.value?.bound || Date.now() > until) {
        clearInterval(tgTimer)
        if (tg.value?.bound) {
          toast('success', t('profile.telegramBound'))
          loadMe().catch(() => {})
        }
      }
    }, 3000)
  } catch (e) {
    toast('error', errText(e))
  }
}
function unbindTelegram() {
  confirm(t('profile.unbindConfirm'), () => http.post('/user/telegram/unbind'), t('common.success')).then((ok) => {
    if (ok) loadTelegram()
  })
}
async function setPref(key: keyof TelegramStatus['prefs'], v: boolean) {
  if (!tg.value) return
  try {
    await http.put('/user/telegram/prefs', { [key]: v })
    tg.value.prefs[key] = v
  } catch (e) {
    toast('error', errText(e))
  }
}
const prefKeys: { key: keyof TelegramStatus['prefs']; label: string }[] = [
  { key: 'notify_order', label: 'profile.notifyOrder' },
  { key: 'notify_ticket', label: 'profile.notifyTicket' },
  { key: 'notify_remind', label: 'profile.notifyRemind' },
]

// 快捷登录
const quickUrl = ref('')
const quickShow = ref(false)
async function quickLogin() {
  try {
    quickUrl.value = await http.post<string>('/auth/quick-login', { redirect: 'dashboard' })
    quickShow.value = true
  } catch (e) {
    toast('error', errText(e))
  }
}

onMounted(() => {
  loadMe().catch(() => {})
  loadSessions()
  loadTelegram()
})
onUnmounted(() => clearInterval(tgTimer))
</script>

<template>
  <div class="page">
    <h2 class="page-title">{{ t('nav.profile') }}</h2>
    <NGrid cols="1 l:2" responsive="screen" :x-gap="16" :y-gap="16">
      <NGi>
        <NCard size="small" :title="t('profile.account')">
          <NDescriptions v-if="me" :column="1" label-placement="left" size="small">
            <NDescriptionsItem :label="t('auth.email')">{{ me.email }}</NDescriptionsItem>
            <NDescriptionsItem :label="t('wallet.balance')">{{ money(me.balance) }}</NDescriptionsItem>
            <NDescriptionsItem v-if="me.discount" :label="t('profile.discountLabel')">{{ t('profile.discount', { n: me.discount }) }}</NDescriptionsItem>
            <NDescriptionsItem :label="t('profile.subLimit')">{{ me.subscription_limit }}</NDescriptionsItem>
            <NDescriptionsItem :label="t('profile.registeredAt')">{{ formatTime(me.created_at) }}</NDescriptionsItem>
            <NDescriptionsItem v-if="me.last_login_at" :label="t('profile.lastLogin')">{{ formatTime(me.last_login_at) }}</NDescriptionsItem>
          </NDescriptions>
          <NButton size="small" style="margin-top: 12px" @click="quickLogin">{{ t('profile.quickLogin') }}</NButton>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" :title="t('profile.changePassword')">
          <NForm @submit.prevent="changePassword">
            <NFormItem :label="t('profile.oldPassword')">
              <NInput v-model:value="pwd.old" type="password" show-password-on="click" autocomplete="current-password" />
            </NFormItem>
            <NFormItem :label="t('profile.newPassword')" :validation-status="pwdError ? 'error' : undefined" :feedback="pwdError">
              <NInput v-model:value="pwd.next" type="password" show-password-on="click" autocomplete="new-password" />
            </NFormItem>
            <NFormItem :label="t('profile.confirmPassword')">
              <NInput v-model:value="pwd.again" type="password" show-password-on="click" autocomplete="new-password" />
            </NFormItem>
            <NButton type="primary" attr-type="submit" :loading="changing" :disabled="!pwd.old || !pwd.next || pwd.next !== pwd.again || !!pwdError">
              {{ t('common.save') }}
            </NButton>
          </NForm>
        </NCard>
      </NGi>
      <NGi v-if="tg?.enabled">
        <NCard size="small" :title="t('profile.telegram')">
          <template #header-extra>
            <NTag size="small" :bordered="false" :type="tg.bound ? 'success' : 'default'">{{ tg.bound ? t('profile.bound') : t('profile.unbound') }}</NTag>
          </template>
          <template v-if="tg.bound">
            <NSpace vertical>
              <div v-for="p in prefKeys" :key="p.key" class="row" style="justify-content: space-between">
                <span>{{ t(p.label) }}</span>
                <NSwitch :value="tg.prefs[p.key]" @update:value="(v: boolean) => setPref(p.key, v)" />
              </div>
              <NSpace>
                <NButton size="small" @click="unbindTelegram">{{ t('profile.unbind') }}</NButton>
                <NButton v-if="tg.discuss_link" size="small" tag="a" :href="tg.discuss_link" target="_blank" rel="noopener">{{ t('profile.discussGroup') }}</NButton>
              </NSpace>
            </NSpace>
          </template>
          <template v-else>
            <p class="muted">{{ t('profile.bindHint') }}<span v-if="tg.username" class="mono"> @{{ tg.username }}</span></p>
            <NButton type="primary" size="small" @click="bindTelegram">{{ t('profile.bind') }}</NButton>
          </template>
        </NCard>
      </NGi>
      <NGi span="1 l:2">
        <NCard size="small" :title="t('profile.sessions')">
          <NDataTable :columns="sessionColumns" :data="sessions" :row-key="(s: Session) => s.id" :scroll-x="640" />
        </NCard>
      </NGi>
    </NGrid>
    <NModal v-model:show="quickShow" preset="card" :title="t('profile.quickLogin')" style="width: 320px">
      <div style="display: flex; justify-content: center"><QrCode :text="quickUrl" :size="240" /></div>
      <p class="muted" style="text-align: center">{{ t('profile.quickLoginHint') }}</p>
    </NModal>
  </div>
</template>
