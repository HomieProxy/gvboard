<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NGrid, NGi, NCard, NStatistic, NAlert, NButton, NEmpty, NList, NListItem, NModal, NSpin, NSpace, NTag } from 'naive-ui'
import { formatTime } from '@gv/shared'
import { http } from '../api'
import { errText, toast } from '../ui'
import { loadMe, loadStat, loadSubs, me, money, stat, subs } from '../store'
import type { Notice } from '../types'
import SubCard from '../components/SubCard.vue'
import Markdown from '../components/Markdown.vue'

const { t } = useI18n()
const router = useRouter()
const loading = ref(true)
const notices = ref<Notice[]>([])
const notice = ref<Notice | null>(null)
const noticeShow = ref(false)

function openNotice(n: Notice) {
  notice.value = n
  noticeShow.value = true
}

onMounted(async () => {
  try {
    const [, , , n] = await Promise.all([
      loadMe(),
      loadStat(),
      loadSubs(),
      http.get<{ total: number; list: Notice[] }>('/user/notices', { current: 1, page_size: 5 }),
    ])
    notices.value = n.list || []
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <NSpin :show="loading">
      <NSpace vertical :size="16">
        <NAlert v-if="stat && stat.pending_orders > 0" type="warning" :show-icon="true">
          <div class="row">
            <span>{{ t('dashboard.pendingOrderTip', { n: stat.pending_orders }) }}</span>
            <span class="spacer" />
            <NButton size="small" type="warning" @click="router.push('/orders')">{{ t('dashboard.goPay') }}</NButton>
          </div>
        </NAlert>

        <NGrid cols="2 m:4" responsive="screen" :x-gap="12" :y-gap="12">
          <NGi>
            <NCard size="small" class="clickable" @click="router.push('/wallet')">
              <NStatistic :label="t('dashboard.balance')" :value="money(me?.balance)" />
            </NCard>
          </NGi>
          <NGi>
            <NCard size="small" class="clickable" @click="router.push('/invite')">
              <NStatistic :label="t('dashboard.commission')" :value="money(me?.commission_balance)" />
            </NCard>
          </NGi>
          <NGi>
            <NCard size="small" class="clickable" @click="router.push('/tickets')">
              <NStatistic :label="t('dashboard.openTickets')" :value="stat?.open_tickets ?? 0" />
            </NCard>
          </NGi>
          <NGi>
            <NCard size="small" class="clickable" @click="router.push('/invite')">
              <NStatistic :label="t('dashboard.invitees')" :value="stat?.invitees ?? 0" />
            </NCard>
          </NGi>
        </NGrid>

        <NGrid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="16">
          <NGi span="1 l:2">
            <div class="row section">
              <strong>{{ t('dashboard.mySubscriptions') }}</strong>
              <span class="spacer" />
              <NButton text type="primary" @click="router.push('/subscriptions')">{{ t('dashboard.manage') }}</NButton>
            </div>
            <NSpace vertical :size="12">
              <SubCard v-for="s in subs" :key="s.id" :sub="s" class="clickable" @click="router.push(`/subscriptions/${s.id}`)" />
            </NSpace>
            <NCard v-if="!loading && !subs.some((s) => s.plan)" size="small" style="margin-top: 12px">
              <NEmpty :description="t('dashboard.noSubscription')">
                <template #extra>
                  <NButton type="primary" @click="router.push('/plans')">{{ t('dashboard.buyNow') }}</NButton>
                </template>
              </NEmpty>
            </NCard>
          </NGi>
          <NGi>
            <div class="row section">
              <strong>{{ t('dashboard.notices') }}</strong>
              <span class="spacer" />
              <NButton text type="primary" @click="router.push('/notices')">{{ t('common.view') }}</NButton>
            </div>
            <NCard size="small">
              <NList v-if="notices.length" hoverable clickable :show-divider="true">
                <NListItem v-for="n in notices" :key="n.id" @click="openNotice(n)">
                  <div>{{ n.title }}</div>
                  <NSpace :size="4" style="margin-top: 4px">
                    <NTag v-for="tg in n.tags || []" :key="tg" size="tiny" :bordered="false">{{ tg }}</NTag>
                    <span class="muted">{{ formatTime(n.created_at, false) }}</span>
                  </NSpace>
                </NListItem>
              </NList>
              <NEmpty v-else :description="t('notice.empty')" />
            </NCard>
          </NGi>
        </NGrid>
      </NSpace>
    </NSpin>
    <NModal v-model:show="noticeShow" preset="card" :title="notice?.title" style="width: 720px; max-width: 95vw">
      <img v-if="notice?.img_url" :src="notice.img_url" alt="" style="max-width: 100%; border-radius: 6px; margin-bottom: 12px" />
      <Markdown :source="notice?.content" />
    </NModal>
  </div>
</template>

<style scoped>
.section { margin-bottom: 10px; }
</style>
