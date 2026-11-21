<script setup lang="ts">
import { computed, onMounted, ref, type Component } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import {
  LayoutDashboardIcon, SettingsIcon, CreditCardIcon, PaletteIcon, PuzzleIcon, ServerIcon, UsersRoundIcon, RouteIcon,
  PackageIcon, ReceiptIcon, TicketPercentIcon, GiftIcon, UserIcon, MegaphoneIcon, MessageSquareIcon, BookOpenIcon,
  ShieldIcon, ActivityIcon, ScrollTextIcon, LogOutIcon, MoonIcon, SunIcon, TagIcon, EyeIcon, HardDriveIcon, FerrisWheelIcon, WalletIcon,
} from '@lucide/vue'
import {
  Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupContent, SidebarGroupLabel, SidebarHeader,
  SidebarInset, SidebarMenu, SidebarMenuButton, SidebarMenuItem, SidebarProvider, SidebarRail, SidebarTrigger,
} from '@gv/shared/ui/sidebar'
import { Separator } from '@gv/shared/ui/separator'
import { Button } from '@gv/shared/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@gv/shared/ui/tooltip'
import { admin, cfg, setToken } from '../api'
import { menu } from '../router'
import { features, loadFeatures } from '../features'
import { colorMode } from '../theme'

const route = useRoute()
const router = useRouter()

const icons: Record<string, Component> = {
  '/dashboard': LayoutDashboardIcon,
  '/config': SettingsIcon,
  '/payments': CreditCardIcon,
  '/themes': PaletteIcon,
  '/plugins': PuzzleIcon,
  '/servers': ServerIcon,
  '/groups': UsersRoundIcon,
  '/routes': RouteIcon,
  '/audit': EyeIcon,
  '/machines': HardDriveIcon,
  '/plans': PackageIcon,
  '/orders': ReceiptIcon,
  '/balance-logs': WalletIcon,
  '/coupons': TicketPercentIcon,
  '/giftcards': GiftIcon,
  '/users': UserIcon,
  '/notices': MegaphoneIcon,
  '/tickets': MessageSquareIcon,
  '/knowledge': BookOpenIcon,
  '/guard': ShieldIcon,
  '/lucky-wheel': FerrisWheelIcon,
  '/system': ActivityIcon,
  '/logs': ScrollTextIcon,
}

const groups = computed(() => {
  const out: { name: string; items: typeof menu }[] = []
  for (const m of menu) {
    if (m.feature && !features.value[m.feature]) continue
    let g = out.find((x) => x.name === m.group)
    if (!g) out.push((g = { name: m.group, items: [] }))
    g.items.push(m)
  }
  return out
})

const current = computed(() => menu.find((m) => m.path === route.path))
const logoFailed = ref(false)
const darkSider = computed(() => cfg.value.theme.sidebar === 'dark' && colorMode.value !== 'dark')
const darkHeader = computed(() => cfg.value.theme.header === 'dark' && colorMode.value !== 'dark')

const activeCls =
  'h-9 data-[active=true]:bg-sidebar-primary data-[active=true]:text-sidebar-primary-foreground data-[active=true]:shadow-sm ' +
  'data-[active=true]:hover:bg-sidebar-primary/90 data-[active=true]:hover:text-sidebar-primary-foreground'

const releasesURL = 'https://github.com/gunzi-666/gvboard/releases'
const version = ref<{ version: string; build_time: string } | null>(null)
const versionTip = computed(() => {
  const v = version.value
  if (!v) return ''
  const built = v.build_time ? `，构建于 ${v.build_time.slice(0, 16).replace('T', ' ')}` : ''
  return `当前版本 ${v.version}${built}，点击查看版本发布`
})
onMounted(async () => {
  loadFeatures()
  try {
    version.value = await admin.get('/system/version')
  } catch {
    version.value = null
  }
})

function toggleDark() {
  colorMode.value = colorMode.value === 'dark' ? 'light' : 'dark'
}
function logout() {
  setToken('')
  router.replace('/login')
}
</script>

<template>
  <SidebarProvider class="h-svh">
    <Sidebar collapsible="icon" :class="darkSider ? 'dark text-sidebar-foreground' : ''">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" as-child>
              <RouterLink to="/dashboard">
                <img v-if="cfg.site.logo && !logoFailed" :src="cfg.site.logo" alt="" class="size-8 shrink-0 object-contain" @error="logoFailed = true" />
                <div v-else class="flex aspect-square size-8 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground">
                  <span class="text-sm font-bold">{{ (cfg.site.name || 'G').slice(0, 1) }}</span>
                </div>
                <div class="grid flex-1 text-left leading-tight">
                  <span class="truncate font-semibold">{{ cfg.site.name || '管理后台' }}</span>
                  <span class="truncate text-xs text-sidebar-foreground/60">管理后台</span>
                </div>
              </RouterLink>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup v-for="g in groups" :key="g.name" class="py-1">
          <SidebarGroupLabel v-if="g.name" class="text-[11px] tracking-wider text-sidebar-foreground/50">{{ g.name }}</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem v-for="m in g.items" :key="m.path">
                <SidebarMenuButton as-child :is-active="route.path === m.path" :tooltip="m.title" :class="activeCls">
                  <RouterLink :to="m.path">
                    <component :is="icons[m.path]" />
                    <span>{{ m.title }}</span>
                  </RouterLink>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem v-if="version">
            <SidebarMenuButton as-child :tooltip="versionTip" class="text-sidebar-foreground/60">
              <a :href="releasesURL" target="_blank" rel="noopener noreferrer" :title="versionTip">
                <TagIcon />
                <span class="font-mono text-xs">版本 {{ version.version }}</span>
              </a>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton tooltip="退出登录" @click="logout">
              <LogOutIcon />
              <span>退出登录</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>

    <SidebarInset class="min-w-0 overflow-hidden">
      <header
        :class="[
          'sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b px-4 backdrop-blur',
          darkHeader ? 'dark bg-sidebar text-foreground' : 'bg-card/90',
        ]"
      >
        <SidebarTrigger class="-ml-1" />
        <Separator orientation="vertical" class="mr-1 data-[orientation=vertical]:h-4" />
        <nav class="flex min-w-0 items-center gap-1.5 text-sm">
          <span v-if="current?.group" class="hidden text-muted-foreground sm:inline">{{ current.group }}</span>
          <span v-if="current?.group" class="hidden text-muted-foreground sm:inline">/</span>
          <span class="truncate font-medium">{{ current?.title }}</span>
        </nav>
        <div class="ml-auto flex items-center gap-1">
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" size="icon" class="size-8" @click="toggleDark">
                <SunIcon v-if="colorMode === 'dark'" />
                <MoonIcon v-else />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{{ colorMode === 'dark' ? '浅色模式' : '深色模式' }}</TooltipContent>
          </Tooltip>
        </div>
      </header>
      <main class="flex-1 overflow-y-auto">
        <div class="w-full p-4 md:p-6">
          <RouterView v-slot="{ Component }">
            <component :is="Component" :key="route.path" />
          </RouterView>
        </div>
      </main>
    </SidebarInset>
  </SidebarProvider>
</template>
