<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { GiftIcon, InfoIcon } from '@lucide/vue'
import { formatBytes, formatTime } from '@gv/shared'
import { Card, CardContent, CardHeader, CardTitle } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { http } from '../api'
import { confirm, errText, toast } from '../ui'
import { loadMe, loadSubs, money, subAvailable, subName, subOptions, subs } from '../store'
import type { WheelInfo, WheelPrize, WheelRecord, WheelResult } from '../types'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable from '../components/common/DataTable.vue'
import SelectBox from '../components/common/SelectBox.vue'
import SimplePager from '../components/common/SimplePager.vue'

const { t } = useI18n()
const base = '/plugin/lucky_wheel'
const MB = 1048576
const SPIN_MS = 4500

const info = ref<WheelInfo | null>(null)
const loading = ref(true)
const unavailable = ref(false)
const subID = ref<number | null>(null)
const spinning = ref(false)
const angle = ref(0)
const result = ref<WheelPrize | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined

const records = ref<WheelRecord[]>([])
const recordsLoading = ref(true)
const total = ref(0)
const page = ref(1)
const pageSize = 10

const usable = computed(() => subs.value.filter(subAvailable))
const choices = computed(() => subOptions(usable.value))
const prizes = computed(() => info.value?.prizes ?? [])
const seg = computed(() => 360 / Math.max(prizes.value.length, 1))
const paidLeft = computed(() => info.value?.paid_left !== 0)

/** 奖品内容；records 中流量为字节，转盘信息中为 MB */
function describe(type: string, value: number, bytes = false) {
  switch (type) {
    case 'balance':
      return t('wheel.prizeBalance', { v: money(value) })
    case 'days':
      return t('wheel.prizeDays', { n: value })
    case 'traffic':
      return t('wheel.prizeTraffic', { v: formatBytes(bytes ? value : value * MB) })
  }
  return t('wheel.none')
}

function costText(r: WheelRecord) {
  if (r.cost_type === 'balance') return money(r.cost)
  if (r.cost_type === 'traffic') return formatBytes(r.cost)
  return t('wheel.costFree')
}

function subLabel(id: number) {
  const s = subs.value.find((x) => x.id === id)
  return s ? subName(s) : `#${id}`
}

// 转盘扇区：角度从正上方顺时针计算，指针固定在正上方
const C = 160
const R = 136
const point = (deg: number, r = R) => {
  const a = (deg * Math.PI) / 180
  return `${(C + r * Math.sin(a)).toFixed(2)} ${(C - r * Math.cos(a)).toFixed(2)}`
}
const palette = ['#ef4444', '#f59e0b', '#10b981', '#3b82f6', '#8b5cf6', '#ec4899', '#14b8a6', '#f97316']
/** 扇区颜色：相邻（含首尾）不同色 */
function colorOf(i: number, n: number) {
  const c = i % palette.length
  return i === n - 1 && n > 1 && c === 0 ? palette[2] : palette[c]
}
const sectors = computed(() =>
  prizes.value.map((p, i) => {
    const n = prizes.value.length
    const a0 = i * seg.value
    const mid = a0 + seg.value / 2
    // 文字沿半径方向，右半边从外向内读、左半边翻转，始终保持正向
    const right = mid < 180
    return {
      path: `M ${C} ${C} L ${point(a0)} A ${R} ${R} 0 ${seg.value > 180 ? 1 : 0} 1 ${point(a0 + seg.value)} Z`,
      color: colorOf(i, n),
      line: `M ${C} ${C} L ${point(a0)}`,
      rotate: right ? mid - 90 : mid + 90,
      x: right ? C + R - 14 : C - R + 14,
      anchor: right ? 'end' : 'start',
      name: [...p.name].slice(0, 6).join(''),
      sub: p.type === 'none' ? '' : describe(p.type, p.value),
    }
  }),
)
const fontSize = computed(() => (prizes.value.length > 8 ? 11 : 13))
const bulbs = Array.from({ length: 24 }, (_, i) => {
  const a = (i * 15 * Math.PI) / 180
  return { x: C + 148 * Math.sin(a), y: C - 148 * Math.cos(a), odd: i % 2 === 1 }
})
const winIndex = ref<number | null>(null)

/** 中心按钮：有免费次数时免费抽，否则使用第一个可用的付费方式 */
const centerPay = computed<'free' | 'balance' | 'traffic' | null>(() => {
  const i = info.value
  if (!i || !usable.value.length) return null
  if (i.free_left > 0) return 'free'
  if (!paidLeft.value) return null
  if (i.balance_enable) return 'balance'
  if (i.traffic_enable) return 'traffic'
  return null
})
function centerClick() {
  if (spinning.value || !subID.value || !centerPay.value) return
  start(centerPay.value === 'free' ? undefined : centerPay.value)
}

async function loadInfo() {
  try {
    info.value = await http.get<WheelInfo>(`${base}/info`)
    unavailable.value = false
  } catch {
    unavailable.value = true
  } finally {
    loading.value = false
  }
}

async function loadRecords() {
  recordsLoading.value = true
  try {
    const r = await http.get<{ data: WheelRecord[]; total: number }>(`${base}/records`, { current: page.value, page_size: pageSize })
    records.value = r?.data || []
    total.value = r?.total || 0
  } catch (e) {
    toast('error', errText(e))
  } finally {
    recordsLoading.value = false
  }
}

async function draw(pay?: 'balance' | 'traffic') {
  if (spinning.value || !subID.value) return
  const r = await http.post<WheelResult>(`${base}/draw`, { subscription_id: subID.value, pay })
  result.value = null
  winIndex.value = null
  spinning.value = true
  // 把抽中的扇区中心转到正上方的指针处，至少转 6 圈
  const target = 360 - (r.index + 0.5) * seg.value
  const cur = ((angle.value % 360) + 360) % 360
  angle.value += 360 * 6 + ((target - cur + 360) % 360)
  timer = setTimeout(async () => {
    spinning.value = false
    result.value = r.prize
    winIndex.value = r.index
    toast(r.prize.type === 'none' ? 'info' : 'success', r.prize.type === 'none' ? t('wheel.none') : t('wheel.won', { name: r.prize.name }))
    page.value = 1
    await Promise.all([loadInfo(), loadRecords(), loadMe().catch(() => {}), loadSubs().catch(() => {})])
  }, SPIN_MS)
}

function start(pay?: 'balance' | 'traffic') {
  if (!pay) {
    draw().catch((e) => toast('error', errText(e)))
    return
  }
  const cost = pay === 'balance' ? money(info.value!.balance_cost) : formatBytes(info.value!.traffic_cost * MB)
  const key = pay === 'balance' ? 'wheel.confirmBalance' : 'wheel.confirmTraffic'
  confirm(t(key, { cost }), () => draw(pay))
}

const columns = computed(() => [
  { key: 'created_at', title: t('wheel.time') },
  { key: 'prize', title: t('wheel.prize') },
  { key: 'subscription_id', title: t('wheel.subscription') },
  { key: 'cost', title: t('wheel.cost'), align: 'right' as const },
])

onMounted(async () => {
  await Promise.all([loadInfo(), loadRecords(), loadSubs().catch(() => {})])
  const primary = usable.value.find((s) => s.is_primary) || usable.value[0]
  subID.value = primary?.id ?? null
})
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <div>
    <PageHeader :title="t('wheel.title')" />
    <Card v-if="unavailable" class="py-10">
      <CardContent class="text-center text-muted-foreground">{{ t('wheel.unavailable') }}</CardContent>
    </Card>
    <template v-else>
      <div class="grid gap-4 lg:grid-cols-[minmax(0,420px)_1fr]">
        <Card class="overflow-hidden py-6">
          <CardContent class="flex justify-center px-4">
            <Skeleton v-if="loading" class="aspect-square w-full max-w-[400px] rounded-full" />
            <div v-else class="wheel relative w-full max-w-[400px] text-primary" :class="{ spinning }">
              <svg viewBox="0 0 320 320" class="w-full select-none">
                <defs>
                  <radialGradient id="wheel-rim" cx="50%" cy="40%" r="60%">
                    <stop offset="0%" stop-color="currentColor" stop-opacity="0.85" />
                    <stop offset="100%" stop-color="currentColor" />
                  </radialGradient>
                  <radialGradient id="wheel-hub" cx="50%" cy="35%" r="65%">
                    <stop offset="0%" stop-color="#ffffff" />
                    <stop offset="100%" stop-color="#e2e8f0" />
                  </radialGradient>
                  <filter id="wheel-shadow" x="-20%" y="-20%" width="140%" height="140%">
                    <feDropShadow dx="0" dy="2" stdDeviation="2.5" flood-opacity="0.3" />
                  </filter>
                </defs>
                <!-- 外圈与灯珠 -->
                <circle :cx="C" :cy="C" r="158" fill="url(#wheel-rim)" />
                <circle :cx="C" :cy="C" r="141" fill="none" stroke="#ffffff" stroke-opacity="0.35" stroke-width="2" />
                <circle v-for="(b, i) in bulbs" :key="i" :cx="b.x" :cy="b.y" r="4" class="bulb" :class="b.odd ? 'odd' : ''" />
                <!-- 转动部分 -->
                <g :style="{ transform: `rotate(${angle}deg)`, transformOrigin: `${C}px ${C}px`, transition: spinning ? `transform ${SPIN_MS}ms cubic-bezier(0.15, 0.6, 0.1, 1)` : 'none' }">
                  <g v-for="(s, i) in sectors" :key="i" :opacity="winIndex !== null && winIndex !== i ? 0.55 : 1" class="transition-opacity duration-500">
                    <path :d="s.path" :fill="s.color" />
                    <text :transform="`rotate(${s.rotate} ${C} ${C})`" :x="s.x" :y="C" :text-anchor="s.anchor" fill="#ffffff" class="wheel-text">
                      <tspan :font-size="fontSize" font-weight="700" :dy="s.sub ? -3 : 4">{{ s.name }}</tspan>
                      <tspan v-if="s.sub" :x="s.x" dy="14" :font-size="fontSize - 3" font-weight="500" opacity="0.9">{{ s.sub }}</tspan>
                    </text>
                  </g>
                  <path v-for="(s, i) in sectors" :key="`l${i}`" :d="s.line" stroke="#ffffff" stroke-opacity="0.7" stroke-width="1.5" />
                  <circle :cx="C" :cy="C" :r="R" fill="none" stroke="#ffffff" stroke-opacity="0.6" stroke-width="2" />
                </g>
                <!-- 中心指针（固定不动，箭头指向正上方的中奖扇区） -->
                <g filter="url(#wheel-shadow)">
                  <path :d="`M ${C} ${C - 74} L ${C + 15} ${C - 30} L ${C - 15} ${C - 30} Z`" fill="currentColor" stroke="#ffffff" stroke-width="2" stroke-linejoin="round" />
                  <circle :cx="C" :cy="C" r="38" fill="currentColor" stroke="#ffffff" stroke-width="3" />
                  <circle :cx="C" :cy="C" r="30" fill="url(#wheel-hub)" />
                </g>
              </svg>
              <button
                type="button"
                class="absolute top-1/2 left-1/2 flex size-[19%] -translate-x-1/2 -translate-y-1/2 flex-col items-center justify-center rounded-full font-bold text-primary transition-transform enabled:hover:scale-105 enabled:active:scale-95 disabled:cursor-not-allowed disabled:opacity-60"
                :disabled="spinning || !centerPay || !subID"
                :title="centerPay ? t('wheel.start') : t('wheel.noChance')"
                @click="centerClick"
              >
                <GiftIcon v-if="spinning" class="size-6 animate-pulse" />
                <span v-else class="text-sm leading-none sm:text-base">{{ t('wheel.start') }}</span>
              </button>
            </div>
          </CardContent>
        </Card>

        <Card class="py-5">
          <CardContent class="space-y-5 px-5">
            <div v-if="loading" class="space-y-3">
              <Skeleton class="h-9 w-full" />
              <Skeleton class="h-5 w-1/2" />
              <Skeleton class="h-9 w-40" />
            </div>
            <template v-else-if="info">
              <div v-if="!usable.length" class="space-y-3 text-sm text-muted-foreground">
                <p>{{ t('wheel.noSub') }}</p>
                <Button size="sm" @click="$router.push('/plans')">{{ t('dashboard.buyNow') }}</Button>
              </div>
              <template v-else>
                <div class="space-y-1.5">
                  <div class="text-sm font-medium">{{ t('wheel.subscription') }}</div>
                  <SelectBox
                    class="w-full sm:w-72"
                    :model-value="subID"
                    :options="choices"
                    :disabled="spinning"
                    @update:model-value="(v) => (subID = v === null ? null : Number(v))"
                  />
                </div>
                <div class="flex flex-wrap gap-x-6 gap-y-1 text-sm">
                  <span>{{ t('wheel.freeLeft', { n: info.free_left }) }}</span>
                  <span v-if="info.free_left <= 0 && info.paid_left > 0">{{ t('wheel.paidLeft', { n: info.paid_left }) }}</span>
                </div>
                <div class="flex flex-wrap gap-2">
                  <Button v-if="info.free_left > 0" :disabled="spinning || !subID" @click="start()">{{ spinning ? t('wheel.spinning') : t('wheel.drawFree') }}</Button>
                  <template v-else-if="paidLeft && (info.balance_enable || info.traffic_enable)">
                    <Button v-if="info.balance_enable" :disabled="spinning || !subID" @click="start('balance')">
                      {{ spinning ? t('wheel.spinning') : t('wheel.drawBalance', { cost: money(info.balance_cost) }) }}
                    </Button>
                    <Button v-if="info.traffic_enable" :disabled="spinning || !subID" variant="outline" @click="start('traffic')">
                      {{ t('wheel.drawTraffic', { cost: formatBytes(info.traffic_cost * MB) }) }}
                    </Button>
                  </template>
                  <p v-else class="text-sm text-muted-foreground">{{ t('wheel.noChance') }}</p>
                </div>
                <div v-if="result" class="rounded-lg border bg-primary/5 px-4 py-3">
                  <div class="text-sm text-muted-foreground">{{ t('wheel.result') }}</div>
                  <div class="font-semibold">
                    {{ result.type === 'none' ? t('wheel.none') : `${result.name} · ${describe(result.type, result.value)}` }}
                  </div>
                </div>
              </template>
              <div>
                <div class="mb-2 text-sm font-medium">{{ t('wheel.prizes') }}</div>
                <ul class="grid gap-1.5 text-sm sm:grid-cols-2">
                  <li v-for="(p, i) in info.prizes" :key="i" class="flex items-center gap-2">
                    <span class="size-2.5 shrink-0 rounded-full" :style="{ background: colorOf(i, info.prizes.length) }" />
                    <span class="truncate">{{ p.name }}</span>
                    <span v-if="p.type !== 'none'" class="shrink-0 text-muted-foreground">{{ describe(p.type, p.value) }}</span>
                  </li>
                </ul>
              </div>
              <p class="flex items-start gap-1.5 text-xs text-muted-foreground"><InfoIcon class="mt-0.5 size-3.5 shrink-0" />{{ t('wheel.tip') }}</p>
            </template>
          </CardContent>
        </Card>
      </div>

      <Card class="mt-4 gap-3 py-4">
        <CardHeader class="px-4"><CardTitle class="text-base">{{ t('wheel.records') }}</CardTitle></CardHeader>
        <CardContent class="px-4">
          <div class="overflow-x-auto">
            <DataTable :columns="columns" :rows="records" :loading="recordsLoading" :row-key="(r) => r.id" class="min-w-[560px]">
              <template #cell-created_at="{ row }">{{ formatTime(row.created_at) }}</template>
              <template #cell-prize="{ row }">
                <span v-if="row.prize_type === 'none'" class="text-muted-foreground">{{ row.prize_name }}</span>
                <span v-else>{{ row.prize_name }} <span class="text-muted-foreground">· {{ describe(row.prize_type, row.prize_value, true) }}</span></span>
              </template>
              <template #cell-subscription_id="{ row }">{{ subLabel(row.subscription_id) }}</template>
              <template #cell-cost="{ row }"><span class="tabular-nums">{{ costText(row) }}</span></template>
            </DataTable>
          </div>
          <SimplePager v-model:page="page" :total="total" :page-size="pageSize" @change="loadRecords" />
        </CardContent>
      </Card>
    </template>
  </div>
</template>

<style scoped>
.wheel-text {
  paint-order: stroke;
  stroke: rgb(0 0 0 / 0.18);
  stroke-width: 2px;
}
.bulb {
  fill: #fde68a;
  animation: bulb 1.2s ease-in-out infinite;
}
.bulb.odd {
  animation-delay: 0.6s;
}
.spinning .bulb {
  animation-duration: 0.3s;
}
.spinning .bulb.odd {
  animation-delay: 0.15s;
}
@keyframes bulb {
  0%,
  100% {
    fill: #fef9c3;
    opacity: 1;
  }
  50% {
    fill: #f59e0b;
    opacity: 0.55;
  }
}
@media (prefers-reduced-motion: reduce) {
  .bulb {
    animation: none;
  }
}
</style>
