<script setup lang="ts">
import { useMessage, useDialog, useLoadingBar } from 'naive-ui'
import { ui } from '../ui'

ui.message = useMessage()
ui.dialog = useDialog()
ui.loading = useLoadingBar()
</script>

<template>
  <span style="display: none" />
</template>
