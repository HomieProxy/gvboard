<script setup lang="ts">
import { ref, watch } from 'vue'
import { Loader2Icon } from '@lucide/vue'
import { bytesToGB, gbToBytes } from '@gv/shared'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Checkbox } from '@gv/shared/ui/checkbox'
import { Label } from '@gv/shared/ui/label'
import FormItem from './common/FormItem.vue'
import SelectBox from './common/SelectBox.vue'
import NumInput from './common/NumInput.vue'
import DateTimeInput from './common/DateTimeInput.vue'
import BoolSwitch from './common/BoolSwitch.vue'
import { admin } from '../api'
import { run } from '../ui'
import { loadPlans, planOptions } from '../refs'
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
        expired_at: s.expired_at,
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
  if (changed('forever') || changed('expired_at')) out.expired_at = f.forever ? null : (f.expired_at ?? 0)
  for (const k of ['banned', 'auto_renewal', 'remind_expire', 'remind_traffic'] as const) {
    if (changed(k)) out[k] = f[k] ? 1 : 0
  }
  return out
}

async function submit() {
  saving.value = true
  const ok = await run(
    () => (props.sub ? admin.put(`/subscriptions/${props.sub.id}`, body()) : admin.post(`/users/${props.userId}/subscriptions`, body())),
    '保存成功',
  )
  saving.value = false
  if (ok) {
    show.value = false
    emit('saved')
  }
}

const planChoices = () => [{ label: '无订阅', value: null }, ...planOptions.value]
</script>

<template>
  <Dialog v-model:open="show">
    <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-xl">
      <DialogHeader>
        <DialogTitle>{{ sub ? `编辑订阅 #${sub.id}` : '新开订阅' }}</DialogTitle>
        <DialogDescription v-if="!sub">选择套餐后，权限组、流量、设备数、限速按套餐设置；下面填写的项会覆盖套餐值。管理员新开订阅不受用户订阅数量上限限制。</DialogDescription>
      </DialogHeader>
      <div class="grid gap-4 sm:grid-cols-2">
        <FormItem label="名称" class="sm:col-span-2">
          <Input v-model="form.name" maxlength="64" placeholder="可留空" />
        </FormItem>
        <FormItem label="订阅计划" class="sm:col-span-2">
          <SelectBox :model-value="form.plan_id" :options="planChoices()" placeholder="无订阅" @update:model-value="(v) => (form.plan_id = v as number | null)" />
        </FormItem>
        <FormItem label="流量" :class="sub ? '' : 'sm:col-span-2'">
          <NumInput v-model="form.transfer_enable" :min="0" :step="0.01" suffix="GB" placeholder="使用套餐流量" />
        </FormItem>
        <template v-if="sub">
          <FormItem label="已用上行">
            <NumInput v-model="form.u" :min="0" :step="0.01" suffix="GB" />
          </FormItem>
          <FormItem label="已用下行">
            <NumInput v-model="form.d" :min="0" :step="0.01" suffix="GB" />
          </FormItem>
        </template>
        <FormItem label="设备数限制">
          <NumInput v-model="form.device_limit" :min="0" placeholder="不限制" />
        </FormItem>
        <FormItem label="限速">
          <NumInput v-model="form.speed_limit" :min="0" suffix="Mbps" placeholder="不限速" />
        </FormItem>
        <FormItem label="到期时间" class="sm:col-span-2">
          <div class="flex items-center gap-3">
            <div class="flex shrink-0 items-center gap-2">
              <Checkbox id="sub-forever" :model-value="form.forever" @update:model-value="(v) => (form.forever = v === true)" />
              <Label for="sub-forever" class="font-normal">长期有效</Label>
            </div>
            <DateTimeInput v-if="!form.forever" v-model="form.expired_at" />
          </div>
        </FormItem>
        <div class="divide-y rounded-lg border sm:col-span-2">
          <label class="flex items-center justify-between gap-4 px-3 py-2.5 text-sm">封禁 <BoolSwitch v-model="form.banned" /></label>
          <label class="flex items-center justify-between gap-4 px-3 py-2.5 text-sm">自动续费 <BoolSwitch v-model="form.auto_renewal" /></label>
          <label class="flex items-center justify-between gap-4 px-3 py-2.5 text-sm">到期提醒 <BoolSwitch v-model="form.remind_expire" /></label>
          <label class="flex items-center justify-between gap-4 px-3 py-2.5 text-sm">流量提醒 <BoolSwitch v-model="form.remind_traffic" /></label>
        </div>
      </div>
      <DialogFooter>
        <Button variant="outline" @click="show = false">取消</Button>
        <Button :disabled="saving" @click="submit">
          <Loader2Icon v-if="saving" class="animate-spin" />
          保存
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
