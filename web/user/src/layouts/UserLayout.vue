<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NLayout, NLayoutSider, NLayoutHeader, NLayoutContent, NMenu, NButton, NDrawer, NDrawerContent, NDropdown, NAvatar, type MenuOption } from 'naive-ui'
import { cfg, http, setToken } from '../api'
import { confirm } from '../ui'
import { nav } from '../router'
import { clearSession, loadBiz, loadMe, me } from '../store'
import TopActions from '../components/TopActions.vue'
import type { Session } from '../types'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const mobile = ref(window.innerWidth < 900)
const drawer = ref(false)
window.addEventListener('resize', () => (mobile.value = window.innerWidth < 900))

const options = computed<MenuOption[]>(() =>
  nav.map((n) => ({
    key: n.key,
    icon: () => h('span', { class: 'icon' }, n.icon),
    label: () => h(RouterLink, { to: n.path }, { default: () => t(`nav.${n.key}`) }),
  })),
)
const active = computed(() => String(route.meta.nav || ''))
watch(() => route.fullPath, () => (drawer.value = false))

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

const userOptions = computed(() => [
  { label: t('nav.profile'), key: 'profile' },
  { label: t('common.logout'), key: 'logout' },
])
function onUser(k: string) {
  if (k === 'profile') router.push('/profile')
  else logout()
}

onMounted(() => {
  loadMe().catch(() => {})
  loadBiz().catch(() => {})
})
</script>

<template>
  <NLayout :has-sider="!mobile" position="absolute">
    <NLayoutSider v-if="!mobile" bordered :width="220" :native-scrollbar="false">
      <div class="brand">
        <img v-if="cfg.site.logo" :src="cfg.site.logo" alt="" />
        <span>{{ cfg.site.name }}</span>
      </div>
      <NMenu :value="active" :options="options" />
    </NLayoutSider>
    <NLayout :native-scrollbar="false">
      <NLayoutHeader bordered class="header">
        <div class="left">
          <NButton v-if="mobile" quaternary @click="drawer = true">☰</NButton>
          <strong v-if="mobile">{{ cfg.site.name }}</strong>
        </div>
        <div class="right">
          <TopActions />
          <NDropdown :options="userOptions" trigger="click" @select="onUser">
            <NButton quaternary size="small" class="user">
              <NAvatar v-if="me?.avatar_url" round :size="22" :src="me.avatar_url" style="margin-right: 6px" />
              <span class="email">{{ me?.email }}</span>
            </NButton>
          </NDropdown>
        </div>
      </NLayoutHeader>
      <NLayoutContent>
        <RouterView v-slot="{ Component }">
          <component :is="Component" :key="route.path" />
        </RouterView>
      </NLayoutContent>
    </NLayout>
  </NLayout>
  <NDrawer v-model:show="drawer" :width="240" placement="left">
    <NDrawerContent :title="cfg.site.name" body-content-style="padding: 0">
      <NMenu :value="active" :options="options" />
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.brand { display: flex; align-items: center; gap: 10px; height: 60px; padding: 0 20px; font-size: 17px; font-weight: 600; white-space: nowrap; overflow: hidden; }
.brand img { height: 28px; }
.header { display: flex; align-items: center; justify-content: space-between; height: 56px; padding: 0 12px; position: sticky; top: 0; z-index: 10; }
.left, .right { display: flex; align-items: center; gap: 4px; }
.email { max-width: 180px; overflow: hidden; text-overflow: ellipsis; }
:deep(.icon) { display: inline-block; width: 18px; text-align: center; }
@media (max-width: 640px) { .email { display: none; } }
</style>
