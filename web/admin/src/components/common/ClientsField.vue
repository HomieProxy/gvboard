<script setup lang="ts">
import { ref, watch } from 'vue'
import { PlusIcon, Trash2Icon, ChevronUpIcon, ChevronDownIcon } from '@lucide/vue'
import { clientIcon, clientPlatforms, parseClients, type ClientDownload, type ClientPlatform } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import SelectBox from './SelectBox.vue'
import { moveItem } from '../../helpers'

// 值为 JSON 字符串（设置 client_downloads）
const props = defineProps<{ modelValue: string | null }>()
const emit = defineEmits<{ 'update:modelValue': [v: string] }>()

const list = ref<ClientDownload[]>([])
let last = ''
watch(
  () => props.modelValue,
  (v) => {
    if ((v || '') === last) return
    last = v || ''
    list.value = parseClients(v)
  },
  { immediate: true },
)

function commit() {
  last = list.value.length ? JSON.stringify(list.value) : ''
  emit('update:modelValue', last)
}
function add() {
  const prev = list.value[list.value.length - 1]
  list.value.push({ platform: prev?.platform || 'windows', name: '', url: '', icon: '' })
  commit()
}
function remove(i: number) {
  list.value.splice(i, 1)
  commit()
}
function move(i: number, d: number) {
  list.value = moveItem(list.value, i, i + d)
  commit()
}
function update<K extends keyof ClientDownload>(i: number, k: K, v: ClientDownload[K]) {
  list.value[i] = { ...list.value[i], [k]: v }
  commit()
}
function setPlatform(i: number, v: unknown) {
  update(i, 'platform', v as ClientPlatform)
}
const tips = { name: '客户端名称，如 Clash Verge', url: '下载地址 https://', icon: '图标地址（留空使用平台预置图标）' }
const hideBroken = (e: Event) => ((e.target as HTMLImageElement).style.visibility = 'hidden')
const showImg = (e: Event) => ((e.target as HTMLImageElement).style.visibility = '')
</script>

<template>
  <div class="grid w-full gap-2">
    <div v-for="(c, i) in list" :key="i" class="flex items-start gap-3 rounded-lg border bg-muted/20 p-3">
      <div class="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-background">
        <img :src="clientIcon(c)" alt="" class="size-6 object-contain" @error="hideBroken" @load="showImg" />
      </div>
      <div class="grid min-w-0 flex-1 gap-2 sm:grid-cols-[9rem_minmax(0,1fr)]">
        <SelectBox :model-value="c.platform" :options="clientPlatforms" @update:model-value="(v) => setPlatform(i, v)" />
        <Input :model-value="c.name" :placeholder="tips.name" @update:model-value="(v) => update(i, 'name', String(v))" />
        <Input class="sm:col-span-2" :model-value="c.url" :placeholder="tips.url" @update:model-value="(v) => update(i, 'url', String(v).trim())" />
        <Input class="sm:col-span-2" :model-value="c.icon" :placeholder="tips.icon" @update:model-value="(v) => update(i, 'icon', String(v).trim())" />
      </div>
      <div class="flex shrink-0 flex-col gap-1">
        <Button variant="ghost" size="icon" class="size-8" :disabled="i === 0" title="上移" @click="move(i, -1)"><ChevronUpIcon /></Button>
        <Button variant="ghost" size="icon" class="size-8" :disabled="i === list.length - 1" title="下移" @click="move(i, 1)"><ChevronDownIcon /></Button>
        <Button variant="ghost" size="icon" class="size-8 text-destructive hover:text-destructive" title="删除" @click="remove(i)"><Trash2Icon /></Button>
      </div>
    </div>
    <div>
      <Button variant="outline" size="sm" @click="add"><PlusIcon />添加客户端</Button>
    </div>
  </div>
</template>
