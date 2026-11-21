import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import type { Component } from 'vue'
import {
  LayoutDashboardIcon, LayersIcon, ShoppingBagIcon, ReceiptTextIcon, WalletIcon, UsersIcon, MessageSquareIcon, BookOpenIcon,
  MegaphoneIcon, ActivityIcon, UserIcon,
} from '@lucide/vue'
import { cfg, token } from './api'

export interface NavItem {
  path: string
  key: string
  icon: Component
  group: 'main' | 'subscribe' | 'finance' | 'more' | 'user'
  /** 插件页面的标题（内置菜单为空，使用 nav.{key} 的翻译） */
  title?: string
}

/** 侧边菜单分组顺序 */
export const navGroups: NavItem['group'][] = ['main', 'subscribe', 'finance', 'more', 'user']

/** 插件页面在菜单中的 key，与插件页面路由的 meta 对应 */
export const pluginNavKey = (plugin: string, key: string) => `plugin:${plugin}/${key}`

/** 侧边菜单（标题为 nav.{key} 的翻译，分组标题为 nav.groups.{group}）；已启用插件的页面在布局中追加 */
export const nav: NavItem[] = [
  { path: '/dashboard', key: 'dashboard', icon: LayoutDashboardIcon, group: 'main' },
  { path: '/knowledge', key: 'knowledge', icon: BookOpenIcon, group: 'main' },
  { path: '/notices', key: 'notices', icon: MegaphoneIcon, group: 'main' },
  { path: '/subscriptions', key: 'subscriptions', icon: LayersIcon, group: 'subscribe' },
  { path: '/plans', key: 'plans', icon: ShoppingBagIcon, group: 'subscribe' },
  { path: '/orders', key: 'orders', icon: ReceiptTextIcon, group: 'finance' },
  { path: '/wallet', key: 'wallet', icon: WalletIcon, group: 'finance' },
  { path: '/invite', key: 'invite', icon: UsersIcon, group: 'finance' },
  { path: '/profile', key: 'profile', icon: UserIcon, group: 'user' },
  { path: '/tickets', key: 'tickets', icon: MessageSquareIcon, group: 'user' },
  { path: '/traffic', key: 'traffic', icon: ActivityIcon, group: 'user' },
]

const pages: RouteRecordRaw[] = [
  { path: '/dashboard', component: () => import('./views/Dashboard.vue'), meta: { nav: 'dashboard' } },
  { path: '/subscriptions', component: () => import('./views/Subscriptions.vue'), meta: { nav: 'subscriptions' } },
  { path: '/subscriptions/:id(\\d+)', component: () => import('./views/SubscriptionDetail.vue'), meta: { nav: 'subscriptions' } },
  { path: '/plans', component: () => import('./views/Plans.vue'), meta: { nav: 'plans' } },
  { path: '/plans/:id(\\d+)', component: () => import('./views/PlanCheckout.vue'), meta: { nav: 'plans' } },
  { path: '/orders', component: () => import('./views/Orders.vue'), meta: { nav: 'orders' } },
  // 支付网关回跳地址固定为 /#/order/{trade_no}
  { path: '/order/:trade_no', component: () => import('./views/OrderDetail.vue'), meta: { nav: 'orders' } },
  { path: '/wallet', component: () => import('./views/Wallet.vue'), meta: { nav: 'wallet' } },
  { path: '/invite', component: () => import('./views/Invite.vue'), meta: { nav: 'invite' } },
  { path: '/tickets', component: () => import('./views/Tickets.vue'), meta: { nav: 'tickets' } },
  { path: '/tickets/:id(\\d+)', component: () => import('./views/TicketDetail.vue'), meta: { nav: 'tickets' } },
  { path: '/knowledge', component: () => import('./views/Knowledge.vue'), meta: { nav: 'knowledge' } },
  { path: '/notices', component: () => import('./views/Notices.vue'), meta: { nav: 'notices' } },
  { path: '/traffic', component: () => import('./views/Traffic.vue'), meta: { nav: 'traffic' } },
  { path: '/profile', component: () => import('./views/Profile.vue'), meta: { nav: 'profile' } },
  // 插件页面：主题提供专门界面的显示该界面，否则按插件返回的区块显示
  { path: '/plugin/:plugin/:key', component: () => import('./views/PluginPage.vue') },
  { path: '/lucky-wheel', redirect: '/plugin/lucky_wheel/wheel' },
]

export const router = createRouter({
  history: createWebHashHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    { path: '/', component: () => import('./views/Home.vue'), meta: { public: true } },
    {
      path: '/',
      component: () => import('./layouts/AuthLayout.vue'),
      children: [
        { path: 'login', component: () => import('./views/Login.vue'), meta: { public: true } },
        { path: 'register', component: () => import('./views/Register.vue'), meta: { public: true } },
        { path: 'forgot', component: () => import('./views/Forgot.vue'), meta: { public: true } },
      ],
    },
    { path: '/', component: () => import('./layouts/UserLayout.vue'), children: pages },
    { path: '/:any(.*)*', redirect: '/dashboard' },
  ],
})

router.beforeEach((to) => {
  if (to.path === '/') {
    if (token.value) return '/dashboard'
    if (cfg.value.site.home === false) return '/login'
    return
  }
  // 快捷登录链接（带 verify 参数）即使已登录也交给登录页处理
  if (to.meta.public) {
    if (token.value && !to.query.verify) return '/dashboard'
    return
  }
  if (!token.value) return { path: '/login', query: { redirect: to.fullPath } }
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
