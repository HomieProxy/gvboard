<script setup lang="ts">
import { ref, watch } from 'vue'
import { NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NDatePicker, NSwitch, NButton, NSpace, NCheckbox } from 'naive-ui'
import { bytesToGB, gbToBytes } from '@gv/shared'
import { admin } from '../api'
import { run } from '../ui'
import { loadPlans, planOptions } from '../refs'
import { toMs, toSec } from '../helpers'
import type { Sub } from '../types'

const props = defineProps<{ userId: number; sub: Sub | null }>()
const show = defineModel<boolean>('show', { required: true })
const emit = defineEmits<{ saved: [] }>()

interface FormState {
  name: string
  plan_id: number | null
  transfer_enable: number | null
  u: number | null
  d: number | null
  device_limit: number | null
  speed_limit: number | null
  expired_at: number | null
  forever: boolean
  banned: boolean
  auto_renewal: boolean
  remind_expire: boolean
  remind_traffic: boolean
}

const form = ref<FormState>(blank())
let origin: FormState = blank()
const saving = ref(false)

function blank(): FormState {
  return {
    name: '',
    plan_id: null,
    transfer_enable: null,
    u: null,
    d: null,
    device_limit: null,
    speed_limit: null,
    expired_at: null,
    forever: true,
    banned: false,
    auto_renewal: false,
    remind_expire: true,
    remind_traffic: true,
  }
}

watch(show, (v) => {
  if (!v) return
  loadPlans()
  const s = props.sub
  form.value = s
    ? {
        name: s.name,
        plan_id: s.plan_id,
        transfer_enable: bytesToGB(s.transfer_enable),
        u: bytesToGB(s.u),
        d: bytesToGB(s.d),
        device_limit: s.device_limit,
        speed_limit: s.speed_limit,
        expired_at: toMs(s.expired_at),
        forever: s.expired_at === null,
        banned: !!s.banned,
        auto_renewal: !!s.auto_renewal,
        remind_expire: !!s.remind_expire,
        remind_traffic: !!s.remind_traffic,
      }
    : blank()
  origin = { ...form.value }
})

function body() {
  const f = form.value
  const out: Record<string, unknown> = {}
  const changed = (k: keyof FormState) => !props.sub || f[k] !== origin[k]
  if (changed('name')) out.name = f.name
  if (changed('plan_id')) out.plan_id = f.plan_id
  // 流量以 GB 编辑，未改动时不提交，避免丢失字节精度
  if (f.transfer_enable !== null && changed('transfer_enable')) out.transfer_enable = gbToBytes(f.transfer_enable)
  if (props.sub && f.u !== null && changed('u')) out.u = gbToBytes(f.u)
  if (props.sub && f.d !== null && changed('d')) out.d = gbToBytes(f.d)
  if (changed('device_limit') && (props.sub || f.device_limit !== null)) out.device_limit = f.device_limit
  if (changed('speed_limit') && (props.sub || f.speed_limit !== null)) out.speed_limit = f.speed_limit
  if (changed('forever') || changed('expired_at')) out.expired_at = f.forever ? null : (toSec(f.expired_at) ?? 0)
  for (const k of ['banned', 'auto_renewal', 'remind_expire', 'remind_traffic'] as const) {
    if (changed(k)) out[k] = f[k] ? 1 : 0
  }
  return out
}

async function submit() {
  saving.value = true
  const ok = await run(
    () => (props.sub ? admin.put(`/subscriptions/${props.sub.id}`, body()) : admin.post(`/users/${props.userId}/subscriptions`, body())),
    '已保存',
  )
  saving.value = false
  if (ok) {
    show.value = false
    emit('saved')
  }
}
</script>

<template>
  <NModal v-model:show="show" preset="card" :title="sub ? `编辑订阅 #${sub.id}` : '新开订阅'" style="width: 560px; max-width: 95vw">
    <NForm label-placement="left" label-width="96">
      <NFormItem label="名称"><NInput v-model:value="form.name" maxlength="64" placeholder="可留空" /></NFormItem>
      <NFormItem label="套餐">
        <NSelect v-model:value="form.plan_id" :options="planOptions" clearable placeholder="无套餐" />
      </NFormItem>
      <div v-if="!sub" class="muted hint">选择套餐后，权限组、流量、设备数、限速按套餐设置；下面填写的项会覆盖套餐值。</div>
      <NFormItem label="总流量 (GB)"><NInputNumber v-model:value="form.transfer_enable" :min="0" clearable style="width: 100%" /></NFormItem>
      <template v-if="sub">
        <NFormItem label="已用上行 (GB)"><NInputNumber v-model:value="form.u" :min="0" style="width: 100%" /></NFormItem>
        <NFormItem label="已用下行 (GB)"><NInputNumber v-model:value="form.d" :min="0" style="width: 100%" /></NFormItem>
      </template>
      <NFormItem label="设备数限制"><NInputNumber v-model:value="form.device_limit" :min="0" clearable placeholder="不限制" style="width: 100%" /></NFormItem>
      <NFormItem label="限速 (Mbps)"><NInputNumber v-model:value="form.speed_limit" :min="0" clearable placeholder="不限速" style="width: 100%" /></NFormItem>
      <NFormItem label="到期时间">
        <NSpace align="center">
          <NCheckbox v-model:checked="form.forever">长期有效</NCheckbox>
          <NDatePicker v-if="!form.forever" v-model:value="form.expired_at" type="datetime" clearable />
        </NSpace>
      </NFormItem>
      <NFormItem label="选项">
        <NSpace>
          <span>封禁 <NSwitch v-model:value="form.banned" size="small" /></span>
          <span>自动续费 <NSwitch v-model:value="form.auto_renewal" size="small" /></span>
          <span>到期提醒 <NSwitch v-model:value="form.remind_expire" size="small" /></span>
          <span>流量提醒 <NSwitch v-model:value="form.remind_traffic" size="small" /></span>
        </NSpace>
      </NFormItem>
    </NForm>
    <template #footer>
      <NSpace justify="end">
        <NButton @click="show = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="submit">保存</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.hint { margin: -8px 0 12px 96px; }
</style>
