<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  WalletIcon, CoinsIcon, MessageSquareIcon, UsersIcon, BookOpenIcon, DownloadIcon, RefreshCwIcon, LifeBuoyIcon,
  ChevronRightIcon, MegaphoneIcon, TriangleAlertIcon,
} from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Card, CardContent, CardHeader, CardTitle, CardAction } from '@gv/shared/ui/card'
import { Button } from '@gv/shared/ui/button'
import { Alert, AlertDescription } from '@gv/shared/ui/alert'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { cfg, http } from '../api'
import { errText, toast } from '../ui'
import { loadMe, loadStat, loadSubs, me, money, stat, subs } from '../store'
import type { Notice } from '../types'
import SubCard from '../components/SubCard.vue'
import NoticeDialog from '../components/NoticeDialog.vue'
import StatusBadge from '../components/common/StatusBadge.vue'

const { t } = useI18n()
const router = useRouter()
const loading = ref(true)
const notices = ref<Notice[]>([])
const notice = ref<Notice | null>(null)
const noticeShow = ref(false)

const primary = computed(() => subs.value.find((s) => s.is_primary) || subs.value[0])
const cards = computed(() => [
  { label: t('dashboard.balance'), value: money(me.value?.balance), icon: WalletIcon, to: '/wallet' },
  { label: t('dashboard.commission'), value: money(me.value?.commission_balance), icon: CoinsIcon, to: '/invite' },
  { label: t('dashboard.openTickets'), value: stat.value?.open_tickets ?? 0, icon: MessageSquareIcon, to: '/tickets' },
  { label: t('dashboard.invitees'), value: stat.value?.invitees ?? 0, icon: UsersIcon, to: '/invite' },
])
const shortcuts = computed(() => [
  { title: t('dashboard.tutorial'), desc: t('dashboard.tutorialDesc', { site: cfg.value.site.name }), icon: BookOpenIcon, to: '/knowledge' },
  {
    title: t('dashboard.oneClick'),
    desc: t('dashboard.oneClickDesc'),
    icon: DownloadIcon,
    to: primary.value ? `/subscriptions/${primary.value.id}` : '/plans',
  },
  {
    title: t('dashboard.renew'),
    desc: t('dashboard.renewDesc'),
    icon: RefreshCwIcon,
    to: primary.value?.plan ? { path: `/plans/${primary.value.plan.id}`, query: { sub: primary.value.id } } : '/plans',
  },
  { title: t('dashboard.problem'), desc: t('dashboard.problemDesc'), icon: LifeBuoyIcon, to: '/tickets' },
])

function openNotice(n: Notice) {
  notice.value = n
  noticeShow.value = true
}

onMounted(async () => {
  try {
    const [, , , n] = await Promise.all([
      loadMe(),
      loadStat(),
      loadSubs(),
      http.get<{ total: number; list: Notice[] }>('/user/notices', { current: 1, page_size: 20 }),
    ])
    notices.value = n.list || []
    const pop = notices.value.find((x) => (x.tags || []).includes('弹窗'))
    if (pop) openNotice(pop)
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="space-y-6">
    <Alert v-if="stat && stat.pending_orders > 0" class="border-warning/50 bg-warning/10">
      <TriangleAlertIcon class="text-warning" />
      <AlertDescription class="flex flex-wrap items-center justify-between gap-2 text-foreground">
        <span>{{ t('dashboard.pendingOrderTip', { n: stat.pending_orders }) }}</span>
        <Button size="sm" @click="router.push('/orders')">{{ t('dashboard.goPay') }}</Button>
      </AlertDescription>
    </Alert>

    <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <Card v-for="c in cards" :key="c.label" class="cursor-pointer gap-2 py-4 transition-colors hover:bg-accent/40" @click="router.push(c.to)">
        <CardContent class="flex items-center justify-between gap-3 px-4">
          <div class="min-w-0">
            <div class="text-sm text-muted-foreground">{{ c.label }}</div>
            <Skeleton v-if="loading" class="mt-2 h-7 w-20" />
            <div v-else class="mt-1 truncate text-2xl font-semibold tabular-nums">{{ c.value }}</div>
          </div>
          <div class="flex size-10 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
            <component :is="c.icon" class="size-5" />
          </div>
        </CardContent>
      </Card>
    </div>

    <div class="grid gap-6 xl:grid-cols-3">
      <div class="space-y-6 xl:col-span-2">
        <section>
          <div class="mb-3 flex items-center justify-between">
            <h2 class="text-base font-semibold">{{ t('dashboard.mySubscriptions') }}</h2>
            <Button variant="link" size="sm" class="px-0" @click="router.push('/subscriptions')">
              {{ t('dashboard.manage') }}<ChevronRightIcon />
            </Button>
          </div>
          <div v-if="loading" class="space-y-3"><Skeleton class="h-40 w-full rounded-xl" /></div>
          <div v-else class="grid gap-4 2xl:grid-cols-2">
            <SubCard v-for="s in subs" :key="s.id" :sub="s" class="cursor-pointer" @click="router.push(`/subscriptions/${s.id}`)" />
          </div>
          <Card v-if="!loading && !subs.some((s) => s.plan)" class="mt-4">
            <CardContent class="flex flex-col items-center gap-3 py-6 text-center">
              <span class="text-muted-foreground">{{ t('dashboard.noSubscription') }}</span>
              <Button @click="router.push('/plans')">{{ t('dashboard.buyNow') }}</Button>
            </CardContent>
          </Card>
        </section>

        <section>
          <h2 class="mb-3 text-base font-semibold">{{ t('dashboard.shortcuts') }}</h2>
          <Card class="py-0">
            <CardContent class="divide-y px-0">
              <button
                v-for="s in shortcuts"
                :key="s.title"
                type="button"
                class="flex w-full items-center gap-4 px-4 py-3.5 text-left transition-colors hover:bg-accent/40"
                @click="router.push(s.to)"
              >
                <div class="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                  <component :is="s.icon" class="size-4.5" />
                </div>
                <div class="min-w-0 flex-1">
                  <div class="font-medium">{{ s.title }}</div>
                  <div class="truncate text-sm text-muted-foreground">{{ s.desc }}</div>
                </div>
                <ChevronRightIcon class="size-4 text-muted-foreground" />
              </button>
            </CardContent>
          </Card>
        </section>
      </div>

      <Card class="h-fit gap-3">
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-base"><MegaphoneIcon class="size-4" />{{ t('dashboard.notices') }}</CardTitle>
          <CardAction>
            <Button variant="link" size="sm" class="h-auto px-0" @click="router.push('/notices')">{{ t('common.view') }}</Button>
          </CardAction>
        </CardHeader>
        <CardContent class="px-2">
          <div v-if="loading" class="space-y-2 px-2"><Skeleton v-for="i in 3" :key="i" class="h-10 w-full" /></div>
          <div v-else-if="notices.length" class="space-y-0.5">
            <button
              v-for="n in notices.slice(0, 6)"
              :key="n.id"
              type="button"
              class="w-full rounded-md px-2 py-2 text-left transition-colors hover:bg-accent"
              @click="openNotice(n)"
            >
              <div class="truncate text-sm font-medium">{{ n.title }}</div>
              <div class="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                <StatusBadge v-for="tg in n.tags || []" :key="tg">{{ tg }}</StatusBadge>
                <span>{{ formatTime(n.created_at, false) }}</span>
              </div>
            </button>
          </div>
          <p v-else class="py-6 text-center text-sm text-muted-foreground">{{ t('notice.empty') }}</p>
        </CardContent>
      </Card>
    </div>

    <NoticeDialog v-model:open="noticeShow" :notice="notice" />
  </div>
</template>
