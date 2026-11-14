<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NInput, NSelect, NSpin, NEmpty, NList, NListItem, NModal, NSpace, NCollapse, NCollapseItem } from 'naive-ui'
import { formatTime } from '@gv/shared'
import { http } from '../api'
import { errText, toast } from '../ui'
import { loadSubs, subOptions } from '../store'
import type { KnowledgeArticle, KnowledgeCategory } from '../types'
import Markdown from '../components/Markdown.vue'

const { t, locale } = useI18n()
const groups = ref<KnowledgeCategory[]>([])
const loading = ref(true)
const keyword = ref('')
const article = ref<KnowledgeArticle | null>(null)
const articleId = ref(0)
const show = ref(false)
const articleLoading = ref(false)
const sub = ref<number | null>(null)
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
  <div class="page">
    <div class="row" style="justify-content: space-between; margin-bottom: 16px; flex-wrap: wrap">
      <h2 class="page-title" style="margin: 0">{{ t('nav.knowledge') }}</h2>
      <NInput v-model:value="keyword" clearable :placeholder="t('knowledge.search')" style="max-width: 280px" />
    </div>
    <NSpin :show="loading">
      <NCard v-if="groups.length" size="small">
        <NCollapse :default-expanded-names="groups.map((g) => g.category)">
          <NCollapseItem v-for="g in groups" :key="g.category" :title="g.category" :name="g.category">
            <NList hoverable clickable>
              <NListItem v-for="a in g.articles" :key="a.id" @click="open(a.id)">
                <div class="row" style="justify-content: space-between">
                  <span>{{ a.title }}</span>
                  <span class="muted">{{ formatTime(a.updated_at, false) }}</span>
                </div>
              </NListItem>
            </NList>
          </NCollapseItem>
        </NCollapse>
      </NCard>
      <NEmpty v-else-if="!loading" :description="t('knowledge.empty')" />
    </NSpin>
    <NModal v-model:show="show" preset="card" :title="article?.title || ''" style="width: 820px; max-width: 96vw">
      <NSpace v-if="subOptions().length > 1" align="center" style="margin-bottom: 12px">
        <span class="muted">{{ t('knowledge.forSub') }}</span>
        <NSelect v-model:value="sub" :options="subOptions()" clearable size="small" style="width: 220px" />
      </NSpace>
      <NSpin :show="articleLoading">
        <Markdown v-if="article" :source="article.body" />
      </NSpin>
    </NModal>
  </div>
</template>
