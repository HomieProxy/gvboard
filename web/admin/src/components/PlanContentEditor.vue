<script setup lang="ts">
import { ref, watch } from 'vue'
import { PlusIcon, Trash2Icon, CheckIcon, XIcon, GripVerticalIcon } from '@lucide/vue'
import { Textarea } from '@gv/shared/ui/textarea'
import { Input } from '@gv/shared/ui/input'
import { Button } from '@gv/shared/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@gv/shared/ui/tabs'

/**
 * 套餐描述：与原版用户端一致，内容可为 JSON 功能列表 [{"feature":"...","support":true}]，
 * 否则按 HTML 展示。功能列表模式下编辑结果序列化为 JSON。
 */
const model = defineModel<string>({ required: true })

interface Feature {
  feature: string
  support: boolean
}

function parse(s: string): Feature[] | null {
  try {
    const v = JSON.parse(s)
    if (Array.isArray(v) && v.every((x) => x && typeof x === 'object' && 'feature' in x)) {
      return v.map((x) => ({ feature: String(x.feature ?? ''), support: !!x.support }))
    }
  } catch {
    /* 不是 JSON，按 HTML */
  }
  return null
}

const mode = ref<'list' | 'html'>('html')
const items = ref<Feature[]>([])
let syncing = false

watch(
  model,
  (v) => {
    if (syncing) return
    const list = parse(v || '')
    if (list) {
      mode.value = 'list'
      items.value = list
    } else if (!v) {
      mode.value = 'list'
      items.value = []
    } else {
      mode.value = 'html'
    }
  },
  { immediate: true },
)

function emitList() {
  if (mode.value !== 'list') return
  const clean = items.value.filter((x) => x.feature.trim())
  if (JSON.stringify(parse(model.value || '') || []) === JSON.stringify(clean)) return
  syncing = true
  model.value = clean.length ? JSON.stringify(clean) : ''
  queueMicrotask(() => (syncing = false))
}
watch(items, emitList, { deep: true })

function switchMode(m: string | number) {
  if (m === mode.value) return
  if (m === 'list') {
    const list = parse(model.value || '')
    if (!list && model.value.trim()) {
      // HTML 转列表：按行拆分，去掉标签
      items.value = model.value
        .replace(/<br\s*\/?>/gi, '\n')
        .replace(/<\/(p|li|div)>/gi, '\n')
        .replace(/<[^>]+>/g, '')
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean)
        .map((feature) => ({ feature, support: true }))
    } else {
      items.value = list || []
    }
    mode.value = 'list'
  } else {
    mode.value = 'html'
  }
}

function add() {
  items.value.push({ feature: '', support: true })
}

let dragFrom = -1
function onDrop(to: number) {
  if (dragFrom < 0 || dragFrom === to) return
  const list = [...items.value]
  const [x] = list.splice(dragFrom, 1)
  list.splice(to, 0, x)
  items.value = list
  dragFrom = -1
}
</script>

<template>
  <div class="grid gap-2">
    <div class="flex items-center justify-between gap-2">
      <Tabs :model-value="mode" @update:model-value="switchMode">
        <TabsList class="h-8">
          <TabsTrigger value="list" class="text-xs">功能列表</TabsTrigger>
          <TabsTrigger value="html" class="text-xs">HTML</TabsTrigger>
        </TabsList>
      </Tabs>
      <span class="text-xs text-muted-foreground">{{ mode === 'list' ? '用户端显示为 ✓ / ✗ 列表' : '支持HTML' }}</span>
    </div>

    <template v-if="mode === 'list'">
      <div class="grid gap-1.5 rounded-md border p-2">
        <div
          v-for="(it, i) in items"
          :key="i"
          class="flex items-center gap-2"
          draggable="true"
          @dragstart="dragFrom = i"
          @dragover.prevent
          @drop="onDrop(i)"
        >
          <GripVerticalIcon class="size-4 shrink-0 cursor-grab text-muted-foreground" />
          <button
            type="button"
            :class="[
              'flex size-7 shrink-0 items-center justify-center rounded-md border transition-colors',
              it.support ? 'border-success/40 bg-success/10 text-success' : 'border-destructive/30 bg-destructive/10 text-destructive',
            ]"
            :title="it.support ? '支持（点击切换）' : '不支持（点击切换）'"
            @click="it.support = !it.support"
          >
            <CheckIcon v-if="it.support" class="size-4" />
            <XIcon v-else class="size-4" />
          </button>
          <Input v-model="it.feature" class="h-8" placeholder="功能描述，如：不限设备速度" />
          <Button variant="ghost" size="icon" class="size-8 shrink-0 text-muted-foreground hover:text-destructive" @click="items.splice(i, 1)">
            <Trash2Icon />
          </Button>
        </div>
        <Button variant="ghost" size="sm" class="justify-start text-muted-foreground" @click="add"><PlusIcon />添加一项</Button>
      </div>
    </template>
    <Textarea v-else v-model="model" rows="6" class="font-mono text-xs" placeholder="请输入套餐描述，支持HTML" />
  </div>
</template>
