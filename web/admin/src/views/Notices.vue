<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { PlusIcon, RefreshCwIcon, PencilIcon, Trash2Icon, Loader2Icon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Switch } from '@gv/shared/ui/switch'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import FormItem from '../components/common/FormItem.vue'
import TagsField from '../components/common/TagsField.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { useList } from '../paged'
import type { Notice } from '../types'

const { rows, loading, load } = useList(() => admin.get<Notice[]>('/notices'))

const columns: Column[] = [
  { key: 'id', title: 'ID', class: 'w-16' },
  { key: 'show', title: '显示', align: 'center', class: 'w-16' },
  { key: 'title', title: '标题' },
  { key: 'created_at', title: '创建时间' },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]

async function setShow(n: Notice, v: boolean) {
  if (await run(() => admin.post(`/notices/${n.id}/show`, { show: v }))) n.show = v ? 1 : 0
}
async function remove(n: Notice) {
  if (await confirm(`确定要删除公告「${n.title}」吗？`, () => admin.del(`/notices/${n.id}`), '删除成功')) load()
}

const show = ref(false)
const saving = ref(false)
const editing = ref<Notice | null>(null)
const form = ref({ title: '', content: '', img_url: '', tags: [] as string[] })

function open(n: Notice | null) {
  editing.value = n
  form.value = { title: n?.title || '', content: n?.content || '', img_url: n?.img_url || '', tags: n?.tags || [] }
  show.value = true
}
async function submit() {
  const f = form.value
  if (!f.title.trim() || !f.content.trim()) {
    message.warning('请输入公告标题与公告内容')
    return
  }
  const body = { title: f.title, content: f.content, img_url: f.img_url || null, tags: f.tags }
  saving.value = true
  const ok = await run(() => (editing.value ? admin.put(`/notices/${editing.value.id}`, body) : admin.post('/notices', body)), '保存成功')
  saving.value = false
  if (ok) {
    show.value = false
    load()
  }
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="公告管理" description="新添加的公告默认不显示；标签为「弹窗」的公告会在用户登录后弹出。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <Button @click="open(null)"><PlusIcon />添加公告</Button>
    </PageHeader>

    <DataTable :columns="columns" :rows="rows" :loading="loading">
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-show="{ row }"><Switch :model-value="!!row.show" @update:model-value="(v: boolean) => setShow(row, v)" /></template>
      <template #cell-title="{ row }">
        <div class="flex flex-wrap items-center gap-1.5">
          <span class="font-medium">{{ row.title }}</span>
          <span v-for="t in row.tags || []" :key="t" class="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{{ t }}</span>
        </div>
      </template>
      <template #cell-created_at="{ row }"><span class="whitespace-nowrap text-muted-foreground">{{ formatTime(row.created_at) }}</span></template>
      <template #cell-op="{ row }">
        <div class="flex justify-end gap-0.5">
          <Button variant="ghost" size="icon" class="size-8" @click="open(row)"><PencilIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" @click="remove(row)"><Trash2Icon /></Button>
        </div>
      </template>
    </DataTable>

    <Dialog v-model:open="show">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader><DialogTitle>{{ editing ? '编辑公告' : '添加公告' }}</DialogTitle></DialogHeader>
        <div class="grid gap-4">
          <FormItem label="标题">
            <Input v-model="form.title" maxlength="255" placeholder="请输入公告标题" />
          </FormItem>
          <FormItem label="公告内容" help="支持 Markdown">
            <Textarea v-model="form.content" rows="10" placeholder="请输入公告内容" />
          </FormItem>
          <FormItem label="公告标签">
            <TagsField v-model="form.tags" placeholder="输入后回车添加标签" />
          </FormItem>
          <FormItem label="图片URL">
            <Input v-model="form.img_url" placeholder="请输入图片URL" />
          </FormItem>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="show = false">取消</Button>
          <Button :disabled="saving" @click="submit"><Loader2Icon v-if="saving" class="animate-spin" />提交</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
