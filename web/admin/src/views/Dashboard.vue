<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import {
  SettingsIcon, ReceiptIcon, PackageIcon, UsersIcon, WifiIcon, WalletIcon, UserPlusIcon, CalendarIcon, HistoryIcon,
  HandCoinsIcon, MessageSquareIcon, TriangleAlertIcon, ChevronRightIcon, CoinsIcon,
} from '@lucide/vue'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart, BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import VChart from 'vue-echarts'
import { formatBytes, formatMoney, formatTime } from '@gv/shared'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@gv/shared/ui/card'
import { Alert, AlertDescription, AlertTitle } from '@gv/shared/ui/alert'
import { Tabs, TabsList, TabsTrigger } from '@gv/shared/ui/tabs'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { admin } from '../api'
import { errText, message } from '../ui'
import { colorMode } from '../theme'

use([CanvasRenderer, LineChart, BarChart, GridComponent, TooltipComponent, LegendComponent])

interface Overview {
  online_user: number
  online_subscription: number
  day_income: number
  month_income: number
  last_month_income: number
  day_register_total: number
  month_register_total: number
  ticket_pending_total: number
  commission_pending_total: number
  commission_month_payout: number
  commission_last_month_payout: number
}
interface DayStat {
  record_at: number
  register_count: number
  paid_count: number
  paid_total: number
  commission_count: number
  commission_total: number
}
interface Rank {
  server_id?: number
  server_type?: string
  server_name?: string
  user_id?: number
  email?: string
  u: number
  d: number
  total: number
}
interface Status {
  schedule: boolean
  queue: boolean
}

const ov = ref<Overview | null>(null)
const days = ref<DayStat[]>([])
const status = ref<Status | null>(null)
const day = ref<'today' | 'yesterday'>('today')
const serverRank = ref<Rank[]>([])
const userRank = ref<Rank[]>([])
const loading = ref(true)
const rankLoading = ref(false)

async function loadRank() {
  rankLoading.value = true
  try {
    const [s, u] = await Promise.all([
      admin.get<Rank[]>('/stat/server-rank', { day: day.value }),
      admin.get<Rank[]>('/stat/user-rank', { day: day.value }),
    ])
    serverRank.value = s || []
    userRank.value = u || []
  } catch (e) {
    message.error(errText(e))
  } finally {
    rankLoading.value = false
  }
}

onMounted(async () => {
  try {
    const [o, d, st] = await Promise.all([
      admin.get<Overview>('/stat/overview'),
      admin.get<DayStat[]>('/stat/orders'),
      admin.get<Status>('/system/status'),
    ])
    ov.value = o
    days.value = d || []
    status.value = st
  } catch (e) {
    message.error(errText(e))
  } finally {
    loading.value = false
  }
  loadRank()
})
watch(day, loadRank)

const shortcuts = [
  { to: '/config', title: '系统设置', icon: SettingsIcon },
  { to: '/orders', title: '订单管理', icon: ReceiptIcon },
  { to: '/plans', title: '订阅管理', icon: PackageIcon },
  { to: '/users', title: '用户管理', icon: UsersIcon },
]

const stats = computed(() => {
  const o = ov.value
  if (!o) return []
  return [
    { label: '在线人数', value: String(o.online_user), sub: `在线订阅 ${o.online_subscription}`, icon: WifiIcon },
    { label: '今日收入', value: formatMoney(o.day_income), sub: '已支付订单金额', icon: WalletIcon },
    { label: '实时注册', value: String(o.day_register_total), sub: '今日新增用户', icon: UserPlusIcon },
    { label: '本月收入', value: formatMoney(o.month_income), sub: `上月 ${formatMoney(o.last_month_income)}`, icon: CalendarIcon },
    { label: '上月收入', value: formatMoney(o.last_month_income), sub: '上个自然月', icon: HistoryIcon },
    { label: '本月新增用户', value: String(o.month_register_total), sub: '本自然月注册', icon: UsersIcon },
    { label: '本月佣金支出', value: formatMoney(o.commission_month_payout), sub: '已确认发放', icon: CoinsIcon },
    { label: '上月佣金支出', value: formatMoney(o.commission_last_month_payout), sub: '上个自然月', icon: HandCoinsIcon },
  ]
})

const palette = ['#3b82f6', '#f59e0b', '#10b981', '#8b5cf6']
const chart = computed(() => {
  const dark = colorMode.value === 'dark'
  const axisColor = dark ? '#a1a1aa' : '#71717a'
  const lineColor = dark ? '#3f3f46' : '#e4e4e7'
  return {
    color: palette,
    tooltip: { trigger: 'axis' },
    legend: { top: 0, left: 'center', icon: 'roundRect', itemWidth: 12, itemHeight: 8, textStyle: { color: axisColor } },
    grid: { left: 8, right: 8, top: 48, bottom: 8, containLabel: true },
    xAxis: {
      type: 'category',
      data: days.value.map((d) => formatTime(d.record_at, false).slice(5)),
      axisLine: { lineStyle: { color: lineColor } },
      axisLabel: { color: axisColor },
      axisTick: { show: false },
    },
    yAxis: [
      { type: 'value', name: '金额', nameTextStyle: { color: axisColor }, axisLabel: { color: axisColor }, splitLine: { lineStyle: { color: lineColor, type: 'dashed' } } },
      { type: 'value', name: '数量', minInterval: 1, nameTextStyle: { color: axisColor }, axisLabel: { color: axisColor }, splitLine: { show: false } },
    ],
    series: [
      { name: '收款金额', type: 'line', smooth: true, symbol: 'none', areaStyle: { opacity: 0.08 }, data: days.value.map((d) => d.paid_total / 100) },
      { name: '佣金金额', type: 'line', smooth: true, symbol: 'none', data: days.value.map((d) => d.commission_total / 100) },
      { name: '注册人数', type: 'bar', yAxisIndex: 1, barMaxWidth: 10, data: days.value.map((d) => d.register_count) },
      { name: '付款订单', type: 'bar', yAxisIndex: 1, barMaxWidth: 10, data: days.value.map((d) => d.paid_count) },
    ],
  }
})

const maxOf = (rows: Rank[], f: (r: Rank) => number) => Math.max(1, ...rows.map(f))
const serverMax = computed(() => maxOf(serverRank.value, (r) => r.u + r.d))
const userMax = computed(() => maxOf(userRank.value, (r) => r.total))
const dayText = computed(() => (day.value === 'today' ? '今日' : '昨日'))
</script>

<template>
  <div class="grid gap-4">
    <Alert v-if="status && (!status.schedule || !status.queue)" variant="destructive">
      <TriangleAlertIcon />
      <AlertTitle>后台服务异常</AlertTitle>
      <AlertDescription>
        {{ !status.schedule ? '定时任务未运行。' : '' }}{{ !status.queue ? '队列处理器未运行，邮件、通知等任务不会执行。' : '' }}请检查 worker 进程。
      </AlertDescription>
    </Alert>

    <div v-if="ov && (ov.ticket_pending_total || ov.commission_pending_total)" class="grid gap-3 sm:grid-cols-2">
      <RouterLink v-if="ov.ticket_pending_total" to="/tickets" class="flex items-center gap-3 rounded-lg border border-warning/40 bg-warning/10 px-4 py-3 text-sm hover:bg-warning/15">
        <MessageSquareIcon class="size-4 text-amber-600" />
        <span>有 <b>{{ ov.ticket_pending_total }}</b> 条工单等待处理</span>
        <ChevronRightIcon class="ml-auto size-4 text-muted-foreground" />
      </RouterLink>
      <RouterLink v-if="ov.commission_pending_total" to="/orders" class="flex items-center gap-3 rounded-lg border border-warning/40 bg-warning/10 px-4 py-3 text-sm hover:bg-warning/15">
        <CoinsIcon class="size-4 text-amber-600" />
        <span>有 <b>{{ ov.commission_pending_total }}</b> 笔佣金等待确认</span>
        <ChevronRightIcon class="ml-auto size-4 text-muted-foreground" />
      </RouterLink>
    </div>

    <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <RouterLink
        v-for="s in shortcuts"
        :key="s.to"
        :to="s.to"
        class="group flex items-center gap-3 rounded-lg border bg-card px-4 py-3 transition-colors hover:border-primary/40 hover:bg-primary/5"
      >
        <span class="flex size-9 items-center justify-center rounded-md bg-primary/10 text-primary"><component :is="s.icon" class="size-4" /></span>
        <span class="text-sm font-medium">{{ s.title }}</span>
        <ChevronRightIcon class="ml-auto size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
      </RouterLink>
    </div>

    <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <template v-if="loading">
        <Skeleton v-for="i in 8" :key="i" class="h-[104px] rounded-xl" />
      </template>
      <Card v-for="s in stats" v-else :key="s.label" class="gap-0 py-4">
        <CardContent class="px-4">
          <div class="flex items-center justify-between text-sm text-muted-foreground">
            {{ s.label }}
            <component :is="s.icon" class="size-4" />
          </div>
          <div class="mt-2 text-2xl font-semibold tabular-nums">{{ s.value }}</div>
          <div class="mt-1 text-xs text-muted-foreground">{{ s.sub }}</div>
        </CardContent>
      </Card>
    </div>

    <Card>
      <CardHeader>
        <CardTitle>收入趋势</CardTitle>
        <CardDescription>最近 31 天的收款、佣金、注册与付款订单</CardDescription>
      </CardHeader>
      <CardContent>
        <VChart v-if="days.length" :option="chart" autoresize class="h-80 w-full" />
        <div v-else class="flex h-40 items-center justify-center text-sm text-muted-foreground">暂无统计数据（每日统计任务在次日生成）</div>
      </CardContent>
    </Card>

    <div class="flex items-center justify-between">
      <h2 class="text-base font-semibold">流量排行</h2>
      <Tabs v-model="day">
        <TabsList>
          <TabsTrigger value="today">今日</TabsTrigger>
          <TabsTrigger value="yesterday">昨日</TabsTrigger>
        </TabsList>
      </Tabs>
    </div>
    <div class="grid gap-4 lg:grid-cols-2" :class="rankLoading ? 'opacity-60' : ''">
      <Card>
        <CardHeader><CardTitle class="text-sm">{{ dayText }}节点流量排行</CardTitle></CardHeader>
        <CardContent class="grid gap-3">
          <div v-for="(r, i) in serverRank" :key="`${r.server_type}-${r.server_id}`" class="grid gap-1">
            <div class="flex items-center gap-2 text-sm">
              <span class="w-5 text-right text-xs text-muted-foreground tabular-nums">{{ i + 1 }}</span>
              <span class="truncate">{{ r.server_name || `${r.server_type}#${r.server_id}` }}</span>
              <span class="rounded bg-muted px-1.5 text-[11px] text-muted-foreground">{{ r.server_type }}</span>
              <span class="ml-auto shrink-0 tabular-nums text-muted-foreground">{{ formatBytes(r.u + r.d) }}</span>
            </div>
            <div class="ml-7 h-1.5 overflow-hidden rounded-full bg-muted">
              <div class="h-full rounded-full bg-primary" :style="{ width: `${((r.u + r.d) / serverMax) * 100}%` }" />
            </div>
          </div>
          <div v-if="!serverRank.length" class="py-8 text-center text-sm text-muted-foreground">暂无数据</div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle class="text-sm">{{ dayText }}用户流量排行</CardTitle></CardHeader>
        <CardContent class="grid gap-3">
          <div v-for="(r, i) in userRank" :key="r.user_id" class="grid gap-1">
            <div class="flex items-center gap-2 text-sm">
              <span class="w-5 text-right text-xs text-muted-foreground tabular-nums">{{ i + 1 }}</span>
              <RouterLink :to="{ path: '/users', query: { id: r.user_id } }" class="truncate hover:text-primary hover:underline">{{ r.email }}</RouterLink>
              <span class="ml-auto shrink-0 tabular-nums text-muted-foreground">{{ r.total.toFixed(2) }} GB</span>
            </div>
            <div class="ml-7 h-1.5 overflow-hidden rounded-full bg-muted">
              <div class="h-full rounded-full bg-emerald-500" :style="{ width: `${(r.total / userMax) * 100}%` }" />
            </div>
          </div>
          <div v-if="!userRank.length" class="py-8 text-center text-sm text-muted-foreground">暂无数据</div>
        </CardContent>
      </Card>
    </div>
  </div>
</template>
