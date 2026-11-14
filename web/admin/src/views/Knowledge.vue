<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NCard, NDataTable, NButton, NModal, NForm, NFormItem, NInput, NSelect, NAutoComplete, NSpace, NGrid, NGi, type DataTableColumns } from 'naive-ui'
import { formatTime } from '@gv/shared'
import { admin } from '../api'
import { run } from '../ui'
import { useList } from '../paged'
import { actions, btn, confirmBtn, sw } from '../helpers'
import type { ArticleBrief } from '../types'

const { rows, loading, load } = useList(() => admin.get<ArticleBrief[]>('/knowledge'))
const categories = ref<string[]>([])

const varsHelp =
  '可用变量：{{siteName}}、{{subscribeUrl}}、{{urlEncodeSubscribeUrl}}、{{safeBase64SubscribeUrl}}、{{subscribeToken}}；' +
  '用 <!--access start--> 与 <!--access end--> 包裹的内容只对有可用订阅的用户显示。'

const languages = [
  { label: '简体中文', value: 'zh-CN' },
  { label: '繁體中文', value: 'zh-TW' },
  { label: 'English', value: 'en-US' },
  { label: '日本語', value: 'ja-JP' },
  { label: 'Tiếng Việt', value: 'vi-VN' },
  { label: '한국어', value: 'ko-KR' },
  { label: 'فارسی', value: 'fa-IR' },
]

async function setShow(a: ArticleBrief, v: boolean) {
  if (await run(() => admin.post(`/knowledge/${a.id}/show`, { show: v }))) a.show = v ? 1 : 0
}
async function remove(a: ArticleBrief) {
  if (await run(() => admin.del(`/knowledge/${a.id}`), '已删除')) load()
}
async function move(i: number, d: number) {
  const list = [...rows.value]
  const j = i + d
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j], list[i]]
  rows.value = list
  if (!(await run(() => admin.post('/knowledge/sort', { ids: list.map((a) => a.id) })))) load()
}

const columns: DataTableColumns<ArticleBrief> = [
  {
    title: '排序',
    key: 'sort',
    width: 70,
    render: (_, i) => actions(btn('↑', () => move(i, -1), 'default'), btn('↓', () => move(i, 1), 'default')),
  },
  { title: 'ID', key: 'id', width: 60 },
  { title: '标题', key: 'title' },
  { title: '分类', key: 'category' },
  { title: '语言', key: 'language', width: 80 },
  { title: '显示', key: 'show', width: 70, render: (a) => sw(!!a.show, (v) => setShow(a, v)) },
  { title: '更新时间', key: 'updated_at', render: (a) => formatTime(a.updated_at) },
  {
    title: '操作',
    key: 'op',
    width: 110,
    render: (a) => actions(btn('编辑', () => open(a)), confirmBtn('删除', '确定删除该文章？', () => remove(a))),
  },
]

const show = ref(false)
const editing = ref<number | null>(null)
const form = ref({ title: '', category: '', language: 'zh-CN', body: '' })

async function open(a: ArticleBrief | null) {
  admin.get<string[]>('/knowledge/categories').then((c) => (categories.value = c || [])).catch(() => {})
  editing.value = a?.id ?? null
  if (!a) {
    form.value = { title: '', category: '', language: 'zh-CN', body: '' }
    show.value = true
    return
  }
  await run(async () => {
    const k = await admin.get<{ title: string; category: string; language: string; body: string }>(`/knowledge/${a.id}`)
    form.value = { title: k.title, category: k.category, language: k.language, body: k.body }
    show.value = true
  })
}

async function submit() {
  const ok = await run(
    () => (editing.value ? admin.put(`/knowledge/${editing.value}`, form.value) : admin.post('/knowledge', form.value)),
    editing.value ? '已保存' : '已创建（默认隐藏）',
  )
  if (ok) {
    show.value = false
    load()
  }
}

onMounted(load)
</script>

<template>
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NButton type="primary" @click="open(null)">新建文章</NButton>
        <NButton @click="load">刷新</NButton>
      </div>
      <NDataTable size="small" :columns="columns" :data="rows" :loading="loading" :row-key="(a: ArticleBrief) => a.id" />
    </NCard>
    <NModal v-model:show="show" preset="card" :title="editing ? '编辑文章' : '新建文章'" style="width: 860px; max-width: 96vw">
      <NForm label-placement="top">
        <NGrid cols="1 s:3" responsive="screen" :x-gap="12">
          <NGi><NFormItem label="标题" required><NInput v-model:value="form.title" /></NFormItem></NGi>
          <NGi>
            <NFormItem label="分类" required>
              <NAutoComplete v-model:value="form.category" :options="categories.map((c) => ({ label: c, value: c }))" :get-show="() => true" />
            </NFormItem>
          </NGi>
          <NGi><NFormItem label="语言" required><NSelect v-model:value="form.language" :options="languages" /></NFormItem></NGi>
        </NGrid>
        <NFormItem label="正文（Markdown / HTML）" required>
          <NInput v-model:value="form.body" type="textarea" class="mono" :autosize="{ minRows: 12, maxRows: 30 }" />
        </NFormItem>
        <div class="muted">{{ varsHelp }}</div>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="show = false">取消</NButton><NButton type="primary" @click="submit">保存</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
