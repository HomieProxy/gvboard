<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { InfoIcon, Loader2Icon, MailIcon, SendIcon, ActivityIcon, ChevronDownIcon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@gv/shared/ui/tabs'
import { Card, CardContent } from '@gv/shared/ui/card'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Button } from '@gv/shared/ui/button'
import { RadioGroup, RadioGroupItem } from '@gv/shared/ui/radio-group'
import { Label } from '@gv/shared/ui/label'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@gv/shared/ui/collapsible'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import SettingRow from '../components/common/SettingRow.vue'
import SelectBox from '../components/common/SelectBox.vue'
import NumInput from '../components/common/NumInput.vue'
import BoolSwitch from '../components/common/BoolSwitch.vue'
import TagsField from '../components/common/TagsField.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import { admin, cfg } from '../api'
import { ask, errText, message, run } from '../ui'
import { configGroups, type ConfigField, type ConfigGroup } from '../configSpec'
import { loadPlans, planOptions } from '../refs'

type Value = string | number | string[] | null
interface Template {
  name: string
  setting_key: string
  format: string
  custom: boolean
  default: string
}

const values = ref<Record<string, Value>>({})
const origin = ref<Record<string, Value>>({})
const loading = ref(true)
const saving = ref(false)
const tab = ref('site')
const templates = ref<Template[]>([])
const mailTemplates = ref<string[]>([])

async function load() {
  loading.value = true
  try {
    const [all, tpls, mails] = await Promise.all([
      admin.get<Record<string, Record<string, Value>>>('/config'),
      admin.get<Template[]>('/subscribe-templates'),
      admin.get<string[]>('/config/email-templates'),
      loadPlans(),
    ])
    const flat: Record<string, Value> = {}
    for (const g of Object.values(all || {})) Object.assign(flat, g)
    values.value = { ...flat }
    origin.value = JSON.parse(JSON.stringify(flat))
    templates.value = tpls || []
    mailTemplates.value = mails || []
  } catch (e) {
    message.error(errText(e))
  } finally {
    loading.value = false
  }
}

const trialOptions = computed(() => [{ label: '关闭', value: 0 }, ...planOptions.value])
function optionsOf(f: ConfigField) {
  if (f.key === 'email_template') return mailTemplates.value.map((m) => ({ label: m, value: m }))
  if (f.kind === 'plan') return trialOptions.value
  return f.options || []
}

const on = (k: string) => Number(values.value[k]) === 1
const visible = (g: ConfigGroup) => g.fields.filter((f) => !f.showIf || on(f.showIf))
const keysOf = (g: ConfigGroup) => g.fields.flatMap((f) => (f.key2 ? [f.key, f.key2] : [f.key]))
const same = (a: Value, b: Value) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null)

const currentKeys = computed(() => {
  const g = configGroups.find((x) => x.name === tab.value)
  if (g) return keysOf(g)
  return templates.value.map((t) => t.setting_key)
})
const dirty = computed(() => currentKeys.value.filter((k) => !same(values.value[k], origin.value[k])))

function str(k: string) {
  const v = values.value[k]
  return v === null || v === undefined ? '' : String(v)
}
function numVal(k: string) {
  const v = values.value[k]
  return v === null || v === '' || v === undefined ? null : Number(v)
}
function set(k: string, v: Value) {
  values.value[k] = v
}

function revert() {
  for (const k of dirty.value) values.value[k] = JSON.parse(JSON.stringify(origin.value[k] ?? null))
}

async function save(keys = dirty.value) {
  const body: Record<string, Value> = {}
  for (const k of keys) if (!same(values.value[k], origin.value[k])) body[k] = values.value[k]
  if (!Object.keys(body).length) return true
  saving.value = true
  const ok = await run(() => admin.put('/config', body), '保存成功')
  saving.value = false
  if (!ok) return false
  for (const k of Object.keys(body)) origin.value[k] = JSON.parse(JSON.stringify(values.value[k]))
  if ('secure_path' in body) await moveSecurePath(String(body.secure_path))
  if (Object.keys(body).some((k) => k.startsWith('subscribe_template_'))) {
    templates.value = (await admin.get<Template[]>('/subscribe-templates')) || templates.value
  }
  return true
}

async function moveSecurePath(next: string) {
  const old = cfg.value.secure_path
  cfg.value.secure_path = next
  if (old && location.pathname.includes(`/${old}`)) {
    const go = await ask('后台路径已修改', `新的后台地址为 /${next}/，请收藏新地址。`)
    if (go) location.assign(location.pathname.replace(`/${old}`, `/${next}`) + location.hash)
  } else {
    message.warning('后台路径已修改。前后分离部署时请同步修改管理端 config.js 中的 secure_path')
  }
}

// ---------- 邮件 / Telegram ----------
const mailBusy = ref(false)
async function testMail() {
  if (dirty.value.length && !(await save())) return
  mailBusy.value = true
  await run(async () => {
    const r = await admin.post<{ error: string | null; email: string }>('/config/test-mail')
    if (r.error) message.error(`发送失败：${r.error}`)
    else message.success(`测试邮件已发送到 ${r.email}`)
  })
  mailBusy.value = false
}

const tgBusy = ref(false)
const tgInfo = ref<Record<string, unknown> | null>(null)
const tgShow = ref(false)
async function tgApply() {
  if (dirty.value.length && !(await save())) return
  tgBusy.value = true
  await run(async () => {
    const r = await admin.post<{ mode: string; username: string }>('/telegram/apply')
    message.success(`设置成功：@${r.username}（${r.mode === 'webhook' ? 'Webhook' : '长轮询'}）`)
    await load()
  })
  tgBusy.value = false
}
async function tgStatus() {
  await run(async () => {
    tgInfo.value = await admin.get('/telegram/info')
    tgShow.value = true
  })
}

// ---------- 订阅模板 ----------
function loadDefault(t: Template) {
  values.value[t.setting_key] = t.default
}
function resetTemplate(t: Template) {
  values.value[t.setting_key] = ''
}

onMounted(load)
</script>

<template>
  <div class="pb-20">
    <PageHeader title="系统配置" description="站点、安全、订阅、邀请、节点、邮件等全局设置" />

    <div v-if="loading" class="flex h-64 items-center justify-center text-muted-foreground">
      <Loader2Icon class="size-6 animate-spin" />
    </div>

    <Tabs v-else v-model="tab" class="gap-4">
      <div class="-mx-1 overflow-x-auto px-1 pb-1">
        <TabsList class="h-auto w-max">
          <TabsTrigger v-for="g in configGroups" :key="g.name" :value="g.name" class="px-3">{{ g.title }}</TabsTrigger>
          <TabsTrigger value="templates" class="px-3">订阅模板</TabsTrigger>
        </TabsList>
      </div>

      <TabsContent v-for="g in configGroups" :key="g.name" :value="g.name">
        <Card class="py-2">
          <CardContent class="px-6">
            <Alert v-if="g.notice" class="mt-4 mb-2">
              <InfoIcon />
              <AlertDescription>{{ g.notice }}</AlertDescription>
            </Alert>

            <SettingRow
              v-for="f in visible(g)"
              :key="f.key"
              :label="f.label"
              :description="f.help"
              :wide="f.kind === 'textarea' || f.kind === 'tags' || !!f.key2"
            >
              <BoolSwitch v-if="f.kind === 'switch'" :model-value="values[f.key] as number" @update:model-value="(v) => set(f.key, v as number)" />
              <NumInput
                v-else-if="f.kind === 'int' || f.kind === 'float'"
                class="w-full"
                :model-value="numVal(f.key)"
                :step="f.kind === 'int' ? 1 : 0.01"
                :placeholder="f.placeholder"
                :suffix="f.suffix"
                @update:model-value="(v) => set(f.key, v)"
              />
              <SelectBox
                v-else-if="f.kind === 'select' || f.kind === 'plan'"
                :model-value="values[f.key] as string | number"
                :options="optionsOf(f)"
                :placeholder="f.placeholder || '请选择'"
                @update:model-value="(v) => set(f.key, v)"
              />
              <RadioGroup
                v-else-if="f.kind === 'radio'"
                class="flex gap-5"
                :model-value="str(f.key)"
                @update:model-value="(v) => set(f.key, String(v))"
              >
                <div v-for="o in f.options" :key="o.value" class="flex items-center gap-2">
                  <RadioGroupItem :id="`${f.key}-${o.value}`" :value="String(o.value)" />
                  <Label :for="`${f.key}-${o.value}`" class="font-normal">{{ o.label }}</Label>
                </div>
              </RadioGroup>
              <TagsField
                v-else-if="f.kind === 'tags'"
                :model-value="values[f.key] as string[]"
                :placeholder="f.placeholder"
                @update:model-value="(v) => set(f.key, v)"
              />
              <Textarea
                v-else-if="f.kind === 'textarea'"
                :model-value="str(f.key)"
                :placeholder="f.placeholder"
                rows="3"
                @update:model-value="(v) => set(f.key, String(v))"
              />
              <div v-else-if="f.key2" class="flex w-full gap-2">
                <Input class="w-32 shrink-0" :model-value="str(f.key)" :placeholder="f.placeholder" @update:model-value="(v) => set(f.key, String(v))" />
                <Input :model-value="str(f.key2)" :placeholder="f.placeholder2" @update:model-value="(v) => set(f.key2!, String(v))" />
              </div>
              <Input
                v-else
                :type="f.kind === 'password' ? 'password' : 'text'"
                autocomplete="new-password"
                :model-value="str(f.key)"
                :placeholder="f.placeholder"
                @update:model-value="(v) => set(f.key, String(v))"
              />
            </SettingRow>

            <SettingRow v-if="g.name === 'email'" label="发送测试邮件" description="邮件将会发送到当前登陆用户邮箱">
              <Button :disabled="mailBusy" @click="testMail">
                <Loader2Icon v-if="mailBusy" class="animate-spin" />
                <MailIcon v-else />
                发送测试邮件
              </Button>
            </SettingRow>
            <SettingRow v-if="g.name === 'telegram'" label="设置Webhook" description="对机器人进行Webhook设置，不设置将无法收到Telegram通知。">
              <div class="flex gap-2">
                <Button variant="outline" @click="tgStatus"><ActivityIcon />状态</Button>
                <Button :disabled="tgBusy" @click="tgApply">
                  <Loader2Icon v-if="tgBusy" class="animate-spin" />
                  <SendIcon v-else />
                  一键设置
                </Button>
              </div>
            </SettingRow>
          </CardContent>
        </Card>
      </TabsContent>

      <TabsContent value="templates">
        <Alert class="mb-4">
          <InfoIcon />
          <AlertDescription>自定义模板为空时使用内置默认模板。YAML / JSON 模板保存时会校验格式。</AlertDescription>
        </Alert>
        <div class="grid gap-3">
          <Collapsible v-for="t in templates" :key="t.name" v-slot="{ open }" class="rounded-lg border bg-card">
            <CollapsibleTrigger class="flex w-full items-center gap-3 px-4 py-3 text-left">
              <span class="font-medium">{{ t.name }}</span>
              <StatusBadge>{{ t.format }}</StatusBadge>
              <StatusBadge :tone="t.custom ? 'success' : 'default'">{{ t.custom ? '已自定义' : '默认' }}</StatusBadge>
              <ChevronDownIcon :class="['ml-auto size-4 text-muted-foreground transition-transform', open ? 'rotate-180' : '']" />
            </CollapsibleTrigger>
            <CollapsibleContent class="border-t px-4 py-3">
              <Textarea
                :model-value="str(t.setting_key)"
                class="min-h-64 font-mono text-xs"
                placeholder="留空使用内置默认模板"
                @update:model-value="(v) => set(t.setting_key, String(v))"
              />
              <div class="mt-3 flex gap-2">
                <Button size="sm" variant="outline" @click="loadDefault(t)">载入默认模板</Button>
                <Button size="sm" variant="outline" @click="resetTemplate(t)">清空（恢复默认）</Button>
              </div>
            </CollapsibleContent>
          </Collapsible>
        </div>
      </TabsContent>
    </Tabs>

    <Transition
      enter-from-class="translate-y-4 opacity-0"
      leave-to-class="translate-y-4 opacity-0"
      enter-active-class="transition"
      leave-active-class="transition"
    >
      <div v-if="dirty.length" class="fixed bottom-6 left-1/2 z-20 flex -translate-x-1/2 items-center gap-4 rounded-full border bg-background px-5 py-2.5 shadow-lg md:left-[calc(50%+8rem)]">
        <span class="text-sm">有 {{ dirty.length }} 项修改未保存</span>
        <Button size="sm" variant="ghost" :disabled="saving" @click="revert">撤销</Button>
        <Button size="sm" :disabled="saving" @click="save()">
          <Loader2Icon v-if="saving" class="animate-spin" />
          保存
        </Button>
      </div>
    </Transition>

    <Dialog v-model:open="tgShow">
      <DialogContent class="sm:max-w-lg">
        <DialogHeader><DialogTitle>Telegram Webhook 状态</DialogTitle></DialogHeader>
        <dl v-if="tgInfo" class="grid grid-cols-[6rem_1fr] gap-x-4 gap-y-2 text-sm">
          <dt class="text-muted-foreground">地址</dt>
          <dd class="break-all">{{ tgInfo.url || '（长轮询模式或未设置）' }}</dd>
          <dt class="text-muted-foreground">待处理更新</dt>
          <dd>{{ tgInfo.pending_update_count }}</dd>
          <dt class="text-muted-foreground">最后错误</dt>
          <dd>{{ tgInfo.last_error_message ? `${formatTime(tgInfo.last_error_date as number)} ${tgInfo.last_error_message}` : '无' }}</dd>
        </dl>
      </DialogContent>
    </Dialog>
  </div>
</template>
