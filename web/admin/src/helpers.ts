import { h, type VNodeChild } from 'vue'
import { NButton, NSpace, NTag, NSwitch, NPopconfirm, NTooltip, NEllipsis } from 'naive-ui'
import { centsToYuan, yuanToCents } from '@gv/shared'

type BtnType = 'default' | 'primary' | 'info' | 'success' | 'warning' | 'error'

/** 表格中的文字按钮 */
export function btn(label: string, onClick: () => void, type: BtnType = 'primary'): VNodeChild {
  return h(NButton, { size: 'small', text: true, type, onClick }, { default: () => label })
}

/** 需要二次确认的文字按钮 */
export function confirmBtn(label: string, tip: string, onConfirm: () => void, type: BtnType = 'error'): VNodeChild {
  return h(
    NPopconfirm,
    { onPositiveClick: onConfirm, positiveText: '确定', negativeText: '取消' },
    { trigger: () => h(NButton, { size: 'small', text: true, type }, { default: () => label }), default: () => tip },
  )
}

export function actions(...nodes: VNodeChild[]): VNodeChild {
  return h(NSpace, { size: 'small', wrap: false }, { default: () => nodes.filter(Boolean) })
}

export function tag(text: string, type: BtnType = 'default'): VNodeChild {
  return h(NTag, { size: 'small', type, bordered: false }, { default: () => text })
}

export function enumTag(map: Record<number, [string, BtnType]>, v: number): VNodeChild {
  const e = map[v]
  return e ? tag(e[0], e[1]) : tag(String(v))
}

export function sw(value: boolean, onChange: (v: boolean) => void): VNodeChild {
  return h(NSwitch, { size: 'small', value, onUpdateValue: onChange })
}

export function ellipsis(text: string, width = 200): VNodeChild {
  return h(NEllipsis, { style: { maxWidth: `${width}px` } }, { default: () => text })
}

export function tip(node: VNodeChild, text: string): VNodeChild {
  return h(NTooltip, null, { trigger: () => node, default: () => text })
}

// ---------- 枚举 ----------

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

export const orderStatus: Record<number, [string, BtnType]> = {
  0: ['待支付', 'warning'],
  1: ['开通中', 'info'],
  2: ['已取消', 'default'],
  3: ['已完成', 'success'],
  4: ['已折抵', 'default'],
}
export const orderType: Record<number, string> = { 1: '新购', 2: '续费', 3: '升级', 4: '流量重置包', 9: '充值' }
export const commissionStatus: Record<number, [string, BtnType]> = {
  0: ['待确认', 'warning'],
  1: ['发放中', 'info'],
  2: ['有效', 'success'],
  3: ['无效', 'default'],
}
export const resetMethods = [
  { label: '跟随系统设置', value: -1 },
  { label: '每月 1 日', value: 0 },
  { label: '按到期日每月', value: 1 },
  { label: '不重置', value: 2 },
  { label: '每年 1 月 1 日', value: 3 },
  { label: '按到期日每年', value: 4 },
]
export const giftcardTypes: Record<number, string> = { 1: '余额', 2: '时长', 3: '流量', 4: '重置流量', 5: '套餐' }

// ---------- 单位换算（表单用元 / 毫秒，接口用分 / 秒） ----------

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
