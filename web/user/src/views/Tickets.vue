<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  NCard, NButton, NDataTable, NModal, NForm, NFormItem, NInput, NSelect, NTag, NAlert, type DataTableColumns,
} from 'naive-ui'
import { formatTime } from '@gv/shared'
import { http } from '../api'
import { errText, toast } from '../ui'
import { biz, loadBiz, loadSubs, subOptions } from '../store'
import type { Ticket } from '../types'

const { t } = useI18n()
const router = useRouter()
const rows = ref<Ticket[]>([])
const loading = ref(true)
const show = ref(false)
const saving = ref(false)
const form = reactive({ subject: '', level: 1, message: '', subscription_id: null as number | null })

const closedAll = computed(() => biz.value?.ticket_status === 2)
const levelOptions = computed(() => [0, 1, 2].map((v) => ({ label: t(`ticket.levels.${v}`), value: v })))

async function load() {
  loading.value = true
  try {
    rows.value = (await http.get<Ticket[]>('/user/tickets')) || []
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
}

async function submit() {
  if (!form.subject.trim() || !form.message.trim()) return
  saving.value = true
  try {
    const tk = await http.post<Ticket>('/user/tickets', {
      subject: form.subject.trim(),
      level: form.level,
      message: form.message,
      subscription_id: form.subscription_id || undefined,
    })
    show.value = false
    Object.assign(form, { subject: '', level: 1, message: '', subscription_id: null })
    router.push(`/tickets/${tk.id}`)
  } catch (e) {
    toast('error', errText(e))
  } finally {
    saving.value = false
  }
}

function statusTag(tk: Ticket) {
  if (tk.status === 1) return h(NTag, { size: 'small', bordered: false }, () => t('ticket.closed'))
  return tk.reply_status === 1
    ? h(NTag, { size: 'small', bordered: false, type: 'success' }, () => t('ticket.replied'))
    : h(NTag, { size: 'small', bordered: false, type: 'warning' }, () => t('ticket.pending'))
}

const columns = computed<DataTableColumns<Ticket>>(() => [
  { title: '#', key: 'id', width: 70 },
  { title: t('ticket.subject'), key: 'subject', ellipsis: { tooltip: true } },
  { title: t('ticket.level'), key: 'level', width: 90, render: (tk) => t(`ticket.levels.${tk.level}`) },
  { title: t('common.status'), key: 'status', width: 100, render: statusTag },
  { title: t('ticket.updatedAt'), key: 'updated_at', width: 170, render: (tk) => formatTime(tk.updated_at) },
  {
    title: t('common.actions'),
    key: 'op',
    width: 90,
    render: (tk) => h(NButton, { size: 'small', text: true, type: 'primary', onClick: () => router.push(`/tickets/${tk.id}`) }, () => t('common.detail')),
  },
])

onMounted(() => {
  load()
  loadBiz().catch(() => {})
  loadSubs().catch(() => {})
})
</script>

<template>
  <div class="page">
    <div class="row" style="justify-content: space-between; margin-bottom: 16px">
      <h2 class="page-title" style="margin: 0">{{ t('nav.tickets') }}</h2>
      <NButton type="primary" :disabled="closedAll" @click="show = true">{{ t('ticket.new') }}</NButton>
    </div>
    <NAlert v-if="closedAll" type="warning" style="margin-bottom: 16px">{{ t('ticket.disabled') }}</NAlert>
    <NCard size="small">
      <NDataTable :columns="columns" :data="rows" :loading="loading" :row-key="(r: Ticket) => r.id" :scroll-x="700">
        <template #empty>{{ t('ticket.empty') }}</template>
      </NDataTable>
    </NCard>
    <NModal v-model:show="show" preset="card" :title="t('ticket.new')" style="width: 560px; max-width: 94vw">
      <NForm @submit.prevent="submit">
        <NFormItem :label="t('ticket.subject')">
          <NInput v-model:value="form.subject" :maxlength="255" />
        </NFormItem>
        <NFormItem :label="t('ticket.level')">
          <NSelect v-model:value="form.level" :options="levelOptions" />
        </NFormItem>
        <NFormItem :label="t('ticket.relatedSub')">
          <NSelect v-model:value="form.subscription_id" :options="subOptions()" clearable />
        </NFormItem>
        <NFormItem :label="t('ticket.message')">
          <NInput v-model:value="form.message" type="textarea" :autosize="{ minRows: 5, maxRows: 12 }" :maxlength="10000" />
        </NFormItem>
        <NButton type="primary" block attr-type="submit" :loading="saving" :disabled="!form.subject.trim() || !form.message.trim()">
          {{ t('common.submit') }}
        </NButton>
      </NForm>
    </NModal>
  </div>
</template>
