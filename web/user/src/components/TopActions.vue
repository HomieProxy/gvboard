<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { LanguagesIcon, MoonIcon, SunIcon, MonitorIcon, CheckIcon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@gv/shared/ui/dropdown-menu'
import { locales, setLocale } from '../i18n'
import { appearance, isDark, setAppearance } from '../theme'

// 语言与外观切换
const { t, locale } = useI18n()
const current = computed(() => locales.find((l) => l.value === locale.value)?.label || '')
const modes = computed(() => [
  { key: 'auto', label: t('common.auto'), icon: MonitorIcon },
  { key: 'light', label: t('common.light'), icon: SunIcon },
  { key: 'dark', label: t('common.dark'), icon: MoonIcon },
])
</script>

<template>
  <div class="flex items-center gap-1">
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Button variant="ghost" size="sm" class="gap-1.5">
          <LanguagesIcon />
          <span class="hidden sm:inline">{{ current }}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem v-for="l in locales" :key="l.value" @click="setLocale(l.value)">
          <CheckIcon :class="l.value === locale ? '' : 'invisible'" />
          {{ l.label }}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Button variant="ghost" size="icon" class="size-8" :title="t('common.appearance')">
          <MoonIcon v-if="isDark" />
          <SunIcon v-else />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem v-for="m in modes" :key="m.key" @click="setAppearance(m.key)">
          <component :is="m.icon" />
          {{ m.label }}
          <CheckIcon v-if="(appearance || 'auto') === m.key" class="ml-auto" />
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
</template>
