<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NCard, NTabs, NTabPane, NDataTable, NButton, NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NSwitch, NSpace,
  NAlert, NDatePicker, type DataTableColumns,
} from 'naive-ui'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import VChart from 'vue-echarts'
import { formatTime } from '@gv/shared'
import { admin } from '../api'
import { message, run } from '../ui'
import { useList, usePaged } from '../paged'
import { actions, btn, confirmBtn, ellipsis, sw, tag, toSec, type PageResult } from '../helpers'
import type { SubscribeLog, SubscribeRule } from '../types'

use([CanvasRenderer, BarChart, GridComponent, TooltipComponent])

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
const resultText: Record<number, [string, 'default' | 'warning' | 'error' | 'success' | 'info']> = {
  0: ['正常下发', 'success'],
  1: ['订阅不可用', 'default'],
  2: ['规则拦截（空内容）', 'warning'],
  3: ['规则拦截（404）', 'error'],
  4: ['超过频率限制', 'warning'],
  5: ['防分享拦截', 'error'],
  6: ['防分享记录', 'info'],
}
const label = (list: { label: string; value: string }[], v: string) => list.find((o) => o.value === v)?.label || v

// ---------- 规则 ----------
const rules = useList(() => admin.get<SubscribeRule[]>('/subscribe-guard/rules'))
const clients = ref<string[]>([])

interface RuleForm {
  name: string
  target: string
  header: string
  match_type: string
  pattern: string
  negate: boolean
  action: string
  priority: number
  enabled: boolean
}
const ruleShow = ref(false)
const ruleEditing = ref<SubscribeRule | null>(null)
const ruleForm = ref<RuleForm>(blankRule())
function blankRule(): RuleForm {
  return { name: '', target: 'ua', header: '', match_type: 'contains', pattern: '', negate: false, action: 'deny', priority: 0, enabled: true }
}
function ruleBody() {
  const f = ruleForm.value
  return { ...f, header: f.target === 'header' ? f.header : '', pattern: f.match_type === 'empty' ? '' : f.pattern }
}
function openRule(r: SubscribeRule | null) {
  ruleEditing.value = r
  ruleForm.value = r
    ? {
        name: r.name,
        target: r.target,
        header: r.header,
        match_type: r.match_type,
        pattern: r.pattern || '',
        negate: !!r.negate,
        action: r.action,
        priority: r.priority,
        enabled: !!r.enabled,
      }
    : blankRule()
  testResult.value = null
  ruleShow.value = true
}
async function saveRule() {
  const body = ruleBody()
  const ok = await run(
    () => (ruleEditing.value ? admin.put(`/subscribe-guard/rules/${ruleEditing.value.id}`, body) : admin.post('/subscribe-guard/rules', body)),
    '已保存',
  )
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
  if (await run(() => admin.del(`/subscribe-guard/rules/${r.id}`), '已删除')) rules.load()
}

const sample = ref({ ip: '', user_agent: '', flag: '', client: '', headers: '' })
const testResult = ref<boolean | null>(null)
async function testRule() {
  let headers: Record<string, string> = {}
  for (const line of sample.value.headers.split('\n')) {
    const i = line.indexOf(':')
    if (i > 0) headers[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  if (!Object.keys(headers).length) headers = {}
  await run(async () => {
    const r = await admin.post<{ matched: boolean }>('/subscribe-guard/rules/test', { rule: ruleBody(), ...sample.value, headers })
    testResult.value = r.matched
  })
}

const ruleCols: DataTableColumns<SubscribeRule> = [
  { title: '优先级', key: 'priority', width: 80 },
  { title: '名称', key: 'name' },
  {
    title: '条件',
    key: 'target',
    render: (r) =>
      ellipsis(
        `${label(targets, r.target)}${r.target === 'header' ? `(${r.header})` : ''} ${r.negate ? '不' : ''}${label(matchTypes, r.match_type)} ${(r.pattern || '').replace(/\n/g, ', ')}`,
        320,
      ),
  },
  { title: '动作', key: 'action', render: (r) => label(ruleActions, r.action) },
  { title: '命中', key: 'hit_count', render: (r) => `${r.hit_count}${r.last_hit_at ? `（${formatTime(r.last_hit_at)}）` : ''}` },
  { title: '启用', key: 'enabled', width: 70, render: (r) => sw(!!r.enabled, (v) => toggleRule(r, v)) },
  {
    title: '操作',
    key: 'op',
    width: 110,
    render: (r) => actions(btn('编辑', () => openRule(r)), confirmBtn('删除', '确定删除该规则？', () => removeRule(r))),
  },
]

// ---------- 日志 ----------
const logQ = ref({ subscription_id: null as number | null, user_id: null as number | null, ip: '', client: '', user_agent: '', result: null as number | null, rule_id: null as number | null, range: null as [number, number] | null })
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
    since: q.range ? (toSec(q.range[0]) ?? undefined) : undefined,
    until: q.range ? (toSec(q.range[1]) ?? undefined) : undefined,
  })
  return { rows: r.data || [], total: r.total }
})
const logCols: DataTableColumns<SubscribeLog> = [
  { title: '时间', key: 'created_at', width: 140, render: (l) => formatTime(l.created_at) },
  { title: '订阅 / 用户', key: 'subscription_id', render: (l) => `#${l.subscription_id} / #${l.user_id}` },
  { title: 'IP', key: 'ip', render: (l) => h('span', { class: 'mono' }, l.ip) },
  { title: '客户端', key: 'client' },
  { title: 'User-Agent', key: 'user_agent', render: (l) => ellipsis(l.user_agent, 220) },
  { title: '结果', key: 'result', render: (l) => tag(resultText[l.result]?.[0] || String(l.result), resultText[l.result]?.[1]) },
  { title: '规则', key: 'rule_id', width: 70, render: (l) => l.rule_id ?? '-' },
  { type: 'expand', expandable: (l) => !!l.headers, renderExpand: (l) => h('pre', { class: 'code' }, l.headers || '') },
]
const resultOptions = Object.entries(resultText).map(([v, t]) => ({ label: t[0], value: Number(v) }))
const clientOptions = computed(() => clients.value.map((c) => ({ label: c, value: c })))
async function cleanLogs() {
  await run(async () => {
    const r = await admin.post<{ deleted: number }>('/subscribe-guard/logs/clean')
    message.success(`已清理 ${r.deleted} 条过期日志`)
    logs.reload()
  })
}

// ---------- 统计 ----------
interface SubStat {
  subscription_id: number
  user_id: number
  ips: number
  requests: number
  blocked: number
  last_at: number
}
const statQ = ref({ hours: 24, order: 'ips', limit: 50 })
const stats = useList(() => admin.get<SubStat[]>('/subscribe-guard/stats', statQ.value))
const statCols: DataTableColumns<SubStat> = [
  { title: '订阅', key: 'subscription_id', render: (s) => `#${s.subscription_id}` },
  { title: '用户', key: 'user_id', render: (s) => `#${s.user_id}` },
  { title: '不同 IP 数', key: 'ips' },
  { title: '请求数', key: 'requests' },
  { title: '被拦截', key: 'blocked' },
  { title: '最后请求', key: 'last_at', render: (s) => formatTime(s.last_at) },
  {
    title: '操作',
    key: 'op',
    render: (s) =>
      btn('查看日志', () => {
        logQ.value.subscription_id = s.subscription_id
        tab.value = 'logs'
        logs.reload()
      }),
  },
]

const invalidDays = ref(30)
const invalid = useList(() => admin.get<{ date: string; count: number }[]>('/subscribe-guard/invalid', { days: invalidDays.value }))
const invalidChart = computed(() => ({
  tooltip: { trigger: 'axis' },
  grid: { left: 50, right: 20, top: 20, bottom: 30 },
  xAxis: { type: 'category', data: invalid.rows.value.map((d) => `${d.date.slice(4, 6)}-${d.date.slice(6)}`) },
  yAxis: { type: 'value', minInterval: 1 },
  series: [{ type: 'bar', name: '无效 token 请求', data: invalid.rows.value.map((d) => d.count) }],
}))

const tab = ref('rules')
function onTab(v: string) {
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
  <div class="page">
    <NCard size="small">
      <NTabs v-model:value="tab" type="line" @update:value="onTab">
        <NTabPane name="rules" tab="规则">
          <NAlert type="info" style="margin-bottom: 12px">
            启用的规则按优先级从小到大匹配：命中“仅记录”继续匹配，命中其它动作即结束。频率限制、防分享等全局参数在“系统配置 → 多订阅与风控”中设置。
          </NAlert>
          <div class="toolbar">
            <NButton type="primary" @click="openRule(null)">新建规则</NButton>
            <NButton @click="rules.load">刷新</NButton>
          </div>
          <NDataTable size="small" :columns="ruleCols" :data="rules.rows.value" :loading="rules.loading.value" :row-key="(r: SubscribeRule) => r.id" />
        </NTabPane>
        <NTabPane name="logs" tab="请求日志">
          <div class="toolbar">
            <NInputNumber v-model:value="logQ.subscription_id" placeholder="订阅 ID" clearable :show-button="false" style="width: 110px" />
            <NInputNumber v-model:value="logQ.user_id" placeholder="用户 ID" clearable :show-button="false" style="width: 110px" />
            <NInput v-model:value="logQ.ip" placeholder="IP" clearable style="width: 150px" />
            <NSelect v-model:value="logQ.client" :options="clientOptions" placeholder="客户端" clearable filterable style="width: 150px" />
            <NInput v-model:value="logQ.user_agent" placeholder="UA 包含" clearable style="width: 160px" />
            <NSelect v-model:value="logQ.result" :options="resultOptions" placeholder="结果" clearable style="width: 160px" />
            <NInputNumber v-model:value="logQ.rule_id" placeholder="规则 ID" clearable :show-button="false" style="width: 100px" />
            <NDatePicker v-model:value="logQ.range" type="datetimerange" clearable />
            <NButton type="primary" @click="logs.reload">查询</NButton>
            <span class="spacer" />
            <NButton @click="cleanLogs">清理过期日志</NButton>
          </div>
          <NDataTable
            remote
            size="small"
            :columns="logCols"
            :data="logs.rows.value"
            :loading="logs.loading.value"
            :pagination="logs.pagination"
            :row-key="(l: SubscribeLog) => l.id"
            :scroll-x="1000"
          />
        </NTabPane>
        <NTabPane name="stats" tab="订阅统计">
          <div class="toolbar">
            <span>最近</span>
            <NInputNumber v-model:value="statQ.hours" :min="1" :max="4320" style="width: 110px" />
            <span>小时，按</span>
            <NSelect v-model:value="statQ.order" :options="[{ label: '不同 IP 数', value: 'ips' }, { label: '请求数', value: 'requests' }]" style="width: 130px" />
            <span>排序，前</span>
            <NInputNumber v-model:value="statQ.limit" :min="1" :max="200" style="width: 100px" />
            <NButton type="primary" @click="stats.load">查询</NButton>
          </div>
          <NDataTable size="small" :columns="statCols" :data="stats.rows.value" :loading="stats.loading.value" />
        </NTabPane>
        <NTabPane name="invalid" tab="无效请求">
          <div class="toolbar">
            <span>最近</span>
            <NInputNumber v-model:value="invalidDays" :min="1" :max="180" style="width: 100px" />
            <span>天使用无效 token 的订阅请求次数</span>
            <NButton type="primary" @click="invalid.load">查询</NButton>
          </div>
          <VChart :option="invalidChart" autoresize style="height: 320px" />
        </NTabPane>
      </NTabs>
    </NCard>

    <NModal v-model:show="ruleShow" preset="card" :title="ruleEditing ? '编辑规则' : '新建规则'" style="width: 680px; max-width: 95vw">
      <NForm label-placement="left" label-width="96">
        <NFormItem label="名称" required><NInput v-model:value="ruleForm.name" maxlength="64" /></NFormItem>
        <NFormItem label="匹配对象">
          <NSpace>
            <NSelect v-model:value="ruleForm.target" :options="targets" style="width: 200px" />
            <NInput v-if="ruleForm.target === 'header'" v-model:value="ruleForm.header" placeholder="请求头名，如 X-Client" style="width: 200px" />
          </NSpace>
        </NFormItem>
        <NFormItem label="匹配方式">
          <NSpace align="center">
            <NSelect v-model:value="ruleForm.match_type" :options="matchTypes" style="width: 160px" />
            <span>取反 <NSwitch v-model:value="ruleForm.negate" size="small" /></span>
          </NSpace>
        </NFormItem>
        <NFormItem v-if="ruleForm.match_type !== 'empty'" label="匹配值">
          <NSelect
            v-if="ruleForm.target === 'client' && ruleForm.match_type === 'equals'"
            :value="ruleForm.pattern.split('\n').filter(Boolean)"
            multiple
            :options="clientOptions"
            @update:value="(v: string[]) => (ruleForm.pattern = v.join('\n'))"
          />
          <NInput
            v-else
            v-model:value="ruleForm.pattern"
            type="textarea"
            :autosize="{ minRows: 3, maxRows: 12 }"
            :placeholder="ruleForm.match_type === 'regex' ? '一个正则表达式（不区分大小写）' : '每行一个值，任一匹配即命中'"
          />
        </NFormItem>
        <NFormItem label="动作"><NSelect v-model:value="ruleForm.action" :options="ruleActions" /></NFormItem>
        <NFormItem label="优先级"><NInputNumber v-model:value="ruleForm.priority" :min="-1000000" :max="1000000" /></NFormItem>
        <NFormItem label="启用"><NSwitch v-model:value="ruleForm.enabled" /></NFormItem>
        <NFormItem label="测试">
          <NSpace vertical style="width: 100%">
            <NSpace>
              <NInput v-model:value="sample.ip" placeholder="IP" style="width: 150px" />
              <NInput v-model:value="sample.flag" placeholder="flag" style="width: 120px" />
              <NSelect v-model:value="sample.client" :options="clientOptions" clearable placeholder="客户端（留空自动识别）" style="width: 200px" />
            </NSpace>
            <NInput v-model:value="sample.user_agent" placeholder="User-Agent" />
            <NInput v-model:value="sample.headers" type="textarea" :autosize="{ minRows: 1, maxRows: 4 }" placeholder="请求头，每行 名称: 值" />
            <NSpace align="center">
              <NButton size="small" @click="testRule">测试规则</NButton>
              <span v-if="testResult !== null" :style="{ color: testResult ? '#18a058' : '#999' }">{{ testResult ? '命中' : '未命中' }}</span>
            </NSpace>
          </NSpace>
        </NFormItem>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="ruleShow = false">取消</NButton><NButton type="primary" @click="saveRule">保存</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
