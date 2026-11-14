<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NGrid, NGi, NCard, NButton, NInputNumber, NInput, NSelect, NStatistic, NSpace, NTag, NForm, NFormItem } from 'naive-ui'
import { http } from '../api'
import { errText, toast } from '../ui'
import { biz, loadBiz, loadMe, loadSubs, me, money, subOptions } from '../store'

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
  if (a <= 0) return
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
  if (a < 1) return
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
  <div class="page">
    <h2 class="page-title">{{ t('nav.wallet') }}</h2>
    <NGrid cols="1 m:2" responsive="screen" :x-gap="16" :y-gap="16">
      <NGi>
        <NCard size="small">
          <NSpace :size="48">
            <NStatistic :label="t('wallet.balance')" :value="money(me?.balance || 0)" />
            <NStatistic :label="t('wallet.commission')" :value="money(me?.commission_balance || 0)" />
          </NSpace>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" :title="t('wallet.deposit')">
          <NSpace vertical>
            <NInputNumber v-model:value="amount" :min="0.01" :precision="2" :placeholder="t('wallet.depositAmount')" style="width: 100%">
              <template #prefix>{{ biz?.currency_symbol }}</template>
            </NInputNumber>
            <NSpace v-if="tiers.length" :size="6">
              <NTag v-for="[need, give] in tiers" :key="need" size="small" :bordered="false" type="success" style="cursor: pointer" @click="amount = need">
                {{ t('wallet.depositBonus', { amount: money(need * 100), bonus: money(give * 100) }) }}
              </NTag>
            </NSpace>
            <div v-if="bonus" class="muted">{{ t('wallet.bonusTip', { n: money(bonus) }) }}</div>
            <NButton type="primary" :disabled="!amount" :loading="depositing" @click="deposit">{{ t('wallet.depositSubmit') }}</NButton>
          </NSpace>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" :title="t('wallet.giftcard')">
          <NForm @submit.prevent="redeem">
            <NFormItem :label="t('wallet.giftcardCode')">
              <NInput v-model:value="code" />
            </NFormItem>
            <NFormItem :label="t('wallet.giftcardTarget')">
              <NSelect v-model:value="giftTarget" :options="subOptions()" clearable :placeholder="t('wallet.giftcardTargetHint')" />
            </NFormItem>
            <NButton type="primary" attr-type="submit" :disabled="!code.trim()" :loading="redeeming">{{ t('wallet.redeem') }}</NButton>
          </NForm>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" :title="t('wallet.transfer')">
          <p class="muted">{{ t('wallet.transferHint') }}</p>
          <NSpace vertical>
            <NInputNumber
              v-model:value="transferAmount"
              :min="0.01"
              :max="(me?.commission_balance || 0) / 100"
              :precision="2"
              :placeholder="t('wallet.transferAmount')"
              style="width: 100%"
            >
              <template #prefix>{{ biz?.currency_symbol }}</template>
            </NInputNumber>
            <NButton :disabled="!transferAmount || !me?.commission_balance" :loading="transferring" @click="transfer">{{ t('invite.transfer') }}</NButton>
          </NSpace>
        </NCard>
      </NGi>
    </NGrid>
  </div>
</template>
