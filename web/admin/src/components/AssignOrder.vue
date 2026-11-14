<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Loader2Icon } from '@lucide/vue'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import FormItem from './common/FormItem.vue'
import SelectBox from './common/SelectBox.vue'
import NumInput from './common/NumInput.vue'
import { admin } from '../api'
import { message, run } from '../ui'
import { periodOptions, toCents } from '../helpers'
import { loadPlans, planOptions } from '../refs'
import type { UserDetail } from '../types'

/** 订单分配：为用户创建待支付订单。传入 userId 时可选择目标订阅。 */
const props = defineProps<{ email?: string; userId?: number | null }>()
const show = defineModel<boolean>('show', { required: true })
const emit = defineEmits<{ done: [tradeNo: string] }>()

const form = ref({ email: '', plan_id: null as number | null, period: 'month_price', total_amount: null as number | null, subscription_id: 0 })
const subs = ref<{ label: string; value: number }[]>([])
const saving = ref(false)

watch(show, async (v) => {
  if (!v) return
  loadPlans()
  form.value = { email: props.email || '', plan_id: null, period: 'month_price', total_amount: null, subscription_id: 0 }
  subs.value = []
  if (props.userId) {
    try {
      const u = await admin.get<UserDetail>(`/users/${props.userId}`)
      subs.value = u.subscriptions.map((s) => ({
        label: `#${s.id} ${s.name || s.plan_name || '无订阅'}${s.is_primary ? '（主订阅）' : ''}`,
        value: s.id,
      }))
    } catch {
      /* 订阅列表加载失败时仍可新开订阅 */
    }
  }
})

const subOptions = computed(() => [{ label: '新开订阅', value: 0 }, ...subs.value])

async function submit() {
  const f = form.value
  if (!f.email.trim() || !f.plan_id || f.total_amount === null) {
    message.warning('请填写用户邮箱、订阅、周期与支付金额')
    return
  }
  saving.value = true
  let tradeNo = ''
  const ok = await run(async () => {
    tradeNo = await admin.post<string>('/orders/assign', {
      email: f.email.trim(),
      plan_id: f.plan_id,
      period: f.period,
      total_amount: toCents(f.total_amount),
      subscription_id: f.subscription_id,
    })
  }, '订单已创建')
  saving.value = false
  if (ok) {
    show.value = false
    emit('done', tradeNo)
  }
}
</script>

<template>
  <Dialog v-model:open="show">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>订单分配</DialogTitle>
        <DialogDescription>为用户创建待支付订单，可在订单管理中标记为已支付完成开通。</DialogDescription>
      </DialogHeader>
      <div class="grid gap-4">
        <FormItem label="用户邮箱" required>
          <Input v-model="form.email" placeholder="请输入用户邮箱" :disabled="!!userId" />
        </FormItem>
        <FormItem label="订阅" required>
          <SelectBox :model-value="form.plan_id" :options="planOptions" placeholder="请选择订阅" @update:model-value="(v) => (form.plan_id = v as number)" />
        </FormItem>
        <FormItem label="周期" required>
          <SelectBox :model-value="form.period" :options="periodOptions" placeholder="请选择周期" @update:model-value="(v) => (form.period = String(v))" />
        </FormItem>
        <FormItem v-if="userId" label="目标订阅" help="续费、变更或购买流量包时选择已有订阅；流量重置包必须选择订阅">
          <SelectBox :model-value="form.subscription_id" :options="subOptions" @update:model-value="(v) => (form.subscription_id = Number(v))" />
        </FormItem>
        <FormItem label="支付金额" required>
          <NumInput v-model="form.total_amount" :min="0" :step="0.01" suffix="元" placeholder="请输入需要支付的金额" />
        </FormItem>
      </div>
      <DialogFooter>
        <Button variant="outline" @click="show = false">取消</Button>
        <Button :disabled="saving" @click="submit">
          <Loader2Icon v-if="saving" class="animate-spin" />
          确定
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
