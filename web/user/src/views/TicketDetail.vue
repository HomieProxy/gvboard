<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowLeftIcon, SendIcon, Loader2Icon, HeadsetIcon, UserIcon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Card, CardContent, CardFooter } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Textarea } from '@gv/shared/ui/textarea'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { cn } from '@gv/shared/lib/utils'
import { http } from '../api'
import { confirm, errText, toast } from '../ui'
import { subOptions, subs, loadSubs } from '../store'
import type { TicketDetail } from '../types'
import StatusBadge from '../components/common/StatusBadge.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const id = Number(route.params.id)
const ticket = ref<TicketDetail | null>(null)
const loading = ref(true)
const missing = ref(false)
const message = ref('')
const sending = ref(false)
const box = ref<HTMLElement | null>(null)

const waiting = computed(() => {
  const m = ticket.value?.messages
  return !!m?.length && m[m.length - 1].is_me
})
const subLabel = computed(() => {
  const sid = ticket.value?.subscription_id
  if (!sid) return ''
  return subOptions(subs.value).find((o) => o.value === sid)?.label || `#${sid}`
})

async function load() {
  try {
    ticket.value = await http.get<TicketDetail>(`/user/tickets/${id}`)
    await nextTick()
    box.value?.scrollTo({ top: box.value.scrollHeight })
  } catch (e) {
    missing.value = true
    toast('error', errText(e))
  }
}

// 定时拉取对方的新回复，有变化时才更新
let poll = 0
async function refresh() {
  if (document.hidden || sending.value || !ticket.value) return
  try {
    const d = await http.get<TicketDetail>(`/user/tickets/${id}`)
    const cur = ticket.value
    if (d.messages.length === cur.messages.length && d.status === cur.status) return
    ticket.value = d
    await nextTick()
    box.value?.scrollTo({ top: box.value.scrollHeight, behavior: 'smooth' })
  } catch {
    // 忽略轮询失败，下次再试
  }
}

async function reply() {
  if (!message.value.trim()) return
  sending.value = true
  try {
    await http.post(`/user/tickets/${id}/reply`, { message: message.value })
    message.value = ''
    await load()
  } catch (e) {
    toast('error', errText(e))
  } finally {
    sending.value = false
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) reply()
}

function close() {
  confirm(t('ticket.closeConfirm'), () => http.post(`/user/tickets/${id}/close`), t('ticket.closedTip')).then((ok) => {
    if (ok) load()
  })
}

onMounted(async () => {
  await load()
  loadSubs().catch(() => {})
  loading.value = false
  poll = window.setInterval(refresh, 5000)
})
onUnmounted(() => clearInterval(poll))
</script>

<template>
  <div>
    <Skeleton v-if="loading" class="h-96 rounded-xl" />
    <div v-else-if="missing" class="flex flex-col items-center gap-4 py-20 text-center">
      <p class="text-lg font-medium">{{ t('ticket.notFound') }}</p>
      <Button variant="outline" @click="router.push('/tickets')"><ArrowLeftIcon />{{ t('common.back') }}</Button>
    </div>
    <template v-else-if="ticket">
      <div class="mb-5 flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="sm" @click="router.push('/tickets')"><ArrowLeftIcon class="rtl:rotate-180" />{{ t('nav.tickets') }}</Button>
        <h1 class="min-w-0 truncate text-xl font-semibold tracking-tight">{{ ticket.subject }}</h1>
        <StatusBadge>{{ t(`ticket.levels.${ticket.level}`) }}</StatusBadge>
        <StatusBadge v-if="ticket.status === 1" dot>{{ t('ticket.closed') }}</StatusBadge>
        <span v-if="subLabel" class="text-sm text-muted-foreground">{{ t('ticket.subscription') }}：{{ subLabel }}</span>
        <Button v-if="ticket.status === 0" variant="outline" size="sm" class="ml-auto" @click="close">{{ t('ticket.close') }}</Button>
      </div>

      <Card class="gap-0 py-0">
        <CardContent class="p-0">
          <div ref="box" class="flex max-h-[60svh] min-h-64 flex-col gap-5 overflow-y-auto p-4 md:p-6">
            <div
              v-for="m in ticket.messages"
              :key="m.id"
              :class="cn('flex max-w-[86%] gap-3', m.is_me ? 'flex-row-reverse self-end' : 'self-start')"
            >
              <div
                :class="
                  cn(
                    'flex size-8 shrink-0 items-center justify-center rounded-full',
                    m.is_me ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground',
                  )
                "
              >
                <UserIcon v-if="m.is_me" class="size-4" />
                <HeadsetIcon v-else class="size-4" />
              </div>
              <div :class="cn('flex min-w-0 flex-col gap-1', m.is_me ? 'items-end' : 'items-start')">
                <span class="text-xs text-muted-foreground">{{ m.is_me ? t('ticket.me') : t('ticket.staff') }} · {{ formatTime(m.created_at) }}</span>
                <div
                  :class="
                    cn(
                      'rounded-2xl px-4 py-2.5 text-sm break-words whitespace-pre-wrap',
                      m.is_me ? 'rounded-tr-sm bg-primary text-primary-foreground' : 'rounded-tl-sm bg-muted',
                    )
                  "
                >
                  {{ m.message }}
                </div>
              </div>
            </div>
          </div>
        </CardContent>
        <CardFooter v-if="ticket.status === 0" class="flex-col items-stretch gap-2 border-t p-4">
          <p v-if="waiting" class="text-sm text-muted-foreground">{{ t('ticket.waitReply') }}</p>
          <div class="flex items-end gap-2">
            <Textarea
              v-model="message"
              rows="2"
              maxlength="10000"
              class="max-h-60 min-h-11 flex-1"
              :disabled="waiting"
              :placeholder="t('ticket.replyPlaceholder')"
              @keydown="onKey"
            />
            <Button size="icon" class="size-11" :disabled="sending || waiting || !message.trim()" :title="t('ticket.reply')" @click="reply">
              <Loader2Icon v-if="sending" class="animate-spin" />
              <SendIcon v-else class="rtl:-scale-x-100" />
            </Button>
          </div>
        </CardFooter>
      </Card>
    </template>
  </div>
</template>
