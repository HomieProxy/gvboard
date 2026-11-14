<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { PlusIcon, RefreshCwIcon, PencilIcon, Trash2Icon, Loader2Icon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import PageHeader from '../components/common/PageHeader.vue'
import DataTable, { type Column } from '../components/common/DataTable.vue'
import FormItem from '../components/common/FormItem.vue'
import SelectBox from '../components/common/SelectBox.vue'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { useList } from '../paged'
import { invalidate } from '../refs'
import type { ServerRoute } from '../types'

const { rows, loading, load } = useList(() => admin.get<ServerRoute[]>('/servers/routes'))

const actionText: Record<string, string> = {
  block: '禁止访问(域名目标)',
  block_ip: '禁止访问(IP目标)',
  block_port: '禁止访问(端口目标)',
  protocol: '禁止访问(协议)',
  dns: '指定DNS服务器进行解析',
  route: '指定出站服务器(域名目标)',
  route_ip: '指定出站服务器(IP目标)',
  default_out: '自定义默认出站',
}
const actionOptions = Object.entries(actionText).map(([value, label]) => ({ value, label }))
const outboundExample = JSON.stringify(
  {
    tag: 'ss_out',
    sendThrough: '0.0.0.0',
    protocol: 'shadowsocks',
    settings: { email: 'love@xray.com', address: '8.8.8.8', port: 5555, method: 'chacha20-ietf-poly1305', password: 'abcdefghijklmnopqrstuvwxyz', level: 0 },
  },
  null,
  4,
)

const matchList = (r: ServerRoute) => (Array.isArray(r.match) ? r.match : String(r.match || '').split(',')).filter(Boolean)

const columns: Column[] = [
  { key: 'id', title: 'ID', class: 'w-16' },
  { key: 'remarks', title: '备注' },
  { key: 'match', title: '匹配数量' },
  { key: 'action', title: '动作' },
  { key: 'op', title: '操作', align: 'right', class: 'w-24' },
]

const show = ref(false)
const saving = ref(false)
const editing = ref<ServerRoute | null>(null)
const form = ref({ remarks: '', action: 'block', match: '', action_value: '' })

function open(r: ServerRoute | null) {
  editing.value = r
  form.value = r
    ? { remarks: r.remarks, action: r.action, match: matchList(r).join('\n'), action_value: r.action_value || '' }
    : { remarks: '', action: 'block', match: '', action_value: '' }
  show.value = true
}

const matchTip = computed(() => {
  const a = form.value.action
  if (a === 'protocol') return 'http\ntls\nquic\nbittorrent'
  if (a === 'block_port') return '53\n443\n1000-2000'
  if (a === 'route_ip' || a === 'block_ip') return '127.0.0.1(单一匹配)\n10.0.0.0/8(范围匹配)\ngeoip:cn(预定义列表匹配)'
  return 'example.com(关键字匹配)\ndomain:example.com(子域名匹配)\ngeosite:netflix(预定义域名列表)'
})

async function submit() {
  const f = form.value
  if (!f.remarks.trim()) {
    message.warning('请输入备注')
    return
  }
  const body = {
    remarks: f.remarks.trim(),
    action: f.action,
    match: f.action === 'default_out' ? [] : f.match.split('\n').map((s) => s.trim()).filter(Boolean),
    action_value: ['dns', 'route', 'route_ip', 'default_out'].includes(f.action) ? f.action_value.trim() || null : null,
  }
  saving.value = true
  const ok = await run(() => (editing.value ? admin.put(`/servers/routes/${editing.value.id}`, body) : admin.post('/servers/routes', body)), '保存成功')
  saving.value = false
  if (ok) {
    show.value = false
    invalidate('routes')
    load()
  }
}

async function remove(r: ServerRoute) {
  if (await confirm(`确定要删除路由「${r.remarks}」吗？`, () => admin.del(`/servers/routes/${r.id}`), '删除成功')) {
    invalidate('routes')
    load()
  }
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="路由管理" description="路由在节点中选择后，下发给服务端执行。">
      <Button variant="ghost" size="icon" :disabled="loading" @click="load"><RefreshCwIcon :class="loading ? 'animate-spin' : ''" /></Button>
      <Button @click="open(null)"><PlusIcon />添加路由</Button>
    </PageHeader>

    <DataTable :columns="columns" :rows="rows" :loading="loading">
      <template #cell-id="{ row }"><span class="text-muted-foreground tabular-nums">{{ row.id }}</span></template>
      <template #cell-remarks="{ row }"><span class="font-medium">{{ row.remarks }}</span></template>
      <template #cell-match="{ row }">
        <span v-if="row.action === 'default_out'" class="text-muted-foreground">无规则时默认</span>
        <span v-else :title="matchList(row).join('\n')">匹配 <b class="tabular-nums">{{ matchList(row).length }}</b> 条规则</span>
      </template>
      <template #cell-action="{ row }">
        <span class="rounded bg-muted px-1.5 py-0.5 text-xs">{{ actionText[row.action] || row.action }}</span>
      </template>
      <template #cell-op="{ row }">
        <div class="flex justify-end gap-0.5">
          <Button variant="ghost" size="icon" class="size-8" @click="open(row)"><PencilIcon /></Button>
          <Button variant="ghost" size="icon" class="size-8 text-muted-foreground hover:text-destructive" @click="remove(row)"><Trash2Icon /></Button>
        </div>
      </template>
    </DataTable>

    <Dialog v-model:open="show">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        <DialogHeader><DialogTitle>{{ editing ? '编辑路由' : '创建路由' }}</DialogTitle></DialogHeader>
        <div class="grid gap-4">
          <FormItem label="备注">
            <Input v-model="form.remarks" placeholder="请输入备注" />
          </FormItem>
          <FormItem v-if="form.action !== 'default_out'" label="匹配值" help="每行一条规则">
            <Textarea v-model="form.match" rows="5" :placeholder="matchTip" class="font-mono text-xs" />
          </FormItem>
          <FormItem label="动作">
            <SelectBox :model-value="form.action" :options="actionOptions" placeholder="请选择动作" @update:model-value="(v) => (form.action = String(v))" />
          </FormItem>
          <FormItem v-if="form.action === 'dns'" label="DNS服务器">
            <Input v-model="form.action_value" placeholder="请输入用于解析的DNS服务器地址" />
          </FormItem>
          <FormItem v-if="['route', 'route_ip', 'default_out'].includes(form.action)" label="Xray出站配置">
            <Textarea v-model="form.action_value" rows="8" :placeholder="outboundExample" class="font-mono text-xs" />
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
