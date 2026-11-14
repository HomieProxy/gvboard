<script setup lang="ts">
import { computed } from 'vue'
import { NConfigProvider, NMessageProvider, NDialogProvider } from 'naive-ui'
import { cfg } from './api'
import { locale, dateLocale, themeOverrides } from './ui'

const overrides = computed(() => themeOverrides(cfg.value.theme.color))
</script>

<template>
  <NConfigProvider :locale="locale" :date-locale="dateLocale" :theme-overrides="overrides">
    <NMessageProvider>
      <NDialogProvider>
        <RouterView />
      </NDialogProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>
