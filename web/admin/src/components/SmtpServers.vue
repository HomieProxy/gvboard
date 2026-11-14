<script setup lang="ts">
import { ref, watch } from 'vue'
import { ArrowDownIcon, ArrowUpIcon, Loader2Icon, MailIcon, PlusIcon, Trash2Icon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import FormItem from './common/FormItem.vue'
import NumInput from './common/NumInput.vue'
import SelectBox from './common/SelectBox.vue'
import BoolSwitch from './common/BoolSwitch.vue'
import StatusBadge from './common/StatusBadge.vue'

/** SMTP 服务器列表（设置项 email_smtp_servers，JSON 字符串），最多 10 个，按顺序故障转移。 */
interface Server {
  name: string
  host: string
  port: number
  encryption: string
  username: string
  password: string
  from_address: string
  enable: boolean
}

const props = defineProps<{ modelValue: string; testing?: number | null }>()
const emit = defineEmits<{ 'update:modelValue': [v: string]; test: [index: number] }>()

const MAX = 10
const encryptions = [
  { label: '不加密', value: '' },
  { label: 'SSL（常用 465 端口）', value: 'ssl' },
  { label: 'TLS / STARTTLS（常用 587 端口）', value: 'tls' },
]

const list = ref<Server[]>([])
let last = ''

function parse(raw: string): Server[] {
  try {
    const v = raw ? JSON.parse(raw) : []
    if (!Array.isArray(v)) return []
    return v.map((s) => ({
      name: s.name || '', host: s.host || '', port: Number(s.port) || 0, encryption: s.encryption || '',
      username: s.username || '', password: s.password || '', from_address: s.from_address || '', enable: !!s.enable,
    }))
  } catch {
    return []
  }
}

watch(
  () => props.modelValue,
  (v) => {
    if (v !== last) {
      list.value = parse(v)
      last = v
    }
  },
  { immediate: true },
)

function commit() {
  last = JSON.stringify(list.value)
  emit('update:modelValue', last)
}
function add() {
  list.value.push({ name: '', host: '', port: 465, encryption: 'ssl', username: '', password: '', from_address: '', enable: true })
  commit()
}
function remove(i: number) {
  list.value.splice(i, 1)
  commit()
}
function move(i: number, d: number) {
  const j = i + d
  if (j < 0 || j >= list.value.length) return
  const [s] = list.value.splice(i, 1)
  list.value.splice(j, 0, s)
  commit()
}
function set<K extends keyof Server>(i: number, k: K, v: Server[K]) {
  list.value[i][k] = v
  commit()
}
</script>

<template>
  <div class="grid w-full gap-3">
    <div v-for="(s, i) in list" :key="i" class="rounded-lg border bg-card">
      <div class="flex items-center gap-2 border-b px-3 py-2">
        <span class="text-sm font-medium">#{{ i + 1 }} {{ s.name || s.host || '未命名' }}</span>
        <StatusBadge v-if="i === 0" tone="primary">首选</StatusBadge>
        <StatusBadge v-if="!s.enable">已停用</StatusBadge>
        <div class="ml-auto flex items-center gap-1">
          <Button size="sm" variant="ghost" :disabled="testing != null" title="保存后向当前登录账号发送测试邮件（只用这台服务器）" @click="emit('test', i)">
            <Loader2Icon v-if="testing === i" class="animate-spin" />
            <MailIcon v-else />
            测试
          </Button>
          <Button size="icon" variant="ghost" class="size-8" :disabled="i === 0" title="上移" @click="move(i, -1)"><ArrowUpIcon /></Button>
          <Button size="icon" variant="ghost" class="size-8" :disabled="i === list.length - 1" title="下移" @click="move(i, 1)"><ArrowDownIcon /></Button>
          <Button size="icon" variant="ghost" class="size-8 text-destructive" title="删除" @click="remove(i)"><Trash2Icon /></Button>
        </div>
      </div>
      <div class="grid gap-3 p-3 sm:grid-cols-2">
        <FormItem label="名称" help="用于区分服务器，可留空">
          <Input :model-value="s.name" maxlength="32" placeholder="如：阿里云邮件推送" @update:model-value="(v) => set(i, 'name', String(v))" />
        </FormItem>
        <FormItem label="启用">
          <div class="flex h-9 items-center"><BoolSwitch :model-value="s.enable" @update:model-value="(v) => set(i, 'enable', !!v)" /></div>
        </FormItem>
        <FormItem label="服务器地址" required>
          <Input :model-value="s.host" placeholder="smtp.example.com" @update:model-value="(v) => set(i, 'host', String(v).trim())" />
        </FormItem>
        <FormItem label="端口" help="留空时 SSL 为 465、TLS 为 587、不加密为 25">
          <NumInput :model-value="s.port || null" :min="0" :max="65535" @update:model-value="(v) => set(i, 'port', v ?? 0)" />
        </FormItem>
        <FormItem label="加密方式">
          <SelectBox :model-value="s.encryption" :options="encryptions" @update:model-value="(v) => set(i, 'encryption', String(v))" />
        </FormItem>
        <FormItem label="发件地址" required help="可写成 名称 &lt;地址&gt;">
          <Input :model-value="s.from_address" placeholder="noreply@example.com" @update:model-value="(v) => set(i, 'from_address', String(v).trim())" />
        </FormItem>
        <FormItem label="账号">
          <Input :model-value="s.username" autocomplete="off" spellcheck="false" @update:model-value="(v) => set(i, 'username', String(v).trim())" />
        </FormItem>
        <FormItem label="密码">
          <Input :model-value="s.password" class="font-mono" autocomplete="off" spellcheck="false" @update:model-value="(v) => set(i, 'password', String(v))" />
        </FormItem>
      </div>
    </div>
    <div class="flex items-center gap-3">
      <Button variant="outline" :disabled="list.length >= MAX" @click="add"><PlusIcon />添加 SMTP 服务器</Button>
      <span class="text-xs text-muted-foreground">{{ list.length }} / {{ MAX }}，未添加任何服务器时不发送邮件</span>
    </div>
  </div>
</template>
