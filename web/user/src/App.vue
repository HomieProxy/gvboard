<script setup lang="ts">
import { NConfigProvider, NMessageProvider, NDialogProvider, NGlobalStyle, NLoadingBarProvider } from 'naive-ui'
import { naiveTheme, overrides, naiveLocale, naiveDateLocale } from './theme'
import UiBridge from './components/UiBridge.vue'
</script>

<template>
  <NConfigProvider :theme="naiveTheme" :theme-overrides="overrides" :locale="naiveLocale" :date-locale="naiveDateLocale">
    <NGlobalStyle />
    <NLoadingBarProvider>
      <NMessageProvider>
        <NDialogProvider>
          <UiBridge />
          <RouterView />
        </NDialogProvider>
      </NMessageProvider>
    </NLoadingBarProvider>
  </NConfigProvider>
</template>
