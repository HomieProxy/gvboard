<script setup lang="ts">
import { computed } from 'vue'
import { Card, CardContent } from '@gv/shared/ui/card'
import { cfg } from '../api'
import TopActions from '../components/TopActions.vue'

const bg = computed(() => String(cfg.value.theme.config.background_url || ''))
</script>

<template>
  <div
    class="flex min-h-svh flex-col bg-muted/40 bg-cover bg-center"
    :style="bg ? { backgroundImage: `url(${bg})` } : undefined"
  >
    <div class="flex justify-end p-3"><TopActions class="rounded-md bg-background/80 backdrop-blur" /></div>
    <div class="mx-auto mt-[4vh] mb-10 w-full max-w-md px-4">
      <div class="mb-6 text-center">
        <img v-if="cfg.site.logo" :src="cfg.site.logo" alt="" class="mx-auto h-12" />
        <h1 class="mt-2 text-2xl font-semibold tracking-tight">{{ cfg.site.name }}</h1>
        <p v-if="cfg.site.description" class="mt-1 text-sm text-muted-foreground">{{ cfg.site.description }}</p>
      </div>
      <Card class="shadow-lg">
        <CardContent>
          <RouterView />
        </CardContent>
      </Card>
    </div>
  </div>
</template>
