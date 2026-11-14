<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NSpin, NEmpty, NPagination, NModal, NSpace, NTag } from 'naive-ui'
import { formatTime } from '@gv/shared'
import { http } from '../api'
import { errText, toast } from '../ui'
import type { Notice } from '../types'
import Markdown from '../components/Markdown.vue'

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
  <div class="page">
    <h2 class="page-title">{{ t('nav.notices') }}</h2>
    <NSpin :show="loading">
      <NSpace vertical :size="12">
        <NCard v-for="n in list" :key="n.id" size="small" hoverable class="notice" @click="open(n)">
          <div class="item">
            <img v-if="n.img_url" :src="n.img_url" alt="" class="cover" />
            <div style="flex: 1; min-width: 0">
              <div class="title">{{ n.title }}</div>
              <NSpace :size="6" style="margin-top: 6px">
                <span class="muted">{{ formatTime(n.created_at) }}</span>
                <NTag v-for="tag in n.tags || []" :key="tag" size="tiny" :bordered="false">{{ tag }}</NTag>
              </NSpace>
            </div>
          </div>
        </NCard>
      </NSpace>
      <NEmpty v-if="!loading && !list.length" :description="t('notice.empty')" />
    </NSpin>
    <div v-if="total > pageSize" style="display: flex; justify-content: center; margin-top: 16px">
      <NPagination v-model:page="page" :item-count="total" :page-size="pageSize" @update:page="load" />
    </div>
    <NModal v-model:show="show" preset="card" :title="current?.title || ''" style="width: 760px; max-width: 96vw">
      <img v-if="current?.img_url" :src="current.img_url" alt="" style="max-width: 100%; border-radius: 6px; margin-bottom: 12px" />
      <Markdown v-if="current" :source="current.content" />
    </NModal>
  </div>
</template>

<style scoped>
.notice { cursor: pointer; }
.item { display: flex; gap: 14px; align-items: center; }
.cover { width: 96px; height: 60px; object-fit: cover; border-radius: 6px; }
.title { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
