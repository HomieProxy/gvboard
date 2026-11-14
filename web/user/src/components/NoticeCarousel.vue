<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronLeftIcon, ChevronRightIcon, MegaphoneIcon } from '@lucide/vue'
import { formatTime } from '@gv/shared'
import { cn } from '@gv/shared/lib/utils'
import type { Notice } from '../types'

const props = defineProps<{ notices: Notice[] }>()
const emit = defineEmits<{ open: [n: Notice] }>()
const { t } = useI18n()

const index = ref(0)
const current = computed(() => props.notices[index.value] || null)
const broken = ref(new Set<number>())
const image = (n: Notice) => (n.img_url && !broken.value.has(n.id) ? n.img_url : '')

let timer: ReturnType<typeof setInterval> | undefined
function restart() {
  clearInterval(timer)
  if (props.notices.length > 1) timer = setInterval(() => go(index.value + 1), 6000)
}
function pause() {
  clearInterval(timer)
}
function go(i: number) {
  const n = props.notices.length
  if (n) index.value = (i + n) % n
}
function step(d: number) {
  go(index.value + d)
  restart()
}
watch(
  () => props.notices.length,
  () => {
    index.value = 0
    restart()
  },
  { immediate: true },
)
onUnmounted(pause)
</script>

<template>
  <div
    v-if="current"
    class="group relative h-44 overflow-hidden rounded-xl border bg-gradient-to-br from-primary to-primary/70 text-primary-foreground shadow-sm sm:h-52"
    @mouseenter="pause"
    @mouseleave="restart"
  >
    <template v-for="(n, i) in notices" :key="n.id">
      <img
        v-if="image(n)"
        :src="image(n)"
        alt=""
        :class="cn('absolute inset-0 size-full object-cover transition-opacity duration-700', i === index ? 'opacity-100' : 'opacity-0')"
        @error="broken = new Set(broken).add(n.id)"
      />
    </template>
    <div :class="cn('absolute inset-0', image(current) ? 'bg-gradient-to-t from-black/75 via-black/30 to-transparent' : '')" />
    <button type="button" class="absolute inset-0 flex flex-col justify-end p-5 text-left text-white sm:p-6" @click="emit('open', current)">
      <span class="mb-2 inline-flex w-fit items-center gap-1.5 rounded-full bg-white/20 px-2.5 py-0.5 text-xs backdrop-blur">
        <MegaphoneIcon class="size-3.5" />{{ t('dashboard.notices') }}
      </span>
      <span class="line-clamp-2 text-lg font-semibold drop-shadow sm:text-xl">{{ current.title }}</span>
      <span class="mt-1 text-xs text-white/80">{{ formatTime(current.created_at, false) }}</span>
    </button>
    <template v-if="notices.length > 1">
      <button
        type="button"
        class="absolute top-1/2 left-2 flex size-8 -translate-y-1/2 items-center justify-center rounded-full bg-black/25 text-white opacity-0 transition-opacity group-hover:opacity-100 hover:bg-black/40"
        @click="step(-1)"
      >
        <ChevronLeftIcon class="size-4 rtl:rotate-180" />
      </button>
      <button
        type="button"
        class="absolute top-1/2 right-2 flex size-8 -translate-y-1/2 items-center justify-center rounded-full bg-black/25 text-white opacity-0 transition-opacity group-hover:opacity-100 hover:bg-black/40"
        @click="step(1)"
      >
        <ChevronRightIcon class="size-4 rtl:rotate-180" />
      </button>
      <div class="absolute right-5 bottom-5 flex gap-1.5 sm:right-6 sm:bottom-6">
        <button
          v-for="(n, i) in notices"
          :key="n.id"
          type="button"
          :aria-label="n.title"
          :class="cn('h-1.5 rounded-full bg-white/50 transition-all', i === index ? 'w-5 bg-white' : 'w-1.5')"
          @click="go(i), restart()"
        />
      </div>
    </template>
  </div>
</template>
