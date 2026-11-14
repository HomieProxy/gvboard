<script setup lang="ts">
import { ref, watchEffect } from 'vue'
import QRCode from 'qrcode'

const props = withDefaults(defineProps<{ text: string; size?: number }>(), { size: 200 })
const src = ref('')

watchEffect(async () => {
  // 部分支付网关直接返回二维码图片（data:image/...），原样显示
  if (/^data:image\//.test(props.text)) {
    src.value = props.text
    return
  }
  src.value = props.text ? await QRCode.toDataURL(props.text, { width: props.size * 2, margin: 1 }) : ''
})
</script>

<template>
  <img v-if="src" :src="src" :width="size" :height="size" alt="QR" class="block rounded-md bg-white p-1" />
</template>
