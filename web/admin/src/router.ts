import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import { token } from './api'

export interface MenuItem {
  path: string
  title: string
  group: string
}

const pages: (RouteRecordRaw & { meta: { title: string; group: string } })[] = [
  { path: '/dashboard', component: () => import('./views/Dashboard.vue'), meta: { title: '仪表盘', group: '' } },
  { path: '/config', component: () => import('./views/Config.vue'), meta: { title: '系统配置', group: '设置' } },
  { path: '/payments', component: () => import('./views/Payments.vue'), meta: { title: '支付方式', group: '设置' } },
  { path: '/themes', component: () => import('./views/Themes.vue'), meta: { title: '主题', group: '设置' } },
  { path: '/plugins', component: () => import('./views/Plugins.vue'), meta: { title: '插件', group: '设置' } },
  { path: '/servers', component: () => import('./views/Servers.vue'), meta: { title: '节点管理', group: '节点' } },
  { path: '/groups', component: () => import('./views/Groups.vue'), meta: { title: '权限组', group: '节点' } },
  { path: '/routes', component: () => import('./views/Routes.vue'), meta: { title: '路由', group: '节点' } },
  { path: '/plans', component: () => import('./views/Plans.vue'), meta: { title: '套餐管理', group: '财务' } },
  { path: '/orders', component: () => import('./views/Orders.vue'), meta: { title: '订单管理', group: '财务' } },
  { path: '/coupons', component: () => import('./views/Coupons.vue'), meta: { title: '优惠券', group: '财务' } },
  { path: '/giftcards', component: () => import('./views/Giftcards.vue'), meta: { title: '礼品卡', group: '财务' } },
  { path: '/users', component: () => import('./views/Users.vue'), meta: { title: '用户管理', group: '用户' } },
  { path: '/notices', component: () => import('./views/Notices.vue'), meta: { title: '公告管理', group: '用户' } },
  { path: '/tickets', component: () => import('./views/Tickets.vue'), meta: { title: '工单管理', group: '用户' } },
  { path: '/knowledge', component: () => import('./views/Knowledge.vue'), meta: { title: '知识库', group: '用户' } },
  { path: '/guard', component: () => import('./views/Guard.vue'), meta: { title: '订阅风控', group: '系统' } },
  { path: '/system', component: () => import('./views/System.vue'), meta: { title: '系统状态', group: '系统' } },
  { path: '/logs', component: () => import('./views/Logs.vue'), meta: { title: '系统日志', group: '系统' } },
]

export const menu: MenuItem[] = pages.map((p) => ({ path: p.path, title: p.meta.title, group: p.meta.group }))

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/login', component: () => import('./views/Login.vue'), meta: { title: '登录', public: true } },
    { path: '/', component: () => import('./layouts/AdminLayout.vue'), redirect: '/dashboard', children: pages },
    { path: '/:any(.*)*', redirect: '/dashboard' },
  ],
})

router.beforeEach((to) => {
  if (!to.meta.public && !token.value) return { path: '/login', query: { redirect: to.fullPath } }
  if (to.path === '/login' && token.value) return { path: '/dashboard' }
})
