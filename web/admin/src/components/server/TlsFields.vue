<script setup lang="ts">
import { Input } from '@gv/shared/ui/input'
import { Switch } from '@gv/shared/ui/switch'
import FormItem from '../common/FormItem.vue'
import SelectBox from '../common/SelectBox.vue'

/** 安全性配置（tls_settings）。tls：1 为 TLS、2 为 REALITY；cert 为 v2node 才有的证书申请选项。 */
const props = defineProps<{ tls: number; cert: boolean }>()
const s = defineModel<Record<string, unknown>>({ required: true })

const str = (k: string) => (s.value[k] === null || s.value[k] === undefined ? '' : String(s.value[k]))
const set = (k: string, v: unknown) => (s.value = { ...s.value, [k]: v })
const on = (k: string) => Number(s.value[k]) === 1

const certModes = [
  { value: 'self', label: '自签名' },
  { value: 'remote', label: '自签名(面板下发)' },
  { value: 'http', label: 'HTTP申请' },
  { value: 'dns', label: 'DNS申请' },
  { value: 'none', label: '无证书(关闭TLS)' },
]
const fingerprints = [
  ['chrome', 'Chrome'],
  ['firefox', 'Firefox'],
  ['safari', 'Safari'],
  ['ios', 'IOS'],
  ['android', 'Android'],
  ['edge', 'Edge'],
  ['360', '360'],
  ['qq', 'QQ'],
].map(([value, label]) => ({ value, label }))
const echModes = [
  { value: '', label: '无' },
  { value: 'cloudflare', label: 'Cloudflare' },
  { value: 'custom', label: '自定义 SNI' },
]
const xvers = [0, 1, 2].map((v) => ({ value: v, label: String(v) }))

const certMode = () => str('cert_mode') || 'self'
const withCert = () => props.tls === 1 && props.cert
</script>

<template>
  <div class="grid gap-4">
    <FormItem label="Server Name(SNI)">
      <Input :model-value="str('server_name')" :placeholder="tls === 2 ? 'REALITY必填，与后端保持一致' : ''" @update:model-value="(v) => set('server_name', v)" />
    </FormItem>
    <FormItem v-if="withCert()" label="证书模式Cert Mode">
      <SelectBox :model-value="certMode()" :options="certModes" @update:model-value="(v) => set('cert_mode', v)" />
    </FormItem>
    <template v-if="withCert() && certMode() === 'dns'">
      <FormItem label="DNS解析提供商Provider" help="填写 lego 支持的 DNS 提供商名称">
        <Input :model-value="str('provider')" placeholder="书写格式cloudflare" @update:model-value="(v) => set('provider', v)" />
      </FormItem>
      <FormItem label="DNS env">
        <Input :model-value="str('dns_env')" placeholder="书写格式CF_DNS_API_TOKEN=xxxxxxx如有多条使用逗号,分隔" @update:model-value="(v) => set('dns_env', v)" />
      </FormItem>
    </template>
    <template v-if="withCert() && certMode() !== 'none'">
      <FormItem label="证书公钥文件地址Cert File Path">
        <Input :model-value="str('cert_file')" placeholder="留空在/etc/v2node/目录自动生成" @update:model-value="(v) => set('cert_file', v)" />
      </FormItem>
      <FormItem label="证书私钥文件地址Key File Path">
        <Input :model-value="str('key_file')" placeholder="留空在/etc/v2node/目录自动生成" @update:model-value="(v) => set('key_file', v)" />
      </FormItem>
    </template>
    <FormItem v-if="withCert() && certMode() === 'remote'" label="pinnedPeerCertSha256">
      <Input :model-value="str('pinned_peer_cert_sha256')" readonly class="bg-muted font-mono text-xs" placeholder="自动生成" />
    </FormItem>
    <template v-if="tls === 2">
      <div class="grid gap-4 sm:grid-cols-2">
        <FormItem label="Server Address">
          <Input :model-value="str('dest')" placeholder="REALITY目标地址,默认使用SNI" @update:model-value="(v) => set('dest', v)" />
        </FormItem>
        <FormItem label="Server Port">
          <Input :model-value="str('server_port')" placeholder="REALITY目标端口,默认443" @update:model-value="(v) => set('server_port', v)" />
        </FormItem>
      </div>
      <FormItem label="Proxy Protocol">
        <SelectBox :model-value="Number(s.xver) || 0" :options="xvers" @update:model-value="(v) => set('xver', v)" />
      </FormItem>
      <FormItem label="Private Key">
        <Input :model-value="str('private_key')" class="font-mono" placeholder="留空自动生成" @update:model-value="(v) => set('private_key', v)" />
      </FormItem>
      <FormItem label="Public Key">
        <Input :model-value="str('public_key')" class="font-mono" placeholder="留空自动生成" @update:model-value="(v) => set('public_key', v)" />
      </FormItem>
      <FormItem label="ShortId">
        <Input :model-value="str('short_id')" class="font-mono" placeholder="留空自动生成" @update:model-value="(v) => set('short_id', v)" />
      </FormItem>
    </template>
    <FormItem label="FingerPrint">
      <SelectBox :model-value="str('fingerprint') || null" :options="fingerprints" placeholder="TLS指纹默认Chrome" @update:model-value="(v) => set('fingerprint', v)" />
    </FormItem>
    <div class="flex flex-wrap gap-6">
      <label v-if="withCert()" class="flex items-center gap-2 text-sm">
        <Switch :model-value="on('reject_unknown_sni')" @update:model-value="(v) => set('reject_unknown_sni', v ? '1' : '0')" />
        Reject unknown sni
      </label>
      <label class="flex items-center gap-2 text-sm">
        <Switch :model-value="on('allow_insecure')" @update:model-value="(v) => set('allow_insecure', v ? '1' : '0')" />
        Allow Insecure
      </label>
    </div>
    <FormItem label="ECH (Encrypted Client Hello)">
      <SelectBox :model-value="str('ech')" :options="echModes" placeholder="选择 ECH 模式" @update:model-value="(v) => set('ech', v)" />
    </FormItem>
    <p v-if="str('ech') === 'cloudflare'" class="rounded-md border border-emerald-300 bg-emerald-50 px-3 py-2 text-sm text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
      ✓ Cloudflare 托管 ECH，密钥由 Cloudflare 自动管理，客户端从 DNS 自动获取配置，服务端无需配置
    </p>
    <template v-if="str('ech') === 'custom'">
      <FormItem label="ECH Server Name (伪装域名/外层SNI)">
        <Input :model-value="str('ech_server_name')" placeholder="必填" @update:model-value="(v) => set('ech_server_name', v)" />
      </FormItem>
      <FormItem label="ECH Key (服务端私钥)">
        <Input :model-value="str('ech_key')" class="font-mono" placeholder="留空自动生成" @update:model-value="(v) => set('ech_key', v)" />
      </FormItem>
      <FormItem label="ECH Config (客户端配置)">
        <Input :model-value="str('ech_config')" class="font-mono" placeholder="留空自动生成" @update:model-value="(v) => set('ech_config', v)" />
      </FormItem>
    </template>
  </div>
</template>
