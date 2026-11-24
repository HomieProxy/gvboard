<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { PlusIcon, RefreshCwIcon, PencilIcon, Trash2Icon, Loader2Icon, SearchIcon, EraserIcon, InfoIcon } from '@lucide/vue'
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
import { groupName, groupOptions, loadGroups } from '../refs'
import { serverTypes, typeLabel } from '../serverSpec'
import type { Tone, PageResult } from '../helpers'
import type { AuditLog, AuditLogDetail, AuditRule } from '../types'

const router = useRouter()

const actions: { label: string; value: AuditRule['action']; tone: Tone }[] = [
  { label: '记录', value: 'log', tone: 'info' },
  { label: '阻断', value: 'block', tone: 'warning' },
  { label: '封禁用户', value: 'ban', tone: 'danger' },
]
const actionOf = (v: string) => actions.find((a) => a.value === v)
const patternTip = [
  '# 每行一条，# 开头为注释',
  'example.com          域名及其子域名（也可写 *.example.com）',
  'full:example.com     只匹配该域名',
  'keyword:torrent      域名包含',
  'regexp:^dl\\d+\\.     域名正则',
  '1.2.3.4              IP',
  '10.0.0.0/8           IP 段',
  '1.2.3.4:25           IP + 端口（IPv6 写作 [2001:db8::1]:25）',
  'example.com:443      域名 + 端口',
  ':25                  只匹配端口（也可写端口段 :6881-6889）',
].join('\n')

// ---------- 规则 ----------
const rules = useList(() => admin.get<AuditRule[]>('/audit/rules'))
const ruleCols: Column[] = [
  { key: 'sort', title: '排序', class: 'w-16' },
  { key: 'enabled', title: '启用', align: 'center', class: 'w-16' },
  { key: 'name', title: '名称' },
  { key: 'pattern', title: '匹配内容' },
  { key: 'action', title: '动作' },
  { key: 'group_id', title: '生效权限组' },
  { key: 'hit_count', title: '命中' },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]
const patternLines = (p: string) => (p || '').split('\n').map((s) => s.trim()).filter((s) => s && !s.startsWith('#'))
const groupsText = (ids: number[] | null) => (ids?.length ? ids.map(groupName).join('、') : '全部节点')

interface RuleForm {
  name: string
  pattern: string
  action: AuditRule['action']
  group_id: number[]
  enabled: boolean
  sort: number | null
}
const ruleShow = ref(false)
const ruleSaving = ref(false)
const ruleEditing = ref<AuditRule | null>(null)
const ruleForm = ref<RuleForm>(blankRule())
function blankRule(): RuleForm {
  return { name: '', pattern: '', action: 'log', group_id: [], enabled: true, sort: 0 }
}
function openRule(r: AuditRule | null) {
  ruleEditing.value = r
  ruleForm.value = r
    ? { name: r.name, pattern: r.pattern, action: r.action, group_id: [...(r.group_id || [])], enabled: !!r.enabled, sort: r.sort }
    : blankRule()
  ruleShow.value = true
}
const bodyOf = (f: RuleForm) => ({ ...f, name: f.name.trim(), sort: f.sort ?? 0 })
async function saveRule() {
  const f = ruleForm.value
  if (!f.name.trim()) {
    message.warning('请输入规则名称')
    return
  }
  if (!patternLines(f.pattern).length) {
    message.warning('请输入匹配内容')
    return
  }
  ruleSaving.value = true
  const body = bodyOf(f)
  const ok = await run(
    () => (ruleEditing.value ? admin.put(`/audit/rules/${ruleEditing.value.id}`, body) : admin.post('/audit/rules', body)),
    '保存成功',
  )
  ruleSaving.value = false
  if (ok) {
    ruleShow.value = false
    rules.load()
  }
}
async function toggleRule(r: AuditRule, v: boolean) {
  const body = { name: r.name, pattern: r.pattern, action: r.action, group_id: r.group_id || [], enabled: v, sort: r.sort }
  if (await run(() => admin.put(`/audit/rules/${r.id}`, body))) r.enabled = v ? 1 : 0
}
async function removeRule(r: AuditRule) {
  if (await confirm(`确定要删除规则「${r.name}」吗？已有的审计日志会保留。`, () => admin.del(`/audit/rules/${r.id}`), '删除成功')) rules.load()
}

// ---------- 测试 ----------
interface TestHit {
  id: number
  name: string
  action: AuditRule['action']
  enabled: 0 | 1
  line: string
}
const testTarget = ref('')
const testHits = ref<TestHit[] | null>(null)
async function test() {
  if (!testTarget.value.trim()) {
    message.warning('请输入要测试的目标')
    return
  }
  await run(async () => {
    testHits.value = await admin.post<TestHit[]>('/audit/test', { target: testTarget.value.trim() })
  })
}

// ---------- 审计日志 ----------
const logQ = ref({
  user_id: null as number | null,
  subscription_id: null as number | null,
  rule_id: null as number | null,
  action: null as string | null,
  target: '',
  ip: '',
  server_type: null as string | null,
  server_id: null as number | null,
  banned: null as number | null,
  since: null as number | null,
  until: null as number | null,
})
const logs = usePaged<AuditLog>(async (current, page_size) => {
  const q = logQ.value
  const r = await admin.get<PageResult<AuditLog>>('/audit/logs', {
    current,
    page_size,
    user_id: q.user_id ?? undefined,
    subscription_id: q.subscription_id ?? undefined,
    rule_id: q.rule_id ?? undefined,
    action: q.action || undefined,
    target: q.target.trim() || undefined,
    ip: q.ip.trim() || undefined,
    server_type: q.server_type || undefined,
    server_id: q.server_id ?? undefined,
    banned: q.banned ?? undefined,
    since: q.since ?? undefined,
    until: q.until ?? undefined,
  })
  return { rows: r.data || [], total: r.total }
})
const logCols: Column[] = [
  { key: 'hit_at', title: '时间' },
  { key: 'user_id', title: '用户 / 订阅' },
  { key: 'target', title: '访问目标' },
  { key: 'rule_id', title: '规则' },
  { key: 'action', title: '动作' },
  { key: 'server_id', title: '节点' },
  { key: 'ip', title: '来源 IP' },
  { key: 'count', title: '次数', align: 'right' },
]
function resetLogQ() {
  logQ.value = {
    user_id: null, subscription_id: null, rule_id: null, action: null, target: '', ip: '',
    server_type: null, server_id: null, banned: null, since: null, until: null,
  }
  logs.reload()
}
function filterLogs(patch: Partial<typeof logQ.value>) {
  Object.assign(logQ.value, patch)
  tab.value = 'logs'
  logs.reload()
}
async function cleanLogs() {
  await run(async () => {
    const r = await admin.post<{ deleted: number }>('/audit/logs/clean')
    message.success(`已清理 ${r.deleted} 条过期日志`)
    logs.reload()
  })
}

const detailShow = ref(false)
const detailLoading = ref(false)
const detailRow = ref<AuditLog | null>(null)
const detailList = ref<AuditLogDetail[]>([])
async function openDetails(row: AuditLog) {
  detailRow.value = row
  detailShow.value = true
  detailLoading.value = true
  detailList.value = []
  try {
    detailList.value = await admin.get<AuditLogDetail[]>(`/audit/logs/${row.id}/details`)
  } catch {
    message.error('加载访问细节失败')
    detailShow.value = false
  } finally {
    detailLoading.value = false
  }
}

const tab = ref('rules')
function onTab(v: string | number) {
  if (v === 'logs' && !logs.rows.value.length) logs.load()
}

onMounted(() => {
  rules.load()
  loadGroups().catch(() => [])
})
</script>

<template>
  <div>
    <PageHeader title="审计规则" description="节点按规则记录、阻断访问指定网站的连接，或直接封禁访问者的账号。" />
    <p class="mb-4 text-sm text-amber-700 dark:text-amber-400">
      必须搭配 gvboard 版 v2node，并在节点配置中开启 <code class="rounded bg-muted px-1 font-mono text-xs">AuditEnable</code>（节点机模式在“节点机”页面为节点开启审计）才能使用。
    </p>

    <Tabs v-model="tab" @update:model-value="onTab">
      <TabsList class="mb-4">
        <TabsTrigger value="rules">规则</TabsTrigger>
        <TabsTrigger value="logs">审计日志</TabsTrigger>
      </TabsList>

      <TabsContent value="rules">
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <div class="flex basis-full items-start sm:min-w-72 sm:flex-1 gap-2 text-sm text-muted-foreground">
            <InfoIcon class="mt-0.5 size-4 shrink-0" />
            <span>
              只有开启了审计（AuditEnable）的 v2node 节点会拉取并执行规则。一个连接命中多条规则时按最严重的动作处理（封禁用户 &gt; 阻断 &gt; 记录）。
              节点不做 DNS 解析：域名规则只匹配客户端请求或嗅探到的域名，IP 规则只匹配直接以 IP 访问的连接。
            </span>
          </div>
          <Button variant="ghost" size="icon" :disabled="rules.loading.value" @click="rules.load"><RefreshCwIcon :class="rules.loading.value ? 'animate-spin' : ''" /></Button>
          <Button @click="openRule(null)"><PlusIcon />添加规则</Button>
        </div>

        <Card class="mb-3 py-3">
          <CardContent class="flex flex-wrap items-center gap-2 px-3">
            <span class="text-sm font-medium">测试目标</span>
            <Input v-model="testTarget" class="w-72" placeholder="如 example.com:443 或 1.2.3.4:25" @keyup.enter="test" />
            <Button variant="outline" size="sm" @click="test"><SearchIcon />测试</Button>
            <template v-if="testHits">
              <span v-if="!testHits.length" class="text-sm text-muted-foreground">没有命中任何规则</span>
              <span v-for="h in testHits" :key="h.id" class="flex items-center gap-1 text-sm">
                <StatusBadge :tone="h.enabled ? actionOf(h.action)?.tone || 'default' : 'default'">{{ actionOf(h.action)?.label }}</StatusBadge>
                <span>{{ h.name }}<template v-if="!h.enabled">（未启用）</template></span>
                <code class="rounded bg-muted px-1 font-mono text-xs">{{ h.line }}</code>
              </span>
            </template>
          </CardContent>
        </Card>

        <DataTable :columns="ruleCols" :rows="rules.rows.value" :loading="rules.loading.value">
          <template #cell-sort="{ row }"><span class="tabular-nums">{{ row.sort }}</span></template>
          <template #cell-enabled="{ row }"><Switch :model-value="!!row.enabled" @update:model-value="(v: boolean) => toggleRule(row, v)" /></template>
          <template #cell-name="{ row }"><span class="font-medium">{{ row.name }}</span></template>
          <template #cell-pattern="{ row }">
            <span class="line-clamp-2 max-w-md font-mono text-xs" :title="patternLines(row.pattern).join('\n')">
              {{ patternLines(row.pattern).slice(0, 5).join(', ') }}<template v-if="patternLines(row.pattern).length > 5"> 等 {{ patternLines(row.pattern).length }} 条</template>
            </span>
          </template>
          <template #cell-action="{ row }"><StatusBadge :tone="actionOf(row.action)?.tone || 'default'">{{ actionOf(row.action)?.label || row.action }}</StatusBadge></template>
          <template #cell-group_id="{ row }"><span class="text-xs">{{ groupsText(row.group_id) }}</span></template>
          <template #cell-hit_count="{ row }">
            <button type="button" class="tabular-nums hover:text-primary" title="查看该规则的审计日志" @click="filterLogs({ rule_id: row.id })">{{ row.hit_count }}</button>
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
            <NumInput v-model="logQ.user_id" placeholder="用户 ID" />
            <NumInput v-model="logQ.subscription_id" placeholder="订阅 ID" />
            <SelectBox
              :model-value="logQ.rule_id"
              :options="[{ label: '全部规则', value: null }, ...rules.rows.value.map((r) => ({ label: r.name, value: r.id as number | null }))]"
              placeholder="规则"
              @update:model-value="(v) => (logQ.rule_id = v as number | null)"
            />
            <SelectBox
              :model-value="logQ.action"
              :options="[{ label: '全部动作', value: null }, ...actions.map((a) => ({ label: a.label, value: a.value as string | null }))]"
              placeholder="动作"
              @update:model-value="(v) => (logQ.action = v as string | null)"
            />
            <Input v-model="logQ.target" placeholder="访问目标包含" />
            <Input v-model="logQ.ip" placeholder="来源 IP" />
            <SelectBox
              :model-value="logQ.server_type"
              :options="[{ label: '全部节点类型', value: null }, ...serverTypes.map((s) => ({ label: s.label, value: s.value as string | null }))]"
              placeholder="节点类型"
              @update:model-value="(v) => (logQ.server_type = v as string | null)"
            />
            <NumInput v-model="logQ.server_id" placeholder="节点 ID" />
            <SelectBox
              :model-value="logQ.banned"
              :options="[{ label: '全部记录', value: null }, { label: '导致封禁', value: 1 }, { label: '未导致封禁', value: 0 }]"
              placeholder="是否封禁"
              @update:model-value="(v) => (logQ.banned = v as number | null)"
            />
            <DateTimeInput v-model="logQ.since" />
            <DateTimeInput v-model="logQ.until" />
            <div class="flex gap-2 xl:justify-end">
              <Button @click="logs.reload"><SearchIcon />查询</Button>
              <Button variant="outline" @click="resetLogQ">重置</Button>
            </div>
            <div class="col-span-2 flex justify-end md:col-span-4 xl:col-span-6">
              <Button variant="outline" size="sm" @click="cleanLogs"><EraserIcon />清理过期日志</Button>
            </div>
          </CardContent>
        </Card>
        <DataTable :columns="logCols" :rows="logs.rows.value" :loading="logs.loading.value">
          <template #cell-hit_at="{ row }"><span class="whitespace-nowrap text-xs">{{ formatTime(row.hit_at) }}</span></template>
          <template #cell-user_id="{ row }">
            <button
              type="button"
              class="block max-w-48 truncate text-left text-primary hover:underline"
              :title="`${row.email}（在用户管理中查看）`"
              @click="router.push({ path: '/users', query: { id: row.user_id } })"
            >
              {{ row.email || `#${row.user_id}` }}
            </button>
            <button type="button" class="text-xs text-muted-foreground hover:text-primary" @click="filterLogs({ subscription_id: row.subscription_id })">订阅 #{{ row.subscription_id }}</button>
          </template>
          <template #cell-target="{ row }">
            <button type="button" class="block max-w-xs truncate text-left font-mono text-xs text-primary hover:underline" :title="row.target" @click="openDetails(row)">
              {{ row.target }}
            </button>
            <button type="button" class="text-xs text-muted-foreground hover:text-primary" @click="openDetails(row)">
              <span v-if="row.network" class="uppercase">{{ row.network }}</span>
              <template v-if="(row.detail_count || 1) > 1"> · {{ row.detail_count }} 个目标</template>
              <template v-else> · 查看细节</template>
            </button>
          </template>
          <template #cell-rule_id="{ row }">{{ row.rule_name || `#${row.rule_id}（已删除）` }}</template>
          <template #cell-action="{ row }">
            <div class="flex flex-wrap gap-1">
              <StatusBadge :tone="actionOf(row.action)?.tone || 'default'">{{ actionOf(row.action)?.label || row.action }}</StatusBadge>
              <StatusBadge v-if="row.banned" tone="danger">已封禁账号</StatusBadge>
            </div>
          </template>
          <template #cell-server_id="{ row }">
            <span class="block max-w-40 truncate text-xs" :title="`${typeLabel(row.server_type)} #${row.server_id}`">
              {{ row.server_name || `${typeLabel(row.server_type)} #${row.server_id}` }}
            </span>
          </template>
          <template #cell-ip="{ row }"><span class="font-mono text-xs">{{ row.ip || '-' }}</span></template>
          <template #cell-count="{ row }"><span class="tabular-nums">{{ row.count }}</span></template>
        </DataTable>
        <Pager :pager="logs.pager" @page="logs.setPage" @size="logs.setPageSize" />
      </TabsContent>
    </Tabs>

    <Dialog v-model:open="detailShow">
      <DialogContent class="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>访问细节</DialogTitle>
        </DialogHeader>
        <p v-if="detailRow" class="text-sm text-muted-foreground">
          {{ detailRow.email || `#${detailRow.user_id}` }} · {{ detailRow.rule_name || `规则 #${detailRow.rule_id}` }} · 合计 {{ detailRow.count }} 次
        </p>
        <div v-if="detailLoading" class="flex justify-center py-10"><Loader2Icon class="size-6 animate-spin text-muted-foreground" /></div>
        <div v-else class="overflow-hidden rounded-lg border">
          <table class="w-full text-sm">
            <thead class="bg-muted/40 text-xs text-muted-foreground">
              <tr>
                <th class="px-3 py-2 text-left font-medium">访问目标</th>
                <th class="px-3 py-2 text-left font-medium">来源 IP</th>
                <th class="px-3 py-2 text-right font-medium">次数</th>
                <th class="px-3 py-2 text-left font-medium">最后命中</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="d in detailList" :key="d.id" class="border-t">
                <td class="px-3 py-2 font-mono text-xs">
                  {{ d.target }}
                  <span v-if="d.network" class="ml-1 text-muted-foreground uppercase">{{ d.network }}</span>
                </td>
                <td class="px-3 py-2 font-mono text-xs">{{ d.ip || '-' }}</td>
                <td class="px-3 py-2 text-right tabular-nums">{{ d.count }}</td>
                <td class="px-3 py-2 whitespace-nowrap text-xs">{{ formatTime(d.hit_at) }}</td>
              </tr>
              <tr v-if="!detailList.length"><td colspan="4" class="px-3 py-8 text-center text-muted-foreground">暂无细节</td></tr>
            </tbody>
          </table>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="detailShow = false">关闭</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="ruleShow">
      <DialogContent class="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader><DialogTitle>{{ ruleEditing ? '编辑规则' : '添加规则' }}</DialogTitle></DialogHeader>
        <div class="grid gap-4 sm:grid-cols-2">
          <FormItem label="名称" class="sm:col-span-2"><Input v-model="ruleForm.name" maxlength="64" /></FormItem>
          <FormItem label="匹配内容" class="sm:col-span-2" help="每行一条，任一行命中即命中本规则">
            <Textarea v-model="ruleForm.pattern" rows="10" class="font-mono text-xs" :placeholder="patternTip" />
          </FormItem>
          <FormItem label="动作">
            <SelectBox
              :model-value="ruleForm.action"
              :options="actions.map((a) => ({ label: a.label, value: a.value }))"
              @update:model-value="(v) => (ruleForm.action = v as AuditRule['action'])"
            />
          </FormItem>
          <FormItem label="排序" help="数字越小越靠前">
            <NumInput v-model="ruleForm.sort" :min="-1000000" :max="1000000" />
          </FormItem>
          <p v-if="ruleForm.action === 'ban'" class="text-sm text-destructive sm:col-span-2">
            封禁用户：节点阻断连接并在本节点暂停该用户 10 分钟；面板收到上报后封禁整个账号、退出全部登录，并通过 Telegram 通知管理员。管理员账号只记录不封禁。
          </p>
          <FormItem label="生效权限组" class="sm:col-span-2" help="下发给属于所选权限组的节点；不选则下发给全部节点">
            <MultiSelect :model-value="ruleForm.group_id" :options="groupOptions" placeholder="全部节点" @update:model-value="(v) => (ruleForm.group_id = v.map(Number))" />
          </FormItem>
          <label class="flex items-center gap-2 text-sm sm:col-span-2"><Switch v-model="ruleForm.enabled" />启用</label>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="ruleShow = false">取消</Button>
          <Button :disabled="ruleSaving" @click="saveRule"><Loader2Icon v-if="ruleSaving" class="animate-spin" />保存</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
