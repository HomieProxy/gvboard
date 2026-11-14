<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NCard, NDataTable, NButton, NModal, NInput, NSpace, type DataTableColumns } from 'naive-ui'
import { admin } from '../api'
import { run } from '../ui'
import { useList } from '../paged'
import { actions, btn, confirmBtn } from '../helpers'
import { invalidate } from '../refs'
import type { ServerGroup } from '../types'

const { rows, loading, load } = useList(() => admin.get<ServerGroup[]>('/servers/groups'))

const show = ref(false)
const editing = ref<ServerGroup | null>(null)
const name = ref('')

function open(g: ServerGroup | null) {
  editing.value = g
  name.value = g?.name || ''
  show.value = true
}

async function submit() {
  const body = { name: name.value.trim() }
  const ok = await run(() => (editing.value ? admin.put(`/servers/groups/${editing.value.id}`, body) : admin.post('/servers/groups', body)), '已保存')
  if (ok) {
    show.value = false
    invalidate('groups')
    load()
  }
}

async function remove(g: ServerGroup) {
  if (await run(() => admin.del(`/servers/groups/${g.id}`), '已删除')) {
    invalidate('groups')
    load()
  }
}

const columns: DataTableColumns<ServerGroup> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '名称', key: 'name' },
  { title: '使用的订阅数', key: 'user_count' },
  { title: '使用的节点数', key: 'server_count' },
  {
    title: '操作',
    key: 'op',
    width: 120,
    render: (g) => actions(btn('编辑', () => open(g)), confirmBtn('删除', '确定删除该权限组？', () => remove(g))),
  },
]

onMounted(load)
</script>

<template>
  <div class="page">
    <NCard size="small">
      <div class="toolbar">
        <NButton type="primary" @click="open(null)">新建权限组</NButton>
        <NButton @click="load">刷新</NButton>
        <span class="muted">权限组决定订阅可以使用哪些节点：套餐与节点各自选择权限组，组相同即可使用。</span>
      </div>
      <NDataTable size="small" :columns="columns" :data="rows" :loading="loading" :row-key="(g: ServerGroup) => g.id" />
    </NCard>
    <NModal v-model:show="show" preset="card" :title="editing ? '编辑权限组' : '新建权限组'" style="width: 420px; max-width: 95vw">
      <NInput v-model:value="name" placeholder="组名" @keyup.enter="submit" />
      <template #footer>
        <NSpace justify="end"><NButton @click="show = false">取消</NButton><NButton type="primary" @click="submit">保存</NButton></NSpace>
      </template>
    </NModal>
  </div>
</template>
