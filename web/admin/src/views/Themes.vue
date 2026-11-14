<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NCard, NGrid, NGi, NButton, NSpace, NTag, NModal, NForm, NFormItem, NInput, NSelect, NSpin, NImage, NAlert } from 'naive-ui'
import { admin } from '../api'
import { errText, message, run } from '../ui'
import type { ThemeView } from '../types'

const themes = ref<ThemeView[]>([])
const active = ref('')
const loading = ref(true)

async function load() {
  loading.value = true
  try {
    const r = await admin.get<{ themes: ThemeView[]; active: string }>('/themes')
    themes.value = r.themes || []
    active.value = r.active
  } catch (e) {
    message.error(errText(e))
  } finally {
    loading.value = false
  }
}

async function activate(t: ThemeView) {
  if (await run(() => admin.put('/themes/active', { id: t.id }), `已切换为 ${t.name}`)) active.value = t.id
}

const editing = ref<ThemeView | null>(null)
const show = ref(false)
const values = ref<Record<string, string>>({})
async function openConfig(t: ThemeView) {
  await run(async () => {
    const r = await admin.get<Record<string, unknown>>(`/themes/${t.id}/config`)
    values.value = Object.fromEntries(t.configs.map((f) => [f.field_name, r?.[f.field_name] === undefined ? '' : String(r[f.field_name])]))
    editing.value = t
    show.value = true
  })
}
async function saveConfig() {
  if (!editing.value) return
  if (await run(() => admin.put(`/themes/${editing.value!.id}/config`, values.value), '已保存')) show.value = false
}

const options = (o?: Record<string, string>) => Object.entries(o || {}).map(([value, label]) => ({ value, label }))

onMounted(load)
</script>

<template>
  <div class="page">
    <NAlert type="info" style="margin-bottom: 12px">
      主题只在前后端不分离部署时生效。外部主题放在服务器的主题目录（配置项 server.theme_dir）中即可出现，无需重启。
    </NAlert>
    <NSpin :show="loading">
      <NGrid cols="1 s:2 l:3" responsive="screen" :x-gap="12" :y-gap="12">
        <NGi v-for="t in themes" :key="t.id">
          <NCard :title="t.name" size="small">
            <template #header-extra>
              <NSpace size="small">
                <NTag v-if="t.id === active" type="success" size="small" :bordered="false">使用中</NTag>
                <NTag v-if="t.builtin" size="small" :bordered="false">内置</NTag>
              </NSpace>
            </template>
            <NImage v-if="t.images" :src="t.images" width="100%" object-fit="cover" style="max-height: 180px; margin-bottom: 8px" />
            <div class="muted">{{ t.description || '无描述' }}</div>
            <div class="muted">ID：{{ t.id }}　版本：{{ t.version || '-' }}</div>
            <template #action>
              <NSpace>
                <NButton size="small" type="primary" :disabled="t.id === active" @click="activate(t)">启用</NButton>
                <NButton size="small" :disabled="!t.configs.length" @click="openConfig(t)">主题设置</NButton>
              </NSpace>
            </template>
          </NCard>
        </NGi>
      </NGrid>
    </NSpin>
    <NModal v-model:show="show" preset="card" :title="`主题设置：${editing?.name}`" style="width: 600px; max-width: 95vw">
      <NForm v-if="editing" label-placement="top">
        <NFormItem v-for="f in editing.configs" :key="f.field_name" :label="f.label">
          <NSelect v-if="f.field_type === 'select'" v-model:value="values[f.field_name]" :options="options(f.select_options)" />
          <NInput
            v-else
            v-model:value="values[f.field_name]"
            :type="f.field_type === 'textarea' ? 'textarea' : 'text'"
            :autosize="f.field_type === 'textarea' ? { minRows: 3, maxRows: 14 } : undefined"
            :placeholder="f.placeholder"
          />
        </NFormItem>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="show = false">取消</NButton><NButton type="primary" @click="saveConfig">保存</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
