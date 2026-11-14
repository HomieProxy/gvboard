<script setup lang="ts">
import { computed } from 'vue'
import { NCard } from 'naive-ui'
import { cfg } from '../api'
import TopActions from '../components/TopActions.vue'

const bg = computed(() => String(cfg.value.theme.config.background_url || ''))
</script>

<template>
  <div class="auth" :style="bg ? { backgroundImage: `url(${bg})` } : undefined">
    <div class="top"><TopActions /></div>
    <div class="box">
      <div class="brand">
        <img v-if="cfg.site.logo" :src="cfg.site.logo" alt="" />
        <h1>{{ cfg.site.name }}</h1>
        <p v-if="cfg.site.description" class="muted">{{ cfg.site.description }}</p>
      </div>
      <NCard :bordered="false" class="card">
        <RouterView />
      </NCard>
    </div>
  </div>
</template>

<style scoped>
.auth { min-height: 100vh; display: flex; flex-direction: column; background-size: cover; background-position: center; }
.top { display: flex; justify-content: flex-end; padding: 12px 16px; }
.box { width: 100%; max-width: 420px; margin: 4vh auto 40px; padding: 0 16px; }
.brand { text-align: center; margin-bottom: 20px; }
.brand img { height: 48px; }
.brand h1 { font-size: 24px; margin: 8px 0 4px; }
.card { border-radius: 12px; box-shadow: 0 6px 30px rgba(0, 0, 0, .08); }
</style>
