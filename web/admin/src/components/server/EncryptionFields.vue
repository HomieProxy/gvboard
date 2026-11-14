<script setup lang="ts">
import { Input } from '@gv/shared/ui/input'
import FormItem from '../common/FormItem.vue'
import SelectBox from '../common/SelectBox.vue'

/** VLESS 加密配置（encryption_settings）。 */
const s = defineModel<Record<string, unknown>>({ required: true })

const str = (k: string) => (s.value[k] === null || s.value[k] === undefined ? '' : String(s.value[k]))
const set = (k: string, v: unknown) => (s.value = { ...s.value, [k]: v })
const opts = (...v: string[]) => v.map((x) => ({ value: x, label: x }))
const paddingTip = '留空使用默认值100-111-1111.75-0-111.50-0-3333'
const keyTip = '留空自动生成，需抗量子加密请自行替换'
</script>

<template>
  <div class="grid gap-4">
    <FormItem label="Mode">
      <SelectBox :model-value="str('mode') || 'native'" :options="opts('native', 'xorpub', 'random')" @update:model-value="(v) => set('mode', v)" />
    </FormItem>
    <div class="grid gap-4 sm:grid-cols-2">
      <FormItem label="RTT">
        <SelectBox :model-value="str('rtt') || '0rtt'" :options="opts('0rtt', '1rtt')" @update:model-value="(v) => set('rtt', v)" />
      </FormItem>
      <FormItem v-if="(str('rtt') || '0rtt') === '0rtt'" label="Ticket time">
        <Input :model-value="str('ticket')" placeholder="最长允许时间" @update:model-value="(v) => set('ticket', v)" />
      </FormItem>
    </div>
    <FormItem label="Server Padding">
      <Input :model-value="str('server_padding')" :placeholder="paddingTip" @update:model-value="(v) => set('server_padding', v)" />
    </FormItem>
    <FormItem label="Private Key">
      <Input :model-value="str('private_key')" class="font-mono" :placeholder="keyTip" @update:model-value="(v) => set('private_key', v)" />
    </FormItem>
    <FormItem label="Client Padding">
      <Input :model-value="str('client_padding')" :placeholder="paddingTip" @update:model-value="(v) => set('client_padding', v)" />
    </FormItem>
    <FormItem label="Password">
      <Input :model-value="str('password')" class="font-mono" :placeholder="keyTip" @update:model-value="(v) => set('password', v)" />
    </FormItem>
  </div>
</template>
