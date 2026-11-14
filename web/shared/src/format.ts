// 显示格式：金额以“分”存储，流量以字节存储，时间为 Unix 秒。

const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function formatBytes(n: number | null | undefined, digits = 2): string {
  let v = Number(n) || 0
  const neg = v < 0
  v = Math.abs(v)
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${neg ? '-' : ''}${i === 0 ? v : v.toFixed(digits)} ${units[i]}`
}

export const GB = 1024 * 1024 * 1024

export function bytesToGB(n: number | null | undefined): number {
  return Math.round(((Number(n) || 0) / GB) * 100) / 100
}

export function gbToBytes(gb: number | null | undefined): number {
  return Math.round((Number(gb) || 0) * GB)
}

/** 分 → 元（两位小数字符串） */
export function formatMoney(cents: number | null | undefined): string {
  return ((Number(cents) || 0) / 100).toFixed(2)
}

export function yuanToCents(yuan: number | null | undefined): number {
  return Math.round((Number(yuan) || 0) * 100)
}

export function centsToYuan(cents: number | null | undefined): number | null {
  if (cents === null || cents === undefined) return null
  return Math.round(Number(cents)) / 100
}

function pad(n: number) {
  return n < 10 ? `0${n}` : String(n)
}

/** Unix 秒 → YYYY-MM-DD HH:mm（time=false 时只到日期） */
export function formatTime(ts: number | null | undefined, time = true): string {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const date = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  return time ? `${date} ${pad(d.getHours())}:${pad(d.getMinutes())}` : date
}

export function nowUnix(): number {
  return Math.floor(Date.now() / 1000)
}

/** 剩余天数（向上取整，已过期为 0） */
export function daysLeft(ts: number | null | undefined): number | null {
  if (!ts) return null
  return Math.max(0, Math.ceil((ts - nowUnix()) / 86400))
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  }
}
