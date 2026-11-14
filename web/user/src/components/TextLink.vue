<script setup lang="ts">
import { NButton } from 'naive-ui'

defineProps<{ to: string }>()
</script>

<template>
  <RouterLink v-slot="{ navigate }" :to="to" custom>
    <NButton text type="primary" @click="navigate"><slot /></NButton>
  </RouterLink>
</template>
