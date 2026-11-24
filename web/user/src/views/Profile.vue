<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { KeyRoundIcon, SendIcon, MonitorSmartphoneIcon, QrCodeIcon, UserIcon, Loader2Icon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle, CardAction } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Switch } from '@gv/shared/ui/switch'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { http, setToken } from '../api'
import { confirm, errText, toast } from '../ui'
import { clearSession, loadMe, me, money } from '../store'
import type { Session, TelegramStatus } from '../types'
import QrCode from '../components/QrCode.vue'
import PageHeader from '../components/common/PageHeader.vue'
import FormItem from '../components/common/FormItem.vue'
import DataTable from '../components/common/DataTable.vue'
import StatusBadge from '../components/common/StatusBadge.vue'

const { t } = useI18n()
const router = useRouter()

const infos = computed(() => {
  const m = me.value
  if (!m) return []
  const out = [
    { label: t('auth.email'), value: m.email },
    { label: t('wallet.balance'), value: money(m.balance) },
    { label: t('profile.subLimit'), value: String(m.subscription_limit) },
    { label: t('profile.registeredAt'), value: formatTime(m.created_at) },
  ]
  if (m.discount) out.splice(2, 0, { label: t('profile.discountLabel'), value: t('profile.discount', { n: m.discount }) })
  if (m.last_login_at) out.push({ label: t('profile.lastLogin'), value: formatTime(m.last_login_at) })
  return out
})

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
  confirm(t('profile.removeSessionConfirm'), () => http.del(`/user/sessions/${encodeURIComponent(s.id)}`), t('common.success'), true).then((ok) => {
    if (!ok) return
    if (s.current) {
      setToken('')
      clearSession()
      router.replace('/login')
    } else loadSessions()
  })
}
const sessionColumns = computed(() => [
  { key: 'ip', title: 'IP', class: 'w-40' },
  { key: 'ua', title: t('profile.device') },
  { key: 'login_at', title: t('profile.loginAt'), class: 'w-44' },
  { key: 'op', title: t('common.actions'), align: 'right' as const, class: 'w-36' },
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
  <div>
    <PageHeader :title="t('nav.profile')" />
    <div class="grid grid-cols-1 items-start gap-6 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-base"><UserIcon class="size-4" />{{ t('profile.account') }}</CardTitle>
        </CardHeader>
        <CardContent>
          <dl class="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2">
            <div v-for="i in infos" :key="i.label" class="min-w-0">
              <dt class="text-muted-foreground">{{ i.label }}</dt>
              <dd class="mt-0.5 truncate font-medium">{{ i.value }}</dd>
            </div>
          </dl>
        </CardContent>
        <CardFooter>
          <Button variant="outline" @click="quickLogin"><QrCodeIcon />{{ t('profile.quickLogin') }}</Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-base"><KeyRoundIcon class="size-4" />{{ t('profile.changePassword') }}</CardTitle>
        </CardHeader>
        <form @submit.prevent="changePassword">
          <CardContent class="space-y-4">
            <FormItem :label="t('profile.oldPassword')">
              <Input v-model="pwd.old" type="password" autocomplete="current-password" />
            </FormItem>
            <FormItem :label="t('profile.newPassword')">
              <Input v-model="pwd.next" type="password" autocomplete="new-password" :aria-invalid="!!pwdError || undefined" />
            </FormItem>
            <FormItem :label="t('profile.confirmPassword')">
              <Input v-model="pwd.again" type="password" autocomplete="new-password" />
            </FormItem>
            <p v-if="pwdError" class="text-sm text-destructive">{{ pwdError }}</p>
          </CardContent>
          <CardFooter class="pt-6">
            <Button type="submit" :disabled="changing || !pwd.old || !pwd.next || pwd.next !== pwd.again || !!pwdError">
              <Loader2Icon v-if="changing" class="animate-spin" />{{ t('common.save') }}
            </Button>
          </CardFooter>
        </form>
      </Card>

      <Card v-if="tg?.enabled">
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-base"><SendIcon class="size-4" />{{ t('profile.telegram') }}</CardTitle>
          <CardAction>
            <StatusBadge :tone="tg.bound ? 'success' : 'default'" dot>{{ tg.bound ? t('profile.bound') : t('profile.unbound') }}</StatusBadge>
          </CardAction>
          <CardDescription v-if="!tg.bound">
            {{ t('profile.bindHint') }}<span v-if="tg.username" class="font-mono"> @{{ tg.username }}</span>
          </CardDescription>
        </CardHeader>
        <CardContent v-if="tg.bound" class="space-y-3">
          <label v-for="p in prefKeys" :key="p.key" class="flex items-center justify-between gap-4 text-sm">
            <span>{{ t(p.label) }}</span>
            <Switch :model-value="tg.prefs[p.key]" @update:model-value="(v: boolean) => setPref(p.key, v)" />
          </label>
        </CardContent>
        <CardFooter class="gap-2">
          <template v-if="tg.bound">
            <Button variant="outline" @click="unbindTelegram">{{ t('profile.unbind') }}</Button>
            <Button v-if="tg.discuss_link" variant="ghost" as="a" :href="tg.discuss_link" target="_blank" rel="noopener">{{ t('profile.discussGroup') }}</Button>
          </template>
          <Button v-else @click="bindTelegram">{{ t('profile.bind') }}</Button>
        </CardFooter>
      </Card>

      <Card class="lg:col-span-2">
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-base"><MonitorSmartphoneIcon class="size-4" />{{ t('profile.sessions') }}</CardTitle>
        </CardHeader>
        <CardContent class="overflow-x-auto">
          <DataTable :columns="sessionColumns" :rows="sessions" class="min-w-[640px]">
            <template #cell-ip="{ row }"><span class="font-mono text-xs">{{ row.ip }}</span></template>
            <template #cell-ua="{ row }"><span class="block max-w-60 truncate text-muted-foreground md:max-w-md" :title="row.ua">{{ row.ua }}</span></template>
            <template #cell-login_at="{ row }"><span class="text-muted-foreground">{{ formatTime(row.login_at) }}</span></template>
            <template #cell-op="{ row }">
              <div class="flex items-center justify-end gap-2">
                <StatusBadge v-if="row.current" tone="success">{{ t('profile.current') }}</StatusBadge>
                <Button size="sm" variant="ghost" class="h-7 text-destructive hover:text-destructive" @click="removeSession(row)">
                  {{ t('profile.removeSession') }}
                </Button>
              </div>
            </template>
          </DataTable>
        </CardContent>
      </Card>
    </div>

    <Dialog v-model:open="quickShow">
      <DialogContent class="sm:max-w-xs">
        <DialogHeader>
          <DialogTitle>{{ t('profile.quickLogin') }}</DialogTitle>
          <DialogDescription>{{ t('profile.quickLoginHint') }}</DialogDescription>
        </DialogHeader>
        <div class="flex justify-center"><QrCode :text="quickUrl" :size="240" /></div>
      </DialogContent>
    </Dialog>
  </div>
</template>
