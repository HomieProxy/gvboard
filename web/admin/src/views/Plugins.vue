<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { UploadIcon, RefreshCwIcon, InfoIcon, SettingsIcon, Trash2Icon, Loader2Icon, TriangleAlertIcon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Switch } from '@gv/shared/ui/switch'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import NumInput from '../components/common/NumInput.vue'
import { admin } from '../api'
import { confirm, errText, message, run } from '../ui'
import { useList } from '../paged'
import type { PluginField, PluginPreview, PluginView } from '../types'

const { rows, loading, load } = useList(() => admin.get<PluginView[]>('/plugins'))

const columns: Column[] = [
  { key: 'enabled', title: '启用', align: 'center', class: 'w-16' },
  { key: 'name', title: '插件' },
  { key: 'author', title: '作者' },
  { key: 'installed_at', title: '安装时间' },
  { key: 'op', title: '操作', align: 'right', class: 'w-32' },
]

async function toggle(p: PluginView, v: boolean) {
  if (await run(() => admin.post(`/plugins/${p.code}/${v ? 'enable' : 'disable'}`), v ? '已启用' : '已停用')) load()
}
async function remove(p: PluginView) {
  if (await confirm(`删除插件「${p.name}」后，它保存的数据也会一并删除，确定吗？`, () => admin.del(`/plugins/${p.code}`), '删除成功')) load()
}
const canRemove = (p: PluginView) => (p.type === 'js' || p.status === 'missing') && !p.enabled

// ---------- 详情 ----------
const detail = ref<PluginView | null>(null)
const detailShow = ref(false)
function openDetail(p: PluginView) {
  detail.value = p
  detailShow.value = true
}
const join = (a: string[]) => a.join('、') || '-'

// ---------- 配置 ----------
const cfgPlugin = ref<PluginView | null>(null)
const cfgShow = ref(false)
const cfgSaving = ref(false)
const cfgValues = ref<Record<string, unknown>>({})
function openConfig(p: PluginView) {
  cfgPlugin.value = p
  cfgValues.value = Object.fromEntries(p.fields.map((f) => [f.key, p.config?.[f.key] ?? f.default ?? (f.type === 'bool' ? false : f.type === 'number' ? null : '')]))
  cfgShow.value = true
}
async function saveConfig() {
  if (!cfgPlugin.value) return
  cfgSaving.value = true
  const ok = await run(() => admin.put(`/plugins/${cfgPlugin.value!.code}/config`, { config: cfgValues.value }), '保存成功')
  cfgSaving.value = false
  if (ok) {
    cfgShow.value = false
    load()
  }
}
const selectOptions = (f: PluginField) => (f.options || []).map((o) => ({ label: o.label, value: o.value }))

// ---------- 上传安装 ----------
const upShow = ref(false)
const file = ref<File | null>(null)
const preview = ref<PluginPreview | null>(null)
const uploading = ref(false)

function onFile(e: Event) {
  file.value = (e.target as HTMLInputElement).files?.[0] || null
  preview.value = null
}
async function upload(confirmed: boolean) {
  if (!file.value) {
    message.warning('请选择 zip 安装包')
    return
  }
  const fd = new FormData()
  fd.append('file', file.value)
  if (confirmed && preview.value) fd.append('sha256', preview.value.sha256)
  uploading.value = true
  try {
    if (!confirmed) {
      preview.value = await admin.upload<PluginPreview>('/plugins/upload', fd)
    } else {
      await admin.upload('/plugins/upload', fd)
      message.success(preview.value?.installed_version ? '已更新' : '已安装（默认停用）')
      upShow.value = false
      load()
    }
  } catch (e) {
    message.error(errText(e))
  } finally {
    uploading.value = false
  }
}
function openUpload() {
  file.value = null
  preview.value = null
  upShow.value = true
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="插件管理" description="内置插件随程序提供；JS 插件以 zip 包上传，在受限环境中运行，只能访问声明过的域名与权限。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <Button @click="openUpload"><UploadIcon />上传 JS 插件</Button>
    </PageHeader>

    <DataTable :columns="columns" :rows="rows" :loading="loading" row-key="code">
      <template #cell-enabled="{ row }">
        <Switch v-if="row.status === 'ok'" :model-value="row.enabled" @update:model-value="(v: boolean) => toggle(row, v)" />
        <span v-else class="text-muted-foreground">-</span>
      </template>
      <template #cell-name="{ row }">
        <div class="grid gap-0.5">
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="font-medium">{{ row.name }}</span>
            <span class="font-mono text-xs text-muted-foreground">{{ row.code }} v{{ row.version }}</span>
            <span class="rounded px-1.5 py-0.5 text-xs" :class="row.type === 'go' ? 'bg-sky-500/15 text-sky-700 dark:text-sky-300' : 'bg-muted'">{{ row.type === 'go' ? '内置' : 'JS' }}</span>
            <span v-if="row.status === 'error'" class="rounded bg-destructive/15 px-1.5 py-0.5 text-xs text-destructive">加载失败</span>
            <span v-else-if="row.status === 'missing'" class="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs text-amber-700 dark:text-amber-300">程序中已移除</span>
          </div>
          <span v-if="row.description" class="text-xs text-muted-foreground">{{ row.description }}</span>
          <span v-if="row.error" class="text-xs text-destructive">{{ row.error }}</span>
        </div>
      </template>
      <template #cell-author="{ row }">{{ row.author || '-' }}</template>
      <template #cell-installed_at="{ row }"><span class="whitespace-nowrap text-muted-foreground">{{ formatTime(row.installed_at) }}</span></template>
      <template #cell-op="{ row }">
        <div class="flex justify-end gap-0.5">
          <Button variant="ghost" size="icon" class="size-8" title="详情" @click="openDetail(row)"><InfoIcon /></Button>
          <Button v-if="row.fields.length && row.status !== 'missing'" variant="ghost" size="icon" class="size-8" title="配置" @click="openConfig(row)"><SettingsIcon /></Button>
          <Button v-if="canRemove(row)" variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" title="删除" @click="remove(row)"><Trash2Icon /></Button>
        </div>
      </template>
    </DataTable>

    <!-- 详情 -->
    <Dialog v-model:open="detailShow">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader><DialogTitle>{{ detail?.name }}</DialogTitle></DialogHeader>
        <dl v-if="detail" class="grid grid-cols-[6rem_1fr] gap-x-3 gap-y-2 text-sm">
          <dt class="text-muted-foreground">标识</dt><dd class="font-mono">{{ detail.code }}</dd>
          <dt class="text-muted-foreground">版本</dt><dd>{{ detail.version }}</dd>
          <dt class="text-muted-foreground">主页</dt>
          <dd><a v-if="detail.url" :href="detail.url" target="_blank" rel="noopener" class="text-primary hover:underline">{{ detail.url }}</a><span v-else>-</span></dd>
          <dt class="text-muted-foreground">事件</dt><dd>{{ join(detail.features.events) }}</dd>
          <dt class="text-muted-foreground">过滤器</dt><dd>{{ join(detail.features.filters) }}</dd>
          <dt class="text-muted-foreground">定时任务</dt><dd>{{ join(detail.features.jobs) }}</dd>
          <dt class="text-muted-foreground">支付方式</dt><dd>{{ join(detail.features.payments) }}</dd>
          <dt class="text-muted-foreground">订阅格式</dt><dd>{{ join(detail.features.formats) }}</dd>
          <dt class="text-muted-foreground">路由</dt>
          <dd>
            <div v-for="r in detail.features.routes" :key="r.method + r.path" class="font-mono text-xs">{{ r.method }} {{ r.path }}（{{ r.scope }}）</div>
            <span v-if="!detail.features.routes.length">-</span>
          </dd>
          <template v-if="detail.package">
            <dt class="text-muted-foreground">可访问域名</dt><dd class="font-mono text-xs">{{ detail.package.http.join('、') || '无' }}</dd>
            <dt class="text-muted-foreground">权限</dt>
            <dd>
              <div v-for="p in detail.package.permissions" :key="p.key">{{ p.description }} <span class="font-mono text-xs text-muted-foreground">{{ p.key }}</span></div>
              <span v-if="!detail.package.permissions.length">无</span>
            </dd>
            <dt class="text-muted-foreground">SHA256</dt><dd class="font-mono text-xs break-all">{{ detail.package.sha256 }}</dd>
          </template>
        </dl>
      </DialogContent>
    </Dialog>

    <!-- 配置 -->
    <Dialog v-model:open="cfgShow">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader><DialogTitle>插件配置：{{ cfgPlugin?.name }}</DialogTitle></DialogHeader>
        <div v-if="cfgPlugin" class="grid gap-4">
          <FormItem v-for="f in cfgPlugin.fields" :key="f.key" :label="f.label" :required="f.required" :help="f.help">
            <Switch v-if="f.type === 'bool'" :model-value="!!cfgValues[f.key]" @update:model-value="(v: boolean) => (cfgValues[f.key] = v)" />
            <NumInput v-else-if="f.type === 'number'" :model-value="cfgValues[f.key] as number | null" @update:model-value="(v) => (cfgValues[f.key] = v)" />
            <SelectBox
              v-else-if="f.type === 'select'"
              :model-value="cfgValues[f.key] as string"
              :options="selectOptions(f)"
              @update:model-value="(v) => (cfgValues[f.key] = v)"
            />
            <Textarea v-else-if="f.type === 'text'" :model-value="String(cfgValues[f.key] ?? '')" rows="4" @update:model-value="(v) => (cfgValues[f.key] = v)" />
            <Input
              v-else
              :type="f.type === 'password' ? 'password' : 'text'"
              :model-value="String(cfgValues[f.key] ?? '')"
              autocomplete="off"
              @update:model-value="(v) => (cfgValues[f.key] = v)"
            />
          </FormItem>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="cfgShow = false">取消</Button>
          <Button :disabled="cfgSaving" @click="saveConfig"><Loader2Icon v-if="cfgSaving" class="animate-spin" />保存</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- 上传 -->
    <Dialog v-model:open="upShow">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-xl" @interact-outside.prevent>
        <DialogHeader>
          <DialogTitle>上传 JS 插件</DialogTitle>
          <DialogDescription>选择 zip 安装包（不超过 2 MB），先检查安装包，确认访问范围后再安装。</DialogDescription>
        </DialogHeader>
        <Input type="file" accept=".zip" @change="onFile" />
        <template v-if="preview">
          <div class="flex gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
            <TriangleAlertIcon class="mt-0.5 size-4 shrink-0" />
            <span>JS 插件运行在受限环境中，只能访问下面列出的外部域名和权限。确认后才会安装。</span>
          </div>
          <dl class="grid grid-cols-[6rem_1fr] gap-x-3 gap-y-2 text-sm">
            <dt class="text-muted-foreground">插件</dt><dd>{{ preview.name }}（{{ preview.code }}）</dd>
            <dt class="text-muted-foreground">版本</dt>
            <dd>{{ preview.version }}<span v-if="preview.installed_version" class="ml-2 text-xs text-muted-foreground">将从 {{ preview.installed_version }} 更新</span></dd>
            <dt class="text-muted-foreground">作者</dt><dd>{{ preview.author || '-' }}</dd>
            <dt class="text-muted-foreground">说明</dt><dd>{{ preview.description || '-' }}</dd>
            <dt class="text-muted-foreground">SHA256</dt><dd class="font-mono text-xs break-all">{{ preview.sha256 }}</dd>
            <dt class="text-muted-foreground">外部域名</dt>
            <dd>
              <div v-for="d in preview.http" :key="d" class="font-mono text-xs">{{ d }}</div>
              <span v-if="!preview.http.length" class="text-muted-foreground">无（不能访问任何外部网络）</span>
            </dd>
            <dt class="text-muted-foreground">申请的权限</dt>
            <dd>
              <div v-for="p in preview.permissions" :key="p.key">{{ p.description }} <span class="font-mono text-xs text-muted-foreground">{{ p.key }}</span></div>
              <span v-if="!preview.permissions.length" class="text-muted-foreground">无</span>
            </dd>
          </dl>
        </template>
        <DialogFooter>
          <Button variant="outline" @click="upShow = false">取消</Button>
          <Button v-if="!preview" :disabled="!file || uploading" @click="upload(false)"><Loader2Icon v-if="uploading" class="animate-spin" />检查安装包</Button>
          <Button v-else variant="destructive" :disabled="uploading" @click="upload(true)"><Loader2Icon v-if="uploading" class="animate-spin" />确认并安装</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
