<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { InfoIcon } from '@lucide/vue'
import { Card, CardContent, CardFooter, CardHeader, CardTitle, CardAction } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@gv/shared/ui/tabs'
import { http } from '../api'
import { errText, toast } from '../ui'
import { loadBiz, loadSubs, money, subName, subs } from '../store'
import { PERIODS, type Plan } from '../types'
import PlanContent from '../components/PlanContent.vue'
import PageHeader from '../components/common/PageHeader.vue'
import StatusBadge from '../components/common/StatusBadge.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const plans = ref<Plan[]>([])
const loading = ref(true)
const tab = ref('all')
const target = Number(route.query.sub || 0)
const targetSub = computed(() => (target > 0 ? subs.value.find((s) => s.id === target) : undefined))

const periodic = ['month_price', 'quarter_price', 'half_year_price', 'year_price', 'two_year_price', 'three_year_price'] as const
const shown = computed(() =>
  plans.value.filter((p) => {
    if (tab.value === 'period') return periodic.some((k) => p[k])
    if (tab.value === 'traffic') return !!p.onetime_price
    return true
  }),
)

/** 卡片上展示的价格：按周期顺序取第一个可售价格 */
function unitPrice(p: Plan): { period: string; price: number } | null {
  for (const k of PERIODS) {
    if (k === 'reset_price') continue
    const v = p[k]
    if (v !== null && v !== undefined) return { period: k, price: v }
  }
  return null
}

const soldOut = (p: Plan) => p.capacity_limit !== null && p.capacity_limit <= 0
const soonSoldOut = (p: Plan) => p.capacity_limit !== null && p.capacity_limit >= 1 && p.capacity_limit <= 5

function choose(p: Plan) {
  if (soldOut(p)) return
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
  <div>
    <PageHeader :title="t('plan.title')">
      <Tabs v-model="tab">
        <TabsList>
          <TabsTrigger value="all">{{ t('plan.tabAll') }}</TabsTrigger>
          <TabsTrigger value="period">{{ t('plan.tabPeriod') }}</TabsTrigger>
          <TabsTrigger value="traffic">{{ t('plan.tabTraffic') }}</TabsTrigger>
        </TabsList>
      </Tabs>
    </PageHeader>
    <Alert v-if="targetSub" class="mb-4">
      <InfoIcon />
      <AlertDescription>{{ t('plan.target') }}：{{ subName(targetSub) }}</AlertDescription>
    </Alert>
    <div v-if="loading" class="grid gap-5 md:grid-cols-2 xl:grid-cols-3">
      <Skeleton v-for="i in 3" :key="i" class="h-80 rounded-xl" />
    </div>
    <div v-else class="grid gap-5 md:grid-cols-2 xl:grid-cols-3">
      <Card
        v-for="p in shown"
        :key="p.id"
        :class="['flex flex-col gap-0 overflow-hidden py-0 transition-shadow', soldOut(p) ? 'opacity-70' : 'cursor-pointer hover:shadow-lg']"
        @click="choose(p)"
      >
        <CardHeader class="border-b py-4">
          <CardTitle class="text-base">{{ p.name }}</CardTitle>
          <CardAction class="flex flex-wrap justify-end gap-1.5">
            <StatusBadge v-for="b in (p.badges || []).slice(0, 3)" :key="b" tone="primary">{{ b }}</StatusBadge>
            <StatusBadge v-if="soldOut(p)" tone="danger">{{ t('plan.soldOut') }}</StatusBadge>
            <StatusBadge v-else-if="soonSoldOut(p)" tone="warning">{{ t('plan.soonSoldOut') }}</StatusBadge>
          </CardAction>
        </CardHeader>
        <div class="bg-muted/50 px-6 py-5">
          <template v-if="unitPrice(p)">
            <div class="text-3xl font-semibold tracking-tight tabular-nums">{{ money(unitPrice(p)!.price) }}</div>
            <div class="mt-1 text-sm text-muted-foreground">{{ t(`period.${unitPrice(p)!.period}`) }}</div>
          </template>
        </div>
        <CardContent class="flex-1 py-5">
          <PlanContent v-if="p.content" :content="p.content" />
        </CardContent>
        <CardFooter class="pb-5">
          <Button class="w-full" :variant="soldOut(p) ? 'secondary' : 'default'" :disabled="soldOut(p)" @click.stop="choose(p)">
            {{ soldOut(p) ? t('plan.soldOut') : t('plan.buy') }}
          </Button>
        </CardFooter>
      </Card>
    </div>
    <p v-if="!loading && !shown.length" class="py-16 text-center text-muted-foreground">{{ t('plan.empty') }}</p>
  </div>
</template>
