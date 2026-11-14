<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RefreshCwIcon, PlayIcon, SearchIcon, ActivityIcon, CheckCheckIcon, TriangleAlertIcon, ServerCogIcon } from '@lucide/vue'
import { formatBytes, formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@gv/shared/ui/card'
import { Tooltip, TooltipContent, TooltipTrigger } from '@gv/shared/ui/tooltip'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import NumInput from '../components/common/NumInput.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
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
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const [s, q, j] = await Promise.all([admin.get<Status>('/system/status'), admin.get<Queue[]>('/system/queue'), admin.get<Job[]>('/system/jobs')])
    status.value = s
    queues.value = q || []
    jobs.value = j || []
  } catch (e) {
    message.error(errText(e))
  } finally {
    loading.value = false
  }
}

const queueNames: Record<string, string> = {
  order_handle: '订单队列',
  send_email: '邮件队列',
  send_email_mass: '邮件群发队列',
  send_telegram: 'Telegram消息队列',
  stat: '统计队列',
  traffic_fetch: '流量消费队列',
  plugin: '插件事件队列',
}
const sum = (f: (q: Queue) => number) => queues.value.reduce((s, q) => s + f(q), 0)
const overview = computed(() => [
  { label: '当前作业量', value: sum((q) => q.pending + q.active + q.scheduled + q.retry), icon: ActivityIcon },
  { label: '今日处理量', value: sum((q) => q.processed), icon: CheckCheckIcon },
  { label: '失败作业', value: sum((q) => q.failed), icon: TriangleAlertIcon },
])

const queueCols: Column[] = [
  { key: 'queue', title: '队列名称' },
  { key: 'pending', title: '等待中', align: 'right' },
  { key: 'active', title: '执行中', align: 'right' },
  { key: 'scheduled', title: '计划中', align: 'right' },
  { key: 'retry', title: '等待重试', align: 'right' },
  { key: 'failed', title: '失败', align: 'right' },
  { key: 'processed', title: '今日已处理', align: 'right' },
]

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
const jobTitle = (name: string) => jobTitles[name] || (name.startsWith('plugin.') ? `插件任务 ${name.slice(7)}` : name)
const jobCols: Column[] = [
  { key: 'name', title: '任务' },
  { key: 'spec', title: '执行时间（cron）' },
  { key: 'last', title: '最近执行' },
  { key: 'result', title: '结果' },
  { key: 'op', title: '操作', align: 'right' },
]
function runJob(j: Job) {
  confirm(
    `立即执行「${jobTitle(j.name)}」？任务在后台执行，完成后刷新可查看结果。`,
    async () => {
      await admin.post(`/system/jobs/${encodeURIComponent(j.name)}/run`)
      j.running = true
      setTimeout(load, 1500)
    },
    '已开始执行',
    false,
  )
}

// ---------- 用户流量记录 ----------
const userId = ref<number | null>(null)
const subId = ref<number | null>(null)
const traffic = usePaged<StatUser>(async (current, page_size) => {
  if (!userId.value) return { rows: [], total: 0 }
  const r = await admin.get<PageResult<StatUser>>('/stat/user', { user_id: userId.value, subscription_id: subId.value ?? undefined, current, page_size })
  return { rows: r.data || [], total: r.total }
})
const trafficCols: Column[] = [
  { key: 'record_at', title: '日期' },
  { key: 'subscription_id', title: '订阅' },
  { key: 'u', title: '上行', align: 'right' },
  { key: 'd', title: '下行', align: 'right' },
  { key: 'server_rate', title: '倍率', align: 'right' },
  { key: 'total', title: '折算合计', align: 'right' },
]

let timer = 0
onMounted(() => {
  load()
  timer = window.setInterval(load, 10000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="队列监控" description="每 10 秒自动刷新。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
    </PageHeader>

    <div class="-mt-2 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <Card v-for="o in overview" :key="o.label" class="py-4">
        <CardContent class="flex items-center justify-between px-4">
          <div>
            <div class="text-sm text-muted-foreground">{{ o.label }}</div>
            <div class="mt-1 text-2xl font-semibold tabular-nums">{{ o.value }}</div>
          </div>
          <component :is="o.icon" class="size-5 text-muted-foreground" />
        </CardContent>
      </Card>
      <Card class="py-4">
        <CardContent class="flex items-center justify-between px-4">
          <div class="grid gap-1.5">
            <div class="text-sm text-muted-foreground">状态</div>
            <div v-if="status" class="flex flex-wrap gap-1.5">
              <StatusBadge :tone="status.queue ? 'success' : 'danger'" dot>队列 {{ status.queue ? '运行中' : '未启动' }}</StatusBadge>
              <StatusBadge :tone="status.schedule ? 'success' : 'danger'" dot>定时任务 {{ status.schedule ? '运行中' : '未启动' }}</StatusBadge>
            </div>
          </div>
          <ServerCogIcon class="size-5 text-muted-foreground" />
        </CardContent>
      </Card>
    </div>

    <Card>
      <CardHeader>
        <CardTitle>当前作业详情</CardTitle>
        <CardDescription v-if="status">
          处理器进程 {{ status.queue_workers }} 个，总并发 {{ status.queue_concurrency }}；定时任务最后运行 {{ formatTime(status.schedule_last_runtime) }}；丢弃的系统日志 {{ status.syslog_dropped }} 条
        </CardDescription>
      </CardHeader>
      <CardContent>
        <DataTable :columns="queueCols" :rows="queues" row-key="queue">
          <template #cell-queue="{ row }">
            <span class="font-medium">{{ queueNames[row.queue] || row.queue }}</span>
            <span class="ml-2 font-mono text-xs text-muted-foreground">{{ row.queue }}</span>
          </template>
          <template #cell-pending="{ row }"><span class="tabular-nums">{{ row.pending }}</span></template>
          <template #cell-active="{ row }"><span class="tabular-nums">{{ row.active }}</span></template>
          <template #cell-scheduled="{ row }"><span class="tabular-nums">{{ row.scheduled }}</span></template>
          <template #cell-retry="{ row }"><span class="tabular-nums" :class="row.retry ? 'text-amber-600' : ''">{{ row.retry }}</span></template>
          <template #cell-failed="{ row }"><span class="tabular-nums" :class="row.failed ? 'text-destructive' : ''">{{ row.failed }}</span></template>
          <template #cell-processed="{ row }"><span class="tabular-nums">{{ row.processed }}</span></template>
        </DataTable>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>定时任务</CardTitle>
        <CardDescription>执行时间按站点时区解释；关闭“定时任务”的实例也可手动执行。同一任务正在执行时（包括定时触发的执行）不能再次执行。</CardDescription>
      </CardHeader>
      <CardContent>
        <DataTable :columns="jobCols" :rows="jobs" row-key="name">
          <template #cell-name="{ row }">
            <div class="grid leading-tight">
              <span>{{ jobTitle(row.name) }}</span>
              <span class="font-mono text-xs text-muted-foreground">{{ row.name }}</span>
            </div>
          </template>
          <template #cell-spec="{ row }"><span class="font-mono text-xs">{{ row.spec }}</span></template>
          <template #cell-last="{ row }">
            <span v-if="!row.last" class="text-muted-foreground">未执行</span>
            <div v-else class="grid leading-tight">
              <span class="text-xs">{{ formatTime(row.last.started_at) }}</span>
              <span class="text-xs text-muted-foreground">
                {{ row.last.manual ? '手动' : '定时' }}{{ row.last.finished_at ? ` · 耗时 ${(row.last.elapsed_ms / 1000).toFixed(1)} 秒` : '' }}
              </span>
            </div>
          </template>
          <template #cell-result="{ row }">
            <StatusBadge v-if="row.running" tone="info">执行中</StatusBadge>
            <span v-else-if="!row.last" class="text-muted-foreground">-</span>
            <StatusBadge v-else-if="!row.last.finished_at">已中断</StatusBadge>
            <Tooltip v-else-if="row.last.error">
              <TooltipTrigger as-child><span><StatusBadge tone="danger">失败</StatusBadge></span></TooltipTrigger>
              <TooltipContent class="max-w-sm">{{ row.last.error }}</TooltipContent>
            </Tooltip>
            <StatusBadge v-else tone="success">成功</StatusBadge>
          </template>
          <template #cell-op="{ row }">
            <Button variant="outline" size="sm" :disabled="row.running" @click="runJob(row)"><PlayIcon />立即执行</Button>
          </template>
        </DataTable>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>用户流量记录</CardTitle>
        <CardDescription>按用户（可选订阅）查询每日流量记录。</CardDescription>
      </CardHeader>
      <CardContent>
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <NumInput v-model="userId" class="w-36" :min="1" placeholder="用户 ID" />
          <NumInput v-model="subId" class="w-40" :min="1" placeholder="订阅 ID（可选）" />
          <Button :disabled="!userId" @click="traffic.reload"><SearchIcon />查询</Button>
        </div>
        <DataTable :columns="trafficCols" :rows="traffic.rows.value" :loading="traffic.loading.value" empty="输入用户 ID 后查询">
          <template #cell-record_at="{ row }">{{ formatTime(row.record_at, false) }}</template>
          <template #cell-subscription_id="{ row }">{{ row.subscription_id ? `#${row.subscription_id}` : '-' }}</template>
          <template #cell-u="{ row }"><span class="tabular-nums">{{ formatBytes(row.u) }}</span></template>
          <template #cell-d="{ row }"><span class="tabular-nums">{{ formatBytes(row.d) }}</span></template>
          <template #cell-server_rate="{ row }"><span class="tabular-nums">{{ row.server_rate }} x</span></template>
          <template #cell-total="{ row }"><span class="font-medium tabular-nums">{{ formatBytes((row.u + row.d) * row.server_rate) }}</span></template>
        </DataTable>
        <Pager v-if="traffic.pager.total" :pager="traffic.pager" @page="traffic.setPage" @size="traffic.setPageSize" />
      </CardContent>
    </Card>
  </div>
</template>
