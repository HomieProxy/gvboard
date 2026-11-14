<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NGrid, NGi, NCard, NButton, NTag, NSpin, NEmpty, NSpace, NAlert } from 'naive-ui'
import { http } from '../api'
import { errText, toast } from '../ui'
import { loadBiz, loadSubs, money, subName, subs } from '../store'
import { PERIODS, type Plan } from '../types'
import Markdown from '../components/Markdown.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const plans = ref<Plan[]>([])
const loading = ref(true)
const target = Number(route.query.sub || 0)

function minPrice(p: Plan): { period: string; price: number } | null {
  let best: { period: string; price: number } | null = null
  for (const k of PERIODS) {
    if (k === 'reset_price') continue
    const v = p[k]
    if (v === null || v === undefined) continue
    if (!best || v < best.price) best = { period: k, price: v }
  }
  return best
}

const soldOut = (p: Plan) => p.capacity_limit !== null && p.capacity_limit <= 0

function choose(p: Plan) {
  router.push({ path: `/plans/${p.id}`, query: route.query.sub !== undefined ? { sub: route.query.sub } : {} })
}

onMounted(async () => {
  try {
    const [list] = await Promise.all([http.get<Plan[]>('/user/plans'), loadSubs(), loadBiz()])
    plans.value = list || []
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <h2 class="page-title">{{ t('plan.title') }}</h2>
    <NAlert v-if="target > 0 && subs.find((s) => s.id === target)" type="info" style="margin-bottom: 16px">
      {{ t('plan.target') }}：{{ subName(subs.find((s) => s.id === target)!) }}
    </NAlert>
    <NSpin :show="loading">
      <NGrid cols="1 s:2 l:3" responsive="screen" :x-gap="16" :y-gap="16">
        <NGi v-for="p in plans" :key="p.id">
          <NCard :title="p.name" class="plan" size="small">
            <template #header-extra>
              <NTag v-if="soldOut(p)" type="error" size="small" :bordered="false">{{ t('plan.soldOut') }}</NTag>
              <NTag v-else-if="p.capacity_limit !== null" type="warning" size="small" :bordered="false">{{ t('plan.left', { n: p.capacity_limit }) }}</NTag>
            </template>
            <div v-if="minPrice(p)" class="price">
              <strong>{{ money(minPrice(p)!.price) }}</strong>
              <span class="muted"> / {{ t(`period.${minPrice(p)!.period}`) }} {{ t('plan.from') }}</span>
            </div>
            <NSpace :size="6" style="margin: 8px 0">
              <NTag size="small" :bordered="false">{{ t('plan.traffic', { n: p.transfer_enable }) }}</NTag>
              <NTag v-if="p.device_limit" size="small" :bordered="false">{{ t('plan.devices', { n: p.device_limit }) }}</NTag>
              <NTag v-if="p.speed_limit" size="small" :bordered="false">{{ t('plan.speed', { n: p.speed_limit }) }}</NTag>
            </NSpace>
            <Markdown :source="p.content" class="content" />
            <template #action>
              <NButton type="primary" block :disabled="soldOut(p)" @click="choose(p)">{{ t('plan.buy') }}</NButton>
            </template>
          </NCard>
        </NGi>
      </NGrid>
      <NEmpty v-if="!loading && !plans.length" :description="t('plan.empty')" />
    </NSpin>
  </div>
</template>

<style scoped>
.plan { height: 100%; display: flex; flex-direction: column; }
.price strong { font-size: 26px; }
.content { font-size: 14px; }
</style>
