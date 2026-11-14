<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SearchIcon, FileTextIcon, ChevronRightIcon, Loader2Icon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { Card, CardContent, CardHeader, CardTitle } from '@gv/shared/ui/card'
import { Input } from '@gv/shared/ui/input'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@gv/shared/ui/sheet'
import { http } from '../api'
import { errText, toast } from '../ui'
import { loadSubs, subOptions } from '../store'
import type { KnowledgeArticle, KnowledgeCategory } from '../types'
import Markdown from '../components/Markdown.vue'
import PageHeader from '../components/common/PageHeader.vue'
import SelectBox from '../components/common/SelectBox.vue'

const { t, locale } = useI18n()
const groups = ref<KnowledgeCategory[]>([])
const loading = ref(true)
const keyword = ref('')
const article = ref<KnowledgeArticle | null>(null)
const articleId = ref(0)
const show = ref(false)
const articleLoading = ref(false)
const sub = ref<number | null>(null)
const subChoices = computed(() => subOptions())
let timer = 0

async function load() {
  loading.value = true
  try {
    groups.value = (await http.get<KnowledgeCategory[]>('/user/knowledge', { language: locale.value, keyword: keyword.value.trim() || undefined })) || []
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
}

async function open(id: number) {
  articleId.value = id
  show.value = true
  articleLoading.value = true
  try {
    article.value = await http.get<KnowledgeArticle>(`/user/knowledge/${id}`, { subscription_id: sub.value || undefined })
  } catch (e) {
    show.value = false
    toast('error', errText(e))
  } finally {
    articleLoading.value = false
  }
}

watch(keyword, () => {
  clearTimeout(timer)
  timer = window.setTimeout(load, 300)
})
watch(locale, load)
watch(sub, () => {
  if (show.value && articleId.value) open(articleId.value)
})

onMounted(() => {
  load()
  loadSubs().catch(() => {})
})
</script>

<template>
  <div>
    <PageHeader :title="t('nav.knowledge')">
      <div class="relative w-64 max-w-full">
        <SearchIcon class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input v-model="keyword" class="pl-8" :placeholder="t('knowledge.search')" />
      </div>
    </PageHeader>
    <div v-if="loading && !groups.length" class="grid gap-6 lg:grid-cols-2">
      <Skeleton v-for="i in 2" :key="i" class="h-48 rounded-xl" />
    </div>
    <div v-else-if="groups.length" class="grid items-start gap-6 lg:grid-cols-2">
      <Card v-for="g in groups" :key="g.category" class="gap-3">
        <CardHeader>
          <CardTitle class="text-base">{{ g.category }}</CardTitle>
        </CardHeader>
        <CardContent class="px-3">
          <button
            v-for="a in g.articles"
            :key="a.id"
            type="button"
            class="flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left transition-colors hover:bg-accent"
            @click="open(a.id)"
          >
            <FileTextIcon class="size-4 shrink-0 text-muted-foreground" />
            <span class="min-w-0 flex-1 truncate text-sm">{{ a.title }}</span>
            <span class="hidden text-xs text-muted-foreground sm:inline">{{ formatTime(a.updated_at, false) }}</span>
            <ChevronRightIcon class="size-4 text-muted-foreground rtl:rotate-180" />
          </button>
        </CardContent>
      </Card>
    </div>
    <p v-else class="py-16 text-center text-muted-foreground">{{ t('knowledge.empty') }}</p>

    <Sheet v-model:open="show">
      <SheetContent class="w-full gap-0 sm:max-w-3xl">
        <SheetHeader class="border-b">
          <SheetTitle>{{ article?.title }}</SheetTitle>
          <SheetDescription v-if="article">{{ t('knowledge.updatedAt', { time: formatTime(article.updated_at, false) }) }}</SheetDescription>
          <div v-if="subChoices.length > 1" class="flex flex-wrap items-center gap-2 pt-2 text-sm">
            <span class="text-muted-foreground">{{ t('knowledge.forSub') }}</span>
            <SelectBox
              class="h-8 w-56"
              :model-value="sub"
              :options="subChoices"
              @update:model-value="(v) => (sub = v === null ? null : Number(v))"
            />
          </div>
        </SheetHeader>
        <div class="relative flex-1 overflow-y-auto p-4 md:p-6">
          <div v-if="articleLoading" class="absolute inset-0 flex items-center justify-center bg-background/60">
            <Loader2Icon class="size-6 animate-spin text-muted-foreground" />
          </div>
          <Markdown v-if="article" :source="article.body" />
        </div>
      </SheetContent>
    </Sheet>
  </div>
</template>
