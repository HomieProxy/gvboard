<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Loader2Icon, PlusIcon } from '@lucide/vue'
import { copyText } from '@gv/shared'
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@gv/shared/ui/sheet'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { Popover, PopoverContent, PopoverTrigger } from '@gv/shared/ui/popover'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Textarea } from '@gv/shared/ui/textarea'
import { Switch } from '@gv/shared/ui/switch'
import FormItem from './common/FormItem.vue'
import SelectBox, { type OptionValue } from './common/SelectBox.vue'
import MultiSelect from './common/MultiSelect.vue'
import TagsField from './common/TagsField.vue'
import TlsFields from './server/TlsFields.vue'
import EncryptionFields from './server/EncryptionFields.vue'
import { admin } from '../api'
import { message, run } from '../ui'
import { groupOptions, invalidate, loadGroups, loadRoutes, routeOptions } from '../refs'
import {
  congestionControls, defaultPaddingScheme, flows, networkExamples, ssCiphers, trojanNetworks, typeLabel, udpRelayModes,
  v2nodeNetworkExamples, v2nodeNetworks, v2nodeProtocols, v2nodeTlsOptions, vlessEncryptions, vmessNetworks, yesNo,
} from '../serverSpec'
import type { ServerRow } from '../types'

const props = defineProps<{ server: ServerRow | null; type: string; parents: ServerRow[] }>()
const show = defineModel<boolean>('show', { required: true })
const emit = defineEmits<{ saved: [] }>()

type Obj = Record<string, unknown>
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const f = ref<Record<string, any>>({})
const type = ref('vless')
const saving = ref(false)

/** 各类型提交的专有列（与后端一致） */
const columns: Record<string, string[]> = {
  shadowsocks: ['cipher', 'obfs', 'obfs_settings'],
  vmess: ['tls', 'network', 'networkSettings', 'ruleSettings', 'tlsSettings', 'dnsSettings'],
  trojan: ['network', 'network_settings', 'allow_insecure', 'server_name'],
  tuic: ['server_name', 'insecure', 'disable_sni', 'udp_relay_mode', 'zero_rtt_handshake', 'congestion_control'],
  hysteria: ['version', 'up_mbps', 'down_mbps', 'obfs', 'obfs_password', 'server_name', 'insecure'],
  vless: ['tls', 'tls_settings', 'flow', 'network', 'network_settings', 'encryption', 'encryption_settings'],
  anytls: ['server_name', 'insecure', 'padding_scheme'],
  v2node: [
    'listen_ip', 'protocol', 'tls', 'tls_settings', 'flow', 'network', 'network_settings', 'trusted_x_forwarded_for', 'encryption',
    'encryption_settings', 'disable_sni', 'udp_relay_mode', 'zero_rtt_handshake', 'congestion_control', 'cipher', 'up_mbps',
    'down_mbps', 'obfs', 'obfs_password', 'padding_scheme',
  ],
}
/** 以 JSON 文本编辑、提交时解析的列 */
const jsonText = ['network_settings', 'networkSettings']
/** 以对象编辑的列 */
const objCols = ['tls_settings', 'encryption_settings', 'tlsSettings', 'obfs_settings']
const flagCols = ['insecure', 'allow_insecure', 'disable_sni', 'zero_rtt_handshake']

const pretty = (v: unknown) => (v === null || v === undefined || v === '' ? '' : typeof v === 'string' ? v : JSON.stringify(v, null, 2))

function reset() {
  const s = props.server
  type.value = s?.type || props.type || 'vless'
  const out: Record<string, unknown> = {
    name: s?.name ?? '',
    rate: s ? String(s.rate) : '1',
    tags: s?.tags || [],
    group_id: (s?.group_id || []).map(Number),
    route_id: (s?.route_id || []).map(Number),
    parent_id: s?.parent_id || null,
    host: s?.host ?? '',
    port: s?.port ?? '',
    server_port: s?.server_port ?? '',
  }
  for (const k of columns[type.value] || []) {
    const v = s ? s[k] : undefined
    if (jsonText.includes(k)) out[k] = pretty(v)
    else if (objCols.includes(k)) out[k] = v && typeof v === 'object' && !Array.isArray(v) ? { ...(v as Obj) } : {}
    else if (k === 'padding_scheme') out[k] = pretty(v)
    else if (flagCols.includes(k)) out[k] = Number(v) ? 1 : 0
    else if (k === 'tls') out[k] = v === undefined || v === null ? 0 : Number(v)
    else out[k] = v ?? null
  }
  if (type.value === 'hysteria') out.version = Number(out.version) || 2
  if (type.value === 'v2node') {
    out.protocol = out.protocol || 'vless'
    out.trusted_x_forwarded_for = (out.trusted_x_forwarded_for as string[] | null) || []
  }
  if (['vless', 'vmess', 'trojan', 'v2node'].includes(type.value)) out.network = out.network || 'tcp'
  if (type.value === 'shadowsocks') out.cipher = out.cipher || 'aes-128-gcm'
  f.value = out
}

watch(show, (v) => {
  if (!v) return
  loadGroups().catch(() => {})
  loadRoutes().catch(() => {})
  reset()
})

const t = computed(() => type.value)
const proto = computed(() => (t.value === 'v2node' ? String(f.value.protocol || '') : ''))
const isV2 = computed(() => t.value === 'v2node')
const set = (k: string, v: unknown) => (f.value[k] = v)

/** v2node 中 hysteria2 / trojan / tuic 固定 TLS，anytls 至少 TLS */
const v2Tls = computed(() => {
  const p = proto.value
  const tls = Number(f.value.tls) || 0
  if (p === 'hysteria2' || p === 'trojan' || p === 'tuic') return tls || 1
  if (p === 'anytls') return tls || 1
  return tls
})
watch(proto, (p, old) => {
  if (!isV2.value || !old) return
  const opts = v2nodeTlsOptions(p).map((o) => o.value)
  if (!opts.includes(Number(f.value.tls))) f.value.tls = opts[0]
  const nets = v2nodeNetworks(p).map((o) => o.value)
  if (nets.length && !nets.includes(f.value.network)) f.value.network = 'tcp'
})

const tlsValue = computed(() => (isV2.value ? v2Tls.value : Number(f.value.tls) || 0))
const canEditTls = computed(() => (isV2.value ? proto.value !== 'shadowsocks' && (tlsValue.value !== 0 || ['hysteria2', 'trojan', 'tuic'].includes(proto.value)) : tlsValue.value !== 0))
const showNetwork = computed(() => {
  if (t.value === 'vless' || t.value === 'vmess' || t.value === 'trojan') return true
  return isV2.value && v2nodeNetworks(proto.value).length > 0
})
const networkOptions = computed(() => {
  if (isV2.value) return v2nodeNetworks(proto.value)
  return t.value === 'trojan' ? trojanNetworks : vmessNetworks
})
const showXff = computed(() => isV2.value && showNetwork.value && ['xhttp', 'ws', 'grpc'].includes(f.value.network))
const isVlessLike = computed(() => t.value === 'vless' || proto.value === 'vless')
const isTuicLike = computed(() => t.value === 'tuic' || proto.value === 'tuic')
const isPadding = computed(() => t.value === 'anytls' || proto.value === 'anytls')
const isSs = computed(() => t.value === 'shadowsocks' || proto.value === 'shadowsocks')
const hyVersion = computed(() => (isV2.value ? 2 : Number(f.value.version) || 2))
const isHy = computed(() => t.value === 'hysteria' || proto.value === 'hysteria2')

const parentOptions = computed(() => [
  { label: '无', value: null as OptionValue },
  ...props.parents.filter((p) => p.type === t.value && p.id !== props.server?.id && !p.parent_id).map((p) => ({ label: p.name, value: p.id as OptionValue })),
])

// ---------- 子配置弹窗 ----------
type SubKind = 'tls' | 'network' | 'encryption' | 'padding' | 'vmessTls'
const sub = ref<{ open: boolean; kind: SubKind; title: string }>({ open: false, kind: 'tls', title: '' })
const draftObj = ref<Obj>({})
const draftText = ref('')
const networkKey = computed(() => (t.value === 'vmess' ? 'networkSettings' : 'network_settings'))

function openSub(kind: SubKind, title: string) {
  if (kind === 'tls') {
    const cur = f.value.tls_settings as Obj
    draftObj.value = Object.keys(cur || {}).length
      ? { ...cur }
      : { server_name: '', cert_mode: 'self', provider: '', dns_env: '', reject_unknown_sni: '0', allow_insecure: '0' }
  } else if (kind === 'vmessTls') {
    const cur = f.value.tlsSettings as Obj
    draftObj.value = Object.keys(cur || {}).length ? { ...cur } : { serverName: '', allowInsecure: 0 }
  } else if (kind === 'encryption') {
    const cur = f.value.encryption_settings as Obj
    draftObj.value = Object.keys(cur || {}).length ? { ...cur } : { mode: 'native', rtt: '0rtt', ticket: '600s' }
  } else if (kind === 'network') {
    draftText.value = String(f.value[networkKey.value] || '')
  } else {
    draftText.value = String(f.value.padding_scheme || '')
  }
  sub.value = { open: true, kind, title }
}
const example = computed(() => {
  if (sub.value.kind === 'padding') return defaultPaddingScheme
  return (isV2.value ? v2nodeNetworkExamples : networkExamples)[f.value.network] || ''
})
function applySub() {
  const k = sub.value.kind
  if (k === 'network' || k === 'padding') {
    const text = draftText.value.trim()
    if (text) {
      try {
        JSON.parse(text)
      } catch {
        message.error(k === 'network' ? '传输协议配置格式有误' : '填充方案格式有误')
        return
      }
    }
    f.value[k === 'network' ? networkKey.value : 'padding_scheme'] = text
  } else {
    f.value[{ tls: 'tls_settings', vmessTls: 'tlsSettings', encryption: 'encryption_settings' }[k]] = { ...draftObj.value }
  }
  sub.value.open = false
}

// ---------- 添加权限组 ----------
const newGroup = ref('')
const groupOpen = ref(false)
async function addGroup() {
  const name = newGroup.value.trim()
  if (!name) return
  if (await run(() => admin.post('/servers/groups', { name }), '已添加')) {
    invalidate('groups')
    await loadGroups(true)
    newGroup.value = ''
    groupOpen.value = false
  }
}

// ---------- 提交 ----------
/** 空字符串的配置项视为未填写，交给后端自动生成或使用默认值 */
function clean(o: unknown): Obj | null {
  if (!o || typeof o !== 'object') return null
  const out: Obj = {}
  for (const [k, v] of Object.entries(o as Obj)) if (v !== '' && v !== null && v !== undefined) out[k] = v
  return Object.keys(out).length ? out : null
}
const nz = (v: unknown) => (v === '' || v === undefined ? null : v)

function build(): Obj | null {
  const v = f.value
  const body: Obj = {
    name: v.name,
    rate: v.rate,
    tags: (v.tags as string[]).length ? v.tags : null,
    group_id: v.group_id,
    route_id: (v.route_id as number[]).length ? v.route_id : null,
    parent_id: v.parent_id || null,
    host: v.host,
    port: String(v.port ?? ''),
    server_port: v.server_port === '' ? null : v.server_port,
  }
  for (const k of columns[t.value] || []) {
    const x = v[k]
    if (jsonText.includes(k)) {
      const text = String(x || '').trim()
      try {
        body[k] = text ? JSON.parse(text) : null
      } catch {
        message.error('传输协议配置格式有误')
        return null
      }
    } else if (objCols.includes(k)) body[k] = clean(x)
    else if (k === 'padding_scheme') body[k] = String(x || '').trim() || null
    else body[k] = nz(x)
  }
  if (t.value === 'shadowsocks' && v.obfs !== 'http') body.obfs_settings = null
  if (isV2.value) {
    body.tls = proto.value === 'shadowsocks' ? 0 : v2Tls.value
    if (!showNetwork.value) body.network = 'tcp'
    body.trusted_x_forwarded_for = showXff.value && (v.trusted_x_forwarded_for as string[]).length ? v.trusted_x_forwarded_for : null
  }
  return body
}

async function submit() {
  const body = build()
  if (!body) return
  saving.value = true
  const ok = await run(
    () => (props.server ? admin.put(`/servers/${t.value}/${props.server.id}`, body) : admin.post(`/servers/${t.value}`, body)),
    '保存成功',
  )
  saving.value = false
  if (ok) {
    show.value = false
    emit('saved')
  }
}

const portTip = computed(() => (t.value === 'vless' || t.value === 'vmess' ? '非NAT同连接端口' : '服务端开放端口'))
const insecureHelp = '使用自签名证书需要允许不安全，用户才可以连接'
const sniTip = '当节点地址与证书不一致时用于证书验证'

async function copyInstall() {
  const ok = await copyText(String(props.server?.install_command || ''))
  message[ok ? 'success' : 'error'](ok ? '复制成功' : '复制失败')
}
</script>

<template>
  <Sheet v-model:open="show">
    <SheetContent class="w-full gap-0 sm:max-w-2xl">
      <SheetHeader class="border-b">
        <SheetTitle>{{ server ? '编辑节点' : '新建节点' }}</SheetTitle>
        <SheetDescription>{{ typeLabel(t) }}{{ server ? ` · 节点ID ${server.id}` : '' }}</SheetDescription>
      </SheetHeader>

      <div class="flex-1 overflow-y-auto p-4">
        <div class="grid gap-4">
          <div class="grid grid-cols-3 gap-4">
            <FormItem label="节点名称" class="col-span-2">
              <Input v-model="f.name" placeholder="请输入节点名称" />
            </FormItem>
            <FormItem label="倍率">
              <div class="relative">
                <Input v-model="f.rate" placeholder="请输入节点倍率" class="pr-7" />
                <span class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-sm text-muted-foreground">x</span>
              </div>
            </FormItem>
          </div>
          <FormItem label="节点标签">
            <TagsField v-model="f.tags" placeholder="输入后回车添加标签" />
          </FormItem>
          <FormItem label="权限组">
            <template #extra>
              <Popover v-model:open="groupOpen">
                <PopoverTrigger as-child>
                  <button type="button" class="ml-1 text-xs font-normal text-primary hover:underline">添加权限组</button>
                </PopoverTrigger>
                <PopoverContent class="w-64" align="start">
                  <div class="flex gap-2">
                    <Input v-model="newGroup" placeholder="请输入组名" class="h-8" @keydown.enter="addGroup" />
                    <Button size="sm" @click="addGroup"><PlusIcon /></Button>
                  </div>
                </PopoverContent>
              </Popover>
            </template>
            <MultiSelect v-model="f.group_id" :options="groupOptions" placeholder="请选择权限组" />
          </FormItem>

          <FormItem v-if="t === 'hysteria'" label="HYSTERIA版本">
            <SelectBox :model-value="Number(f.version)" :options="[{ value: 1, label: 'v1' }, { value: 2, label: 'v2' }]" @update:model-value="(x) => set('version', x)" />
          </FormItem>

          <div class="grid gap-4" :class="t === 'vless' || t === 'vmess' || isV2 ? 'sm:grid-cols-3' : ''">
            <FormItem :label="isV2 ? '连接地址' : '节点地址'" class="sm:col-span-2">
              <Input v-model="f.host" :placeholder="t === 'vless' || t === 'vmess' ? '请输入连接地址' : '地址或IP'" />
            </FormItem>
            <FormItem v-if="t === 'vless'" label="安全性">
              <template #extra>
                <button v-if="canEditTls" type="button" class="ml-1 text-xs font-normal text-primary hover:underline" @click="openSub('tls', '编辑安全性配置')">编辑配置</button>
              </template>
              <SelectBox :model-value="Number(f.tls) || 0" :options="[{ value: 0, label: '无' }, { value: 1, label: 'TLS' }, { value: 2, label: 'Reality' }]" @update:model-value="(x) => set('tls', x)" />
            </FormItem>
            <FormItem v-if="t === 'vmess'" label="TLS">
              <template #extra>
                <button v-if="Number(f.tls)" type="button" class="ml-1 text-xs font-normal text-primary hover:underline" @click="openSub('vmessTls', '编辑TLS配置')">编辑配置</button>
              </template>
              <SelectBox :model-value="Number(f.tls) || 0" :options="[{ value: 0, label: '不支持' }, { value: 1, label: '支持' }]" placeholder="是否支持TLS" @update:model-value="(x) => set('tls', x)" />
            </FormItem>
            <FormItem v-if="isV2" label="监听地址">
              <Input v-model="f.listen_ip" placeholder="地址或IP默认为0.0.0.0" />
            </FormItem>
          </div>

          <div class="grid grid-cols-2 gap-4">
            <FormItem label="连接端口">
              <Input v-model="f.port" placeholder="用户连接端口" />
            </FormItem>
            <FormItem label="服务端口">
              <Input v-model="f.server_port" :placeholder="portTip" />
            </FormItem>
          </div>

          <!-- v2node 节点协议与安全性 -->
          <div v-if="isV2" class="grid gap-4 sm:grid-cols-2">
            <FormItem label="节点协议">
              <SelectBox :model-value="f.protocol" :options="v2nodeProtocols" @update:model-value="(x) => set('protocol', x)" />
            </FormItem>
            <FormItem v-if="proto !== 'shadowsocks'" label="安全性">
              <template #extra>
                <button v-if="canEditTls" type="button" class="ml-1 text-xs font-normal text-primary hover:underline" @click="openSub('tls', '编辑安全性配置')">编辑配置</button>
              </template>
              <SelectBox :model-value="v2Tls" :options="v2nodeTlsOptions(proto)" @update:model-value="(x) => set('tls', x)" />
            </FormItem>
          </div>

          <!-- 允许不安全 / SNI：Trojan、TUIC、Hysteria、AnyTLS -->
          <template v-if="['trojan', 'tuic', 'hysteria', 'anytls'].includes(t)">
            <FormItem label="允许不安全" :help="insecureHelp">
              <SelectBox
                :model-value="Number(f[t === 'trojan' ? 'allow_insecure' : 'insecure']) ? 1 : 0"
                :options="yesNo"
                @update:model-value="(x) => set(t === 'trojan' ? 'allow_insecure' : 'insecure', x)"
              />
            </FormItem>
          </template>

          <!-- Shadowsocks -->
          <template v-if="isSs">
            <FormItem label="加密算法">
              <SelectBox :model-value="f.cipher || 'aes-128-gcm'" :options="ssCiphers" @update:model-value="(x) => set('cipher', x)" />
            </FormItem>
            <template v-if="t === 'shadowsocks'">
              <FormItem label="混淆">
                <SelectBox :model-value="f.obfs || null" :options="[{ value: null, label: '无' }, { value: 'http', label: 'HTTP' }]" @update:model-value="(x) => set('obfs', x)" />
              </FormItem>
              <div v-if="f.obfs === 'http'" class="grid grid-cols-2 gap-4">
                <FormItem label="路径">
                  <Input :model-value="String(f.obfs_settings?.path ?? '')" placeholder="路径" @update:model-value="(x) => (f.obfs_settings = { ...f.obfs_settings, path: x })" />
                </FormItem>
                <FormItem label="Host">
                  <Input :model-value="String(f.obfs_settings?.host ?? '')" placeholder="Host" @update:model-value="(x) => (f.obfs_settings = { ...f.obfs_settings, host: x })" />
                </FormItem>
              </div>
            </template>
          </template>

          <!-- TUIC -->
          <template v-if="isTuicLike">
            <div class="grid grid-cols-2 gap-4">
              <FormItem label="禁用SNI">
                <SelectBox :model-value="Number(f.disable_sni) ? 1 : 0" :options="yesNo" @update:model-value="(x) => set('disable_sni', x)" />
              </FormItem>
              <FormItem label="数据包中继模式">
                <SelectBox :model-value="f.udp_relay_mode || 'native'" :options="udpRelayModes" @update:model-value="(x) => set('udp_relay_mode', x)" />
              </FormItem>
            </div>
          </template>

          <FormItem v-if="['trojan', 'hysteria', 'anytls'].includes(t) || (t === 'tuic' && !Number(f.disable_sni))" label="服务器名称指示(sni)">
            <Input v-model="f.server_name" :placeholder="sniTip" />
          </FormItem>

          <div v-if="isTuicLike" class="grid grid-cols-2 gap-4">
            <FormItem label="拥塞控制算法">
              <SelectBox :model-value="f.congestion_control || 'cubic'" :options="congestionControls" @update:model-value="(x) => set('congestion_control', x)" />
            </FormItem>
            <FormItem label="客户端启用 0-RTT">
              <SelectBox :model-value="Number(f.zero_rtt_handshake) ? 1 : 0" :options="yesNo" @update:model-value="(x) => set('zero_rtt_handshake', x)" />
            </FormItem>
          </div>

          <!-- 传输协议 -->
          <FormItem v-if="showNetwork" label="传输协议">
            <template #extra>
              <button type="button" class="ml-1 text-xs font-normal text-primary hover:underline" @click="openSub('network', '编辑协议配置')">编辑配置</button>
            </template>
            <SelectBox :model-value="f.network || 'tcp'" :options="networkOptions" placeholder="选择传输协议" @update:model-value="(x) => set('network', x)" />
          </FormItem>
          <FormItem v-if="showXff" label="信任的XFF头部(获取真实IP)">
            <TagsField v-model="f.trusted_x_forwarded_for" placeholder="常见头部:X-Forwarded-For CF-Connecting-IP X-Real-IP" />
          </FormItem>

          <!-- AnyTLS 填充方案 -->
          <div v-if="isPadding">
            <Button variant="outline" size="sm" @click="openSub('padding', '编辑填充方案')">编辑填充方案</Button>
          </div>

          <!-- Hysteria -->
          <template v-if="isHy">
            <div class="grid grid-cols-2 gap-4">
              <FormItem label="混淆方式obfs">
                <SelectBox
                  :model-value="f.obfs || null"
                  :options="[{ value: null, label: '无' }, hyVersion === 1 ? { value: 'xplus', label: 'xplus' } : { value: 'salamander', label: 'salamander' }]"
                  @update:model-value="(x) => set('obfs', x)"
                />
              </FormItem>
              <FormItem v-if="f.obfs" :label="hyVersion === 1 ? '混淆密码obfsParam' : '混淆密码obfs_password'">
                <Input v-model="f.obfs_password" placeholder="留空自动生成" />
              </FormItem>
            </div>
            <div class="grid grid-cols-2 gap-4">
              <FormItem label="上行带宽">
                <div class="relative">
                  <Input v-model="f.up_mbps" placeholder="服务端发送带宽,留空或填0使用BBR" class="pr-14" />
                  <span class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-sm text-muted-foreground">Mbps</span>
                </div>
              </FormItem>
              <FormItem label="下行带宽">
                <div class="relative">
                  <Input v-model="f.down_mbps" placeholder="服务端接收带宽,留空或填0使用BBR" class="pr-14" />
                  <span class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-sm text-muted-foreground">Mbps</span>
                </div>
              </FormItem>
            </div>
          </template>

          <!-- VLESS 加密与流控 -->
          <template v-if="isVlessLike">
            <FormItem label="加密方式">
              <template #extra>
                <button v-if="f.encryption" type="button" class="ml-1 text-xs font-normal text-primary hover:underline" @click="openSub('encryption', '编辑加密配置')">编辑配置</button>
              </template>
              <SelectBox :model-value="f.encryption || null" :options="vlessEncryptions" placeholder="选择加密方式" @update:model-value="(x) => set('encryption', x)" />
            </FormItem>
            <FormItem label="XTLS流控算法">
              <SelectBox :model-value="f.flow || null" :options="flows" placeholder="选择XTLS流控算法" @update:model-value="(x) => set('flow', x)" />
            </FormItem>
          </template>

          <FormItem label="父节点" help="子节点与父节点共用同一个服务端，在线状态与流量按父节点统计，常用于中转入口">
            <SelectBox :model-value="f.parent_id || null" :options="parentOptions" @update:model-value="(x) => set('parent_id', x)" />
          </FormItem>
          <FormItem label="路由组">
            <MultiSelect v-model="f.route_id" :options="routeOptions" placeholder="请选择路由组" />
          </FormItem>

          <FormItem v-if="isV2 && server?.install_command" label="一键安装指令">
            <template #extra>
              <button type="button" class="ml-1 text-xs font-normal text-primary hover:underline" @click="copyInstall">复制</button>
            </template>
            <Textarea :model-value="String(server.install_command)" readonly rows="4" class="bg-muted font-mono text-xs" />
          </FormItem>
        </div>
      </div>

      <SheetFooter class="flex-row justify-end border-t">
        <Button variant="outline" @click="show = false">取消</Button>
        <Button :disabled="saving" @click="submit">
          <Loader2Icon v-if="saving" class="animate-spin" />
          提交
        </Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <Dialog v-model:open="sub.open">
    <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ sub.title }}</DialogTitle>
        <DialogDescription v-if="sub.kind === 'network'">协议详细配置（JSON），留空使用默认配置。下方灰字为 {{ f.network }} 的参考写法。</DialogDescription>
        <DialogDescription v-else-if="sub.kind === 'padding'">AnyTLS 填充方案（JSON 数组），留空使用默认方案。</DialogDescription>
        <DialogDescription v-else class="sr-only">{{ sub.title }}</DialogDescription>
      </DialogHeader>
      <TlsFields v-if="sub.kind === 'tls'" v-model="draftObj" :tls="tlsValue" :cert="isV2" />
      <EncryptionFields v-else-if="sub.kind === 'encryption'" v-model="draftObj" />
      <div v-else-if="sub.kind === 'vmessTls'" class="grid gap-4">
        <FormItem label="Server Name">
          <Input :model-value="String(draftObj.serverName ?? '')" placeholder="不使用请留空" @update:model-value="(x) => (draftObj = { ...draftObj, serverName: x })" />
        </FormItem>
        <label class="flex items-center gap-2 text-sm">
          <Switch :model-value="Number(draftObj.allowInsecure) === 1" @update:model-value="(x) => (draftObj = { ...draftObj, allowInsecure: x ? '1' : '0' })" />
          Allow Insecure
        </label>
      </div>
      <div v-else class="grid gap-2">
        <Textarea v-model="draftText" :placeholder="example" rows="14" class="font-mono text-xs" spellcheck="false" />
        <div v-if="example">
          <Button variant="link" size="sm" class="h-auto px-0" @click="draftText = example">填入参考</Button>
        </div>
      </div>
      <DialogFooter>
        <Button variant="outline" @click="sub.open = false">取消</Button>
        <Button @click="applySub">确定</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
