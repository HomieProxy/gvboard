<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NSwitch, NButton, NSpace, NDynamicTags, NGrid, NGi, NDivider,
} from 'naive-ui'
import { admin } from '../api'
import { message, run } from '../ui'
import { groupOptions, loadGroups, loadRoutes, routeOptions } from '../refs'
import { serverTypes, typeFields, typeLabel, type FieldSpec } from '../serverSpec'
import type { ServerRow } from '../types'

const props = defineProps<{ server: ServerRow | null; type: string; parents: ServerRow[] }>()
const show = defineModel<boolean>('show', { required: true })
const emit = defineEmits<{ saved: [] }>()

const type = ref('vless')
const form = ref<Record<string, unknown>>({})
const saving = ref(false)

const specs = computed(() => typeFields[type.value] || [])
const visible = computed(() => specs.value.filter((s) => !s.when || s.when(form.value)))
const parentOptions = computed(() =>
  props.parents.filter((p) => p.type === type.value && p.id !== props.server?.id && !p.parent_id).map((p) => ({ label: `#${p.id} ${p.name}`, value: p.id })),
)

function fieldInit(s: FieldSpec, v: unknown) {
  if (s.kind === 'json') return v === null || v === undefined ? '' : typeof v === 'string' ? v : JSON.stringify(v, null, 2)
  if (s.kind === 'switch') return v === undefined || v === null ? !!s.default : !!Number(v)
  if (s.kind === 'raw' || s.kind === 'text') return v === null || v === undefined ? '' : String(v)
  if (s.kind === 'select') return v === null || v === undefined ? (s.default ?? '') : v
  return v ?? s.default ?? null
}

function reset() {
  const s = props.server
  type.value = s?.type || props.type || 'vless'
  const f: Record<string, unknown> = {
    name: s?.name || '',
    group_id: s?.group_id || [],
    route_id: s?.route_id || [],
    parent_id: s?.parent_id ?? null,
    host: s?.host || '',
    port: s?.port || '',
    server_port: s?.server_port ?? null,
    tags: s?.tags || [],
    rate: s ? Number(s.rate) : 1,
    show: s ? !!s.show : false,
  }
  for (const spec of typeFields[type.value] || []) f[spec.key] = fieldInit(spec, s ? s[spec.key] : undefined)
  form.value = f
}

watch(show, (v) => {
  if (v) {
    loadGroups().catch(() => {})
    loadRoutes().catch(() => {})
    reset()
  }
})

watch(type, () => {
  if (props.server) return
  for (const spec of specs.value) if (!(spec.key in form.value)) form.value[spec.key] = fieldInit(spec, undefined)
})

function build(): Record<string, unknown> | null {
  const f = form.value
  const body: Record<string, unknown> = {
    name: f.name,
    group_id: f.group_id,
    route_id: (f.route_id as number[]).length ? f.route_id : null,
    parent_id: f.parent_id || null,
    host: f.host,
    port: String(f.port ?? ''),
    server_port: f.server_port,
    tags: (f.tags as string[]).length ? f.tags : null,
    rate: f.rate,
    show: f.show ? 1 : 0,
  }
  for (const s of specs.value) {
    const v = f[s.key]
    if (s.kind === 'json') {
      const text = String(v ?? '').trim()
      if (!text) {
        body[s.key] = null
        continue
      }
      try {
        body[s.key] = JSON.parse(text)
      } catch {
        message.error(`${s.label} 不是有效的 JSON`)
        return null
      }
    } else if (s.kind === 'switch') {
      body[s.key] = v ? 1 : 0
    } else {
      body[s.key] = v === '' ? null : v
    }
  }
  return body
}

async function submit() {
  const body = build()
  if (!body) return
  saving.value = true
  const ok = await run(
    () => (props.server ? admin.put(`/servers/${type.value}/${props.server.id}`, body) : admin.post(`/servers/${type.value}`, body)),
    '已保存',
  )
  saving.value = false
  if (ok) {
    show.value = false
    emit('saved')
  }
}
</script>

<template>
  <NModal
    v-model:show="show"
    preset="card"
    :title="server ? `编辑 ${typeLabel(type)} 节点 #${server.id}` : '新建节点'"
    style="width: 820px; max-width: 96vw"
  >
    <NForm label-placement="top" size="small">
      <NGrid cols="1 s:2 m:3" responsive="screen" :x-gap="12">
        <NGi v-if="!server"><NFormItem label="类型"><NSelect v-model:value="type" :options="serverTypes" /></NFormItem></NGi>
        <NGi><NFormItem label="名称" required><NInput v-model:value="form.name as string" /></NFormItem></NGi>
        <NGi><NFormItem label="倍率" required><NInputNumber v-model:value="form.rate as number" :min="0" :step="0.1" style="width: 100%" /></NFormItem></NGi>
        <NGi><NFormItem label="连接地址" required><NInput v-model:value="form.host as string" placeholder="域名或 IP" /></NFormItem></NGi>
        <NGi><NFormItem label="连接端口" required><NInput v-model:value="form.port as string" placeholder="443 或 10000-20000" /></NFormItem></NGi>
        <NGi><NFormItem label="服务端口" required><NInputNumber v-model:value="form.server_port as number" :min="1" :max="65535" style="width: 100%" /></NFormItem></NGi>
        <NGi><NFormItem label="权限组" required><NSelect v-model:value="form.group_id as number[]" multiple :options="groupOptions" /></NFormItem></NGi>
        <NGi><NFormItem label="路由"><NSelect v-model:value="form.route_id as number[]" multiple clearable :options="routeOptions" /></NFormItem></NGi>
        <NGi><NFormItem label="父节点"><NSelect v-model:value="form.parent_id as number" clearable :options="parentOptions" placeholder="无" /></NFormItem></NGi>
        <NGi><NFormItem label="显示"><NSwitch v-model:value="form.show as boolean" /></NFormItem></NGi>
      </NGrid>
      <NFormItem label="标签"><NDynamicTags v-model:value="form.tags as string[]" /></NFormItem>
      <NDivider title-placement="left">{{ typeLabel(type) }} 配置</NDivider>
      <NGrid cols="1 s:2" responsive="screen" :x-gap="12">
        <NGi v-for="s in visible" :key="s.key" :span="s.kind === 'json' || s.kind === 'raw' ? 2 : 1">
          <NFormItem :label="s.label" :required="s.required">
            <NSelect v-if="s.kind === 'select'" v-model:value="form[s.key] as string" :options="s.options" />
            <NSwitch v-else-if="s.kind === 'switch'" v-model:value="form[s.key] as boolean" />
            <NInputNumber v-else-if="s.kind === 'number'" v-model:value="form[s.key] as number" :min="0" style="width: 100%" />
            <NInput
              v-else-if="s.kind === 'json' || s.kind === 'raw'"
              v-model:value="form[s.key] as string"
              type="textarea"
              class="mono"
              :autosize="{ minRows: 2, maxRows: 12 }"
              :placeholder="s.placeholder || s.help || 'JSON，留空为不设置'"
            />
            <NInput v-else v-model:value="form[s.key] as string" :placeholder="s.placeholder" />
          </NFormItem>
          <div v-if="s.help && s.kind === 'json'" class="muted help">{{ s.help }}</div>
        </NGi>
      </NGrid>
    </NForm>
    <template #footer>
      <NSpace justify="end">
        <NButton @click="show = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="submit">保存</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.help { margin: -18px 0 12px; }
</style>
