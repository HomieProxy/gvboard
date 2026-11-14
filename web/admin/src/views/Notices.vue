<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NCard, NDataTable, NButton, NModal, NForm, NFormItem, NInput, NDynamicTags, NSpace, type DataTableColumns } from 'naive-ui'
import { formatTime } from '@gv/shared'
import { admin } from '../api'
import { run } from '../ui'
import { useList } from '../paged'
import { actions, btn, confirmBtn, sw } from '../helpers'
import type { Notice } from '../types'

const { rows, loading, load } = useList(() => admin.get<Notice[]>('/notices'))

async function setShow(n: Notice, v: boolean) {
  if (await run(() => admin.post(`/notices/${n.id}/show`, { show: v }))) n.show = v ? 1 : 0
}
async function remove(n: Notice) {
  if (await run(() => admin.del(`/notices/${n.id}`), '已删除')) load()
}

const columns: DataTableColumns<Notice> = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '标题', key: 'title' },
  { title: '标签', key: 'tags', render: (n) => (n.tags || []).join('、') || '-' },
  { title: '显示', key: 'show', width: 70, render: (n) => sw(!!n.show, (v) => setShow(n, v)) },
  { title: '创建时间', key: 'created_at', render: (n) => formatTime(n.created_at) },
  {
    title: '操作',
    key: 'op',
    width: 110,
    render: (n) => actions(btn('编辑', () => open(n)), confirmBtn('删除', '确定删除该公告？', () => remove(n))),
  },
]

const show = ref(false)
const editing = ref<Notice | null>(null)
const form = ref({ title: '', content: '', img_url: '', tags: [] as string[] })
function open(n: Notice | null) {
  editing.value = n
  form.value = { title: n?.title || '', content: n?.content || '', img_url: n?.img_url || '', tags: n?.tags || [] }
  show.value = true
}
async function submit() {
  const f = form.value
  const body = { title: f.title, content: f.content, img_url: f.img_url || null, tags: f.tags }
  const ok = await run(() => (editing.value ? admin.put(`/notices/${editing.value.id}`, body) : admin.post('/notices', body)), editing.value ? '已保存' : '已创建（默认隐藏）')
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
        <NButton type="primary" @click="open(null)">新建公告</NButton>
        <NButton @click="load">刷新</NButton>
      </div>
      <NDataTable size="small" :columns="columns" :data="rows" :loading="loading" :row-key="(n: Notice) => n.id" />
    </NCard>
    <NModal v-model:show="show" preset="card" :title="editing ? '编辑公告' : '新建公告'" style="width: 720px; max-width: 95vw">
      <NForm label-placement="top">
        <NFormItem label="标题" required><NInput v-model:value="form.title" maxlength="255" /></NFormItem>
        <NFormItem label="内容（Markdown / HTML）" required>
          <NInput v-model:value="form.content" type="textarea" :autosize="{ minRows: 8, maxRows: 24 }" />
        </NFormItem>
        <NFormItem label="封面图 URL"><NInput v-model:value="form.img_url" placeholder="https://，可留空" /></NFormItem>
        <NFormItem label="标签"><NDynamicTags v-model:value="form.tags" /></NFormItem>
      </NForm>
      <template #footer>
        <NSpace justify="end"><NButton @click="show = false">取消</NButton><NButton type="primary" @click="submit">保存</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
