<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { PlusIcon, RefreshCwIcon, PencilIcon, Trash2Icon, Loader2Icon, SearchIcon, EraserIcon, InfoIcon } from '@lucide/vue'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import VChart from 'vue-echarts'
import { formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Switch } from '@gv/shared/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@gv/shared/ui/tabs'
import { Card, CardContent } from '@gv/shared/ui/card'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import Pager from '../components/common/Pager.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import MultiSelect from '../components/common/MultiSelect.vue'
import NumInput from '../components/common/NumInput.vue'
import DateTimeInput from '../components/common/DateTimeInput.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { useList, usePaged } from '../paged'
import { colorMode } from '../theme'
import type { Tone, PageResult } from '../helpers'
import type { SubscribeLog, SubscribeRule } from '../types'

use([CanvasRenderer, BarChart, GridComponent, TooltipComponent])
const router = useRouter()

const targets = [
  { label: 'User-Agent', value: 'ua' },
  { label: '指定请求头', value: 'header' },
  { label: '客户端 IP', value: 'ip' },
  { label: 'flag 参数', value: 'flag' },
  { label: '识别出的客户端格式', value: 'client' },
]
const matchTypes = [
  { label: '包含', value: 'contains' },
  { label: '等于', value: 'equals' },
  { label: '前缀', value: 'prefix' },
  { label: '正则', value: 'regex' },
  { label: '为空', value: 'empty' },
  { label: 'IP / CIDR', value: 'cidr' },
]
const ruleActions = [
  { label: '放行（跳过后续规则与限制）', value: 'allow' },
  { label: '仅记录', value: 'log' },
  { label: '返回空内容', value: 'empty' },
  { label: '返回 404', value: 'deny' },
]
const resultText: Record<number, [string, Tone]> = {
  0: ['正常下发', 'success'],
  1: ['订阅不可用', 'default'],
  2: ['规则拦截（空内容）', 'warning'],
  3: ['规则拦截（404）', 'danger'],
  4: ['超过频率限制', 'warning'],
  5: ['防分享拦截', 'danger'],
  6: ['防分享记录', 'info'],
}
const label = (list: { label: string; value: string }[], v: string) => list.find((o) => o.value === v)?.label || v
const clients = ref<string[]>([])
const clientOptions = computed(() => clients.value.map((c) => ({ label: c, value: c })))

// ---------- 规则 ----------
const rules = useList(() => admin.get<SubscribeRule[]>('/subscribe-guard/rules'))
const ruleCols: Column[] = [
  { key: 'priority', title: '优先级', class: 'w-20' },
  { key: 'enabled', title: '启用', align: 'center', class: 'w-16' },
  { key: 'name', title: '名称' },
  { key: 'cond', title: '条件' },
  { key: 'action', title: '动作' },
  { key: 'hit_count', title: '命中' },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]
const condText = (r: SubscribeRule) =>
  `${label(targets, r.target)}${r.target === 'header' ? `(${r.header})` : ''} ${r.negate ? '不' : ''}${label(matchTypes, r.match_type)} ${(r.pattern || '').replace(/\n/g, ', ')}`

interface RuleForm {
  name: string
  target: string
  header: string
  match_type: string
  pattern: string
  negate: boolean
  action: string
  priority: number | null
  enabled: boolean
}
const ruleShow = ref(false)
const ruleSaving = ref(false)
const ruleEditing = ref<SubscribeRule | null>(null)
const ruleForm = ref<RuleForm>(blankRule())
function blankRule(): RuleForm {
  return { name: '', target: 'ua', header: '', match_type: 'contains', pattern: '', negate: false, action: 'deny', priority: 0, enabled: true }
}
function ruleBody() {
  const f = ruleForm.value
  return { ...f, priority: f.priority ?? 0, header: f.target === 'header' ? f.header : '', pattern: f.match_type === 'empty' ? '' : f.pattern }
}
function openRule(r: SubscribeRule | null) {
  ruleEditing.value = r
  ruleForm.value = r
    ? { name: r.name, target: r.target, header: r.header, match_type: r.match_type, pattern: r.pattern || '', negate: !!r.negate, action: r.action, priority: r.priority, enabled: !!r.enabled }
    : blankRule()
  testResult.value = null
  ruleShow.value = true
}
async function saveRule() {
  if (!ruleForm.value.name.trim()) {
    message.warning('请输入规则名称')
    return
  }
  const body = ruleBody()
  ruleSaving.value = true
  const ok = await run(
    () => (ruleEditing.value ? admin.put(`/subscribe-guard/rules/${ruleEditing.value.id}`, body) : admin.post('/subscribe-guard/rules', body)),
    '保存成功',
  )
  ruleSaving.value = false
  if (ok) {
    ruleShow.value = false
    rules.load()
  }
}
async function toggleRule(r: SubscribeRule, v: boolean) {
  const body = { ...r, pattern: r.pattern || '', negate: !!r.negate, enabled: v }
  if (await run(() => admin.put(`/subscribe-guard/rules/${r.id}`, body))) r.enabled = v ? 1 : 0
}
async function removeRule(r: SubscribeRule) {
  if (await confirm(`确定要删除规则「${r.name}」吗？`, () => admin.del(`/subscribe-guard/rules/${r.id}`), '删除成功')) rules.load()
}

const sample = ref({ ip: '', user_agent: '', flag: '', client: '', headers: '' })
const testResult = ref<boolean | null>(null)
async function testRule() {
  const headers: Record<string, string> = {}
  for (const line of sample.value.headers.split('\n')) {
    const i = line.indexOf(':')
    if (i > 0) headers[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  await run(async () => {
    const r = await admin.post<{ matched: boolean }>('/subscribe-guard/rules/test', { rule: ruleBody(), ...sample.value, headers })
    testResult.value = r.matched
  })
}

// ---------- 请求日志 ----------
const logQ = ref({
  subscription_id: null as number | null,
  user_id: null as number | null,
  ip: '',
  client: null as string | null,
  user_agent: '',
  result: null as number | null,
  rule_id: null as number | null,
  since: null as number | null,
  until: null as number | null,
})
const logs = usePaged<SubscribeLog>(async (current, page_size) => {
  const q = logQ.value
  const r = await admin.get<PageResult<SubscribeLog>>('/subscribe-guard/logs', {
    current,
    page_size,
    subscription_id: q.subscription_id ?? undefined,
    user_id: q.user_id ?? undefined,
    ip: q.ip.trim() || undefined,
    client: q.client || undefined,
    user_agent: q.user_agent.trim() || undefined,
    result: q.result ?? undefined,
    rule_id: q.rule_id ?? undefined,
    since: q.since ?? undefined,
    until: q.until ?? undefined,
  })
  return { rows: r.data || [], total: r.total }
})
const logCols: Column[] = [
  { key: 'created_at', title: '时间' },
  { key: 'subscription_id', title: '订阅 / 用户' },
  { key: 'ip', title: 'IP' },
  { key: 'client', title: '客户端' },
  { key: 'user_agent', title: 'User-Agent' },
  { key: 'result', title: '结果' },
  { key: 'rule_id', title: '规则', align: 'center' },
  { key: 'headers', title: '请求头', align: 'right' },
]
const resultOptions = [{ label: '全部结果', value: null as number | null }, ...Object.entries(resultText).map(([v, t]) => ({ label: t[0], value: Number(v) as number | null }))]
const headerView = ref({ open: false, text: '' })
function resetLogQ() {
  logQ.value = { subscription_id: null, user_id: null, ip: '', client: null, user_agent: '', result: null, rule_id: null, since: null, until: null }
  logs.reload()
}
async function cleanLogs() {
  await run(async () => {
    const r = await admin.post<{ deleted: number }>('/subscribe-guard/logs/clean')
    message.success(`已清理 ${r.deleted} 条过期日志`)
    logs.reload()
  })
}

// ---------- 订阅统计 ----------
interface SubStat {
  subscription_id: number
  user_id: number
  ips: number
  requests: number
  blocked: number
  last_at: number
}
const statQ = ref({ hours: 24 as number | null, order: 'ips', limit: 50 as number | null })
const stats = useList(() => admin.get<SubStat[]>('/subscribe-guard/stats', { hours: statQ.value.hours ?? 24, order: statQ.value.order, limit: statQ.value.limit ?? 50 }))
const statCols: Column[] = [
  { key: 'subscription_id', title: '订阅' },
  { key: 'user_id', title: '用户' },
  { key: 'ips', title: '不同 IP 数', align: 'right' },
  { key: 'requests', title: '请求数', align: 'right' },
  { key: 'blocked', title: '被拦截', align: 'right' },
  { key: 'last_at', title: '最后请求' },
  { key: 'op', title: '操作', align: 'right' },
]
function viewLogs(s: SubStat) {
  logQ.value.subscription_id = s.subscription_id
  tab.value = 'logs'
  logs.reload()
}

// ---------- 无效请求 ----------
const invalidDays = ref<number | null>(30)
const invalid = useList(() => admin.get<{ date: string; count: number }[]>('/subscribe-guard/invalid', { days: invalidDays.value ?? 30 }))
const invalidChart = computed(() => {
  const dark = colorMode.value === 'dark'
  const axisColor = dark ? '#a1a1aa' : '#71717a'
  const lineColor = dark ? '#3f3f46' : '#e4e4e7'
  return {
    color: ['#ef4444'],
    tooltip: { trigger: 'axis' },
    grid: { left: 8, right: 8, top: 16, bottom: 8, containLabel: true },
    xAxis: {
      type: 'category',
      data: invalid.rows.value.map((d) => `${d.date.slice(4, 6)}-${d.date.slice(6)}`),
      axisLine: { lineStyle: { color: lineColor } },
      axisLabel: { color: axisColor },
      axisTick: { show: false },
    },
    yAxis: { type: 'value', minInterval: 1, axisLabel: { color: axisColor }, splitLine: { lineStyle: { color: lineColor, type: 'dashed' } } },
    series: [{ type: 'bar', name: '无效 token 请求', barMaxWidth: 16, data: invalid.rows.value.map((d) => d.count) }],
  }
})

const tab = ref('rules')
function onTab(v: string | number) {
  if (v === 'logs' && !logs.rows.value.length) logs.load()
  if (v === 'stats' && !stats.rows.value.length) stats.load()
  if (v === 'invalid' && !invalid.rows.value.length) invalid.load()
}

onMounted(async () => {
  rules.load()
  clients.value = (await admin.get<string[]>('/subscribe-guard/clients').catch(() => [])) || []
})
</script>

<template>
  <div>
    <PageHeader title="订阅风控" description="按规则拦截或记录订阅请求，并统计订阅的使用情况。" />

    <Tabs v-model="tab" @update:model-value="onTab">
      <TabsList class="mb-4">
        <TabsTrigger value="rules">规则</TabsTrigger>
        <TabsTrigger value="logs">请求日志</TabsTrigger>
        <TabsTrigger value="stats">订阅统计</TabsTrigger>
        <TabsTrigger value="invalid">无效请求</TabsTrigger>
      </TabsList>

      <TabsContent value="rules">
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <div class="flex flex-1 items-start gap-2 text-sm text-muted-foreground">
            <InfoIcon class="mt-0.5 size-4 shrink-0" />
            <span>
              启用的规则按优先级从小到大匹配：命中“仅记录”继续匹配，命中其它动作即结束。频率限制、防分享等全局参数在
              <button type="button" class="text-primary hover:underline" @click="router.push('/config')">系统配置 → 多订阅与风控</button>
              中设置。
            </span>
          </div>
          <Button variant="ghost" size="icon" :disabled="rules.loading.value" @click="rules.load"><RefreshCwIcon :class="rules.loading.value ? 'animate-spin' : ''" /></Button>
          <Button @click="openRule(null)"><PlusIcon />添加规则</Button>
        </div>
        <DataTable :columns="ruleCols" :rows="rules.rows.value" :loading="rules.loading.value">
          <template #cell-priority="{ row }"><span class="tabular-nums">{{ row.priority }}</span></template>
          <template #cell-enabled="{ row }"><Switch :model-value="!!row.enabled" @update:model-value="(v: boolean) => toggleRule(row, v)" /></template>
          <template #cell-name="{ row }"><span class="font-medium">{{ row.name }}</span></template>
          <template #cell-cond="{ row }"><span class="line-clamp-2 max-w-md text-xs" :title="condText(row)">{{ condText(row) }}</span></template>
          <template #cell-action="{ row }"><span class="rounded bg-muted px-1.5 py-0.5 text-xs">{{ label(ruleActions, row.action) }}</span></template>
          <template #cell-hit_count="{ row }">
            <span class="tabular-nums">{{ row.hit_count }}</span>
            <span v-if="row.last_hit_at" class="ml-1 text-xs text-muted-foreground">{{ formatTime(row.last_hit_at) }}</span>
          </template>
          <template #cell-op="{ row }">
            <div class="flex justify-end gap-0.5">
              <Button variant="ghost" size="icon" class="size-8" @click="openRule(row)"><PencilIcon /></Button>
              <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" @click="removeRule(row)"><Trash2Icon /></Button>
            </div>
          </template>
        </DataTable>
      </TabsContent>

      <TabsContent value="logs">
        <Card class="mb-3 py-3">
          <CardContent class="grid grid-cols-2 gap-2 px-3 md:grid-cols-4 xl:grid-cols-6">
            <NumInput v-model="logQ.subscription_id" placeholder="订阅 ID" />
            <NumInput v-model="logQ.user_id" placeholder="用户 ID" />
            <Input v-model="logQ.ip" placeholder="IP" />
            <SelectBox :model-value="logQ.client" :options="[{ label: '全部客户端', value: null }, ...clientOptions]" placeholder="客户端" @update:model-value="(v) => (logQ.client = v as string | null)" />
            <Input v-model="logQ.user_agent" placeholder="UA 包含" />
            <SelectBox :model-value="logQ.result" :options="resultOptions" placeholder="结果" @update:model-value="(v) => (logQ.result = v as number | null)" />
            <NumInput v-model="logQ.rule_id" placeholder="规则 ID" />
            <DateTimeInput v-model="logQ.since" />
            <DateTimeInput v-model="logQ.until" />
            <div class="col-span-2 flex gap-2 md:col-span-3 xl:col-span-3 xl:justify-end">
              <Button @click="logs.reload"><SearchIcon />查询</Button>
              <Button variant="outline" @click="resetLogQ">重置</Button>
              <Button variant="outline" @click="cleanLogs"><EraserIcon />清理过期日志</Button>
            </div>
          </CardContent>
        </Card>
        <DataTable :columns="logCols" :rows="logs.rows.value" :loading="logs.loading.value">
          <template #cell-created_at="{ row }"><span class="whitespace-nowrap text-xs">{{ formatTime(row.created_at) }}</span></template>
          <template #cell-subscription_id="{ row }"><span class="tabular-nums">#{{ row.subscription_id }} / #{{ row.user_id }}</span></template>
          <template #cell-ip="{ row }"><span class="font-mono text-xs">{{ row.ip }}</span></template>
          <template #cell-client="{ row }">{{ row.client || '-' }}</template>
          <template #cell-user_agent="{ row }"><span class="block max-w-xs truncate text-xs" :title="row.user_agent">{{ row.user_agent }}</span></template>
          <template #cell-result="{ row }">
            <StatusBadge :tone="resultText[row.result]?.[1] || 'default'">{{ resultText[row.result]?.[0] || row.result }}</StatusBadge>
          </template>
          <template #cell-rule_id="{ row }">{{ row.rule_id ?? '-' }}</template>
          <template #cell-headers="{ row }">
            <Button v-if="row.headers" variant="ghost" size="sm" @click="headerView = { open: true, text: row.headers }">查看</Button>
            <span v-else class="text-muted-foreground">-</span>
          </template>
        </DataTable>
        <Pager :pager="logs.pager" @page="logs.setPage" @size="logs.setPageSize" />
      </TabsContent>

      <TabsContent value="stats">
        <div class="mb-3 flex flex-wrap items-center gap-2 text-sm">
          <span>最近</span>
          <NumInput v-model="statQ.hours" class="w-24" :min="1" :max="4320" />
          <span>小时，按</span>
          <SelectBox class="w-32" :model-value="statQ.order" :options="[{ label: '不同 IP 数', value: 'ips' }, { label: '请求数', value: 'requests' }]" @update:model-value="(v) => (statQ.order = String(v))" />
          <span>排序，前</span>
          <NumInput v-model="statQ.limit" class="w-24" :min="1" :max="200" />
          <span>个</span>
          <Button @click="stats.load"><SearchIcon />查询</Button>
        </div>
        <DataTable :columns="statCols" :rows="stats.rows.value" :loading="stats.loading.value" row-key="subscription_id">
          <template #cell-subscription_id="{ row }"><span class="tabular-nums">#{{ row.subscription_id }}</span></template>
          <template #cell-user_id="{ row }">
            <button type="button" class="tabular-nums hover:text-primary" @click="router.push({ path: '/users', query: { id: row.user_id } })">#{{ row.user_id }}</button>
          </template>
          <template #cell-ips="{ row }"><span class="font-medium tabular-nums">{{ row.ips }}</span></template>
          <template #cell-requests="{ row }"><span class="tabular-nums">{{ row.requests }}</span></template>
          <template #cell-blocked="{ row }"><span class="tabular-nums" :class="row.blocked ? 'text-destructive' : ''">{{ row.blocked }}</span></template>
          <template #cell-last_at="{ row }"><span class="whitespace-nowrap text-xs text-muted-foreground">{{ formatTime(row.last_at) }}</span></template>
          <template #cell-op="{ row }"><Button variant="ghost" size="sm" @click="viewLogs(row)">查看日志</Button></template>
        </DataTable>
      </TabsContent>

      <TabsContent value="invalid">
        <Card>
          <CardContent>
            <div class="mb-3 flex flex-wrap items-center gap-2 text-sm">
              <span>最近</span>
              <NumInput v-model="invalidDays" class="w-24" :min="1" :max="180" />
              <span>天使用无效 token 的订阅请求次数</span>
              <Button size="sm" @click="invalid.load"><SearchIcon />查询</Button>
            </div>
            <div v-if="invalid.loading.value" class="flex h-80 items-center justify-center"><Loader2Icon class="size-5 animate-spin text-muted-foreground" /></div>
            <div v-else class="h-80"><VChart :option="invalidChart" autoresize /></div>
          </CardContent>
        </Card>
      </TabsContent>
    </Tabs>

    <!-- 规则表单 -->
    <Dialog v-model:open="ruleShow">
      <DialogContent class="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader><DialogTitle>{{ ruleEditing ? '编辑规则' : '添加规则' }}</DialogTitle></DialogHeader>
        <div class="grid gap-4 sm:grid-cols-2">
          <FormItem label="名称" class="sm:col-span-2"><Input v-model="ruleForm.name" maxlength="64" /></FormItem>
          <FormItem label="匹配对象">
            <SelectBox :model-value="ruleForm.target" :options="targets" @update:model-value="(v) => (ruleForm.target = String(v))" />
          </FormItem>
          <FormItem v-if="ruleForm.target === 'header'" label="请求头名">
            <Input v-model="ruleForm.header" placeholder="如 X-Client" />
          </FormItem>
          <FormItem label="匹配方式" :class="ruleForm.target === 'header' ? 'sm:col-span-2' : ''">
            <div class="flex items-center gap-3">
              <SelectBox class="flex-1" :model-value="ruleForm.match_type" :options="matchTypes" @update:model-value="(v) => (ruleForm.match_type = String(v))" />
              <label class="flex shrink-0 items-center gap-2 text-sm"><Switch v-model="ruleForm.negate" />取反</label>
            </div>
          </FormItem>
          <FormItem v-if="ruleForm.match_type !== 'empty'" label="匹配值" class="sm:col-span-2">
            <MultiSelect
              v-if="ruleForm.target === 'client' && ruleForm.match_type === 'equals'"
              :model-value="ruleForm.pattern.split('\n').filter(Boolean)"
              :options="clientOptions"
              @update:model-value="(v) => (ruleForm.pattern = v.join('\n'))"
            />
            <Textarea
              v-else
              v-model="ruleForm.pattern"
              rows="4"
              class="font-mono text-xs"
              :placeholder="ruleForm.match_type === 'regex' ? '一个正则表达式（不区分大小写）' : '每行一个值，任一匹配即命中'"
            />
          </FormItem>
          <FormItem label="动作">
            <SelectBox :model-value="ruleForm.action" :options="ruleActions" @update:model-value="(v) => (ruleForm.action = String(v))" />
          </FormItem>
          <FormItem label="优先级" help="数字越小越先匹配">
            <NumInput v-model="ruleForm.priority" :min="-1000000" :max="1000000" />
          </FormItem>
          <label class="flex items-center gap-2 text-sm sm:col-span-2"><Switch v-model="ruleForm.enabled" />启用</label>

          <div class="grid gap-2 rounded-lg border bg-muted/30 p-3 sm:col-span-2">
            <div class="text-sm font-medium">测试规则</div>
            <div class="grid gap-2 sm:grid-cols-3">
              <Input v-model="sample.ip" placeholder="IP" />
              <Input v-model="sample.flag" placeholder="flag" />
              <SelectBox
                :model-value="sample.client || null"
                :options="[{ label: '自动识别客户端', value: null }, ...clientOptions]"
                @update:model-value="(v) => (sample.client = (v as string) || '')"
              />
            </div>
            <Input v-model="sample.user_agent" placeholder="User-Agent" />
            <Textarea v-model="sample.headers" rows="2" class="font-mono text-xs" placeholder="请求头，每行 名称: 值" />
            <div class="flex items-center gap-3">
              <Button size="sm" variant="outline" @click="testRule">测试</Button>
              <StatusBadge v-if="testResult !== null" :tone="testResult ? 'success' : 'default'">{{ testResult ? '命中' : '未命中' }}</StatusBadge>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="ruleShow = false">取消</Button>
          <Button :disabled="ruleSaving" @click="saveRule"><Loader2Icon v-if="ruleSaving" class="animate-spin" />保存</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="headerView.open">
      <DialogContent class="sm:max-w-2xl">
        <DialogHeader><DialogTitle>请求头</DialogTitle></DialogHeader>
        <pre class="max-h-[60svh] overflow-auto rounded-md bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{{ headerView.text }}</pre>
      </DialogContent>
    </Dialog>
  </div>
</template>
