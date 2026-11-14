<script setup lang="ts">
import { Toaster } from '@gv/shared/ui/sonner'
import ConfirmHost from './components/common/ConfirmHost.vue'
</script>

<template>
  <RouterView />
  <Toaster position="top-center" rich-colors close-button />
  <ConfirmHost />
</template>
