<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { InfoIcon, Loader2Icon, PlusIcon, Trash2Icon, CheckIcon, RotateCcwIcon } from '@lucide/vue'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import SelectBox from './common/SelectBox.vue'
import StatusBadge from './common/StatusBadge.vue'
import FormItem from './common/FormItem.vue'
import { admin } from '../api'
import { ask, confirm, errText, message, run } from '../ui'

const CodeEditor = defineAsyncComponent(() => import('./common/CodeEditor.vue'))

interface Item {
  name: string
  title: string
  vars: string[]
  custom: boolean
}
interface Theme {
  theme: string
  builtin: boolean
  active: boolean
  templates: Item[]
}
interface Detail {
  content: string
  default: string
  custom: boolean
}

const emit = defineEmits<{ changed: []; activate: [theme: string] }>()

const themes = ref<Theme[]>([])
const theme = ref('')
const name = ref('verify')
const detail = ref<Detail | null>(null)
const content = ref('')
const loading = ref(false)
const saving = ref(false)
const preview = ref('')
const previewErr = ref('')

const current = computed(() => themes.value.find((t) => t.theme === theme.value))
const item = computed(() => current.value?.templates.find((t) => t.name === name.value))
const dirty = computed(() => !!detail.value && content.value !== detail.value.content)
const themeOptions = computed(() =>
  themes.value.map((t) => ({ label: `${t.theme}${t.builtin ? '（内置）' : ''}${t.active ? ' · 使用中' : ''}`, value: t.theme })),
)

async function loadThemes() {
  try {
    themes.value = (await admin.get<Theme[]>('/config/mail-templates')) || []
  } catch (e) {
    message.error(errText(e))
    return
  }
  if (!current.value) theme.value = themes.value.find((t) => t.active)?.theme || themes.value[0]?.theme || ''
}

async function loadDetail() {
  if (!theme.value) return
  loading.value = true
  try {
    detail.value = await admin.get<Detail>(`/config/mail-templates/${theme.value}/${name.value}`)
    content.value = detail.value.content
  } catch (e) {
    message.error(errText(e))
  } finally {
    loading.value = false
  }
}

/** 切换前确认放弃未保存的修改。 */
async function leave() {
  return !dirty.value || ask('放弃修改', '当前模板有未保存的修改，确定放弃吗？', true)
}
async function pickTheme(t: string) {
  if (t === theme.value || !(await leave())) return
  theme.value = t
  loadDetail()
}
async function pickName(n: string) {
  if (n === name.value || !(await leave())) return
  name.value = n
  loadDetail()
}

let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0
watch(content, () => {
  clearTimeout(timer)
  timer = setTimeout(renderPreview, 400)
})
async function renderPreview() {
  const id = ++seq
  try {
    const r = await admin.post<{ html: string }>('/config/mail-templates/preview', { name: name.value, content: content.value })
    if (id !== seq) return
    preview.value = r.html
    previewErr.value = ''
  } catch (e) {
    if (id === seq) previewErr.value = errText(e)
  }
}
onBeforeUnmount(() => clearTimeout(timer))

async function save(body = content.value, ok = '保存成功') {
  saving.value = true
  const done = await run(() => admin.put(`/config/mail-templates/${theme.value}/${name.value}`, { content: body }), ok)
  saving.value = false
  if (!done) return
  await loadThemes()
  await loadDetail()
}

async function restore() {
  if (await ask('恢复默认', `确定将「${item.value?.title}」恢复为内置内容吗？已自定义的内容将被删除。`, true)) save('', '已恢复默认')
}

const createShow = ref(false)
const createForm = ref({ theme: '', from: '' })
const creating = ref(false)
async function openCreate() {
  if (!(await leave())) return
  createForm.value = { theme: '', from: theme.value }
  createShow.value = true
}
async function create() {
  creating.value = true
  const f = { ...createForm.value }
  const done = await run(() => admin.post('/config/mail-templates', f), '新增成功')
  creating.value = false
  if (!done) return
  createShow.value = false
  theme.value = f.theme
  await loadThemes()
  await loadDetail()
  emit('changed')
}

async function remove() {
  const t = theme.value
  if (!(await confirm(`确定删除模板「${t}」吗？`, () => admin.del(`/config/mail-templates/${t}`), '删除成功'))) return
  theme.value = ''
  await loadThemes()
  await loadDetail()
  emit('changed')
}

const tag = (expr: string) => `{{${expr}}}`

/** 父组件保存 email_template 后刷新使用状态。 */
defineExpose({ reload: loadThemes })

onMounted(async () => {
  await loadThemes()
  await loadDetail()
})
</script>

<template>
  <div class="grid gap-4">
    <Alert>
      <InfoIcon />
      <AlertDescription>
        邮件模板使用 Go html/template 语法，变量写作 <code class="font-mono">{{ tag('.name') }}</code>，变量值会自动转义。
        内置模板（default、classic）可直接修改，恢复默认即还原内置内容；也可以基于现有模板新增。预览使用示例数据，不会发送邮件。
      </AlertDescription>
    </Alert>

    <div class="flex flex-wrap items-center gap-2">
      <SelectBox class="w-64" :model-value="theme" :options="themeOptions" @update:model-value="(v) => pickTheme(String(v))" />
      <StatusBadge v-if="current?.builtin">内置</StatusBadge>
      <StatusBadge v-if="current?.active" tone="success">使用中</StatusBadge>
      <Button v-if="current && !current.active" size="sm" variant="outline" @click="emit('activate', theme)"><CheckIcon />设为使用中</Button>
      <Button size="sm" variant="outline" @click="openCreate"><PlusIcon />新增模板</Button>
      <Button v-if="current && !current.builtin" size="sm" variant="outline" class="text-destructive" :disabled="current.active" :title="current.active ? '正在使用的模板不能删除' : ''" @click="remove">
        <Trash2Icon />删除模板
      </Button>
    </div>

    <div class="flex flex-wrap gap-2 border-b pb-3">
      <Button
        v-for="t in current?.templates || []"
        :key="t.name"
        size="sm"
        :variant="t.name === name ? 'secondary' : 'ghost'"
        @click="pickName(t.name)"
      >
        {{ t.title.replace(/（.*）/, '') }}
        <span v-if="t.custom && current?.builtin" class="size-1.5 rounded-full bg-primary" title="已自定义" />
      </Button>
    </div>

    <div v-if="item" class="grid gap-4 xl:grid-cols-2">
      <div class="flex min-w-0 flex-col gap-3">
        <div class="flex flex-wrap items-center gap-2 text-sm">
          <span class="font-medium">{{ item.title }}</span>
          <StatusBadge v-if="current?.builtin" :tone="item.custom ? 'primary' : 'default'">{{ item.custom ? '已自定义' : '内置内容' }}</StatusBadge>
          <Loader2Icon v-if="loading" class="size-4 animate-spin text-muted-foreground" />
        </div>
        <div class="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
          可用变量：
          <code v-for="v in item.vars" :key="v" class="rounded bg-muted px-1.5 py-0.5 font-mono text-foreground">{{ tag('.' + v) }}</code>
          <span v-if="name === 'notify'">（保留换行可写 <code class="font-mono">{{ tag('nl2br .content') }}</code>）</span>
        </div>
        <CodeEditor v-model="content" lang="html" />
        <div class="flex flex-wrap gap-2">
          <Button size="sm" :disabled="saving || !dirty" @click="save()">
            <Loader2Icon v-if="saving" class="animate-spin" />
            保存
          </Button>
          <Button v-if="dirty" size="sm" variant="ghost" @click="content = detail!.content">撤销修改</Button>
          <Button v-if="current?.builtin && item.custom" size="sm" variant="outline" :disabled="saving" @click="restore"><RotateCcwIcon />恢复默认</Button>
          <Button v-if="detail?.default && content !== detail.default" size="sm" variant="outline" @click="content = detail.default">载入内置内容</Button>
        </div>
      </div>
      <div class="flex min-w-0 flex-col gap-3">
        <div class="text-sm font-medium">预览</div>
        <Alert v-if="previewErr" variant="destructive">
          <AlertDescription class="break-all">{{ previewErr }}</AlertDescription>
        </Alert>
        <iframe :srcdoc="preview" sandbox="" title="邮件预览" class="h-[36rem] w-full rounded-md border bg-white" />
      </div>
    </div>

    <Dialog v-model:open="createShow">
      <DialogContent class="sm:max-w-md">
        <DialogHeader><DialogTitle>新增邮件模板</DialogTitle></DialogHeader>
        <div class="grid gap-4">
          <FormItem label="模板名称" required help="字母、数字、下划线、短横线，最长 32 位">
            <Input v-model="createForm.theme" placeholder="如 mybrand" />
          </FormItem>
          <FormItem label="复制自">
            <SelectBox :model-value="createForm.from" :options="themes.map((t) => ({ label: t.theme, value: t.theme }))" @update:model-value="(v) => (createForm.from = String(v))" />
          </FormItem>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="createShow = false">取消</Button>
          <Button :disabled="creating || !createForm.theme" @click="create"><Loader2Icon v-if="creating" class="animate-spin" />确定</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
