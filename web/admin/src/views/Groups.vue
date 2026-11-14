<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { PlusIcon, RefreshCwIcon, PencilIcon, Trash2Icon, Loader2Icon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import FormItem from '../components/common/FormItem.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { useList } from '../paged'
import { invalidate } from '../refs'
import type { ServerGroup } from '../types'

const { rows, loading, load } = useList(() => admin.get<ServerGroup[]>('/servers/groups'))

const columns: Column[] = [
  { key: 'id', title: '组ID', class: 'w-20' },
  { key: 'name', title: '组名称' },
  { key: 'user_count', title: '用户数量', align: 'center' },
  { key: 'server_count', title: '节点数量', align: 'center' },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]

const show = ref(false)
const saving = ref(false)
const editing = ref<ServerGroup | null>(null)
const name = ref('')

function open(g: ServerGroup | null) {
  editing.value = g
  name.value = g?.name || ''
  show.value = true
}

async function submit() {
  if (!name.value.trim()) {
    message.warning('请输入组名')
    return
  }
  const body = { name: name.value.trim() }
  saving.value = true
  const ok = await run(() => (editing.value ? admin.put(`/servers/groups/${editing.value.id}`, body) : admin.post('/servers/groups', body)), '保存成功')
  saving.value = false
  if (ok) {
    show.value = false
    invalidate('groups')
    load()
  }
}

async function remove(g: ServerGroup) {
  if (await confirm(`确定要删除权限组「${g.name}」吗？`, () => admin.del(`/servers/groups/${g.id}`), '删除成功')) {
    invalidate('groups')
    load()
  }
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="权限组管理" description="订阅与节点各自选择权限组，组相同的节点即可被该订阅使用。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <Button @click="open(null)"><PlusIcon />添加权限组</Button>
    </PageHeader>

    <DataTable :columns="columns" :rows="rows" :loading="loading">
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-name="{ row }"><span class="font-medium">{{ row.name }}</span></template>
      <template #cell-user_count="{ row }"><span class="tabular-nums">{{ row.user_count ?? 0 }}</span></template>
      <template #cell-server_count="{ row }"><span class="tabular-nums">{{ row.server_count ?? 0 }}</span></template>
      <template #cell-op="{ row }">
        <div class="flex justify-end gap-0.5">
          <Button variant="ghost" size="icon" class="size-8" @click="open(row)"><PencilIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" @click="remove(row)"><Trash2Icon /></Button>
        </div>
      </template>
    </DataTable>

    <Dialog v-model:open="show">
      <DialogContent class="sm:max-w-md">
        <DialogHeader><DialogTitle>{{ editing ? '编辑权限组' : '创建权限组' }}</DialogTitle></DialogHeader>
        <FormItem label="组名">
          <Input v-model="name" placeholder="请输入组名" @keyup.enter="submit" />
        </FormItem>
        <DialogFooter>
          <Button variant="outline" @click="show = false">取消</Button>
          <Button :disabled="saving" @click="submit"><Loader2Icon v-if="saving" class="animate-spin" />提交</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
