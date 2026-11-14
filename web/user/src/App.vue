<script setup lang="ts">
import { Toaster } from '@gv/shared/ui/sonner'
import ConfirmHost from './components/ConfirmHost.vue'
import { isDark } from './theme'
</script>

<template>
  <RouterView />
  <Toaster position="top-center" rich-colors close-button :theme="isDark ? 'dark' : 'light'" />
  <ConfirmHost />
</template>
