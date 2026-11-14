<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RefreshCwIcon, CheckIcon, SettingsIcon, Loader2Icon, InfoIcon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@gv/shared/ui/card'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
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
  if (await run(() => admin.put('/themes/active', { id: t.id }), '保存成功')) active.value = t.id
}

const editing = ref<ThemeView | null>(null)
const show = ref(false)
const saving = ref(false)
const values = ref<Record<string, string>>({})
async function openConfig(t: ThemeView) {
  await run(async () => {
    const r = await admin.get<Record<string, unknown>>(`/themes/${t.id}/config`)
    values.value = Object.fromEntries(t.configs.map((f) => [f.field_name, r?.[f.field_name] === undefined || r[f.field_name] === null ? '' : String(r[f.field_name])]))
    editing.value = t
    show.value = true
  })
}
async function saveConfig() {
  if (!editing.value) return
  saving.value = true
  const ok = await run(() => admin.put(`/themes/${editing.value!.id}/config`, values.value), '保存成功')
  saving.value = false
  if (ok) show.value = false
}

const options = (o?: Record<string, string>) => Object.entries(o || {}).map(([value, label]) => ({ value, label }))

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="主题配置">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
    </PageHeader>

    <div class="mb-4 flex items-start gap-2 rounded-lg border bg-muted/40 px-4 py-3 text-sm">
      <InfoIcon class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <span>如果你采用前后分离的方式部署，那么主题配置将不会生效。外部主题放在服务器的主题目录（配置项 server.theme_dir）中即可出现，无需重启。</span>
    </div>

    <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
      <template v-if="loading && !themes.length">
        <Skeleton v-for="i in 3" :key="i" class="h-72 rounded-xl" />
      </template>
      <Card v-for="t in themes" :key="t.id" class="gap-0 overflow-hidden py-0" :class="t.id === active ? 'ring-2 ring-primary' : ''">
        <div class="aspect-video bg-muted">
          <img v-if="t.images" :src="t.images" :alt="t.name" class="size-full object-cover" />
          <div v-else class="flex size-full items-center justify-center text-sm text-muted-foreground">暂无预览图</div>
        </div>
        <CardHeader class="pt-4">
          <CardTitle class="flex items-center gap-2">
            {{ t.name }}
            <span v-if="t.id === active" class="rounded bg-primary px-1.5 py-0.5 text-xs font-normal text-primary-foreground">当前主题</span>
            <span v-if="t.builtin" class="rounded bg-muted px-1.5 py-0.5 text-xs font-normal text-muted-foreground">内置</span>
          </CardTitle>
          <CardDescription>{{ t.description || '无描述' }}</CardDescription>
        </CardHeader>
        <CardContent class="pb-4 text-xs text-muted-foreground">ID：{{ t.id }} · 版本：{{ t.version || '-' }}</CardContent>
        <CardFooter class="gap-2 border-t py-3">
          <Button size="sm" :variant="t.id === active ? 'secondary' : 'default'" :disabled="t.id === active" @click="activate(t)">
            <CheckIcon />{{ t.id === active ? '当前主题' : '激活主题' }}
          </Button>
          <Button size="sm" variant="outline" :disabled="!t.configs.length" @click="openConfig(t)"><SettingsIcon />主题设置</Button>
        </CardFooter>
      </Card>
    </div>

    <Dialog v-model:open="show">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader><DialogTitle>配置{{ editing?.name }}主题</DialogTitle></DialogHeader>
        <div v-if="editing" class="grid gap-4">
          <FormItem v-for="f in editing.configs" :key="f.field_name" :label="f.label">
            <SelectBox
              v-if="f.field_type === 'select'"
              :model-value="values[f.field_name]"
              :options="options(f.select_options)"
              @update:model-value="(v) => (values[f.field_name] = String(v ?? ''))"
            />
            <Textarea v-else-if="f.field_type === 'textarea'" v-model="values[f.field_name]" rows="4" :placeholder="f.placeholder" />
            <Input v-else v-model="values[f.field_name]" :placeholder="f.placeholder" />
          </FormItem>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="show = false">取消</Button>
          <Button :disabled="saving" @click="saveConfig"><Loader2Icon v-if="saving" class="animate-spin" />提交</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
