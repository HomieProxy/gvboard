<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { CopyIcon } from '@lucide/vue'
import { daysLeft, formatBytes, formatTime } from '@gv/shared'
import { Card, CardContent, CardFooter, CardHeader, CardTitle, CardAction } from '@gv/shared/ui/card'
import { Progress } from '@gv/shared/ui/progress'
import { Button } from '@gv/shared/ui/button'
import { cn } from '@gv/shared/lib/utils'
import StatusBadge from './common/StatusBadge.vue'
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
  if (!s.plan) return '-'
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
const barCls = computed(() =>
  pct.value >= 90 ? '[&>[data-slot=progress-indicator]]:bg-destructive' : pct.value >= 70 ? '[&>[data-slot=progress-indicator]]:bg-warning' : '',
)

const copy = () => copy2(props.sub.subscribe_url)
</script>

<template>
  <Card class="gap-4 py-4 transition-shadow hover:shadow-md">
    <CardHeader class="px-4">
      <CardTitle class="flex min-w-0 flex-wrap items-center gap-2 text-base">
        <span class="truncate">{{ subName(sub) }}</span>
        <StatusBadge v-if="sub.is_primary" tone="primary">{{ t('sub.primary') }}</StatusBadge>
        <StatusBadge v-if="sub.banned" tone="danger" dot>{{ t('sub.banned') }}</StatusBadge>
        <StatusBadge v-else :tone="ok ? 'success' : 'warning'" dot>{{ ok ? t('sub.available') : t('sub.unavailable') }}</StatusBadge>
      </CardTitle>
      <CardAction class="max-w-[45%] truncate text-sm text-muted-foreground">{{ sub.plan?.name || t('sub.noPlan') }}</CardAction>
    </CardHeader>
    <CardContent class="space-y-2 px-4 text-sm">
      <div class="flex flex-wrap justify-between gap-2">
        <span>{{ t('sub.used', { used: formatBytes(used), total: formatBytes(sub.transfer_enable) }) }}</span>
        <span class="text-muted-foreground">{{ resetText }}</span>
      </div>
      <Progress :model-value="pct" :class="cn('h-2', barCls)" />
      <div class="flex flex-wrap justify-between gap-2 text-muted-foreground">
        <span>{{ t('sub.expire') }}：{{ expireText }}</span>
        <span>{{ t('sub.devices') }}：{{ sub.alive_ip }} / {{ sub.device_limit ?? '∞' }}</span>
      </div>
    </CardContent>
    <CardFooter class="justify-end gap-2 px-4">
      <Button size="sm" variant="outline" @click.stop="copy"><CopyIcon />{{ t('sub.copyLink') }}</Button>
      <slot name="actions" />
    </CardFooter>
  </Card>
</template>
