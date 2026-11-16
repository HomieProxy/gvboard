// 管理端接口数据结构（金额：分；时间：Unix 秒；订阅流量：字节；套餐流量：GB）

export interface AdminUser {
  id: number
  email: string
  balance: number
  commission_balance: number
  commission_type: 0 | 1 | 2
  commission_rate: number | null
  discount: number | null
  banned: 0 | 1
  is_admin: 0 | 1
  is_staff: 0 | 1
  invite_user_id: number | null
  telegram_id: number | null
  remarks: string | null
  subscription_limit: number | null
  last_login_at: number | null
  created_at: number
  updated_at: number
}

export interface Sub {
  id: number
  user_id: number
  name: string
  is_primary: 0 | 1
  plan_id: number | null
  group_id: number | null
  uuid: string
  token: string
  transfer_enable: number
  u: number
  d: number
  t: number
  device_limit: number | null
  speed_limit: number | null
  expired_at: number | null
  auto_renewal: 0 | 1
  remind_expire: 0 | 1
  remind_traffic: 0 | 1
  banned: 0 | 1
  created_at: number
  updated_at: number
  plan_name?: string
  alive_ip?: number
  subscribe_url?: string
  devices?: { node: string; ip: string; update_at: number }[]
  share_ips?: number
}

export interface UserRow extends AdminUser {
  subscription_count: number
  alive_ip: number
  primary: Sub | null
}

export interface UserDetail extends AdminUser {
  invite_user: { id: number; email: string } | null
  subscriptions: Sub[]
}

export interface Plan {
  id: number
  group_id: number
  transfer_enable: number
  device_limit: number | null
  name: string
  speed_limit: number | null
  show: 0 | 1
  sort: number | null
  renew: 0 | 1
  content: string | null
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
  created_at: number
  updated_at: number
  count?: number
}

export interface Order {
  id: number
  invite_user_id: number | null
  user_id: number
  plan_id: number
  coupon_id: number | null
  payment_id: number | null
  type: number
  period: string
  trade_no: string
  callback_no: string | null
  total_amount: number
  handling_amount: number | null
  discount_amount: number | null
  surplus_amount: number | null
  refund_amount: number | null
  balance_amount: number | null
  surplus_order_ids: number[] | null
  status: number
  commission_status: number
  commission_balance: number
  actual_commission_balance: number | null
  paid_at: number | null
  subscription_id: number | null
  immediate: number
  created_at: number
  updated_at: number
  plan_name?: string
  email?: string
  subscription_name?: string
}

export interface OrderDetail extends Order {
  commission_log: { id: number; invite_user_id: number; get_amount: number; order_amount: number; created_at: number }[]
  surplus_orders: Order[]
}

export interface Coupon {
  id: number
  code: string
  name: string
  type: 1 | 2
  value: number
  show: 0 | 1
  limit_use: number | null
  limit_use_with_user: number | null
  limit_plan_ids: number[] | null
  limit_period: string[] | null
  started_at: number
  ended_at: number
  created_at: number
}

export interface Giftcard {
  id: number
  code: string
  name: string
  type: 1 | 2 | 3 | 4 | 5
  value: number | null
  plan_id: number | null
  limit_use: number | null
  used_user_ids: number[] | null
  started_at: number
  ended_at: number
  created_at: number
}

export interface ServerGroup {
  id: number
  name: string
  user_count?: number
  server_count?: number
}

export interface ServerRoute {
  id: number
  remarks: string
  match: string[] | string
  action: string
  action_value: string | null
}

export interface ServerRow {
  id: number
  type: string
  name: string
  group_id: number[]
  route_id: number[] | null
  parent_id: number | null
  host: string
  port: string
  server_port: number
  tags: string[] | null
  rate: string
  show: 0 | 1
  sort: number | null
  online: number | null
  last_check_at: number | null
  last_push_at: number | null
  available_status: 0 | 1 | 2
  install_command?: string
  [k: string]: unknown
}

export interface Payment {
  id: number
  uuid: string
  payment: string
  name: string
  icon: string | null
  config: Record<string, unknown> | null
  notify_domain: string | null
  handling_fee_fixed: number | null
  handling_fee_percent: number | null
  enable: 0 | 1
  sort: number | null
  notify_url: string
}

export interface Notice {
  id: number
  title: string
  content: string
  show: 0 | 1
  img_url: string | null
  tags: string[] | null
  created_at: number
}

export interface ArticleBrief {
  id: number
  language: string
  category: string
  title: string
  sort: number | null
  show: 0 | 1
  updated_at: number
}

export interface Ticket {
  id: number
  user_id: number
  subject: string
  level: number
  status: number
  reply_status: number
  subscription_id: number | null
  created_at: number
  updated_at: number
  email: string
  subscription_name: string | null
  plan_name: string | null
  subscription_expired_at: number | null
  subscription_used: number | null
  subscription_total: number | null
  subscription_inferred: boolean
  inferred_subscription_id: number | null
}

export interface TicketMessage {
  id: number
  user_id: number
  message: string
  is_me: boolean
  created_at: number
}

export interface PluginField {
  key: string
  label: string
  type: 'string' | 'text' | 'password' | 'number' | 'bool' | 'select'
  default: unknown
  options?: { value: string; label: string }[]
  required: boolean
  help?: string
}

export interface PluginView {
  code: string
  name: string
  version: string
  author: string
  description: string
  url: string
  type: 'go' | 'js'
  status: 'ok' | 'error' | 'missing'
  error?: string
  enabled: boolean
  installed_at: number
  fields: PluginField[]
  config: Record<string, unknown>
  features: {
    events: string[]
    filters: string[]
    routes: { method: string; path: string; scope: string }[]
    jobs: string[]
    payments: string[]
    formats: string[]
    widgets?: string[]
  }
  package?: { sha256: string; size: number; http: string[]; permissions: { key: string; description: string }[] }
}

export interface PluginPreview {
  code: string
  name: string
  version: string
  author: string
  description: string
  url: string
  sha256: string
  size: number
  http: string[]
  permissions: { key: string; description: string }[]
  fields: PluginField[]
  installed_version: string
}

export interface ThemeField {
  label: string
  placeholder?: string
  field_name: string
  field_type: 'input' | 'select' | 'textarea'
  select_options?: Record<string, string>
  default_value?: unknown
}

export interface ThemeView {
  id: string
  name: string
  description: string
  version: string
  images: string
  configs: ThemeField[]
  builtin: boolean
}

export interface SubscribeRule {
  id: number
  name: string
  target: string
  header: string
  match_type: string
  pattern: string | null
  negate: 0 | 1
  action: string
  priority: number
  enabled: 0 | 1
  hit_count: number
  last_hit_at: number | null
}

export interface SubscribeLog {
  id: number
  subscription_id: number
  user_id: number
  ip: string
  user_agent: string
  client: string
  headers: string | null
  result: number
  rule_id: number | null
  created_at: number
  token: string | null
  path: string | null
  email: string
  subscription_name: string
}
