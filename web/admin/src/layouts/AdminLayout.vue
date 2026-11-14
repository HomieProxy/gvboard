<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { NLayout, NLayoutSider, NLayoutHeader, NLayoutContent, NMenu, NButton, NSpace, type MenuOption } from 'naive-ui'
import { cfg, setToken } from '../api'
import { menu } from '../router'

const route = useRoute()
const router = useRouter()
const collapsed = ref(window.innerWidth < 900)

const options = computed<MenuOption[]>(() => {
  const out: MenuOption[] = []
  const groups = new Map<string, MenuOption[]>()
  for (const m of menu) {
    const item: MenuOption = { key: m.path, label: () => h(RouterLink, { to: m.path }, { default: () => m.title }) }
    if (!m.group) {
      out.push(item)
      continue
    }
    if (!groups.has(m.group)) {
      const children: MenuOption[] = []
      groups.set(m.group, children)
      out.push({ type: 'group', key: `g-${m.group}`, label: m.group, children })
    }
    groups.get(m.group)!.push(item)
  }
  return out
})

const title = computed(() => menu.find((m) => m.path === route.path)?.title || '')
const darkSider = computed(() => cfg.value.theme.sidebar === 'dark')
const darkHeader = computed(() => cfg.value.theme.header === 'dark')

function logout() {
  setToken('')
  router.replace('/login')
}
</script>

<template>
  <NLayout has-sider style="height: 100%">
    <NLayoutSider
      bordered
      collapse-mode="width"
      :collapsed-width="0"
      :width="200"
      :collapsed="collapsed"
      show-trigger="bar"
      :inverted="darkSider"
      :native-scrollbar="false"
      @collapse="collapsed = true"
      @expand="collapsed = false"
    >
      <div class="brand">
        <img v-if="cfg.site.logo" :src="cfg.site.logo" alt="" />
        <span>{{ cfg.site.name || '管理后台' }}</span>
      </div>
      <NMenu :inverted="darkSider" :value="route.path" :options="options" :indent="18" />
    </NLayoutSider>
    <NLayout>
      <NLayoutHeader bordered :inverted="darkHeader" class="header">
        <NSpace align="center">
          <NButton quaternary size="small" @click="collapsed = !collapsed">☰</NButton>
          <strong>{{ title }}</strong>
        </NSpace>
        <NButton quaternary size="small" @click="logout">退出登录</NButton>
      </NLayoutHeader>
      <NLayoutContent :native-scrollbar="false" style="height: calc(100% - 49px)">
        <RouterView v-slot="{ Component }">
          <component :is="Component" :key="route.path" />
        </RouterView>
      </NLayoutContent>
    </NLayout>
  </NLayout>
</template>

<style scoped>
.brand { display: flex; align-items: center; gap: 8px; height: 48px; padding: 0 18px; font-weight: 600; overflow: hidden; white-space: nowrap; }
.brand img { height: 24px; }
.header { display: flex; align-items: center; justify-content: space-between; height: 48px; padding: 0 12px; }
</style>
