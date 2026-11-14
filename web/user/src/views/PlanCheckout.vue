<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowLeftIcon, TriangleAlertIcon, TicketPercentIcon, CheckIcon, Loader2Icon } from '@lucide/vue'
import { nowUnix } from '@gv/shared'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Separator } from '@gv/shared/ui/separator'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import { cn } from '@gv/shared/lib/utils'
import { http } from '../api'
import { errText, toast } from '../ui'
import { biz, loadBiz, loadMe, loadSubs, me, money, subOptions, subs } from '../store'
import { PERIODS, type Coupon, type Period, type Plan } from '../types'
import PlanContent from '../components/PlanContent.vue'
import SelectBox from '../components/common/SelectBox.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const id = Number(route.params.id)
const plan = ref<Plan | null>(null)
const loading = ref(true)
const missing = ref(false)
const period = ref<Period | null>(null)
const target = ref<number>(0)
const couponCode = ref('')
const coupon = ref<Coupon | null>(null)
const checking = ref(false)
const submitting = ref(false)

const targetSub = computed(() => subs.value.find((s) => s.id === target.value) || null)
const sameAndActive = computed(() => {
  const s = targetSub.value
  return !!s && s.plan?.id === id && !s.banned && (s.expired_at === null || s.expired_at > nowUnix())
})

const periods = computed(() => {
  const p = plan.value
  if (!p) return []
  return PERIODS.filter((k) => p[k] !== null && p[k] !== undefined && (k !== 'reset_price' || sameAndActive.value)).map((k) => ({
    label: t(`period.${k}`),
    value: k,
    price: p[k] as number,
  }))
})
const price = computed(() => periods.value.find((p) => p.value === period.value)?.price ?? 0)

const couponOff = computed(() => {
  const c = coupon.value
  if (!c) return 0
  return c.type === 1 ? Math.min(c.value, price.value) : Math.round((price.value * c.value) / 100)
})
const userOff = computed(() => {
  const d = me.value?.discount
  return d ? Math.round(((price.value - couponOff.value) * d) / 100) : 0
})
const payable = computed(() => Math.max(0, price.value - couponOff.value - userOff.value))

const changing = computed(() => {
  const s = targetSub.value
  return !!s && !!s.plan && s.plan.id !== id && (s.expired_at === null || s.expired_at > nowUnix())
})

const targets = computed(() => [{ label: t('plan.targetNew'), value: 0 }, ...subOptions()])

watch(periods, (list) => {
  if (!list.some((p) => p.value === period.value)) period.value = list[0]?.value ?? null
})
watch([period, target], () => (coupon.value = null))

async function applyCoupon() {
  if (!couponCode.value.trim() || !period.value) return
  checking.value = true
  try {
    coupon.value = await http.post<Coupon>('/user/coupons/check', { code: couponCode.value.trim(), plan_id: id, period: period.value })
    toast('success', t('plan.couponOk', { name: coupon.value.name }))
  } catch (e) {
    coupon.value = null
    toast('error', errText(e))
  } finally {
    checking.value = false
  }
}

async function submit() {
  if (!period.value) return
  submitting.value = true
  try {
    const tradeNo = await http.post<string>('/user/orders', {
      plan_id: id,
      period: period.value,
      coupon_code: coupon.value?.code || undefined,
      subscription_id: target.value || 0,
    })
    router.replace(`/order/${tradeNo}`)
  } catch (e) {
    toast('error', errText(e))
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  try {
    const [p] = await Promise.all([http.get<Plan>(`/user/plans/${id}`), loadSubs(), loadMe(), loadBiz()])
    plan.value = p
    const q = route.query.sub
    if (q !== undefined) target.value = Number(q) || 0
    else {
      // 默认作用于尚未购买套餐的主订阅，避免首次购买时多出一个订阅
      const primary = subs.value.find((s) => s.is_primary)
      target.value = primary && !primary.plan ? primary.id : 0
    }
    const want = route.query.period as Period | undefined
    if (want && periods.value.some((x) => x.value === want)) period.value = want
  } catch (e) {
    missing.value = true
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div>
    <div v-if="loading" class="grid gap-6 lg:grid-cols-3">
      <Skeleton class="h-96 rounded-xl lg:col-span-2" />
      <Skeleton class="h-72 rounded-xl" />
    </div>
    <div v-else-if="missing" class="flex flex-col items-center gap-4 py-20 text-center">
      <p class="text-lg font-medium">{{ t('plan.notFound') }}</p>
      <Button variant="outline" @click="router.push('/plans')"><ArrowLeftIcon />{{ t('common.back') }}</Button>
    </div>
    <template v-else-if="plan">
      <div class="mb-5 flex items-center gap-2">
        <Button variant="ghost" size="sm" @click="router.back()"><ArrowLeftIcon class="rtl:rotate-180" />{{ t('common.back') }}</Button>
        <h1 class="text-xl font-semibold tracking-tight">{{ t('plan.configure') }}</h1>
      </div>
      <div class="grid items-start gap-6 lg:grid-cols-3">
        <div class="space-y-6 lg:col-span-2">
          <Card>
            <CardHeader>
              <CardTitle>{{ plan.name }}</CardTitle>
            </CardHeader>
            <CardContent v-if="plan.content">
              <PlanContent :content="plan.content" />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('plan.target') }}</CardTitle>
              <CardDescription>{{ t('plan.targetHint') }}</CardDescription>
            </CardHeader>
            <CardContent class="space-y-3">
              <SelectBox :model-value="target" :options="targets" @update:model-value="(v) => (target = Number(v))" />
              <Alert v-if="changing && biz && !biz.plan_change" variant="destructive">
                <TriangleAlertIcon />
                <AlertDescription>{{ t('plan.changeDisabled') }}</AlertDescription>
              </Alert>
              <Alert v-else-if="changing" class="border-warning/50 bg-warning/10">
                <TriangleAlertIcon class="text-warning" />
                <AlertDescription class="text-foreground">{{ t('plan.changeWarn') }}</AlertDescription>
              </Alert>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('plan.period') }}</CardTitle>
              <CardDescription v-if="plan.reset_price !== null && !sameAndActive">{{ t('plan.resetNeedSub') }}</CardDescription>
            </CardHeader>
            <CardContent>
              <div v-if="periods.length" class="grid grid-cols-2 gap-3 sm:grid-cols-3">
                <button
                  v-for="p in periods"
                  :key="p.value"
                  type="button"
                  :class="
                    cn(
                      'relative rounded-lg border p-3 text-left transition-colors hover:border-primary/60',
                      period === p.value ? 'border-primary bg-primary/5 ring-1 ring-primary' : '',
                    )
                  "
                  @click="period = p.value"
                >
                  <div class="text-sm text-muted-foreground">{{ p.label }}</div>
                  <div class="mt-1 text-lg font-semibold tabular-nums">{{ money(p.price) }}</div>
                  <CheckIcon v-if="period === p.value" class="absolute top-2.5 right-2.5 size-4 text-primary" />
                </button>
              </div>
              <p v-else class="text-sm text-muted-foreground">{{ t('plan.noPeriod') }}</p>
            </CardContent>
          </Card>
        </div>

        <Card class="lg:sticky lg:top-20">
          <CardHeader>
            <CardTitle class="text-base">{{ t('plan.summary') }}</CardTitle>
          </CardHeader>
          <CardContent class="space-y-4">
            <div class="flex gap-2">
              <div class="relative flex-1">
                <TicketPercentIcon class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input v-model="couponCode" class="pl-8" :placeholder="t('plan.couponPlaceholder')" @keyup.enter="applyCoupon" />
              </div>
              <Button variant="outline" :disabled="!couponCode.trim() || !period || checking" @click="applyCoupon">
                <Loader2Icon v-if="checking" class="animate-spin" />{{ t('plan.apply') }}
              </Button>
            </div>
            <Separator />
            <div class="space-y-2 text-sm">
              <div class="flex justify-between gap-2">
                <span class="text-muted-foreground">{{ plan.name }} × {{ periods.find((p) => p.value === period)?.label }}</span>
                <span class="tabular-nums">{{ money(price) }}</span>
              </div>
              <div v-if="couponOff" class="flex justify-between gap-2">
                <span class="text-muted-foreground">{{ t('plan.couponDiscount') }}</span>
                <span class="text-success tabular-nums">-{{ money(couponOff) }}</span>
              </div>
              <div v-if="userOff" class="flex justify-between gap-2">
                <span class="text-muted-foreground">{{ t('profile.discount', { n: me?.discount }) }}</span>
                <span class="text-success tabular-nums">-{{ money(userOff) }}</span>
              </div>
            </div>
            <Separator />
            <div class="flex items-baseline justify-between gap-2">
              <span class="text-sm">{{ t('plan.payable') }}</span>
              <span class="text-2xl font-semibold tabular-nums">{{ money(payable) }}</span>
            </div>
            <p class="text-xs text-muted-foreground">{{ t('plan.payableHint') }}</p>
            <Button class="w-full" size="lg" :disabled="!period || submitting" @click="submit">
              <Loader2Icon v-if="submitting" class="animate-spin" />{{ t('plan.submit') }}
            </Button>
          </CardContent>
        </Card>
      </div>
    </template>
  </div>
</template>
