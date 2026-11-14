<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { PlusIcon, TriangleAlertIcon, Loader2Icon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { http } from '../api'
import { errText, toast } from '../ui'
import { biz, loadBiz, loadSubs, subOptions } from '../store'
import type { Ticket } from '../types'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable from '../components/common/DataTable.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import StatusBadge from '../components/common/StatusBadge.vue'

const { t } = useI18n()
const router = useRouter()
const rows = ref<Ticket[]>([])
const loading = ref(true)
const show = ref(false)
const saving = ref(false)
const form = reactive({ subject: '', level: 1, message: '', subscription_id: null as number | null })

const closedAll = computed(() => biz.value?.ticket_status === 2)
const levelOptions = computed(() => [0, 1, 2].map((v) => ({ label: t(`ticket.levels.${v}`), value: v })))
const subChoices = computed(() => [{ label: t('common.none'), value: null }, ...subOptions()])

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

const columns = computed(() => [
  { key: 'id', title: '#', class: 'w-16' },
  { key: 'subject', title: t('ticket.subject') },
  { key: 'level', title: t('ticket.level'), class: 'w-24' },
  { key: 'status', title: t('common.status'), class: 'w-28' },
  { key: 'created_at', title: t('order.createdAt'), class: 'w-44' },
  { key: 'updated_at', title: t('ticket.updatedAt'), class: 'w-44' },
])

onMounted(() => {
  load()
  loadBiz().catch(() => {})
  loadSubs().catch(() => {})
})
</script>

<template>
  <div>
    <PageHeader :title="t('nav.tickets')">
      <Button :disabled="closedAll" @click="show = true"><PlusIcon />{{ t('ticket.new') }}</Button>
    </PageHeader>
    <Alert v-if="closedAll" class="mb-4 border-warning/50 bg-warning/10">
      <TriangleAlertIcon class="text-warning" />
      <AlertDescription class="text-foreground">{{ t('ticket.disabled') }}</AlertDescription>
    </Alert>
    <div class="overflow-x-auto">
      <DataTable
        :columns="columns"
        :rows="rows"
        :loading="loading"
        :empty="t('ticket.empty')"
        clickable
        class="min-w-[720px]"
        @row-click="(r) => router.push(`/tickets/${r.id}`)"
      >
        <template #cell-subject="{ row }"><span class="font-medium">{{ row.subject }}</span></template>
        <template #cell-level="{ row }">{{ t(`ticket.levels.${row.level}`) }}</template>
        <template #cell-status="{ row }">
          <StatusBadge v-if="row.status === 1" dot>{{ t('ticket.closed') }}</StatusBadge>
          <StatusBadge v-else-if="row.reply_status === 1" tone="success" dot>{{ t('ticket.replied') }}</StatusBadge>
          <StatusBadge v-else tone="warning" dot>{{ t('ticket.pending') }}</StatusBadge>
        </template>
        <template #cell-created_at="{ row }"><span class="text-muted-foreground">{{ formatTime(row.created_at) }}</span></template>
        <template #cell-updated_at="{ row }"><span class="text-muted-foreground">{{ formatTime(row.updated_at) }}</span></template>
      </DataTable>
    </div>

    <Dialog v-model:open="show">
      <DialogContent class="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{{ t('ticket.new') }}</DialogTitle>
        </DialogHeader>
        <form class="space-y-4" @submit.prevent="submit">
          <FormItem :label="t('ticket.subject')" required>
            <Input v-model="form.subject" maxlength="255" />
          </FormItem>
          <div class="grid gap-4 sm:grid-cols-2">
            <FormItem :label="t('ticket.level')">
              <SelectBox :model-value="form.level" :options="levelOptions" @update:model-value="(v) => (form.level = Number(v))" />
            </FormItem>
            <FormItem :label="t('ticket.relatedSub')">
              <SelectBox
                :model-value="form.subscription_id"
                :options="subChoices"
                @update:model-value="(v) => (form.subscription_id = v === null ? null : Number(v))"
              />
            </FormItem>
          </div>
          <FormItem :label="t('ticket.message')" required>
            <Textarea v-model="form.message" rows="6" maxlength="10000" class="max-h-80" />
          </FormItem>
          <DialogFooter>
            <Button type="button" variant="outline" @click="show = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="saving || !form.subject.trim() || !form.message.trim()">
              <Loader2Icon v-if="saving" class="animate-spin" />{{ t('common.submit') }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </div>
</template>
