<script setup lang="ts">
import { h, onMounted, onUnmounted, ref } from 'vue'
import {
  NCard, NDescriptions, NDescriptionsItem, NDataTable, NButton, NSpace, NInputNumber, NTag, NTooltip, type DataTableColumns,
} from 'naive-ui'
import { formatBytes, formatTime } from '@gv/shared'
import { admin } from '../api'
import { confirm, errText, message } from '../ui'
import { usePaged } from '../paged'
import type { PageResult } from '../helpers'

interface Status {
  schedule: boolean
  schedule_last_runtime: number | null
  queue: boolean
  queue_workers: number
  queue_concurrency: number
  syslog_dropped: number
}
interface Queue {
  queue: string
  pending: number
  active: number
  scheduled: number
  retry: number
  failed: number
  processed: number
}
interface StatUser {
  id: number
  server_rate: number
  u: number
  d: number
  record_type: string
  record_at: number
  subscription_id: number | null
}

interface JobState {
  started_at: number
  finished_at: number
  elapsed_ms: number
  error: string
  manual: boolean
}
interface Job {
  name: string
  spec: string
  running: boolean
  last: JobState | null
}

const status = ref<Status | null>(null)
const queues = ref<Queue[]>([])
const jobs = ref<Job[]>([])

async function load() {
  try {
    const [s, q, j] = await Promise.all([
      admin.get<Status>('/system/status'),
      admin.get<Queue[]>('/system/queue'),
      admin.get<Job[]>('/system/jobs'),
    ])
    status.value = s
    queues.value = q || []
    jobs.value = j || []
  } catch (e) {
    message.error(errText(e))
  }
}

// ---------- 定时任务 ----------
const jobTitles: Record<string, string> = {
  traffic_update: '写入节点上报流量',
  check_order: '处理已支付订单 / 关闭超时订单',
  check_commission: '发放邀请佣金',
  check_ticket: '自动关闭超时工单',
  check_renewal: '自动续费',
  statistics: '每日统计',
  reset_traffic: '流量重置',
  reset_log: '清理过期记录',
  send_remind_mail: '到期与流量提醒',
  check_server: '节点掉线检查',
  plugin_kv_clean: '清理插件过期数据',
  plugin_js_jobs: 'JS 插件定时任务调度',
}
const jobTitle = (name: string) =>
  jobTitles[name] || (name.startsWith('plugin.') ? `插件任务 ${name.slice(7)}` : name)

function runJob(j: Job) {
  confirm(`立即执行「${jobTitle(j.name)}」？任务在后台执行，完成后刷新可查看结果。`, async () => {
    await admin.post(`/system/jobs/${encodeURIComponent(j.name)}/run`)
    j.running = true
    setTimeout(load, 1500)
  }, '已开始执行')
}

const jobCols: DataTableColumns<Job> = [
  {
    title: '任务',
    key: 'name',
    render: (r) => h('div', [h('div', jobTitle(r.name)), h('div', { class: 'muted mono' }, r.name)]),
  },
  { title: '执行时间（cron）', key: 'spec', render: (r) => h('span', { class: 'mono' }, r.spec) },
  {
    title: '最近执行',
    key: 'last',
    render: (r) => {
      if (!r.last) return h('span', { class: 'muted' }, '未执行')
      return h('div', [
        h('div', formatTime(r.last.started_at)),
        h('div', { class: 'muted' }, `${r.last.manual ? '手动' : '定时'}${r.last.finished_at ? ` · 耗时 ${(r.last.elapsed_ms / 1000).toFixed(1)} 秒` : ''}`),
      ])
    },
  },
  {
    title: '结果',
    key: 'result',
    render: (r) => {
      if (r.running) return h(NTag, { type: 'info', size: 'small', bordered: false }, () => '执行中')
      if (!r.last) return '-'
      if (!r.last.finished_at) return h(NTag, { size: 'small', bordered: false }, () => '已中断')
      if (r.last.error) {
        return h(NTooltip, null, {
          trigger: () => h(NTag, { type: 'error', size: 'small', bordered: false }, () => '失败'),
          default: () => r.last!.error,
        })
      }
      return h(NTag, { type: 'success', size: 'small', bordered: false }, () => '成功')
    },
  },
  {
    title: '操作',
    key: 'actions',
    render: (r) => h(NButton, { size: 'small', disabled: r.running, onClick: () => runJob(r) }, () => '立即执行'),
  },
]

const queueCols: DataTableColumns<Queue> = [
  { title: '队列', key: 'queue' },
  { title: '等待中', key: 'pending' },
  { title: '执行中', key: 'active' },
  { title: '计划中', key: 'scheduled' },
  { title: '等待重试', key: 'retry' },
  { title: '失败', key: 'failed' },
  { title: '今日已处理', key: 'processed' },
]

// ---------- 用户流量记录 ----------
const userId = ref<number | null>(null)
const subId = ref<number | null>(null)
const traffic = usePaged<StatUser>(async (current, page_size) => {
  if (!userId.value) return { rows: [], total: 0 }
  const r = await admin.get<PageResult<StatUser>>('/stat/user', {
    user_id: userId.value,
    subscription_id: subId.value ?? undefined,
    current,
    page_size,
  })
  return { rows: r.data || [], total: r.total }
})
const trafficCols: DataTableColumns<StatUser> = [
  { title: '日期', key: 'record_at', render: (r) => formatTime(r.record_at, false) },
  { title: '订阅', key: 'subscription_id', render: (r) => (r.subscription_id ? `#${r.subscription_id}` : '-') },
  { title: '上行', key: 'u', render: (r) => formatBytes(r.u) },
  { title: '下行', key: 'd', render: (r) => formatBytes(r.d) },
  { title: '倍率', key: 'server_rate', render: (r) => `${r.server_rate}x` },
  { title: '折算合计', key: 'total', render: (r) => formatBytes((r.u + r.d) * r.server_rate) },
]

let timer = 0
onMounted(() => {
  load()
  timer = window.setInterval(load, 10000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="page">
    <NSpace vertical :size="16">
      <NCard title="运行状态" size="small">
        <template #header-extra><NButton size="small" @click="load">刷新</NButton></template>
        <NDescriptions v-if="status" :column="3" label-placement="left" bordered size="small">
          <NDescriptionsItem label="定时任务">
            <NTag :type="status.schedule ? 'success' : 'error'" size="small" :bordered="false">{{ status.schedule ? '运行中' : '未运行' }}</NTag>
          </NDescriptionsItem>
          <NDescriptionsItem label="最后运行">{{ formatTime(status.schedule_last_runtime) }}</NDescriptionsItem>
          <NDescriptionsItem label="队列处理器">
            <NTag :type="status.queue ? 'success' : 'error'" size="small" :bordered="false">{{ status.queue ? '运行中' : '未运行' }}</NTag>
          </NDescriptionsItem>
          <NDescriptionsItem label="处理器进程数">{{ status.queue_workers }}</NDescriptionsItem>
          <NDescriptionsItem label="总并发数">{{ status.queue_concurrency }}</NDescriptionsItem>
          <NDescriptionsItem label="丢弃的系统日志">{{ status.syslog_dropped }}</NDescriptionsItem>
        </NDescriptions>
      </NCard>
      <NCard title="队列" size="small">
        <NDataTable size="small" :columns="queueCols" :data="queues" />
      </NCard>
      <NCard title="定时任务" size="small">
        <p class="muted" style="margin-top: 0">
          执行时间按站点时区解释；关闭“定时任务”的实例也可手动执行。同一任务正在执行时（包括定时触发的执行）不能再次执行。
        </p>
        <NDataTable size="small" :columns="jobCols" :data="jobs" :row-key="(r: Job) => r.name" />
      </NCard>
      <NCard title="用户流量记录" size="small">
        <div class="toolbar">
          <NInputNumber v-model:value="userId" placeholder="用户 ID" :min="1" :show-button="false" style="width: 140px" />
          <NInputNumber v-model:value="subId" placeholder="订阅 ID（可选）" :min="1" clearable :show-button="false" style="width: 160px" />
          <NButton type="primary" :disabled="!userId" @click="traffic.reload">查询</NButton>
        </div>
        <NDataTable
          remote
          size="small"
          :columns="trafficCols"
          :data="traffic.rows.value"
          :loading="traffic.loading.value"
          :pagination="traffic.pagination"
          :row-key="(r: StatUser) => r.id"
        />
      </NCard>
    </NSpace>
  </div>
</template>
