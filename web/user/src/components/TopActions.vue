<script setup lang="ts">
import { computed } from 'vue'
import { NDropdown, NButton, NSpace } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { locales, setLocale, type Locale } from '../i18n'
import { appearance, isDark, setAppearance } from '../theme'

// 语言与外观切换
const { t, locale } = useI18n()
const langOptions = locales.map((l) => ({ label: l.label, key: l.value }))
const current = computed(() => locales.find((l) => l.value === locale.value)?.label || '')
const themeOptions = computed(() => [
  { label: t('common.auto'), key: 'auto' },
  { label: t('common.light'), key: 'light' },
  { label: t('common.dark'), key: 'dark' },
])
</script>

<template>
  <NSpace :size="4" align="center">
    <NDropdown :options="langOptions" trigger="click" :value="locale" @select="(k: Locale) => setLocale(k)">
      <NButton quaternary size="small">🌐 {{ current }}</NButton>
    </NDropdown>
    <NDropdown :options="themeOptions" trigger="click" :value="appearance || 'auto'" @select="setAppearance">
      <NButton quaternary size="small" :title="t('common.appearance')">{{ isDark ? '☾' : '☀' }}</NButton>
    </NDropdown>
  </NSpace>
</template>
