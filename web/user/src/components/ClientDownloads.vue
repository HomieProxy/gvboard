<script setup lang="ts">
import { computed, ref, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import { DownloadIcon } from '@lucide/vue'
import { clientIcon, clientPlatforms, platformIcon, type ClientDownload, type ClientPlatform } from '@gv/shared'
import { Button } from '@gv/shared/ui/button'
import { cn } from '@gv/shared/lib/utils'

const props = defineProps<{ clients: ClientDownload[] }>()
const { t } = useI18n()

function detect(): ClientPlatform {
  const ua = navigator.userAgent
  if (/HarmonyOS|OpenHarmony/i.test(ua)) return 'harmony'
  if (/iPhone|iPad|iPod/i.test(ua) || (/Macintosh/i.test(ua) && navigator.maxTouchPoints > 1)) return 'ios'
  if (/Android/i.test(ua)) return 'android'
  if (/Mac OS X|Macintosh/i.test(ua)) return 'macos'
  if (/Windows/i.test(ua)) return 'windows'
  return 'linux'
}

const platforms = computed(() => clientPlatforms.filter((p) => props.clients.some((c) => c.platform === p.value)))
const active = ref<ClientPlatform>(detect())
watchEffect(() => {
  const list = platforms.value
  if (list.length && !list.some((p) => p.value === active.value)) active.value = list[0].value
})
const shown = computed(() => props.clients.filter((c) => c.platform === active.value))
const hideBroken = (e: Event) => ((e.target as HTMLImageElement).style.visibility = 'hidden')
</script>

<template>
  <div v-if="platforms.length" class="space-y-4">
    <div class="flex flex-wrap gap-2">
      <button
        v-for="p in platforms"
        :key="p.value"
        type="button"
        :class="
          cn(
            'inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-sm transition-colors',
            p.value === active ? 'border-primary bg-primary/10 text-primary' : 'hover:bg-accent',
          )
        "
        @click="active = p.value"
      >
        <img :src="platformIcon(p.value)" alt="" class="size-4" />
        {{ p.label }}
      </button>
    </div>
    <div class="grid gap-3 sm:grid-cols-2">
      <div v-for="(c, i) in shown" :key="i" class="flex items-center gap-3 rounded-lg border p-3">
        <div class="flex size-11 shrink-0 items-center justify-center rounded-lg bg-muted">
          <img :src="clientIcon(c)" alt="" class="size-7 object-contain" @error="hideBroken" />
        </div>
        <div class="min-w-0 flex-1 truncate font-medium">{{ c.name }}</div>
        <Button as="a" size="sm" :href="c.url" target="_blank" rel="noopener noreferrer"><DownloadIcon />{{ t('client.download') }}</Button>
      </div>
    </div>
  </div>
  <p v-else class="py-6 text-center text-sm text-muted-foreground">{{ t('client.empty') }}</p>
</template>
