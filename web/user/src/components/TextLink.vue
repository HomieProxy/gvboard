<script setup lang="ts">
defineProps<{ to: string }>()
</script>

<template>
  <RouterLink :to="to" class="font-medium text-primary underline-offset-4 hover:underline"><slot /></RouterLink>
</template>
