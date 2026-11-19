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
  { path: '/payments', component: () => import('./views/Payments.vue'), meta: { title: '支付配置', group: '设置' } },
  { path: '/themes', component: () => import('./views/Themes.vue'), meta: { title: '主题配置', group: '设置' } },
  { path: '/plugins', component: () => import('./views/Plugins.vue'), meta: { title: '插件管理', group: '设置' } },
  { path: '/servers', component: () => import('./views/Servers.vue'), meta: { title: '节点管理', group: '服务器' } },
  { path: '/groups', component: () => import('./views/Groups.vue'), meta: { title: '权限组管理', group: '服务器' } },
  { path: '/routes', component: () => import('./views/Routes.vue'), meta: { title: '路由管理', group: '服务器' } },
  { path: '/audit', component: () => import('./views/Audit.vue'), meta: { title: '审计规则', group: '服务器' } },
  { path: '/plans', component: () => import('./views/Plans.vue'), meta: { title: '订阅管理', group: '财务' } },
  { path: '/orders', component: () => import('./views/Orders.vue'), meta: { title: '订单管理', group: '财务' } },
  { path: '/coupons', component: () => import('./views/Coupons.vue'), meta: { title: '优惠券管理', group: '财务' } },
  { path: '/giftcards', component: () => import('./views/Giftcards.vue'), meta: { title: '礼品卡管理', group: '财务' } },
  { path: '/users', component: () => import('./views/Users.vue'), meta: { title: '用户管理', group: '用户' } },
  { path: '/notices', component: () => import('./views/Notices.vue'), meta: { title: '公告管理', group: '用户' } },
  { path: '/tickets', component: () => import('./views/Tickets.vue'), meta: { title: '工单管理', group: '用户' } },
  { path: '/knowledge', component: () => import('./views/Knowledge.vue'), meta: { title: '知识库管理', group: '用户' } },
  { path: '/guard', component: () => import('./views/Guard.vue'), meta: { title: '订阅风控', group: '用户' } },
  { path: '/system', component: () => import('./views/System.vue'), meta: { title: '队列监控', group: '指标' } },
  { path: '/logs', component: () => import('./views/Logs.vue'), meta: { title: '系统日志', group: '指标' } },
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

// 升级部署后旧页面引用的分包文件已不存在，整页刷新一次以加载新版本
router.onError((err, to) => {
  if (!/dynamically imported module|Importing a module script failed/i.test(String(err?.message))) return
  if (sessionStorage.getItem('gv_chunk_reload') === to.fullPath) return
  sessionStorage.setItem('gv_chunk_reload', to.fullPath)
  location.hash = to.fullPath
  location.reload()
})
router.afterEach(() => sessionStorage.removeItem('gv_chunk_reload'))
