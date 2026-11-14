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
import SelectBox from '../components/common/SelectBox.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { useList } from '../paged'
import { moveItem } from '../helpers'
import type { ArticleBrief } from '../types'

const { rows, loading, load } = useList(() => admin.get<ArticleBrief[]>('/knowledge'))
const categories = ref<string[]>([])

const languages = [
  { label: '简体中文', value: 'zh-CN' },
  { label: '繁體中文', value: 'zh-TW' },
  { label: 'English', value: 'en-US' },
  { label: '日本語', value: 'ja-JP' },
  { label: 'Tiếng Việt', value: 'vi-VN' },
  { label: '한국어', value: 'ko-KR' },
  { label: 'فارسی', value: 'fa-IR' },
]
const langName = (v: string) => languages.find((l) => l.value === v)?.label || v

const columns: Column[] = [
  { key: 'id', title: '文章ID', class: 'w-20' },
  { key: 'show', title: '显示', align: 'center', class: 'w-16' },
  { key: 'title', title: '标题' },
  { key: 'category', title: '分类' },
  { key: 'updated_at', title: '更新时间' },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]

async function setShow(a: ArticleBrief, v: boolean) {
  if (await run(() => admin.post(`/knowledge/${a.id}/show`, { show: v }))) a.show = v ? 1 : 0
}
async function remove(a: ArticleBrief) {
  if (await confirm(`确定要删除「${a.title}」吗？`, () => admin.del(`/knowledge/${a.id}`), '删除成功')) load()
}
async function reorder(from: number, to: number) {
  const list = moveItem(rows.value, from, to)
  rows.value = list
  if (!(await run(() => admin.post('/knowledge/sort', { ids: list.map((a) => a.id) })))) load()
}

const show = ref(false)
const saving = ref(false)
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
  const f = form.value
  if (!f.title.trim() || !f.category.trim() || !f.body.trim()) {
    message.warning('请填写标题、分类与内容')
    return
  }
  saving.value = true
  const ok = await run(() => (editing.value ? admin.put(`/knowledge/${editing.value}`, f) : admin.post('/knowledge', f)), '保存成功')
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
    <PageHeader title="知识库管理" description="新增的文章默认不显示；用户端按语言与分类展示。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <Button @click="open(null)"><PlusIcon />新增</Button>
    </PageHeader>

    <DataTable :columns="columns" :rows="rows" :loading="loading" draggable @reorder="reorder">
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-show="{ row }"><Switch :model-value="!!row.show" @update:model-value="(v: boolean) => setShow(row, v)" /></template>
      <template #cell-title="{ row }">
        <span class="font-medium">{{ row.title }}</span>
        <span class="ml-2 text-xs text-muted-foreground">{{ langName(row.language) }}</span>
      </template>
      <template #cell-category="{ row }"><span class="rounded bg-muted px-1.5 py-0.5 text-xs">{{ row.category }}</span></template>
      <template #cell-updated_at="{ row }"><span class="whitespace-nowrap text-muted-foreground">{{ formatTime(row.updated_at) }}</span></template>
      <template #cell-op="{ row }">
        <div class="flex justify-end gap-0.5">
          <Button variant="ghost" size="icon" class="size-8" @click="open(row)"><PencilIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" @click="remove(row)"><Trash2Icon /></Button>
        </div>
      </template>
    </DataTable>

    <Dialog v-model:open="show">
      <DialogContent class="max-h-[92svh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader><DialogTitle>{{ editing ? '编辑知识' : '新增知识' }}</DialogTitle></DialogHeader>
        <div class="grid gap-4 sm:grid-cols-3">
          <FormItem label="标题">
            <Input v-model="form.title" placeholder="请输入知识标题" />
          </FormItem>
          <FormItem label="分类">
            <Input v-model="form.category" list="kb-categories" placeholder="请输入分类，分类将会自动归集" />
            <datalist id="kb-categories">
              <option v-for="c in categories" :key="c" :value="c" />
            </datalist>
          </FormItem>
          <FormItem label="语言">
            <SelectBox :model-value="form.language" :options="languages" placeholder="请选择知识语言" @update:model-value="(v) => (form.language = String(v))" />
          </FormItem>
          <FormItem label="内容" class="sm:col-span-3" help="支持 Markdown。可用变量：{{siteName}}、{{subscribeUrl}}、{{urlEncodeSubscribeUrl}}、{{safeBase64SubscribeUrl}}、{{subscribeToken}}；用 <!--access start--> 与 <!--access end--> 包裹的内容只对有可用订阅的用户显示。">
            <Textarea v-model="form.body" rows="16" class="font-mono text-xs" />
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
