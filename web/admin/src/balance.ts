export interface BalanceLogRow {
  id: number
  user_id: number
  email: string
  amount: number
  balance: number
  type: string
  trade_no: string
  operator_id: number | null
  operator_email: string
  plugin: string
  remark: string
  created_at: number
}

export interface BalanceLogPage {
  data: BalanceLogRow[]
  total: number
  income: number
  expense: number
}

export const balanceTypeNames: Record<string, string> = {
  order_pay: '下单抵扣',
  order_cancel: '取消订单退回',
  order_restore: '补单扣回',
  renewal: '自动续费',
  deposit: '充值到账',
  plan_refund: '换套餐差额退回',
  giftcard: '礼品卡',
  commission: '邀请佣金',
  transfer: '佣金转余额',
  admin: '管理员修改',
  plugin: '插件',
}

export const balanceTypeOptions = [{ label: '全部类型', value: '' }, ...Object.entries(balanceTypeNames).map(([value, label]) => ({ label, value }))]

export function balanceNote(r: BalanceLogRow) {
  const parts: string[] = []
  if (r.operator_id) parts.push(`管理员 ${r.operator_email || `#${r.operator_id}`}`)
  if (r.plugin) parts.push(`插件 ${r.plugin}`)
  if (r.remark) parts.push(r.remark)
  return parts.join(' · ') || '-'
}
