<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { NCard, NGrid, NGi, NStatistic, NDataTable, NRadioGroup, NRadioButton, NSpin, NAlert, NSpace } from 'naive-ui'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart, BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import VChart from 'vue-echarts'
import { formatBytes, formatMoney, formatTime } from '@gv/shared'
import { admin } from '../api'
import { errText, message } from '../ui'

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

async function loadRank() {
  try {
    const [s, u] = await Promise.all([
      admin.get<Rank[]>('/stat/server-rank', { day: day.value }),
      admin.get<Rank[]>('/stat/user-rank', { day: day.value }),
    ])
    serverRank.value = s || []
    userRank.value = u || []
  } catch (e) {
    message.error(errText(e))
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

const chart = computed(() => ({
  tooltip: { trigger: 'axis' },
  legend: { data: ['收款金额', '佣金', '注册', '付款订单'] },
  grid: { left: 50, right: 50, top: 40, bottom: 30 },
  xAxis: { type: 'category', data: days.value.map((d) => formatTime(d.record_at, false).slice(5)) },
  yAxis: [
    { type: 'value', name: '金额' },
    { type: 'value', name: '数量', minInterval: 1 },
  ],
  series: [
    { name: '收款金额', type: 'line', smooth: true, data: days.value.map((d) => d.paid_total / 100) },
    { name: '佣金', type: 'line', smooth: true, data: days.value.map((d) => d.commission_total / 100) },
    { name: '注册', type: 'bar', yAxisIndex: 1, data: days.value.map((d) => d.register_count) },
    { name: '付款订单', type: 'bar', yAxisIndex: 1, data: days.value.map((d) => d.paid_count) },
  ],
}))

const traffic = (r: Rank) => formatBytes(r.u + r.d)
const serverCols = [
  { title: '节点', key: 'server_name', render: (r: Rank) => r.server_name || `${r.server_type}#${r.server_id}` },
  { title: '类型', key: 'server_type' },
  { title: '流量', key: 'total', render: traffic },
]
const userCols = [
  { title: '用户', key: 'email', render: (r: Rank) => `${r.email} (#${r.user_id})` },
  { title: '折算流量', key: 'total', render: (r: Rank) => `${r.total.toFixed(2)} GB` },
]
</script>

<template>
  <div class="page">
    <NSpin :show="loading">
      <NSpace vertical :size="16">
        <NAlert v-if="status && (!status.schedule || !status.queue)" type="warning" title="后台服务异常">
          {{ !status.schedule ? '定时任务未运行。' : '' }}{{ !status.queue ? '队列处理器未运行，邮件、通知等任务不会执行。' : '' }}
          请检查 worker 进程。
        </NAlert>
        <NGrid v-if="ov" cols="2 s:3 m:4 l:6" responsive="screen" :x-gap="12" :y-gap="12">
          <NGi><NCard size="small"><NStatistic label="今日收入" :value="formatMoney(ov.day_income)" /></NCard></NGi>
          <NGi><NCard size="small"><NStatistic label="本月收入" :value="formatMoney(ov.month_income)" /></NCard></NGi>
          <NGi><NCard size="small"><NStatistic label="上月收入" :value="formatMoney(ov.last_month_income)" /></NCard></NGi>
          <NGi><NCard size="small"><NStatistic label="在线用户 / 订阅" :value="`${ov.online_user} / ${ov.online_subscription}`" /></NCard></NGi>
          <NGi><NCard size="small"><NStatistic label="今日 / 本月注册" :value="`${ov.day_register_total} / ${ov.month_register_total}`" /></NCard></NGi>
          <NGi>
            <NCard size="small">
              <NStatistic label="待回复工单" :value="ov.ticket_pending_total" />
            </NCard>
          </NGi>
          <NGi><NCard size="small"><NStatistic label="待确认佣金订单" :value="ov.commission_pending_total" /></NCard></NGi>
          <NGi><NCard size="small"><NStatistic label="本月发放佣金" :value="formatMoney(ov.commission_month_payout)" /></NCard></NGi>
          <NGi><NCard size="small"><NStatistic label="上月发放佣金" :value="formatMoney(ov.commission_last_month_payout)" /></NCard></NGi>
        </NGrid>
        <NCard title="最近 31 天" size="small">
          <VChart v-if="days.length" :option="chart" autoresize style="height: 320px" />
          <div v-else class="empty">暂无统计数据（每日统计任务在次日生成）</div>
        </NCard>
        <NCard size="small" title="流量排行">
          <template #header-extra>
            <NRadioGroup v-model:value="day" size="small">
              <NRadioButton value="today">今日</NRadioButton>
              <NRadioButton value="yesterday">昨日</NRadioButton>
            </NRadioGroup>
          </template>
          <NGrid cols="1 m:2" responsive="screen" :x-gap="16" :y-gap="16">
            <NGi><NDataTable size="small" :columns="serverCols" :data="serverRank" :bordered="false" /></NGi>
            <NGi><NDataTable size="small" :columns="userCols" :data="userRank" :bordered="false" /></NGi>
          </NGrid>
        </NCard>
      </NSpace>
    </NSpin>
  </div>
</template>

<style scoped>
.empty { padding: 40px; text-align: center; color: #999; }
</style>
