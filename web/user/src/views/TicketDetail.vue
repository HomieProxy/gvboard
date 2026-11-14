<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NCard, NButton, NInput, NSpin, NTag, NResult, NSpace } from 'naive-ui'
import { formatTime } from '@gv/shared'
import { http } from '../api'
import { confirm, errText, toast } from '../ui'
import { subName, subs, loadSubs } from '../store'
import type { TicketDetail } from '../types'

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
  const s = subs.value.find((x) => x.id === sid)
  return s ? subName(s) : `#${sid}`
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

function close() {
  confirm(t('ticket.closeConfirm'), () => http.post(`/user/tickets/${id}/close`), t('ticket.closedTip')).then((ok) => {
    if (ok) load()
  })
}

onMounted(async () => {
  await load()
  loadSubs().catch(() => {})
  loading.value = false
})
</script>

<template>
  <div class="page">
    <NSpin :show="loading">
      <NResult v-if="missing" status="404" :title="t('nav.tickets')">
        <template #footer><NButton @click="router.push('/tickets')">{{ t('common.back') }}</NButton></template>
      </NResult>
      <template v-else-if="ticket">
        <div class="row" style="margin-bottom: 16px; flex-wrap: wrap">
          <NButton quaternary size="small" @click="router.push('/tickets')">← {{ t('nav.tickets') }}</NButton>
          <h2 class="page-title" style="margin: 0">{{ ticket.subject }}</h2>
          <NTag size="small" :bordered="false">{{ t(`ticket.levels.${ticket.level}`) }}</NTag>
          <NTag v-if="ticket.status === 1" size="small" :bordered="false">{{ t('ticket.closed') }}</NTag>
          <span v-if="subLabel" class="muted">{{ t('ticket.subscription') }}：{{ subLabel }}</span>
          <div style="flex: 1" />
          <NButton v-if="ticket.status === 0" size="small" @click="close">{{ t('ticket.close') }}</NButton>
        </div>
        <NCard size="small">
          <div ref="box" class="messages">
            <div v-for="m in ticket.messages" :key="m.id" class="msg" :class="{ me: m.is_me }">
              <div class="who muted">{{ m.is_me ? t('ticket.me') : t('ticket.staff') }} · {{ formatTime(m.created_at) }}</div>
              <div class="bubble">{{ m.message }}</div>
            </div>
          </div>
        </NCard>
        <NCard v-if="ticket.status === 0" size="small" style="margin-top: 16px">
          <NSpace vertical>
            <span v-if="waiting" class="muted">{{ t('ticket.waitReply') }}</span>
            <NInput
              v-model:value="message"
              type="textarea"
              :autosize="{ minRows: 3, maxRows: 10 }"
              :maxlength="10000"
              :disabled="waiting"
              :placeholder="t('ticket.replyPlaceholder')"
            />
            <NButton type="primary" :loading="sending" :disabled="waiting || !message.trim()" @click="reply">{{ t('ticket.reply') }}</NButton>
          </NSpace>
        </NCard>
      </template>
    </NSpin>
  </div>
</template>

<style scoped>
.messages { max-height: 60vh; overflow-y: auto; display: flex; flex-direction: column; gap: 14px; padding: 4px; }
.msg { display: flex; flex-direction: column; align-items: flex-start; max-width: 86%; }
.msg.me { align-self: flex-end; align-items: flex-end; }
.who { font-size: 12px; margin-bottom: 4px; }
.bubble {
  padding: 10px 14px;
  border-radius: 10px;
  background: rgba(127, 127, 127, .12);
  white-space: pre-wrap;
  word-break: break-word;
}
.msg.me .bubble { background: var(--gv-primary-soft, rgba(24, 160, 88, .16)); }
</style>
