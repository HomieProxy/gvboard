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

// 转盘扇区：角度从正上方顺时针计算
const R = 140
const point = (deg: number, r = R) => {
  const a = (deg * Math.PI) / 180
  return `${(150 + r * Math.sin(a)).toFixed(2)} ${(150 - r * Math.cos(a)).toFixed(2)}`
}
const sectors = computed(() =>
  prizes.value.map((p, i) => {
    const a0 = i * seg.value
    const a1 = a0 + seg.value
    const path =
      prizes.value.length === 1
        ? `M 150 10 A ${R} ${R} 0 1 1 149.99 10 Z`
        : `M 150 150 L ${point(a0)} A ${R} ${R} 0 ${seg.value > 180 ? 1 : 0} 1 ${point(a1)} Z`
    return { path, mid: a0 + seg.value / 2, label: [...p.name].slice(0, 8).join(''), even: i % 2 === 0 }
  }),
)
const fontSize = computed(() => (prizes.value.length > 8 ? 11 : 13))

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
  spinning.value = true
  // 把抽中的扇区中心转到正上方的指针处，至少转 6 圈
  const target = 360 - (r.index + 0.5) * seg.value
  const cur = ((angle.value % 360) + 360) % 360
  angle.value += 360 * 6 + ((target - cur + 360) % 360)
  timer = setTimeout(async () => {
    spinning.value = false
    result.value = r.prize
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
        <Card class="py-6">
          <CardContent class="flex justify-center px-4">
            <Skeleton v-if="loading" class="aspect-square w-full max-w-[360px] rounded-full" />
            <div v-else class="relative w-full max-w-[360px]">
              <svg viewBox="0 0 300 300" class="w-full drop-shadow-sm">
                <circle cx="150" cy="150" r="146" class="fill-primary/15" />
                <g :style="{ transform: `rotate(${angle}deg)`, transformOrigin: '150px 150px', transition: spinning ? `transform ${SPIN_MS}ms cubic-bezier(0.15, 0.6, 0.1, 1)` : 'none' }">
                  <g v-for="(s, i) in sectors" :key="i">
                    <path :d="s.path" :class="s.even ? 'fill-primary' : 'fill-muted'" class="stroke-background" stroke-width="1.5" />
                    <text
                      x="150"
                      y="42"
                      text-anchor="middle"
                      :font-size="fontSize"
                      font-weight="600"
                      :class="s.even ? 'fill-primary-foreground' : 'fill-foreground'"
                      :transform="`rotate(${s.mid} 150 150)`"
                    >
                      {{ s.label }}
                    </text>
                  </g>
                </g>
                <circle cx="150" cy="150" r="30" class="fill-background stroke-primary" stroke-width="3" />
                <path d="M 150 2 L 162 26 L 138 26 Z" class="fill-primary stroke-background" stroke-width="2" />
              </svg>
              <div class="pointer-events-none absolute inset-0 flex items-center justify-center">
                <GiftIcon class="size-7 text-primary" :class="{ 'animate-pulse': spinning }" />
              </div>
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
                    <span class="size-2 shrink-0 rounded-full" :class="i % 2 === 0 ? 'bg-primary' : 'bg-muted-foreground/40'" />
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
