<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { InfoIcon, Loader2Icon, MailIcon, SendIcon, ActivityIcon, ChevronDownIcon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@gv/shared/ui/tabs'
import { Card, CardContent } from '@gv/shared/ui/card'
import { Alert, AlertDescription, AlertTitle } from '@gv/shared/ui/alert'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Button } from '@gv/shared/ui/button'
import { RadioGroup, RadioGroupItem } from '@gv/shared/ui/radio-group'
import { Label } from '@gv/shared/ui/label'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@gv/shared/ui/collapsible'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import FieldRow from '../components/common/FieldRow.vue'
import SelectBox from '../components/common/SelectBox.vue'
import NumInput from '../components/common/NumInput.vue'
import BoolSwitch from '../components/common/BoolSwitch.vue'
import TagsField from '../components/common/TagsField.vue'
import ClientsField from '../components/common/ClientsField.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import { admin, cfg } from '../api'
import { loadFeatures } from '../features'
import { ask, errText, message, run } from '../ui'
import { configGroups, type ConfigField, type ConfigGroup } from '../configSpec'
import { loadPlans, planOptions } from '../refs'

const CodeEditor = defineAsyncComponent(() => import('../components/common/CodeEditor.vue'))
const MailTemplates = defineAsyncComponent(() => import('../components/MailTemplates.vue'))
const SmtpServers = defineAsyncComponent(() => import('../components/SmtpServers.vue'))

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
const route = useRoute()
const tab = ref(typeof route.query.tab === 'string' ? route.query.tab : 'site')
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
    legacySmtp(flat)
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
  if (tab.value === 'templates') return templates.value.map((t) => t.setting_key)
  return []
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

const tabItems = computed(() => [
  ...configGroups.map((g) => ({ name: g.name, title: g.title })),
  { name: 'templates', title: '订阅模板' },
  { name: 'mail', title: '邮件模板' },
])
const tabCls =
  'h-11 flex-none rounded-none border-0 border-b-2 border-transparent px-3 text-muted-foreground hover:text-foreground ' +
  'data-[state=active]:border-primary data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none ' +
  'dark:data-[state=active]:border-primary dark:data-[state=active]:bg-transparent'

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
  if ('node_machine_enable' in body) await loadFeatures()
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
// 还没有使用过多服务器列表时（如从原项目迁移），把原单个 SMTP 配置显示为列表中的第一个服务器
function legacySmtp(flat: Record<string, Value>) {
  if (flat.email_smtp_servers || !flat.email_host) return
  const s = (k: string) => (flat[k] === null || flat[k] === undefined ? '' : String(flat[k]))
  const enc = s('email_encryption').toLowerCase()
  flat.email_smtp_servers = JSON.stringify([{
    name: '', host: s('email_host'), port: Number(flat.email_port) || 0, encryption: enc === 'ssl' || enc === 'tls' ? enc : '',
    username: s('email_username'), password: s('email_password'), from_address: s('email_from_address'), enable: true,
  }])
}

const mailBusy = ref(false)
const mailTesting = ref<number | null>(null)
async function testMail(server?: number) {
  if (dirty.value.length && !(await save())) return
  mailBusy.value = true
  mailTesting.value = server ?? -1
  await run(async () => {
    const r = await admin.post<{ error: string | null; email: string }>('/config/test-mail', server === undefined ? undefined : { server })
    if (r.error) message.error(`发送失败：${r.error}`)
    else message.success(`测试邮件已发送到 ${r.email}`)
  })
  mailBusy.value = false
  mailTesting.value = null
}

const topics = [
  { name: 'order', label: '订单' },
  { name: 'ticket', label: '工单' },
  { name: 'alert', label: '告警' },
  { name: 'daily', label: '每日统计' },
]
const topicBusy = ref('')
async function testTopic(topic: string, label: string) {
  if (dirty.value.length && !(await save())) return
  topicBusy.value = topic
  await run(() => admin.post('/telegram/test-topic', { topic }), `已向「${label}」话题发送测试消息`)
  topicBusy.value = ''
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

// ---------- 邮件模板 ----------
const mailRef = ref<{ reload: () => Promise<void> }>()
async function reloadMailThemes() {
  mailTemplates.value = (await admin.get<string[]>('/config/email-templates')) || mailTemplates.value
}
async function activateMail(theme: string) {
  const prev = values.value.email_template
  values.value.email_template = theme
  if (!(await save(['email_template']))) values.value.email_template = prev
  await mailRef.value?.reload()
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
  <div>
    <PageHeader title="系统配置" description="站点、安全、订阅、邀请、节点、邮件等全局设置" />

    <div v-if="loading" class="flex h-64 items-center justify-center text-muted-foreground">
      <Loader2Icon class="size-6 animate-spin" />
    </div>

    <Card v-else class="gap-0 overflow-hidden py-0">
    <Tabs v-model="tab" class="gap-0">
      <div class="overflow-x-auto border-b px-4">
        <TabsList class="h-auto w-max gap-1 rounded-none bg-transparent p-0">
          <TabsTrigger v-for="t in tabItems" :key="t.name" :value="t.name" :class="tabCls">{{ t.title }}</TabsTrigger>
        </TabsList>
      </div>

      <TabsContent v-for="g in configGroups" :key="g.name" :value="g.name">
        <CardContent class="px-4 py-5 md:px-8">
          <div class="max-w-4xl">
            <Alert v-if="g.notice" class="mb-4">
              <InfoIcon />
              <AlertDescription>{{ g.notice }}</AlertDescription>
            </Alert>
            <Alert v-if="g.name === 'telegram'" class="mb-4">
              <InfoIcon />
              <AlertTitle>管理员通知发到话题群组</AlertTitle>
              <AlertDescription>
                <ol class="list-decimal space-y-1 pl-4">
                  <li>在 Telegram 新建一个群组，进入群组设置开启「话题」（Topics），按需要建立订单、工单、告警、每日统计等话题，也可以都发到同一个话题。</li>
                  <li>把机器人拉进群组并设为管理员（至少需要「发送消息」权限），并确认上方已开启机器人通知、完成「一键设置」。</li>
                  <li>在群组的任意话题中发送 <code class="rounded bg-muted px-1">/chatid</code>，机器人会回复「群组 ID」和当前话题的「话题 ID」；在每个话题里各发一次即可得到全部话题 ID。</li>
                  <li>把群组 ID 填到「管理员话题群组 ID」，各话题 ID 填到对应项（General 话题填 0），保存后用下方「测试发送」检查每个话题。</li>
                  <li>填写群组 ID 后管理员通知只发到群组，不再私聊管理员；留空时仍私聊已绑定 Telegram 的管理员（与原项目一致）。群组中的成员都能看到通知，请只邀请管理人员。</li>
                </ol>
              </AlertDescription>
            </Alert>

            <FieldRow
              v-for="f in visible(g)"
              :key="f.key"
              :label="f.label"
              :help="f.help"
            >
              <BoolSwitch v-if="f.kind === 'switch'" :model-value="values[f.key] as number" @update:model-value="(v) => set(f.key, v as number)" />
              <SmtpServers
                v-else-if="f.kind === 'smtp'"
                :model-value="str(f.key)"
                :testing="mailTesting"
                @update:model-value="(v) => set(f.key, v)"
                @test="testMail"
              />
              <NumInput
                v-else-if="f.kind === 'int' || f.kind === 'float'"
                class="w-full max-w-60"
                :model-value="numVal(f.key)"
                :step="f.kind === 'int' ? 1 : 0.01"
                :placeholder="f.placeholder"
                :suffix="f.suffix"
                @update:model-value="(v) => set(f.key, v)"
              />
              <SelectBox
                v-else-if="f.kind === 'select' || f.kind === 'plan'"
                class="w-full max-w-80"
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
              <ClientsField
                v-else-if="f.kind === 'clients'"
                :model-value="values[f.key] as string | null"
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
                :class="f.kind === 'password' ? 'font-mono' : ''"
                autocomplete="off"
                spellcheck="false"
                :model-value="str(f.key)"
                :placeholder="f.placeholder"
                @update:model-value="(v) => set(f.key, String(v))"
              />
            </FieldRow>

            <FieldRow v-if="g.name === 'telegram'" label="测试发送" help="保存后向管理员话题群组的对应话题发送一条测试消息">
              <div class="flex flex-wrap gap-2">
                <Button v-for="t in topics" :key="t.name" variant="outline" size="sm" :disabled="!!topicBusy" @click="testTopic(t.name, t.label)">
                  <Loader2Icon v-if="topicBusy === t.name" class="animate-spin" />
                  <SendIcon v-else />
                  {{ t.label }}
                </Button>
              </div>
            </FieldRow>
            <FieldRow v-if="g.name === 'email'" label="发送测试邮件" help="按当前发送方式发送到当前登录用户邮箱；单独测试某台服务器请用列表中的「测试」">
              <Button variant="outline" :disabled="mailBusy" @click="testMail()">
                <Loader2Icon v-if="mailTesting === -1" class="animate-spin" />
                <MailIcon v-else />
                发送测试邮件
              </Button>
            </FieldRow>
            <FieldRow v-if="g.name === 'telegram'" label="设置Webhook" help="对机器人进行Webhook设置，不设置将无法收到Telegram通知。">
              <div class="flex gap-2">
                <Button variant="outline" @click="tgStatus"><ActivityIcon />状态</Button>
                <Button variant="outline" :disabled="tgBusy" @click="tgApply">
                  <Loader2Icon v-if="tgBusy" class="animate-spin" />
                  <SendIcon v-else />
                  一键设置
                </Button>
              </div>
            </FieldRow>

            <FieldRow label="">
              <div class="flex items-center gap-3">
                <Button :disabled="saving || !dirty.length" @click="save()">
                  <Loader2Icon v-if="saving" class="animate-spin" />
                  保存
                </Button>
                <Button v-if="dirty.length" variant="ghost" :disabled="saving" @click="revert">撤销修改</Button>
                <span v-if="dirty.length" class="text-xs text-warning">有 {{ dirty.length }} 项修改未保存</span>
              </div>
            </FieldRow>
          </div>
        </CardContent>
      </TabsContent>

      <TabsContent value="templates" class="px-4 py-5 md:px-8">
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
              <CodeEditor
                :model-value="str(t.setting_key)"
                :lang="t.format"
                placeholder="留空使用内置默认模板"
                @update:model-value="(v) => set(t.setting_key, v)"
              />
              <div class="mt-3 flex gap-2">
                <Button size="sm" :disabled="saving || same(values[t.setting_key], origin[t.setting_key])" @click="save([t.setting_key])">保存</Button>
                <Button size="sm" variant="outline" @click="loadDefault(t)">载入默认模板</Button>
                <Button size="sm" variant="outline" @click="resetTemplate(t)">清空（恢复默认）</Button>
              </div>
            </CollapsibleContent>
          </Collapsible>
        </div>
      </TabsContent>

      <TabsContent value="mail" class="px-4 py-5 md:px-8">
        <MailTemplates ref="mailRef" @changed="reloadMailThemes" @activate="activateMail" />
      </TabsContent>
    </Tabs>
    </Card>

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
