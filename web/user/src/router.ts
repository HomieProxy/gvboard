import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import { token } from './api'
import { ui } from './ui'

export interface NavItem {
  path: string
  key: string
  icon: string
}

/** 侧边菜单（标题为 nav.{key} 的翻译） */
export const nav: NavItem[] = [
  { path: '/dashboard', key: 'dashboard', icon: '◧' },
  { path: '/subscriptions', key: 'subscriptions', icon: '◉' },
  { path: '/plans', key: 'plans', icon: '✦' },
  { path: '/orders', key: 'orders', icon: '☰' },
  { path: '/wallet', key: 'wallet', icon: '¤' },
  { path: '/invite', key: 'invite', icon: '❖' },
  { path: '/tickets', key: 'tickets', icon: '✉' },
  { path: '/knowledge', key: 'knowledge', icon: '❏' },
  { path: '/notices', key: 'notices', icon: '◆' },
  { path: '/traffic', key: 'traffic', icon: '⇅' },
  { path: '/profile', key: 'profile', icon: '☺' },
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
]

export const router = createRouter({
  history: createWebHashHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
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
  ui.loading?.start()
  if (to.path === '/') return token.value ? '/dashboard' : '/login'
  // 快捷登录链接（带 verify 参数）即使已登录也交给登录页处理
  if (to.meta.public) {
    if (token.value && !to.query.verify) return '/dashboard'
    return
  }
  if (!token.value) return { path: '/login', query: { redirect: to.fullPath } }
})
router.afterEach(() => ui.loading?.finish())
router.onError(() => ui.loading?.error())
