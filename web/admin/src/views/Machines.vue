<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { PlusIcon, RefreshCwIcon, PencilIcon, Trash2Icon, Loader2Icon, KeyRoundIcon, BanIcon, CopyIcon, InfoIcon, CircleArrowUpIcon, SearchCheckIcon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import MultiSelect from '../components/common/MultiSelect.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { useList } from '../paged'
import type { NodeMachine, ServerRow, V2nodeRelease } from '../types'

const installScript = 'https://raw.githubusercontent.com/gunzi-666/v2node/gvboard/script/install.sh'
const logLevels = ['debug', 'info', 'warning', 'error', 'none'].map((v) => ({ label: v, value: v }))

const list = useList(() => admin.get<NodeMachine[]>('/machines'))
const servers = ref<ServerRow[]>([])
const v2nodes = computed(() => servers.value.filter((s) => s.type === 'v2node'))
const serverName = (id: number) => v2nodes.value.find((s) => s.id === id)?.name || `#${id}（已删除）`
const panelURL = ref('')

const cols: Column[] = [
  { key: 'name', title: '名称' },
  { key: 'status', title: '状态' },
  { key: 'server_ids', title: '运行的节点' },
  { key: 'host', title: '服务器' },
  { key: 'last_seen_at', title: '最后连接' },
  { key: 'op', title: '操作', align: 'right', class: 'w-44' },
]

function status(m: NodeMachine): { label: string; tone: 'success' | 'warning' | 'danger' | 'default' | 'info' } {
  if (m.enrolled) return m.online ? { label: '在线', tone: 'success' } : { label: '离线', tone: 'danger' }
  if (m.enroll_expires_at && m.enroll_expires_at * 1000 > Date.now()) return { label: '等待接入', tone: 'info' }
  return { label: '未接入', tone: 'default' }
}

// ---------- 新建 / 编辑 ----------
interface Form {
  name: string
  server_ids: number[]
  audit_ids: number[]
  log_level: string
}
const show = ref(false)
const saving = ref(false)
const editing = ref<NodeMachine | null>(null)
const form = ref<Form>({ name: '', server_ids: [], audit_ids: [], log_level: 'info' })
// 已属于其他节点机的节点不可选
const serverOptions = computed(() => {
  const used = new Set(list.rows.value.filter((m) => m.id !== editing.value?.id).flatMap((m) => m.server_ids))
  return v2nodes.value.filter((s) => !used.has(s.id)).map((s) => ({ label: `${s.name}（#${s.id}）`, value: s.id }))
})
const auditOptions = computed(() => serverOptions.value.filter((o) => form.value.server_ids.includes(o.value)))

function open(m: NodeMachine | null) {
  editing.value = m
  form.value = m
    ? { name: m.name, server_ids: [...m.server_ids], audit_ids: [...m.audit_ids], log_level: m.log_level || 'info' }
    : { name: '', server_ids: [], audit_ids: [], log_level: 'info' }
  show.value = true
}
function setServers(v: (string | number)[]) {
  form.value.server_ids = v.map(Number)
  form.value.audit_ids = form.value.audit_ids.filter((id) => form.value.server_ids.includes(id))
}

interface Created {
  machine: NodeMachine
  code: string
  expires_at: number
}
async function save() {
  const f = form.value
  if (!f.name.trim()) {
    message.warning('请输入名称')
    return
  }
  saving.value = true
  let created: Created | null = null
  const ok = await run(async () => {
    if (editing.value) await admin.put(`/machines/${editing.value.id}`, f)
    else created = await admin.post<Created>('/machines', f)
  }, editing.value ? '保存成功，节点机下次拉取配置时生效' : undefined)
  saving.value = false
  if (!ok) return
  show.value = false
  list.load()
  if (created) showInstall(created)
}

// ---------- 安装命令 ----------
const install = ref<Created | null>(null)
const installShow = ref(false)
const command = computed(() => {
  if (!install.value) return ''
  const panel = panelURL.value.trim().replace(/\/+$/, '')
  return `wget -N ${installScript} && bash install.sh --panel ${panel} --code ${install.value.code}`
})
function showInstall(c: Created) {
  install.value = c
  installShow.value = true
}
async function copy() {
  try {
    await navigator.clipboard.writeText(command.value)
    message.success('已复制')
  } catch {
    message.warning('复制失败，请手动选择复制')
  }
}

async function reenroll(m: NodeMachine) {
  const tip = m.enrolled
    ? `重新生成安装命令会立即吊销「${m.name}」现有的凭据，这台服务器上的节点会全部停止，直到用新命令重新接入。确定继续吗？`
    : `为「${m.name}」重新生成安装命令？之前的命令将失效。`
  await confirm(tip, async () => {
    const c = await admin.post<Created>(`/machines/${m.id}/enroll`)
    list.load()
    showInstall(c)
  }, '已生成', m.enrolled)
}
async function revoke(m: NodeMachine) {
  const tip = `确定要吊销「${m.name}」的凭据吗？这台服务器上的节点会全部停止，之后需要重新生成安装命令才能接入。`
  if (await confirm(tip, () => admin.post(`/machines/${m.id}/revoke`), '已吊销')) list.load()
}
async function remove(m: NodeMachine) {
  const tip = m.enrolled
    ? `确定要删除「${m.name}」吗？凭据随之失效，这台服务器上的节点会全部停止。`
    : `确定要删除「${m.name}」吗？`
  if (await confirm(tip, () => admin.del(`/machines/${m.id}`), '删除成功')) list.load()
}

// ---------- 远程更新 v2node ----------
const selected = ref<(string | number)[]>([])
const releases = ref<V2nodeRelease[]>([])
const checking = ref(false)
// 只有管理员点击「检查更新」时才查询 GitHub
const latest = computed(() => releases.value[0]?.version || '')
const releaseOptions = computed(() =>
  releases.value.map((r) => ({
    label: `${r.version}${r.prerelease ? '（预发布）' : ''}${r.published_at ? ' · ' + r.published_at.slice(0, 10) : ''}`,
    value: r.version,
  })),
)
const outdated = (m: NodeMachine) => !!latest.value && m.enrolled && !!m.version && m.version !== latest.value && !m.update_version

async function checkUpdate() {
  checking.value = true
  const ok = await run(async () => {
    releases.value = await admin.get<V2nodeRelease[]>('/machines/releases')
  })
  checking.value = false
  if (ok) {
    if (upShow.value && !upVersion.value.trim()) upVersion.value = latest.value
    if (latest.value) message.success(`最新版本 ${latest.value}`)
    else message.warning('没有找到可用的 gvboard 版 v2node')
  }
}

const upShow = ref(false)
const upIDs = ref<number[]>([])
const upVersion = ref('')
const upSaving = ref(false)
const upNames = computed(() => list.rows.value.filter((m) => upIDs.value.includes(m.id)).map((m) => m.name).join('、'))
function openUpdate(ids: number[]) {
  upIDs.value = ids
  upVersion.value = latest.value
  upShow.value = true
}
async function submitUpdate() {
  const version = upVersion.value.trim()
  if (!version) {
    message.warning('请选择或输入版本号')
    return
  }
  upSaving.value = true
  let n = 0
  const ok = await run(async () => {
    n = await admin.post<number>('/machines/update', { ids: upIDs.value, version })
  })
  upSaving.value = false
  if (!ok) return
  message.success(`已向 ${n} 台节点机下发更新，节点机下次拉取配置时开始更新`)
  upShow.value = false
  selected.value = []
  list.load()
}
async function retryUpdate(m: NodeMachine) {
  if (await run(() => admin.post('/machines/update', { ids: [m.id], version: m.update_version }), '已重新下发')) list.load()
}
async function cancelUpdate(m: NodeMachine) {
  if (await run(() => admin.post(`/machines/${m.id}/update/cancel`), '已取消')) list.load()
}

onMounted(async () => {
  list.load()
  servers.value = (await admin.get<ServerRow[]>('/servers').catch(() => [])) || []
  const cfg = await admin.get<Record<string, Record<string, unknown>>>('/config', { group: 'server' }).catch(() => null)
  panelURL.value = String(cfg?.server?.server_api_url || '') || location.origin
})
</script>

<template>
  <div>
    <PageHeader title="节点机" description="在服务器上执行一条安装命令即可接入，节点配置由面板下发，不需要分发通讯密钥或编写配置文件。" />
    <p class="mb-4 text-sm text-amber-700 dark:text-amber-400">只支持 gvboard 版 v2node，节点类型必须是 v2node。原有配置文件方式不受影响，两种方式可以同时使用。</p>

    <div class="mb-3 flex flex-wrap items-center gap-2">
      <div class="flex basis-full items-start sm:min-w-72 sm:flex-1 gap-2 text-sm text-muted-foreground">
        <InfoIcon class="mt-0.5 size-4 shrink-0" />
        <span>安装命令中的接入码 30 分钟内有效，只能使用一次。吊销或删除后，节点机会停止本面板的全部节点（同一台服务器接入的其它面板不受影响）；修改节点、审计或日志级别后，节点机在下次拉取配置时自动生效。</span>
      </div>
      <Button variant="ghost" size="icon" :disabled="list.loading.value" @click="list.load"><RefreshCwIcon :class="list.loading.value ? 'animate-spin' : ''" /></Button>
      <Button variant="outline" :disabled="checking" title="从 GitHub 查询 gvboard 版 v2node 的最新版本" @click="checkUpdate">
        <Loader2Icon v-if="checking" class="animate-spin" /><SearchCheckIcon v-else />检查更新
      </Button>
      <Button v-if="selected.length" variant="outline" @click="openUpdate(selected.map(Number))"><CircleArrowUpIcon />更新所选（{{ selected.length }}）</Button>
      <Button @click="open(null)"><PlusIcon />添加节点机</Button>
    </div>

    <DataTable v-model:selected="selected" selectable :columns="cols" :rows="list.rows.value" :loading="list.loading.value">
      <template #cell-name="{ row }"><span class="font-medium">{{ row.name }}</span></template>
      <template #cell-status="{ row }">
        <StatusBadge :tone="status(row).tone">{{ status(row).label }}</StatusBadge>
        <div v-if="status(row).label === '等待接入'" class="mt-0.5 text-xs whitespace-nowrap text-muted-foreground" :title="`${formatTime(row.enroll_expires_at)} 前有效`">
          {{ formatTime(row.enroll_expires_at).slice(-5) }} 前有效
        </div>
      </template>
      <template #cell-server_ids="{ row }">
        <div class="flex max-w-sm flex-wrap gap-1">
          <span v-for="id in row.server_ids" :key="id" class="rounded bg-muted px-1.5 py-0.5 text-xs">
            {{ serverName(id) }}<template v-if="row.audit_ids.includes(id)"> · 审计</template>
          </span>
          <span v-if="!row.server_ids.length" class="text-xs text-muted-foreground">未分配</span>
        </div>
        <div class="mt-0.5 text-xs text-muted-foreground">日志级别 {{ row.log_level || 'info' }}</div>
        <div v-if="row.node_errors?.length" class="mt-1 max-w-sm space-y-0.5 text-xs text-destructive">
          <div v-for="e in row.node_errors" :key="e.node_id" class="break-all">{{ serverName(e.node_id) }} 未运行：{{ e.error }}</div>
        </div>
      </template>
      <template #cell-host="{ row }">
        <template v-if="row.enrolled_at">
          <div class="max-w-44 truncate text-xs" :title="row.hostname">{{ row.hostname || '-' }}</div>
          <div class="font-mono text-xs text-muted-foreground">{{ row.ip || '-' }}</div>
          <div v-if="row.version" class="flex max-w-44 items-center gap-1 font-mono text-xs text-muted-foreground" :title="row.version">
            <span class="truncate">{{ row.version }}</span>
            <span v-if="outdated(row)" class="shrink-0 rounded bg-amber-100 px-1 font-sans text-amber-700 dark:bg-amber-950 dark:text-amber-400">可更新</span>
          </div>
        </template>
        <span v-else class="text-xs text-muted-foreground">-</span>
        <div v-if="row.update_version" class="mt-1 max-w-44 text-xs">
          <template v-if="row.update_error">
            <div class="line-clamp-2 break-all text-destructive" :title="row.update_error">更新到 {{ row.update_version }} 失败：{{ row.update_error }}</div>
            <button class="mr-2 text-primary hover:underline" @click="retryUpdate(row)">重试</button>
          </template>
          <div v-else class="truncate text-amber-700 dark:text-amber-400" :title="`下次拉取配置时更新到 ${row.update_version}`">等待更新到 {{ row.update_version }}</div>
          <button class="text-muted-foreground hover:underline" @click="cancelUpdate(row)">取消更新</button>
        </div>
      </template>
      <template #cell-last_seen_at="{ row }"><span class="whitespace-nowrap text-xs">{{ row.last_seen_at ? formatTime(row.last_seen_at) : '-' }}</span></template>
      <template #cell-op="{ row }">
        <div class="flex justify-end gap-0.5">
          <Button v-if="row.enrolled" variant="ghost" size="icon" class="size-8" title="更新 v2node" @click="openUpdate([row.id])"><CircleArrowUpIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8" title="编辑" @click="open(row)"><PencilIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8" title="生成安装命令" @click="reenroll(row)"><KeyRoundIcon /></Button>
          <Button v-if="row.enrolled" variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" title="吊销凭据" @click="revoke(row)"><BanIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" title="删除" @click="remove(row)"><Trash2Icon /></Button>
        </div>
      </template>
    </DataTable>

    <Dialog v-model:open="show">
      <DialogContent class="max-h-[92svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader><DialogTitle>{{ editing ? '编辑节点机' : '添加节点机' }}</DialogTitle></DialogHeader>
        <div class="grid gap-4">
          <FormItem label="名称"><Input v-model="form.name" maxlength="64" placeholder="如 香港-01" /></FormItem>
          <FormItem label="运行的节点" help="只能选择 v2node 节点，一个节点只能属于一台节点机">
            <MultiSelect :model-value="form.server_ids" :options="serverOptions" placeholder="选择节点" @update:model-value="setServers" />
          </FormItem>
          <FormItem label="开启审计的节点" help="开启后节点拉取并执行审计规则">
            <MultiSelect :model-value="form.audit_ids" :options="auditOptions" placeholder="不开启" @update:model-value="(v) => (form.audit_ids = v.map(Number))" />
          </FormItem>
          <FormItem label="日志级别">
            <SelectBox :model-value="form.log_level" :options="logLevels" @update:model-value="(v) => (form.log_level = String(v))" />
          </FormItem>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="show = false">取消</Button>
          <Button :disabled="saving" @click="save"><Loader2Icon v-if="saving" class="animate-spin" />{{ editing ? '保存' : '添加并生成安装命令' }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="upShow">
      <DialogContent class="sm:max-w-lg">
        <DialogHeader><DialogTitle>更新 v2node</DialogTitle></DialogHeader>
        <div class="grid gap-4">
          <p class="text-sm text-muted-foreground">节点机：{{ upNames }}</p>
          <FormItem label="版本" help="点击查询后默认填入最新版本，也可以选择旧版本回退或手动输入；未接入或已是该版本的节点机会跳过">
            <div class="grid gap-2">
              <div class="flex gap-2">
                <Input v-model="upVersion" class="flex-1 font-mono" placeholder="如 v0.4.9-gvboard.3" />
                <Button variant="outline" :disabled="checking" @click="checkUpdate"><Loader2Icon v-if="checking" class="animate-spin" /><SearchCheckIcon v-else />查询</Button>
              </div>
              <SelectBox
                v-if="releaseOptions.length"
                :model-value="upVersion"
                :options="releaseOptions"
                placeholder="从已发布的版本中选择"
                @update:model-value="(v) => (upVersion = String(v))"
              />
            </div>
          </FormItem>
          <p class="text-sm text-amber-700 dark:text-amber-400">
            节点机下次拉取配置时从 GitHub 下载该版本、校验后替换程序并自动重启，节点会中断几秒。失败时保留原程序继续运行，失败原因显示在列表中。需要节点机已运行 v0.4.9-gvboard.3 及以上版本，更早的版本请登录服务器重新执行安装脚本。
          </p>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="upShow = false">取消</Button>
          <Button :disabled="upSaving" @click="submitUpdate"><Loader2Icon v-if="upSaving" class="animate-spin" />下发更新</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="installShow">
      <DialogContent class="sm:max-w-2xl">
        <DialogHeader><DialogTitle>安装命令</DialogTitle></DialogHeader>
        <div class="grid gap-3">
          <FormItem label="面板地址" help="节点机访问面板的地址，默认取系统配置中的“节点对接API地址”">
            <Input v-model="panelURL" placeholder="https://" />
          </FormItem>
          <FormItem label="在服务器上以 root 执行">
            <Textarea :model-value="command" readonly rows="4" class="font-mono text-xs" />
          </FormItem>
          <p class="text-sm text-amber-700 dark:text-amber-400">
            命令只显示这一次，{{ install ? formatTime(install.expires_at) : '' }} 前有效，使用一次后失效。已安装 v2node 的服务器执行后改为节点机模式，原配置文件保留但不再使用。
          </p>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="installShow = false">关闭</Button>
          <Button @click="copy"><CopyIcon />复制命令</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
