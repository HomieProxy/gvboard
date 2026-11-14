import { computed, ref } from 'vue'
import { admin } from './api'
import type { Plan, ServerGroup, ServerRoute } from './types'

// 套餐、权限组、路由在多个页面的下拉框中使用，按需加载并短时缓存（其他管理员的修改最迟 30 秒后可见）；
// 本页修改后调用 invalidate 立即刷新。

type Kind = 'plans' | 'groups' | 'routes'
const TTL = 30_000

const plans = ref<Plan[]>([])
const groups = ref<ServerGroup[]>([])
const routes = ref<ServerRoute[]>([])
const loadedAt: Record<Kind, number> = { plans: 0, groups: 0, routes: 0 }

const fresh = (kind: Kind, force: boolean) => !force && Date.now() - loadedAt[kind] < TTL

export async function loadPlans(force = false) {
  if (fresh('plans', force)) return plans.value
  plans.value = (await admin.get<Plan[]>('/plans')) || []
  loadedAt.plans = Date.now()
  return plans.value
}

export async function loadGroups(force = false) {
  if (fresh('groups', force)) return groups.value
  groups.value = (await admin.get<ServerGroup[]>('/servers/groups')) || []
  loadedAt.groups = Date.now()
  return groups.value
}

export async function loadRoutes(force = false) {
  if (fresh('routes', force)) return routes.value
  routes.value = (await admin.get<ServerRoute[]>('/servers/routes')) || []
  loadedAt.routes = Date.now()
  return routes.value
}

export function invalidate(kind: Kind) {
  loadedAt[kind] = 0
}

export const planOptions = computed(() => plans.value.map((p) => ({ label: p.name, value: p.id })))
export const groupOptions = computed(() => groups.value.map((g) => ({ label: g.name, value: g.id })))
export const routeOptions = computed(() => routes.value.map((r) => ({ label: r.remarks, value: r.id })))
export const planName = (id: number | null | undefined) => plans.value.find((p) => p.id === id)?.name || (id ? `#${id}` : '-')
export const groupName = (id: number | null | undefined) => groups.value.find((g) => g.id === id)?.name || (id ? `#${id}` : '-')
