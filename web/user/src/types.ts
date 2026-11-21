// 原生 API 数据结构（金额：分；流量：字节，套餐流量为 GB；时间：Unix 秒）
import type { ClientDownload } from '@gv/shared'

export interface AuthResult {
  token: string
  is_admin: boolean
}

export interface BizConfig {
  currency: string
  currency_symbol: string
  withdraw: { closed: boolean; methods: string[]; min: number }
  distribution: { enabled: boolean; levels: (number | null)[] }
  plan_change: boolean
  ticket_status: number
  deposit_bonus: string[]
  /** 只允许使用客户端：为 true 时不显示订阅链接 */
  client_only: boolean
  clients: ClientDownload[]
}

export interface UserInfo {
  id: number
  email: string
  balance: number
  commission_balance: number
  discount: number | null
  commission_rate: number | null
  telegram_bound: boolean
  subscription_limit: number
  avatar_url: string
  last_login_at: number | null
  created_at: number
}

export interface UserStat {
  pending_orders: number
  open_tickets: number
  invitees: number
}

export interface Session {
  id: string
  ip: string
  ua: string
  login_at: number
  current: boolean
}

export interface Subscription {
  id: number
  name: string
  is_primary: boolean
  plan: { id: number; name: string } | null
  transfer_enable: number
  u: number
  d: number
  device_limit: number | null
  speed_limit: number | null
  expired_at: number | null
  auto_renewal: boolean
  remind_expire: boolean
  remind_traffic: boolean
  banned: boolean
  reset_day: number | null
  alive_ip: number
  subscribe_url: string
  allow_new_period: boolean
  created_at: number
}

export type Period =
  | 'month_price'
  | 'quarter_price'
  | 'half_year_price'
  | 'year_price'
  | 'two_year_price'
  | 'three_year_price'
  | 'onetime_price'
  | 'reset_price'
  | 'deposit'

export type PricePeriod = Exclude<Period, 'deposit'>

export const PERIODS: PricePeriod[] = [
  'month_price',
  'quarter_price',
  'half_year_price',
  'year_price',
  'two_year_price',
  'three_year_price',
  'onetime_price',
  'reset_price',
]

export interface Plan {
  id: number
  group_id: number
  name: string
  content: string | null
  transfer_enable: number
  device_limit: number | null
  speed_limit: number | null
  show: number
  renew: number
  sort: number | null
  month_price: number | null
  quarter_price: number | null
  half_year_price: number | null
  year_price: number | null
  two_year_price: number | null
  three_year_price: number | null
  onetime_price: number | null
  reset_price: number | null
  reset_traffic_method: number | null
  capacity_limit: number | null
  /** 插件追加的标签（如“首单立减 20%”），纯文本显示 */
  badges?: string[]
}

/** 插件对订单金额的调整明细（分，负数为减免） */
export interface AmountItem {
  label: string
  amount: number
  plugin: string
}

/** 结算试算（POST /user/orders/preview），各项为分 */
export interface Quote {
  type: number
  price: number
  coupon: number
  user_discount: number
  surplus: number
  refund: number
  items: AmountItem[]
  balance: number
  total: number
}

export interface Order {
  trade_no: string
  plan_id: number
  plan: { id: number; name: string } | null
  subscription_id: number | null
  type: number
  period: Period
  total_amount: number
  handling_amount: number | null
  discount_amount: number | null
  surplus_amount: number | null
  refund_amount: number | null
  balance_amount: number | null
  status: number
  paid_at: number | null
  created_at: number
  updated_at: number
}

export interface OrderDetail extends Order {
  plan_detail?: Plan
  bonus?: number
  get_amount?: number
  try_out_plan_id: number
  surplus_orders?: Order[]
  /** 插件调整明细，减免部分已计入 discount_amount */
  adjustments?: AmountItem[]
}

export interface CheckoutResult {
  type: -1 | 0 | 1 | 2
  data: unknown
}

export interface Coupon {
  code: string
  name: string
  type: 1 | 2
  value: number
  limit_plan_ids: number[] | null
  limit_period: Period[] | null
  started_at: number
  ended_at: number
}

export interface PaymentMethod {
  id: number
  name: string
  payment: string
  icon: string | null
  handling_fee_fixed: number | null
  handling_fee_percent: number | null
}

export interface InviteCode {
  id: number
  code: string
  pv: number
  created_at: number
}

export interface InviteOverview {
  codes: InviteCode[]
  stat: {
    invitees: number
    total_commission: number
    pending_commission: number
    commission_rate: number
    commission_balance: number
  }
}

export interface CommissionDetail {
  id: number
  trade_no: string
  order_amount: number
  get_amount: number
  created_at: number
}

export interface Ticket {
  id: number
  subject: string
  level: number
  status: number
  reply_status: number
  subscription_id: number | null
  created_at: number
  updated_at: number
}

export interface TicketMessage {
  id: number
  message: string
  created_at: number
  is_me: boolean
}

export interface TicketDetail extends Ticket {
  messages: TicketMessage[]
}

export interface Notice {
  id: number
  title: string
  content: string
  img_url: string | null
  tags: string[] | null
  created_at: number
}

/** 插件卡片（GET /user/plugin-widgets） */
export interface PluginWidget {
  plugin: string
  key: string
  slot: string
  title: string
  icon: string
  state: string
}

/** 插件卡片状态（插件的 state 路由返回） */
export interface PluginWidgetState {
  hidden?: boolean
  text?: string
  value?: string
  actions?: { label: string; path?: string; link?: string; disabled?: boolean; confirm?: string }[]
}

export type PluginAction = NonNullable<PluginWidgetState['actions']>[number]

/** 插件页面（GET /user/plugin-pages），地址为 /plugin/{plugin}/{key} */
export interface PluginPage {
  plugin: string
  key: string
  title: string
  titles?: Record<string, string>
  icon: string
  group: 'subscribe' | 'finance' | 'more' | 'user'
  content: string
}

/** 插件页面区块（插件的 content 路由返回 { blocks }） */
export interface PluginBlock {
  type: 'markdown' | 'alert' | 'fields' | 'table' | 'actions'
  title?: string
  text?: string
  tone?: 'info' | 'success' | 'warning' | 'danger'
  fields?: { label: string; value: string; copy?: boolean; secret?: boolean }[]
  columns?: string[]
  rows?: string[][]
  actions?: PluginAction[]
}

export type WheelPrizeType = 'balance' | 'days' | 'traffic' | 'none'

/** 幸运大转盘奖品：value 为分 / 天 / MB */
export interface WheelPrize {
  name: string
  type: WheelPrizeType
  value: number
}

/** 幸运大转盘信息（插件 lucky_wheel）：traffic_cost 为 MB，paid_left 为 -1 表示不限 */
export interface WheelInfo {
  prizes: WheelPrize[]
  free_daily: number
  free_left: number
  balance_enable: boolean
  balance_cost: number
  traffic_enable: boolean
  traffic_cost: number
  paid_daily: number
  paid_left: number
}

export interface WheelResult {
  index: number
  prize: WheelPrize
  cost_type: 'free' | 'balance' | 'traffic'
  cost: number
}

/** 抽奖记录：流量奖品与流量消耗的数值为字节 */
export interface WheelRecord {
  id: number
  subscription_id: number
  prize_name: string
  prize_type: WheelPrizeType
  prize_value: number
  cost_type: 'free' | 'balance' | 'traffic'
  cost: number
  created_at: number
}

export interface KnowledgeCategory {
  category: string
  articles: { id: number; category: string; title: string; updated_at: number }[]
}

export interface KnowledgeArticle {
  id: number
  category: string
  title: string
  body: string
  updated_at: number
}

export interface TrafficLog {
  u: number
  d: number
  record_at: number
  subscription_id: number | null
  server_rate: number
}

export interface ServerView {
  id: number | string
  type: string
  name: string
  rate: number | string
  tags: string[] | null
  is_online: number
  last_check_at: number | null
}

export interface TelegramStatus {
  enabled: boolean
  username: string
  bound: boolean
  discuss_link: string
  prefs: { notify_order: boolean; notify_ticket: boolean; notify_remind: boolean }
}
