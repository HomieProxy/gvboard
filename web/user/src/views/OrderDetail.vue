<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  NCard, NGrid, NGi, NButton, NSpin, NResult, NDescriptions, NDescriptionsItem, NRadioGroup, NRadio, NSpace, NModal, NTag, NDivider,
} from 'naive-ui'
import { formatTime } from '@gv/shared'
import { http } from '../api'
import { confirm, errText, toast } from '../ui'
import { loadMe, money, subName, subs, loadSubs } from '../store'
import type { CheckoutResult, OrderDetail, PaymentMethod } from '../types'
import QrCode from '../components/QrCode.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const tradeNo = String(route.params.trade_no)
const order = ref<OrderDetail | null>(null)
const methods = ref<PaymentMethod[]>([])
const method = ref<number | null>(null)
const loading = ref(true)
const missing = ref(false)
const paying = ref(false)
const qr = ref('')
const qrShow = ref(false)
let timer = 0

const current = computed(() => methods.value.find((m) => m.id === method.value) || null)
const fee = (m: PaymentMethod | null) =>
  m && order.value ? Math.round((order.value.total_amount * (m.handling_fee_percent || 0)) / 100 + (m.handling_fee_fixed || 0)) : 0
const productName = computed(() => (order.value?.plan_id === 0 ? t('order.deposit') : order.value?.plan?.name || ''))
const targetName = computed(() => {
  const id = order.value?.subscription_id
  if (!id) return t('order.newSubscription')
  const s = subs.value.find((x) => x.id === id)
  return s ? subName(s) : `#${id}`
})

async function load() {
  try {
    order.value = await http.get<OrderDetail>(`/user/orders/${tradeNo}`)
  } catch (e) {
    missing.value = true
    toast('error', errText(e))
  }
}

function poll() {
  clearInterval(timer)
  timer = window.setInterval(async () => {
    try {
      const s = await http.get<number>(`/user/orders/${tradeNo}/status`)
      if (s !== 0) {
        clearInterval(timer)
        qrShow.value = false
        toast('success', s === 1 ? t('order.processing') : t('order.paid'))
        await load()
        loadMe().catch(() => {})
        if (s === 1) poll()
      }
    } catch {
      /* 忽略单次失败 */
    }
  }, 3000)
}

// ---------- Stripe 信用卡 ----------
interface StripeLike {
  elements: () => { create: (type: string) => { mount: (el: HTMLElement) => void } }
  createToken: (card: unknown) => Promise<{ token?: { id: string }; error?: { message: string } }>
}
declare global {
  interface Window {
    Stripe?: (key: string) => StripeLike
  }
}
const stripeBox = ref<HTMLElement | null>(null)
let stripe: StripeLike | null = null
let card: unknown = null
const isStripeCard = computed(() => current.value?.payment === 'StripeCredit')

watch(method, () => {
  stripe = null
  card = null
  if (isStripeCard.value) setupStripe().catch((e) => toast('error', errText(e)))
})

async function setupStripe() {
  if (!current.value || stripe) return
  const key = await http.get<string>(`/user/payment-methods/${current.value.id}/stripe-key`)
  if (!window.Stripe) {
    await new Promise<void>((resolve, reject) => {
      const s = document.createElement('script')
      s.src = 'https://js.stripe.com/v3/'
      s.onload = () => resolve()
      s.onerror = () => reject(new Error(t('common.networkError')))
      document.head.appendChild(s)
    })
  }
  stripe = window.Stripe!(key)
  const el = stripe.elements().create('card')
  await nextTick()
  if (stripeBox.value) el.mount(stripeBox.value)
  card = el
}

async function pay() {
  if (!order.value) return
  const free = order.value.total_amount === 0
  if (!free && !method.value) return
  paying.value = true
  try {
    const body: Record<string, unknown> = {}
    if (!free) body.method = method.value
    if (!free && isStripeCard.value) {
      await setupStripe()
      const r = await stripe!.createToken(card)
      if (!r.token) throw new Error(r.error?.message || t('common.networkError'))
      body.token = r.token.id
    }
    const r = await http.post<CheckoutResult>(`/user/orders/${tradeNo}/checkout`, body)
    switch (r.type) {
      case -1:
        toast('success', t('order.paid'))
        await load()
        loadMe().catch(() => {})
        break
      case 0:
        qr.value = typeof r.data === 'string' ? r.data : ''
        qrShow.value = true
        poll()
        break
      case 1:
        toast('info', t('order.redirecting'))
        window.location.href = String(r.data)
        break
      case 2:
        poll()
        break
    }
  } catch (e) {
    toast('error', errText(e))
  } finally {
    paying.value = false
  }
}

function cancel() {
  confirm(t('order.cancelConfirm'), () => http.post(`/user/orders/${tradeNo}/cancel`), t('order.cancelled')).then((ok) => {
    if (ok) load()
  })
}

onMounted(async () => {
  await load()
  loadSubs().catch(() => {})
  if (order.value && order.value.status === 0) {
    try {
      methods.value = (await http.get<PaymentMethod[]>('/user/payment-methods')) || []
      method.value = methods.value[0]?.id ?? null
    } catch (e) {
      toast('error', errText(e))
    }
    // 从支付网关返回时订单可能已支付，短暂轮询状态
    poll()
  } else if (order.value?.status === 1) {
    poll()
  }
  loading.value = false
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="page">
    <NSpin :show="loading">
      <NResult v-if="missing" status="404" :title="t('order.title')">
        <template #footer><NButton @click="router.push('/orders')">{{ t('common.back') }}</NButton></template>
      </NResult>
      <template v-else-if="order">
        <div class="row" style="margin-bottom: 16px">
          <NButton quaternary size="small" @click="router.push('/orders')">← {{ t('nav.orders') }}</NButton>
          <h2 class="page-title" style="margin: 0">{{ t('order.title') }}</h2>
          <NTag :type="order.status === 0 ? 'warning' : order.status === 3 ? 'success' : order.status === 1 ? 'info' : 'default'" :bordered="false">
            {{ t(`order.statuses.${order.status}`) }}
          </NTag>
        </div>
        <NGrid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="16">
          <NGi span="1 l:2">
            <NCard size="small">
              <NDescriptions :column="1" label-placement="left" size="small">
                <NDescriptionsItem :label="t('order.tradeNo')"><span class="mono">{{ order.trade_no }}</span></NDescriptionsItem>
                <NDescriptionsItem :label="t('order.plan')">{{ productName }}</NDescriptionsItem>
                <NDescriptionsItem :label="t('order.type')">{{ t(`order.types.${order.type}`) }}</NDescriptionsItem>
                <NDescriptionsItem :label="t('order.period')">{{ t(`period.${order.period}`) }}</NDescriptionsItem>
                <NDescriptionsItem v-if="order.plan_id !== 0" :label="t('order.subscription')">{{ targetName }}</NDescriptionsItem>
                <NDescriptionsItem :label="t('order.createdAt')">{{ formatTime(order.created_at) }}</NDescriptionsItem>
                <NDescriptionsItem v-if="order.paid_at" :label="t('order.paidAt')">{{ formatTime(order.paid_at) }}</NDescriptionsItem>
                <template v-if="order.plan_id === 0 && order.bonus">
                  <NDescriptionsItem :label="t('order.bonus')">{{ money(order.bonus) }}</NDescriptionsItem>
                  <NDescriptionsItem :label="t('order.getAmount')">{{ money(order.get_amount) }}</NDescriptionsItem>
                </template>
              </NDescriptions>
              <template v-if="order.surplus_orders?.length">
                <NDivider title-placement="left">{{ t('order.surplusOrders') }}</NDivider>
                <div v-for="s in order.surplus_orders" :key="s.trade_no" class="muted mono">{{ s.trade_no }} · {{ money(s.total_amount) }}</div>
              </template>
            </NCard>
            <NCard v-if="order.status === 0 && order.total_amount > 0" size="small" :title="t('order.paymentMethod')" style="margin-top: 16px">
              <NRadioGroup v-if="methods.length" v-model:value="method">
                <NSpace vertical>
                  <NRadio v-for="m in methods" :key="m.id" :value="m.id">
                    <span class="method">
                      <img v-if="m.icon" :src="m.icon" alt="" />
                      {{ m.name }}
                      <span v-if="fee(m)" class="muted">（{{ t('order.handlingFee') }} {{ money(fee(m)) }}）</span>
                    </span>
                  </NRadio>
                </NSpace>
              </NRadioGroup>
              <span v-else class="muted">{{ t('order.noMethod') }}</span>
              <div v-if="isStripeCard" style="margin-top: 16px">
                <div ref="stripeBox" class="stripe" />
                <p class="muted">{{ t('order.stripeHint') }}</p>
              </div>
            </NCard>
          </NGi>
          <NGi>
            <NCard size="small">
              <div class="line"><span>{{ t('plan.price') }}</span><span>{{ money(order.total_amount + (order.discount_amount || 0) + (order.surplus_amount || 0) + (order.balance_amount || 0)) }}</span></div>
              <div v-if="order.discount_amount" class="line"><span>{{ t('order.discount') }}</span><span>-{{ money(order.discount_amount) }}</span></div>
              <div v-if="order.surplus_amount && order.type === 3" class="line"><span>{{ t('order.surplus') }}</span><span>-{{ money(order.surplus_amount) }}</span></div>
              <div v-if="order.balance_amount" class="line"><span>{{ t('order.balance') }}</span><span>-{{ money(order.balance_amount) }}</span></div>
              <div v-if="order.refund_amount" class="line muted"><span>{{ t('order.refund') }}</span><span>{{ money(order.refund_amount) }}</span></div>
              <div v-if="order.status === 0 ? fee(current) : order.handling_amount" class="line">
                <span>{{ t('order.handlingFee') }}</span>
                <span>{{ money(order.status === 0 ? fee(current) : order.handling_amount) }}</span>
              </div>
              <NDivider />
              <div class="line total">
                <span>{{ t('order.total') }}</span>
                <strong>{{ money(order.total_amount + (order.status === 0 ? fee(current) : order.handling_amount || 0)) }}</strong>
              </div>
              <template v-if="order.status === 0">
                <p v-if="order.total_amount === 0" class="muted">{{ t('order.free') }}</p>
                <NSpace vertical style="margin-top: 12px">
                  <NButton type="primary" block size="large" :loading="paying" :disabled="order.total_amount > 0 && !method" @click="pay">
                    {{ order.total_amount === 0 ? t('order.confirmFree') : t('order.checkout') }}
                  </NButton>
                  <NButton block @click="cancel">{{ t('order.cancel') }}</NButton>
                </NSpace>
              </template>
              <p v-else-if="order.status === 1" class="muted">{{ t('order.processing') }}</p>
            </NCard>
          </NGi>
        </NGrid>
      </template>
    </NSpin>
    <NModal v-model:show="qrShow" preset="card" :title="current?.name || t('order.pay')" style="width: 320px">
      <div style="display: flex; justify-content: center"><QrCode :text="qr" :size="240" /></div>
      <p class="muted" style="text-align: center">{{ t('order.scan') }}</p>
      <p style="text-align: center"><strong>{{ order ? money(order.total_amount + fee(current)) : '' }}</strong></p>
      <p class="muted" style="text-align: center">{{ t('order.waiting') }}</p>
    </NModal>
  </div>
</template>

<style scoped>
.line { display: flex; justify-content: space-between; margin: 6px 0; }
.total strong { font-size: 22px; }
.method { display: inline-flex; align-items: center; gap: 6px; }
.method img { height: 18px; }
.stripe { padding: 10px; border: 1px solid rgba(127, 127, 127, .3); border-radius: 6px; }
</style>
