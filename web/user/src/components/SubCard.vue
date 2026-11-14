<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NProgress, NTag, NSpace, NButton } from 'naive-ui'
import { daysLeft, formatBytes, formatTime } from '@gv/shared'
import { subAvailable, subName } from '../store'
import { copy as copy2 } from '../ui'
import type { Subscription } from '../types'

const props = defineProps<{ sub: Subscription }>()
const { t } = useI18n()

const used = computed(() => props.sub.u + props.sub.d)
const pct = computed(() => (props.sub.transfer_enable ? Math.min(100, Math.round((used.value / props.sub.transfer_enable) * 100)) : 0))
const ok = computed(() => subAvailable(props.sub))
const expireText = computed(() => {
  const s = props.sub
  if (s.expired_at === null) return t('sub.forever')
  const d = daysLeft(s.expired_at)
  if (!d) return t('sub.expired')
  return `${formatTime(s.expired_at, false)} · ${t('sub.expireIn', { n: d })}`
})
const resetText = computed(() => {
  const d = props.sub.reset_day
  if (d === null || d === undefined) return ''
  return d === 0 ? t('sub.resetToday') : t('sub.resetIn', { n: d })
})

const copy = () => copy2(props.sub.subscribe_url)
</script>

<template>
  <NCard size="small" hoverable class="sub-card">
    <template #header>
      <NSpace align="center" :size="6">
        <span>{{ subName(sub) }}</span>
        <NTag v-if="sub.is_primary" size="small" type="info" :bordered="false">{{ t('sub.primary') }}</NTag>
        <NTag v-if="sub.banned" size="small" type="error" :bordered="false">{{ t('sub.banned') }}</NTag>
        <NTag v-else size="small" :type="ok ? 'success' : 'warning'" :bordered="false">{{ ok ? t('sub.available') : t('sub.unavailable') }}</NTag>
      </NSpace>
    </template>
    <template #header-extra>
      <span class="muted">{{ sub.plan?.name || t('sub.noPlan') }}</span>
    </template>
    <div class="line">
      <span>{{ t('sub.used', { used: formatBytes(used), total: formatBytes(sub.transfer_enable) }) }}</span>
      <span class="muted">{{ resetText }}</span>
    </div>
    <NProgress type="line" :percentage="pct" :show-indicator="false" :status="pct >= 90 ? 'error' : pct >= 70 ? 'warning' : 'success'" />
    <div class="line muted" style="margin-top: 8px">
      <span>{{ t('sub.expire') }}：{{ expireText }}</span>
      <span>{{ t('sub.devices') }}：{{ sub.alive_ip }} / {{ sub.device_limit ?? '∞' }}</span>
    </div>
    <template #action>
      <NSpace justify="end" :size="8">
        <NButton size="small" @click.stop="copy">{{ t('sub.copyLink') }}</NButton>
        <slot name="actions" />
      </NSpace>
    </template>
  </NCard>
</template>

<style scoped>
.line { display: flex; justify-content: space-between; gap: 8px; flex-wrap: wrap; margin-bottom: 6px; }
</style>
