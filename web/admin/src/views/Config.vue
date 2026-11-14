<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NCard, NTabs, NTabPane, NForm, NFormItem, NInput, NInputNumber, NSwitch, NSelect, NDynamicTags, NButton, NSpace, NSpin,
  NCollapse, NCollapseItem, NTag, NDescriptions, NDescriptionsItem, NModal,
} from 'naive-ui'
import { formatTime } from '@gv/shared'
import { admin, cfg } from '../api'
import { dialog, errText, message, run } from '../ui'
import { configGroups, type ConfigField } from '../configSpec'

type Value = string | number | string[] | null
const values = ref<Record<string, Value>>({})
const origin = ref<Record<string, Value>>({})
const loading = ref(true)
const tab = ref('site')
const templates = ref<{ name: string; setting_key: string; format: string; custom: boolean; default: string }[]>([])

async function load() {
  loading.value = true
  try {
    const [all, tpls, mails] = await Promise.all([
      admin.get<Record<string, Record<string, Value>>>('/config'),
      admin.get<typeof templates.value>('/subscribe-templates'),
      admin.get<string[]>('/config/email-templates'),
    ])
    const flat: Record<string, Value> = {}
    for (const g of Object.values(all || {})) Object.assign(flat, g)
    values.value = { ...flat }
    origin.value = JSON.parse(JSON.stringify(flat))
    templates.value = tpls || []
    const mailField = configGroups.find((g) => g.name === 'email')!.fields.find((f) => f.key === 'email_template')!
    mailField.options = (mails || []).map((m) => ({ label: m, value: m }))
  } catch (e) {
    message.error(errText(e))
  } finally {
    loading.value = false
  }
}

function changed(keys: string[]) {
  const out: Record<string, Value> = {}
  for (const k of keys) if (JSON.stringify(values.value[k]) !== JSON.stringify(origin.value[k])) out[k] = values.value[k]
  return out
}

async function save(keys: string[]) {
  const body = changed(keys)
  if (!Object.keys(body).length) {
    message.info('没有修改')
    return
  }
  const ok = await run(() => admin.put('/config', body), '已保存')
  if (!ok) return
  for (const k of Object.keys(body)) origin.value[k] = JSON.parse(JSON.stringify(values.value[k]))
  if ('secure_path' in body) moveSecurePath(String(body.secure_path))
}

function moveSecurePath(next: string) {
  const old = cfg.value.secure_path
  cfg.value.secure_path = next
  if (old && location.pathname.includes(`/${old}`)) {
    dialog.info({
      title: '管理端路径已修改',
      content: `新的管理端地址为 /${next}/，请收藏新地址。`,
      positiveText: '前往新地址',
      onPositiveClick: () => location.assign(location.pathname.replace(`/${old}`, `/${next}`) + location.hash),
    })
  } else {
    message.warning('管理端路径已修改。分离部署时请同步修改管理端 config.js 中的 secure_path')
  }
}

function num(f: ConfigField) {
  return (v: number | null) => (values.value[f.key] = v)
}

// ---------- 邮件 / Telegram ----------
async function testMail() {
  await run(async () => {
    const r = await admin.post<{ error: string | null; email: string }>('/config/test-mail')
    if (r.error) message.error(`发送失败：${r.error}`)
    else message.success(`测试邮件已发送到 ${r.email}`)
  })
}

const tgInfo = ref<Record<string, unknown> | null>(null)
const tgShow = ref(false)
async function tgApply() {
  await run(async () => {
    const r = await admin.post<{ mode: string; username: string; webhook_url?: string }>('/telegram/apply')
    message.success(`已应用：@${r.username}（${r.mode === 'webhook' ? 'Webhook' : '长轮询'}）`)
    await load()
  })
}
async function tgStatus() {
  await run(async () => {
    tgInfo.value = await admin.get('/telegram/info')
    tgShow.value = true
  })
}

// ---------- 订阅模板 ----------
function loadDefault(t: { setting_key: string; default: string }) {
  values.value[t.setting_key] = t.default
}
async function saveTemplate(t: { setting_key: string }) {
  await save([t.setting_key])
  templates.value = (await admin.get<typeof templates.value>('/subscribe-templates')) || templates.value
}
async function resetTemplate(t: { setting_key: string }) {
  values.value[t.setting_key] = ''
  await saveTemplate(t)
}

onMounted(load)
</script>

<template>
  <div class="page">
    <NCard size="small">
      <NSpin :show="loading">
        <NTabs v-model:value="tab" type="line" animated>
          <NTabPane v-for="g in configGroups" :key="g.name" :name="g.name" :tab="g.title">
            <NForm label-placement="left" label-width="200" style="max-width: 860px">
              <NFormItem v-for="f in g.fields" :key="f.key" :label="f.label">
                <NSpace vertical style="width: 100%" :size="2">
                  <NSwitch
                    v-if="f.kind === 'switch'"
                    :value="Number(values[f.key]) === 1"
                    @update:value="(v: boolean) => (values[f.key] = v ? 1 : 0)"
                  />
                  <NInputNumber
                    v-else-if="f.kind === 'int' || f.kind === 'float'"
                    :value="values[f.key] === null || values[f.key] === '' ? null : Number(values[f.key])"
                    :precision="f.kind === 'int' ? 0 : undefined"
                    clearable
                    style="width: 240px"
                    @update:value="num(f)"
                  />
                  <NSelect v-else-if="f.kind === 'select'" v-model:value="values[f.key] as string" :options="f.options" style="width: 320px" />
                  <NDynamicTags v-else-if="f.kind === 'tags'" v-model:value="values[f.key] as string[]" />
                  <NInput
                    v-else-if="f.kind === 'textarea'"
                    v-model:value="values[f.key] as string"
                    type="textarea"
                    :autosize="{ minRows: 3, maxRows: 12 }"
                  />
                  <NInput
                    v-else
                    v-model:value="values[f.key] as string"
                    :type="f.kind === 'password' ? 'password' : 'text'"
                    show-password-on="click"
                    :placeholder="f.placeholder"
                  />
                  <span v-if="f.help" class="muted">{{ f.help }}</span>
                </NSpace>
              </NFormItem>
              <NFormItem label=" ">
                <NSpace>
                  <NButton type="primary" @click="save(g.fields.map((f) => f.key))">保存</NButton>
                  <NButton v-if="g.name === 'email'" @click="testMail">发送测试邮件（先保存）</NButton>
                  <template v-if="g.name === 'telegram'">
                    <NButton @click="tgApply">应用机器人设置（先保存）</NButton>
                    <NButton @click="tgStatus">Webhook 状态</NButton>
                  </template>
                </NSpace>
              </NFormItem>
            </NForm>
          </NTabPane>
          <NTabPane name="templates" tab="订阅模板">
            <div class="muted" style="margin-bottom: 12px">自定义模板为空时使用内置默认模板。YAML / JSON 模板保存时会校验格式。</div>
            <NCollapse>
              <NCollapseItem v-for="t in templates" :key="t.name" :name="t.name">
                <template #header>
                  <NSpace align="center">
                    <strong>{{ t.name }}</strong>
                    <NTag size="small" :bordered="false">{{ t.format }}</NTag>
                    <NTag size="small" :type="t.custom ? 'success' : 'default'" :bordered="false">{{ t.custom ? '已自定义' : '默认' }}</NTag>
                  </NSpace>
                </template>
                <NInput
                  v-model:value="values[t.setting_key] as string"
                  type="textarea"
                  class="mono"
                  :autosize="{ minRows: 8, maxRows: 30 }"
                  placeholder="留空使用内置默认模板"
                />
                <NSpace style="margin-top: 8px">
                  <NButton type="primary" size="small" @click="saveTemplate(t)">保存</NButton>
                  <NButton size="small" @click="loadDefault(t)">载入默认模板</NButton>
                  <NButton size="small" @click="resetTemplate(t)">恢复默认</NButton>
                </NSpace>
              </NCollapseItem>
            </NCollapse>
          </NTabPane>
        </NTabs>
      </NSpin>
    </NCard>
    <NModal v-model:show="tgShow" preset="card" title="Telegram Webhook 状态" style="width: 560px; max-width: 95vw">
      <NDescriptions v-if="tgInfo" :column="1" label-placement="left" bordered size="small">
        <NDescriptionsItem label="地址">{{ tgInfo.url || '（长轮询模式或未设置）' }}</NDescriptionsItem>
        <NDescriptionsItem label="待处理更新">{{ tgInfo.pending_update_count }}</NDescriptionsItem>
        <NDescriptionsItem label="最后错误">
          {{ tgInfo.last_error_message ? `${formatTime(tgInfo.last_error_date as number)} ${tgInfo.last_error_message}` : '无' }}
        </NDescriptionsItem>
      </NDescriptions>
    </NModal>
  </div>
</template>
