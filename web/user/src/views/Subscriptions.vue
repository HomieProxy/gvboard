<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { PlusIcon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { Skeleton } from '@gv/shared/ui/skeleton'
import { errText, toast } from '../ui'
import { loadMe, loadSubs, me, subs } from '../store'
import SubCard from '../components/SubCard.vue'
import PageHeader from '../components/common/PageHeader.vue'

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
  <div>
    <PageHeader :title="t('nav.subscriptions')" :description="me ? t('sub.limitTip', { n: subs.length, max: me.subscription_limit }) : ''">
      <Button :disabled="full" :title="full ? t('sub.limitReached') : ''" @click="router.push({ path: '/plans', query: { sub: 0 } })">
        <PlusIcon />{{ t('sub.add') }}
      </Button>
    </PageHeader>
    <div v-if="loading" class="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
      <Skeleton v-for="i in 2" :key="i" class="h-44 rounded-xl" />
    </div>
    <div v-else class="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
      <SubCard v-for="s in subs" :key="s.id" :sub="s" class="cursor-pointer" @click="router.push(`/subscriptions/${s.id}`)">
        <template #actions>
          <Button size="sm" @click.stop="router.push(`/subscriptions/${s.id}`)">{{ t('common.detail') }}</Button>
        </template>
      </SubCard>
    </div>
    <p v-if="!loading && !subs.length" class="py-16 text-center text-muted-foreground">{{ t('common.empty') }}</p>
  </div>
</template>
