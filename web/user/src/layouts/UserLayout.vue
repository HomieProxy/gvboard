<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { LogOutIcon, UserIcon, ChevronsUpDownIcon } from '@lucide/vue'
import {
  Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupContent, SidebarGroupLabel, SidebarHeader,
  SidebarInset, SidebarMenu, SidebarMenuButton, SidebarMenuItem, SidebarProvider, SidebarRail, SidebarTrigger,
} from '@gv/shared/ui/sidebar'
import { Separator } from '@gv/shared/ui/separator'
import { Avatar, AvatarFallback, AvatarImage } from '@gv/shared/ui/avatar'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@gv/shared/ui/dropdown-menu'
import { cfg, http, setToken } from '../api'
import { confirm } from '../ui'
import { nav, navGroups, pluginNavKey, type NavItem } from '../router'
import { clearSession, loadBiz, loadMe, loadPluginPages, me, pageTitle, pluginPages } from '../store'
import { pluginIcon } from '../pluginIcons'
import TopActions from '../components/TopActions.vue'
import { rtl } from '../i18n'
import type { Session } from '../types'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const items = computed<NavItem[]>(() => [
  ...nav,
  ...(pluginPages.value || []).map((p) => ({
    path: `/plugin/${p.plugin}/${p.key}`,
    key: pluginNavKey(p.plugin, p.key),
    icon: pluginIcon(p.icon),
    group: navGroups.includes(p.group) ? p.group : 'more',
    title: pageTitle(p),
  })),
])
const groups = computed(() =>
  navGroups.map((name) => ({ name, items: items.value.filter((n) => n.group === name) })).filter((g) => g.items.length),
)
const active = computed(() =>
  route.params.plugin ? pluginNavKey(String(route.params.plugin), String(route.params.key)) : String(route.meta.nav || ''),
)
const current = computed(() => items.value.find((n) => n.key === active.value))
const label = (n: NavItem) => n.title || t(`nav.${n.key}`)
const logoFailed = ref(false)

async function logout() {
  await confirm(t('common.logoutConfirm'), async () => {
    try {
      const list = await http.get<Session[]>('/user/sessions')
      const cur = list.find((s) => s.current)
      if (cur) await http.del(`/user/sessions/${encodeURIComponent(cur.id)}`)
    } catch {
      /* 会话已失效时直接退出 */
    }
    setToken('')
    clearSession()
    router.replace('/login')
  })
}

onMounted(() => {
  loadMe().catch(() => {})
  loadBiz().catch(() => {})
  loadPluginPages()
})
</script>

<template>
  <SidebarProvider class="min-h-svh">
    <Sidebar collapsible="icon" :side="rtl ? 'right' : 'left'">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" as-child>
              <RouterLink to="/dashboard">
                <img v-if="cfg.site.logo && !logoFailed" :src="cfg.site.logo" alt="" class="size-8 shrink-0 object-contain" @error="logoFailed = true" />
                <div v-else class="flex aspect-square size-8 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground">
                  <span class="text-sm font-bold">{{ (cfg.site.name || 'G').slice(0, 1) }}</span>
                </div>
                <span class="truncate font-semibold">{{ cfg.site.name }}</span>
              </RouterLink>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup v-for="g in groups" :key="g.name" class="py-1">
          <SidebarGroupLabel v-if="t(`nav.groups.${g.name}`)" class="text-[11px] tracking-wider text-sidebar-foreground/50">{{ t(`nav.groups.${g.name}`) }}</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem v-for="n in g.items" :key="n.key">
                <SidebarMenuButton
                  as-child
                  :is-active="active === n.key"
                  :tooltip="label(n)"
                  class="h-9 data-[active=true]:bg-sidebar-primary data-[active=true]:text-sidebar-primary-foreground data-[active=true]:shadow-sm data-[active=true]:hover:bg-sidebar-primary/90 data-[active=true]:hover:text-sidebar-primary-foreground"
                >
                  <RouterLink :to="n.path">
                    <component :is="n.icon" />
                    <span>{{ label(n) }}</span>
                  </RouterLink>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <DropdownMenu>
              <DropdownMenuTrigger as-child>
                <SidebarMenuButton size="lg" class="data-[state=open]:bg-sidebar-accent">
                  <Avatar class="size-8 rounded-lg">
                    <AvatarImage v-if="me?.avatar_url" :src="me.avatar_url" />
                    <AvatarFallback class="rounded-lg">{{ (me?.email || '?').slice(0, 1).toUpperCase() }}</AvatarFallback>
                  </Avatar>
                  <span class="truncate text-sm">{{ me?.email }}</span>
                  <ChevronsUpDownIcon class="ms-auto size-4" />
                </SidebarMenuButton>
              </DropdownMenuTrigger>
              <DropdownMenuContent side="top" align="start" class="w-(--reka-dropdown-menu-trigger-width) min-w-56">
                <DropdownMenuLabel class="truncate font-normal text-muted-foreground">{{ me?.email }}</DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem @click="router.push('/profile')"><UserIcon />{{ t('nav.profile') }}</DropdownMenuItem>
                <DropdownMenuItem variant="destructive" @click="logout"><LogOutIcon />{{ t('common.logout') }}</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>

    <SidebarInset class="min-w-0">
      <header class="sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b bg-card/90 px-4 backdrop-blur">
        <SidebarTrigger class="-ms-1" />
        <Separator orientation="vertical" class="me-1 data-[orientation=vertical]:h-4" />
        <span class="truncate text-sm font-medium">{{ current ? label(current) : '' }}</span>
        <div class="ms-auto"><TopActions /></div>
      </header>
      <main class="flex-1">
        <div class="mx-auto w-full max-w-7xl p-4 md:p-6">
          <RouterView v-slot="{ Component }">
            <component :is="Component" :key="route.path" />
          </RouterView>
        </div>
      </main>
    </SidebarInset>
  </SidebarProvider>
</template>
