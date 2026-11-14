import { centsToYuan, yuanToCents } from '@gv/shared'

/** 状态徽标的语义色 */
export type Tone = 'default' | 'primary' | 'success' | 'warning' | 'danger' | 'info'

// ---------- 枚举（文字与原版管理端一致） ----------

export const periodLabels: Record<string, string> = {
  month_price: '月付',
  quarter_price: '季付',
  half_year_price: '半年付',
  year_price: '年付',
  two_year_price: '两年付',
  three_year_price: '三年付',
  onetime_price: '一次性',
  reset_price: '流量重置包',
  deposit: '充值',
}
export const planPeriods = Object.keys(periodLabels).filter((k) => k !== 'deposit')
export const periodOptions = planPeriods.map((k) => ({ label: periodLabels[k], value: k }))

export const orderStatus: Record<number, [string, Tone]> = {
  0: ['待支付', 'warning'],
  1: ['开通中', 'info'],
  2: ['已取消', 'default'],
  3: ['已完成', 'success'],
  4: ['已折抵', 'default'],
}
export const orderType: Record<number, string> = { 1: '新购', 2: '续费', 3: '变更', 4: '流量包', 9: '充值' }
export const commissionStatus: Record<number, [string, Tone]> = {
  0: ['待确认', 'warning'],
  1: ['发放中', 'info'],
  2: ['有效', 'success'],
  3: ['无效', 'default'],
}

/** 流量重置方式（系统配置；套餐中额外有“跟随系统设置”） */
export const resetMethodOptions = [
  { label: '每月1号', value: 0 },
  { label: '按月重置', value: 1 },
  { label: '不重置', value: 2 },
  { label: '每年1月1日', value: 3 },
  { label: '按年重置', value: 4 },
]
export const resetMethods = [{ label: '跟随系统设置', value: -1 }, ...resetMethodOptions]

export const giftcardTypes: Record<number, string> = { 1: '金额', 2: '时长', 3: '流量', 4: '重置', 5: '套餐' }
export const giftcardTypeOptions = [
  { label: '增加账户余额', value: 1 },
  { label: '增加订阅时长', value: 2 },
  { label: '增加套餐流量', value: 3 },
  { label: '重置套餐流量', value: 4 },
  { label: '兑换订阅套餐', value: 5 },
]

// ---------- 单位换算（表单用元 / 毫秒，接口用分 / 秒） ----------

/** 返回把 from 位置的元素移动到 to 位置后的新数组 */
export function moveItem<T>(list: T[], from: number, to: number): T[] {
  const out = [...list]
  const [item] = out.splice(from, 1)
  out.splice(to, 0, item)
  return out
}

export function toYuan(cents: number | null | undefined): number | null {
  return centsToYuan(cents ?? null)
}
export function toCents(yuan: number | null | undefined): number | null {
  return yuan === null || yuan === undefined ? null : yuanToCents(yuan)
}
export function toMs(sec: number | null | undefined): number | null {
  return sec ? sec * 1000 : null
}
export function toSec(ms: number | null | undefined): number | null {
  return ms ? Math.floor(ms / 1000) : null
}

/** 去掉值为 undefined 的键 */
export function compact<T extends Record<string, unknown>>(o: T): Partial<T> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(o)) if (v !== undefined) out[k] = v
  return out as Partial<T>
}

export interface PageResult<T> {
  data: T[]
  total: number
}

export interface Filter {
  key: string
  condition: string
  value: string
}

export const conditions = ['=', '!=', '>', '<', '>=', '<=', '模糊'].map((c) => ({ label: c, value: c }))
