<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { WalletIcon, CoinsIcon, GiftIcon, ArrowRightLeftIcon, PlusIcon, Loader2Icon } from '@lucide/vue'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { http } from '../api'
import { errText, toast } from '../ui'
import { biz, loadBiz, loadMe, loadSubs, me, money, subOptions } from '../store'
import PageHeader from '../components/common/PageHeader.vue'
import FormItem from '../components/common/FormItem.vue'
import NumInput from '../components/common/NumInput.vue'
import SelectBox from '../components/common/SelectBox.vue'

const { t } = useI18n()
const router = useRouter()

// 充值奖励档位：“充值金额:赠送金额”，单位元
const tiers = computed(() =>
  (biz.value?.deposit_bonus || [])
    .map((s) => s.split(':').map(Number))
    .filter((p) => p.length === 2 && p.every((n) => Number.isFinite(n)) && p[0] > 0 && p[1] > 0)
    .sort((a, b) => a[0] - b[0]),
)
const amount = ref<number | null>(null)
const bonus = computed(() => {
  const a = amount.value || 0
  let add = 0
  for (const [need, give] of tiers.value) if (a >= need && give > add) add = give
  return Math.trunc(add * 100)
})
const depositing = ref(false)

async function deposit() {
  const a = amount.value || 0
  if (a <= 0) return toast('warning', t('wallet.minAmount'))
  depositing.value = true
  try {
    const no = await http.post<string>('/user/orders', { plan_id: 0, period: 'deposit', deposit_amount: Math.round(a * 100) })
    router.push(`/order/${no}`)
  } catch (e) {
    toast('error', errText(e))
  } finally {
    depositing.value = false
  }
}

const code = ref('')
const giftTarget = ref<number | null>(null)
const giftOptions = computed(() => [{ label: t('common.none'), value: null }, ...subOptions()])
const redeeming = ref(false)
async function redeem() {
  if (!code.value.trim()) return
  redeeming.value = true
  try {
    await http.post('/user/giftcard/redeem', { code: code.value.trim(), subscription_id: giftTarget.value || undefined })
    toast('success', t('wallet.redeemOk'))
    code.value = ''
    await Promise.all([loadMe(), loadSubs()])
  } catch (e) {
    toast('error', errText(e))
  } finally {
    redeeming.value = false
  }
}

const transferAmount = ref<number | null>(null)
const transferring = ref(false)
async function transfer() {
  const a = Math.round((transferAmount.value || 0) * 100)
  if (a < 1) return toast('warning', t('wallet.minAmount'))
  transferring.value = true
  try {
    await http.post('/user/commission/transfer', { amount: a })
    toast('success', t('wallet.transferOk'))
    transferAmount.value = null
    await loadMe()
  } catch (e) {
    toast('error', errText(e))
  } finally {
    transferring.value = false
  }
}

onMounted(() => {
  Promise.all([loadMe(), loadBiz(), loadSubs()]).catch((e) => toast('error', errText(e)))
})
</script>

<template>
  <div>
    <PageHeader :title="t('nav.wallet')" />
    <div class="grid gap-6 lg:grid-cols-2">
      <Card class="bg-gradient-to-br from-primary to-primary/80 text-primary-foreground">
        <CardContent class="grid grid-cols-2 gap-6">
          <div>
            <div class="flex items-center gap-1.5 text-sm opacity-80"><WalletIcon class="size-4" />{{ t('wallet.balance') }}</div>
            <div class="mt-2 text-3xl font-semibold tabular-nums">{{ money(me?.balance || 0) }}</div>
          </div>
          <div>
            <div class="flex items-center gap-1.5 text-sm opacity-80"><CoinsIcon class="size-4" />{{ t('wallet.commission') }}</div>
            <div class="mt-2 text-3xl font-semibold tabular-nums">{{ money(me?.commission_balance || 0) }}</div>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-base"><PlusIcon class="size-4" />{{ t('wallet.deposit') }}</CardTitle>
        </CardHeader>
        <CardContent class="space-y-3">
          <NumInput v-model="amount" :min="0.01" :step="0.01" :placeholder="t('wallet.depositAmount')" :suffix="biz?.currency || ''" />
          <div v-if="tiers.length" class="flex flex-wrap gap-2">
            <button
              v-for="[need, give] in tiers"
              :key="need"
              type="button"
              class="rounded-md border border-success/40 bg-success/10 px-2 py-1 text-xs text-success transition-colors hover:bg-success/20"
              @click="amount = need"
            >
              {{ t('wallet.depositBonus', { amount: money(need * 100), bonus: money(give * 100) }) }}
            </button>
          </div>
          <p v-if="bonus" class="text-sm text-success">{{ t('wallet.bonusTip', { n: money(bonus) }) }}</p>
        </CardContent>
        <CardFooter>
          <Button :disabled="!amount || depositing" @click="deposit">
            <Loader2Icon v-if="depositing" class="animate-spin" />{{ t('wallet.depositSubmit') }}
          </Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-base"><GiftIcon class="size-4" />{{ t('wallet.giftcard') }}</CardTitle>
        </CardHeader>
        <form @submit.prevent="redeem">
          <CardContent class="space-y-4">
            <FormItem :label="t('wallet.giftcardCode')">
              <Input v-model="code" autocomplete="off" />
            </FormItem>
            <FormItem :label="t('wallet.giftcardTarget')" :help="t('wallet.giftcardTargetHint')">
              <SelectBox :model-value="giftTarget" :options="giftOptions" @update:model-value="(v) => (giftTarget = v === null ? null : Number(v))" />
            </FormItem>
          </CardContent>
          <CardFooter class="pt-6">
            <Button type="submit" :disabled="!code.trim() || redeeming">
              <Loader2Icon v-if="redeeming" class="animate-spin" />{{ t('wallet.redeem') }}
            </Button>
          </CardFooter>
        </form>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-base"><ArrowRightLeftIcon class="size-4" />{{ t('wallet.transfer') }}</CardTitle>
          <CardDescription>{{ t('wallet.transferHint') }}</CardDescription>
        </CardHeader>
        <CardContent>
          <NumInput
            v-model="transferAmount"
            :min="0.01"
            :max="(me?.commission_balance || 0) / 100"
            :step="0.01"
            :placeholder="t('wallet.transferAmount')"
            :suffix="biz?.currency || ''"
          />
        </CardContent>
        <CardFooter>
          <Button variant="outline" :disabled="!transferAmount || !me?.commission_balance || transferring" @click="transfer">
            <Loader2Icon v-if="transferring" class="animate-spin" />{{ t('invite.transfer') }}
          </Button>
        </CardFooter>
      </Card>
    </div>
  </div>
</template>
