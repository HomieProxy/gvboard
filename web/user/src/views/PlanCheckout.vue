<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  NGrid, NGi, NCard, NButton, NRadioGroup, NRadioButton, NSelect, NInput, NInputGroup, NSpin, NResult, NAlert, NDivider, NSpace, NTag,
} from 'naive-ui'
import { nowUnix } from '@gv/shared'
import { http } from '../api'
import { errText, toast } from '../ui'
import { biz, loadBiz, loadMe, loadSubs, me, money, subOptions, subs } from '../store'
import { PERIODS, type Coupon, type Period, type Plan } from '../types'
import Markdown from '../components/Markdown.vue'

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
  try {
    coupon.value = await http.post<Coupon>('/user/coupons/check', { code: couponCode.value.trim(), plan_id: id, period: period.value })
    toast('success', t('plan.couponOk', { name: coupon.value.name }))
  } catch (e) {
    coupon.value = null
    toast('error', errText(e))
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
  <div class="page">
    <NSpin :show="loading">
      <NResult v-if="missing" status="404" :title="t('plan.notFound')">
        <template #footer><NButton @click="router.push('/plans')">{{ t('common.back') }}</NButton></template>
      </NResult>
      <template v-else-if="plan">
        <div class="row" style="margin-bottom: 16px">
          <NButton quaternary size="small" @click="router.back()">← {{ t('common.back') }}</NButton>
          <h2 class="page-title" style="margin: 0">{{ plan.name }}</h2>
        </div>
        <NGrid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="16">
          <NGi span="1 l:2">
            <NSpace vertical :size="16">
              <NCard size="small" :title="t('plan.target')">
                <NSelect v-model:value="target" :options="targets" />
                <p class="muted">{{ t('plan.targetHint') }}</p>
                <NAlert v-if="changing && biz && !biz.plan_change" type="warning">{{ t('plan.changeDisabled') }}</NAlert>
              </NCard>
              <NCard size="small" :title="t('plan.period')">
                <NRadioGroup v-if="periods.length" v-model:value="period">
                  <NSpace>
                    <NRadioButton v-for="p in periods" :key="p.value" :value="p.value">{{ p.label }} · {{ money(p.price) }}</NRadioButton>
                  </NSpace>
                </NRadioGroup>
                <span v-else class="muted">{{ t('plan.noPeriod') }}</span>
                <p v-if="plan.reset_price !== null && !sameAndActive" class="muted">{{ t('plan.resetNeedSub') }}</p>
              </NCard>
              <NCard size="small">
                <NSpace :size="6" style="margin-bottom: 8px">
                  <NTag size="small" :bordered="false">{{ t('plan.traffic', { n: plan.transfer_enable }) }}</NTag>
                  <NTag v-if="plan.device_limit" size="small" :bordered="false">{{ t('plan.devices', { n: plan.device_limit }) }}</NTag>
                  <NTag v-if="plan.speed_limit" size="small" :bordered="false">{{ t('plan.speed', { n: plan.speed_limit }) }}</NTag>
                </NSpace>
                <Markdown :source="plan.content" />
              </NCard>
            </NSpace>
          </NGi>
          <NGi>
            <NCard size="small" class="summary">
              <NInputGroup>
                <NInput v-model:value="couponCode" :placeholder="t('plan.couponPlaceholder')" @keyup.enter="applyCoupon" />
                <NButton :disabled="!couponCode.trim() || !period" @click="applyCoupon">{{ t('plan.apply') }}</NButton>
              </NInputGroup>
              <NDivider />
              <div class="line"><span>{{ t('plan.price') }}</span><span>{{ money(price) }}</span></div>
              <div v-if="couponOff" class="line"><span>{{ t('plan.couponDiscount') }}</span><span>-{{ money(couponOff) }}</span></div>
              <div v-if="userOff" class="line"><span>{{ t('profile.discount', { n: me?.discount }) }}</span><span>-{{ money(userOff) }}</span></div>
              <NDivider />
              <div class="line total"><span>{{ t('plan.payable') }}</span><strong>{{ money(payable) }}</strong></div>
              <p class="muted">{{ t('plan.payableHint') }}</p>
              <NButton type="primary" block size="large" :disabled="!period" :loading="submitting" @click="submit">{{ t('plan.submit') }}</NButton>
            </NCard>
          </NGi>
        </NGrid>
      </template>
    </NSpin>
  </div>
</template>

<style scoped>
.line { display: flex; justify-content: space-between; margin: 6px 0; }
.total strong { font-size: 22px; }
.summary { position: sticky; top: 72px; }
</style>
