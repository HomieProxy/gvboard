<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import {
  NCard, NDataTable, NButton, NSpace, NModal, NForm, NFormItem, NInput, NInputNumber, NSwitch, NSelect, NAlert, NUpload,
  NDescriptions, NDescriptionsItem, NList, NListItem, NTag, type DataTableColumns, type UploadFileInfo,
} from 'naive-ui'
import { formatTime } from '@gv/shared'
import { admin } from '../api'
import { errText, message, run } from '../ui'
import { useList } from '../paged'
import { actions, btn, confirmBtn, sw, tag } from '../helpers'
import type { PluginField, PluginPreview, PluginView } from '../types'

const { rows, loading, load } = useList(() => admin.get<PluginView[]>('/plugins'))

async function toggle(p: PluginView, v: boolean) {
  if (await run(() => admin.post(`/plugins/${p.code}/${v ? 'enable' : 'disable'}`), v ? '已启用' : '已停用')) load()
}
async function remove(p: PluginView) {
  if (await run(() => admin.del(`/plugins/${p.code}`), '已删除')) load()
}

const columns: DataTableColumns<PluginView> = [
  {
    title: '插件',
    key: 'name',
    render: (p) =>
      h(NSpace, { vertical: true, size: 2 }, () => [
        h(NSpace, { size: 6, align: 'center' }, () => [
          h('strong', null, p.name),
          h('span', { class: 'muted' }, `${p.code} v${p.version}`),
          tag(p.type === 'go' ? '内置' : 'JS', p.type === 'go' ? 'info' : 'default'),
          p.status === 'error' ? tag('加载失败', 'error') : p.status === 'missing' ? tag('程序中已移除', 'warning') : null,
        ]),
        h('span', { class: 'muted' }, p.description || ''),
        p.error ? h('span', { style: 'color:#d03050;font-size:12px' }, p.error) : null,
      ]),
  },
  { title: '作者', key: 'author', width: 120 },
  { title: '安装时间', key: 'installed_at', width: 150, render: (p) => formatTime(p.installed_at) },
  { title: '启用', key: 'enabled', width: 70, render: (p) => (p.status === 'ok' ? sw(p.enabled, (v) => toggle(p, v)) : '-') },
  {
    title: '操作',
    key: 'op',
    width: 150,
    render: (p) =>
      actions(
        btn('详情', () => openDetail(p), 'info'),
        p.fields.length && p.status !== 'missing' ? btn('配置', () => openConfig(p)) : null,
        (p.type === 'js' || p.status === 'missing') && !p.enabled
          ? confirmBtn('删除', '删除后插件的数据（KV）也会一并删除，确定？', () => remove(p))
          : null,
      ),
  },
]

// ---------- 详情 ----------
const detail = ref<PluginView | null>(null)
const detailShow = ref(false)
function openDetail(p: PluginView) {
  detail.value = p
  detailShow.value = true
}

// ---------- 配置 ----------
const cfgPlugin = ref<PluginView | null>(null)
const cfgShow = ref(false)
const cfgValues = ref<Record<string, unknown>>({})
function openConfig(p: PluginView) {
  cfgPlugin.value = p
  cfgValues.value = Object.fromEntries(p.fields.map((f) => [f.key, p.config?.[f.key] ?? f.default ?? (f.type === 'bool' ? false : '')]))
  cfgShow.value = true
}
async function saveConfig() {
  if (!cfgPlugin.value) return
  if (await run(() => admin.put(`/plugins/${cfgPlugin.value!.code}/config`, { config: cfgValues.value }), '已保存')) {
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

function onFile(list: UploadFileInfo[]) {
  file.value = list[0]?.file || null
  preview.value = null
}

async function upload(confirm: boolean) {
  if (!file.value) {
    message.warning('请选择 zip 安装包')
    return
  }
  const fd = new FormData()
  fd.append('file', file.value)
  if (confirm && preview.value) fd.append('sha256', preview.value.sha256)
  uploading.value = true
  try {
    if (!confirm) {
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
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NButton type="primary" @click="openUpload">上传 JS 插件</NButton>
        <NButton @click="load">刷新</NButton>
      </div>
      <NDataTable size="small" :columns="columns" :data="rows" :loading="loading" :row-key="(p: PluginView) => p.code" />
    </NCard>

    <NModal v-model:show="detailShow" preset="card" :title="detail?.name" style="width: 640px; max-width: 95vw">
      <NDescriptions v-if="detail" :column="1" label-placement="left" bordered size="small">
        <NDescriptionsItem label="标识">{{ detail.code }}</NDescriptionsItem>
        <NDescriptionsItem label="版本">{{ detail.version }}</NDescriptionsItem>
        <NDescriptionsItem label="主页"><a v-if="detail.url" :href="detail.url" target="_blank" rel="noopener">{{ detail.url }}</a></NDescriptionsItem>
        <NDescriptionsItem label="事件">{{ detail.features.events.join('、') || '-' }}</NDescriptionsItem>
        <NDescriptionsItem label="过滤器">{{ detail.features.filters.join('、') || '-' }}</NDescriptionsItem>
        <NDescriptionsItem label="定时任务">{{ detail.features.jobs.join('、') || '-' }}</NDescriptionsItem>
        <NDescriptionsItem label="支付方式">{{ detail.features.payments.join('、') || '-' }}</NDescriptionsItem>
        <NDescriptionsItem label="订阅格式">{{ detail.features.formats.join('、') || '-' }}</NDescriptionsItem>
        <NDescriptionsItem label="路由">
          <div v-for="r in detail.features.routes" :key="r.method + r.path" class="mono">{{ r.method }} {{ r.path }}（{{ r.scope }}）</div>
          <span v-if="!detail.features.routes.length">-</span>
        </NDescriptionsItem>
        <template v-if="detail.package">
          <NDescriptionsItem label="可访问域名">{{ detail.package.http.join('、') || '无' }}</NDescriptionsItem>
          <NDescriptionsItem label="权限">
            <div v-for="p in detail.package.permissions" :key="p.key">{{ p.description }}（{{ p.key }}）</div>
            <span v-if="!detail.package.permissions.length">无</span>
          </NDescriptionsItem>
          <NDescriptionsItem label="SHA256"><span class="mono">{{ detail.package.sha256 }}</span></NDescriptionsItem>
        </template>
      </NDescriptions>
    </NModal>

    <NModal v-model:show="cfgShow" preset="card" :title="`插件配置：${cfgPlugin?.name}`" style="width: 600px; max-width: 95vw">
      <NForm v-if="cfgPlugin" label-placement="top">
        <NFormItem v-for="f in cfgPlugin.fields" :key="f.key" :label="f.label" :required="f.required">
          <NSpace vertical style="width: 100%" :size="2">
            <NSwitch v-if="f.type === 'bool'" v-model:value="cfgValues[f.key] as boolean" />
            <NInputNumber v-else-if="f.type === 'number'" v-model:value="cfgValues[f.key] as number" style="width: 100%" />
            <NSelect v-else-if="f.type === 'select'" v-model:value="cfgValues[f.key] as string" :options="selectOptions(f)" clearable />
            <NInput
              v-else
              v-model:value="cfgValues[f.key] as string"
              :type="f.type === 'text' ? 'textarea' : f.type === 'password' ? 'password' : 'text'"
              show-password-on="click"
              :autosize="f.type === 'text' ? { minRows: 3, maxRows: 12 } : undefined"
            />
            <span v-if="f.help" class="muted">{{ f.help }}</span>
          </NSpace>
        </NFormItem>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="cfgShow = false">取消</NButton><NButton type="primary" @click="saveConfig">保存</NButton></NSpace>
      </template>
    </NModal>

    <NModal v-model:show="upShow" preset="card" title="上传 JS 插件" style="width: 620px; max-width: 95vw" :mask-closable="false">
      <NSpace vertical>
        <NUpload accept=".zip" :max="1" :default-upload="false" @update:file-list="onFile">
          <NButton>选择 zip 安装包（不超过 2 MB）</NButton>
        </NUpload>
        <template v-if="preview">
          <NAlert type="warning" title="请确认插件申请的访问范围">
            JS 插件运行在受限环境中，只能访问下面列出的外部域名和权限。确认后才会安装。
          </NAlert>
          <NDescriptions :column="1" label-placement="left" bordered size="small">
            <NDescriptionsItem label="插件">{{ preview.name }}（{{ preview.code }}）</NDescriptionsItem>
            <NDescriptionsItem label="版本">
              {{ preview.version }}
              <NTag v-if="preview.installed_version" size="small" type="info" :bordered="false">将从 {{ preview.installed_version }} 更新</NTag>
            </NDescriptionsItem>
            <NDescriptionsItem label="作者">{{ preview.author || '-' }}</NDescriptionsItem>
            <NDescriptionsItem label="说明">{{ preview.description || '-' }}</NDescriptionsItem>
            <NDescriptionsItem label="SHA256"><span class="mono">{{ preview.sha256 }}</span></NDescriptionsItem>
          </NDescriptions>
          <strong>可访问的外部域名</strong>
          <NList bordered size="small">
            <NListItem v-for="d in preview.http" :key="d"><span class="mono">{{ d }}</span></NListItem>
            <NListItem v-if="!preview.http.length"><span class="muted">无（不能访问任何外部网络）</span></NListItem>
          </NList>
          <strong>申请的权限</strong>
          <NList bordered size="small">
            <NListItem v-for="p in preview.permissions" :key="p.key">{{ p.description }} <span class="muted mono">{{ p.key }}</span></NListItem>
            <NListItem v-if="!preview.permissions.length"><span class="muted">无</span></NListItem>
          </NList>
        </template>
      </NSpace>
      <template #footer>
        <NSpace justify="end">
          <NButton @click="upShow = false">取消</NButton>
          <NButton v-if="!preview" type="primary" :loading="uploading" :disabled="!file" @click="upload(false)">检查安装包</NButton>
          <NButton v-else type="warning" :loading="uploading" @click="upload(true)">确认并安装</NButton>
        </NSpace>
      </template>
    </NModal>
  </div>
</template>
