<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NGrid, NGi, NButton, NSpin, NEmpty } from 'naive-ui'
import { errText, toast } from '../ui'
import { loadMe, loadSubs, me, subs } from '../store'
import SubCard from '../components/SubCard.vue'

const { t } = useI18n()
const router = useRouter()
const loading = ref(true)
const full = computed(() => !!me.value && subs.value.length >= me.value.subscription_limit)

onMounted(async () => {
  try {
    await Promise.all([loadSubs(), loadMe()])
  } catch (e) {
    toast('error', errText(e))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <div class="row" style="margin-bottom: 16px">
      <h2 class="page-title" style="margin: 0">{{ t('nav.subscriptions') }}</h2>
      <span v-if="me" class="muted">{{ t('sub.limitTip', { n: subs.length, max: me.subscription_limit }) }}</span>
      <span class="spacer" />
      <NButton type="primary" :disabled="full" :title="full ? t('sub.limitReached') : ''" @click="router.push({ path: '/plans', query: { sub: 0 } })">
        {{ t('sub.add') }}
      </NButton>
    </div>
    <NSpin :show="loading">
      <NGrid cols="1 m:2" responsive="screen" :x-gap="16" :y-gap="16">
        <NGi v-for="s in subs" :key="s.id">
          <SubCard :sub="s" class="clickable" @click="router.push(`/subscriptions/${s.id}`)">
            <template #actions>
              <NButton size="small" type="primary" secondary @click.stop="router.push(`/subscriptions/${s.id}`)">{{ t('common.detail') }}</NButton>
            </template>
          </SubCard>
        </NGi>
      </NGrid>
      <NEmpty v-if="!loading && !subs.length" :description="t('common.empty')" />
    </NSpin>
  </div>
</template>
