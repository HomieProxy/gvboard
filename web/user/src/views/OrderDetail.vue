<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowLeftIcon, CheckIcon, Loader2Icon, ClockIcon, CircleCheckIcon, CircleXIcon, BookOpenIcon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Card, CardContent, CardHeader, CardTitle } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Separator } from '@gv/shared/ui/separator'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { cn } from '@gv/shared/lib/utils'
import { http } from '../api'
import { confirm, errText, toast } from '../ui'
import { loadMe, money, subName, subs, loadSubs } from '../store'
import type { CheckoutResult, OrderDetail, PaymentMethod } from '../types'
import QrCode from '../components/QrCode.vue'
import StatusBadge, { type Tone } from '../components/common/StatusBadge.vue'

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

const statusTone: Record<number, Tone> = { 0: 'warning', 1: 'info', 2: 'default', 3: 'success', 4: 'default' }
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
const infos = computed(() => {
  const o = order.value
  if (!o) return []
  const out: { label: string; value: string; mono?: boolean }[] = [
    { label: t('order.tradeNo'), value: o.trade_no, mono: true },
    { label: t('order.plan'), value: productName.value },
    { label: t('order.type'), value: t(`order.types.${o.type}`) },
    { label: t('order.period'), value: t(`period.${o.period}`) },
  ]
  if (o.plan_id !== 0) out.push({ label: t('order.subscription'), value: targetName.value })
  out.push({ label: t('order.createdAt'), value: formatTime(o.created_at) })
  if (o.paid_at) out.push({ label: t('order.paidAt'), value: formatTime(o.paid_at) })
  if (o.plan_id === 0 && o.bonus) {
    out.push({ label: t('order.bonus'), value: money(o.bonus) })
    out.push({ label: t('order.getAmount'), value: money(o.get_amount) })
  }
  return out
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
  confirm(t('order.cancelConfirm'), () => http.post(`/user/orders/${tradeNo}/cancel`), t('order.cancelled'), true).then((ok) => {
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
  <div>
    <div v-if="loading" class="grid gap-6 lg:grid-cols-3">
      <Skeleton class="h-80 rounded-xl lg:col-span-2" />
      <Skeleton class="h-64 rounded-xl" />
    </div>
    <div v-else-if="missing" class="flex flex-col items-center gap-4 py-20 text-center">
      <p class="text-lg font-medium">{{ t('order.notFound') }}</p>
      <Button variant="outline" @click="router.push('/orders')"><ArrowLeftIcon />{{ t('common.back') }}</Button>
    </div>
    <template v-else-if="order">
      <div class="mb-5 flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="sm" @click="router.push('/orders')"><ArrowLeftIcon class="rtl:rotate-180" />{{ t('nav.orders') }}</Button>
        <h1 class="text-xl font-semibold tracking-tight">{{ t('order.title') }}</h1>
        <StatusBadge :tone="statusTone[order.status]" dot>{{ t(`order.statuses.${order.status}`) }}</StatusBadge>
      </div>

      <Card v-if="order.status !== 0" class="mb-6">
        <CardContent class="flex flex-col items-center gap-3 py-4 text-center">
          <Loader2Icon v-if="order.status === 1" class="size-12 animate-spin text-primary" />
          <CircleCheckIcon v-else-if="order.status === 3" class="size-12 text-success" />
          <CircleXIcon v-else class="size-12 text-muted-foreground" />
          <div class="text-lg font-semibold">{{ t(`order.statuses.${order.status}`) }}</div>
          <p class="text-sm text-muted-foreground">{{ t(`order.tips.${order.status}`) }}</p>
          <Button v-if="order.status === 3 && order.plan_id !== 0" variant="outline" @click="router.push('/knowledge')">
            <BookOpenIcon />{{ t('order.tutorial') }}
          </Button>
        </CardContent>
      </Card>

      <div class="grid items-start gap-6 lg:grid-cols-3">
        <div class="space-y-6 lg:col-span-2">
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('order.info') }}</CardTitle>
            </CardHeader>
            <CardContent>
              <dl class="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2">
                <div v-for="i in infos" :key="i.label">
                  <dt class="text-muted-foreground">{{ i.label }}</dt>
                  <dd :class="['mt-0.5 font-medium break-all', i.mono ? 'font-mono text-xs' : '']">{{ i.value }}</dd>
                </div>
              </dl>
              <template v-if="order.surplus_orders?.length">
                <Separator class="my-4" />
                <div class="mb-2 text-sm text-muted-foreground">{{ t('order.surplusOrders') }}</div>
                <div v-for="s in order.surplus_orders" :key="s.trade_no" class="font-mono text-xs text-muted-foreground">
                  {{ s.trade_no }} · {{ money(s.total_amount) }}
                </div>
              </template>
            </CardContent>
          </Card>

          <Card v-if="order.status === 0 && order.total_amount > 0">
            <CardHeader>
              <CardTitle class="text-base">{{ t('order.paymentMethod') }}</CardTitle>
            </CardHeader>
            <CardContent class="space-y-4">
              <div v-if="methods.length" class="grid gap-3 sm:grid-cols-2">
                <button
                  v-for="m in methods"
                  :key="m.id"
                  type="button"
                  :class="
                    cn(
                      'relative flex items-center gap-3 rounded-lg border p-3 text-left transition-colors hover:border-primary/60',
                      method === m.id ? 'border-primary bg-primary/5 ring-1 ring-primary' : '',
                    )
                  "
                  @click="method = m.id"
                >
                  <img v-if="m.icon" :src="m.icon" alt="" class="h-6 max-w-12 object-contain" />
                  <div class="min-w-0 flex-1">
                    <div class="truncate text-sm font-medium">{{ m.name }}</div>
                    <div v-if="fee(m)" class="text-xs text-muted-foreground">{{ t('order.handlingFee') }} {{ money(fee(m)) }}</div>
                  </div>
                  <CheckIcon v-if="method === m.id" class="size-4 text-primary" />
                </button>
              </div>
              <p v-else class="text-sm text-muted-foreground">{{ t('order.noMethod') }}</p>
              <div v-if="isStripeCard" class="space-y-2">
                <div ref="stripeBox" class="rounded-md border px-3 py-2.5" />
                <p class="text-xs text-muted-foreground">{{ t('order.stripeHint') }}</p>
              </div>
            </CardContent>
          </Card>
        </div>

        <Card class="lg:sticky lg:top-20">
          <CardHeader>
            <CardTitle class="text-base">{{ t('plan.summary') }}</CardTitle>
          </CardHeader>
          <CardContent class="space-y-4">
            <div class="space-y-2 text-sm">
              <div class="flex justify-between gap-2">
                <span class="text-muted-foreground">{{ t('plan.price') }}</span>
                <span class="tabular-nums">{{ money(order.total_amount + (order.discount_amount || 0) + (order.surplus_amount || 0) + (order.balance_amount || 0)) }}</span>
              </div>
              <div v-if="order.discount_amount" class="flex justify-between gap-2">
                <span class="text-muted-foreground">{{ t('order.discount') }}</span>
                <span class="tabular-nums text-success">-{{ money(order.discount_amount) }}</span>
              </div>
              <div v-if="order.surplus_amount && order.type === 3" class="flex justify-between gap-2">
                <span class="text-muted-foreground">{{ t('order.surplus') }}</span>
                <span class="tabular-nums text-success">-{{ money(order.surplus_amount) }}</span>
              </div>
              <div v-if="order.balance_amount" class="flex justify-between gap-2">
                <span class="text-muted-foreground">{{ t('order.balance') }}</span>
                <span class="tabular-nums text-success">-{{ money(order.balance_amount) }}</span>
              </div>
              <div v-if="order.refund_amount" class="flex justify-between gap-2 text-muted-foreground">
                <span>{{ t('order.refund') }}</span>
                <span class="tabular-nums">{{ money(order.refund_amount) }}</span>
              </div>
              <div v-if="order.status === 0 ? fee(current) : order.handling_amount" class="flex justify-between gap-2">
                <span class="text-muted-foreground">{{ t('order.handlingFee') }}</span>
                <span class="tabular-nums">{{ money(order.status === 0 ? fee(current) : order.handling_amount) }}</span>
              </div>
            </div>
            <Separator />
            <div class="flex items-baseline justify-between gap-2">
              <span class="text-sm">{{ t('order.total') }}</span>
              <span class="text-2xl font-semibold tabular-nums">
                {{ money(order.total_amount + (order.status === 0 ? fee(current) : order.handling_amount || 0)) }}
              </span>
            </div>
            <template v-if="order.status === 0">
              <p v-if="order.total_amount === 0" class="text-xs text-muted-foreground">{{ t('order.free') }}</p>
              <div class="space-y-2">
                <Button class="w-full" size="lg" :disabled="paying || (order.total_amount > 0 && !method)" @click="pay">
                  <Loader2Icon v-if="paying" class="animate-spin" />
                  {{ order.total_amount === 0 ? t('order.confirmFree') : t('order.checkout') }}
                </Button>
                <Button class="w-full" variant="outline" @click="cancel">{{ t('order.cancel') }}</Button>
              </div>
            </template>
          </CardContent>
        </Card>
      </div>
    </template>

    <Dialog v-model:open="qrShow">
      <DialogContent class="sm:max-w-xs">
        <DialogHeader>
          <DialogTitle>{{ current?.name || t('order.pay') }}</DialogTitle>
          <DialogDescription>{{ t('order.scan') }}</DialogDescription>
        </DialogHeader>
        <div class="flex justify-center"><QrCode :text="qr" :size="240" /></div>
        <div class="text-center text-xl font-semibold tabular-nums">{{ order ? money(order.total_amount + fee(current)) : '' }}</div>
        <p class="flex items-center justify-center gap-1.5 text-sm text-muted-foreground"><ClockIcon class="size-4" />{{ t('order.waiting') }}</p>
      </DialogContent>
    </Dialog>
  </div>
</template>
