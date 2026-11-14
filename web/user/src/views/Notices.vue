<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatTime } from '@gv/shared'
import { Card, CardContent } from '@gv/shared/ui/card'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { http } from '../api'
import { errText, toast } from '../ui'
import type { Notice } from '../types'
import PageHeader from '../components/common/PageHeader.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import NoticeDialog from '../components/NoticeDialog.vue'
import SimplePager from '../components/common/SimplePager.vue'

const { t } = useI18n()
const list = ref<Notice[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 10
const loading = ref(true)
const current = ref<Notice | null>(null)
const show = ref(false)

async function load() {
  loading.value = true
  try {
    const r = await http.get<{ total: number; list: Notice[] }>('/user/notices', { current: page.value, page_size: pageSize })
    list.value = r.list || []
    total.value = r.total
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
}
function open(n: Notice) {
  current.value = n
  show.value = true
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader :title="t('nav.notices')" />
    <div v-if="loading && !list.length" class="space-y-3"><Skeleton v-for="i in 3" :key="i" class="h-20 rounded-xl" /></div>
    <div v-else class="space-y-3">
      <Card v-for="n in list" :key="n.id" class="cursor-pointer py-3 transition-colors hover:bg-accent/40" @click="open(n)">
        <CardContent class="flex items-center gap-4 px-4">
          <img v-if="n.img_url" :src="n.img_url" alt="" class="h-16 w-28 shrink-0 rounded-md object-cover" />
          <div class="min-w-0 flex-1">
            <div class="truncate font-medium">{{ n.title }}</div>
            <div class="mt-1.5 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
              <span>{{ formatTime(n.created_at) }}</span>
              <StatusBadge v-for="tag in n.tags || []" :key="tag">{{ tag }}</StatusBadge>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
    <p v-if="!loading && !list.length" class="py-16 text-center text-muted-foreground">{{ t('notice.empty') }}</p>
    <SimplePager v-model:page="page" :total="total" :page-size="pageSize" @change="load" />
    <NoticeDialog v-model:open="show" :notice="current" />
  </div>
</template>
