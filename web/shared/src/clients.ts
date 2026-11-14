// 客户端下载（决策 D67）：平台与预置图标，管理端与用户端共用。

export type ClientPlatform = 'windows' | 'macos' | 'ios' | 'android' | 'linux' | 'harmony'

export interface ClientDownload {
  platform: ClientPlatform
  name: string
  url: string
  /** 为空时使用平台预置图标 */
  icon: string
}

const apple =
  'M16.37 12.6c-.02-2.2 1.8-3.26 1.88-3.31-1.02-1.5-2.62-1.7-3.19-1.72-1.36-.14-2.65.8-3.34.8-.69 0-1.75-.78-2.88-.76-1.48.02-2.85.86-3.61 2.19-1.54 2.67-.39 6.62 1.11 8.79.73 1.06 1.6 2.25 2.75 2.21 1.1-.04 1.52-.71 2.85-.71 1.33 0 1.71.71 2.88.69 1.19-.02 1.94-1.08 2.67-2.15.84-1.23 1.19-2.42 1.21-2.48-.03-.01-2.32-.89-2.33-3.55zM14.18 6.13c.61-.74 1.02-1.76.91-2.78-.88.04-1.94.59-2.57 1.32-.56.65-1.06 1.69-.93 2.69.98.08 1.98-.5 2.59-1.23z'

const svgs: Record<ClientPlatform, string> = {
  windows:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#0078d4"><path d="M3 5.1 10.4 4v7.2H3zm8.3-1.2L21 2.5v8.7h-9.7zM3 12.8h7.4V20L3 18.9zm8.3 0H21v8.7l-9.7-1.4z"/></svg>',
  macos: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#6b7280"><path d="${apple}"/></svg>`,
  ios:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="20" rx="5" fill="#1f2937"/>' +
    `<path transform="translate(2.4 2.6) scale(.8)" fill="#fff" d="${apple}"/></svg>`,
  android:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#3ddc84"><path d="M6 18c0 .55.45 1 1 1h1v3.5a1.5 1.5 0 0 0 3 0V19h2v3.5a1.5 1.5 0 0 0 3 0V19h1c.55 0 1-.45 1-1V8H6zM3.5 8A1.5 1.5 0 0 0 2 9.5v7a1.5 1.5 0 0 0 3 0v-7A1.5 1.5 0 0 0 3.5 8m17 0A1.5 1.5 0 0 0 19 9.5v7a1.5 1.5 0 0 0 3 0v-7A1.5 1.5 0 0 0 20.5 8m-4.97-5.84 1.3-1.3a.5.5 0 0 0-.7-.7l-1.48 1.48A6 6 0 0 0 12 1c-.96 0-1.86.23-2.66.63L7.85.15a.5.5 0 1 0-.7.7l1.31 1.31A6 6 0 0 0 6 7h12a6 6 0 0 0-2.47-4.84M10 5H9V4h1zm5 0h-1V4h1z"/></svg>',
  linux:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><ellipse cx="12" cy="12.5" rx="7" ry="9.3" fill="#1f2937"/>' +
    '<ellipse cx="12" cy="15" rx="4.6" ry="6.2" fill="#fff"/><circle cx="10" cy="7.3" r="1.4" fill="#fff"/><circle cx="14" cy="7.3" r="1.4" fill="#fff"/>' +
    '<circle cx="10.3" cy="7.5" r=".6" fill="#1f2937"/><circle cx="13.7" cy="7.5" r=".6" fill="#1f2937"/><path d="M10.2 9.5h3.6L12 11.3z" fill="#f5b70a"/>' +
    '<ellipse cx="8" cy="21.6" rx="3.2" ry="1.4" fill="#f5b70a"/><ellipse cx="16" cy="21.6" rx="3.2" ry="1.4" fill="#f5b70a"/></svg>',
  harmony:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="20" rx="5" fill="#cf0a2c"/>' +
    '<circle cx="12" cy="12" r="5.6" fill="none" stroke="#fff" stroke-width="1.8"/><circle cx="12" cy="12" r="1.6" fill="#fff"/></svg>',
}

export const clientPlatforms: { value: ClientPlatform; label: string }[] = [
  { value: 'windows', label: 'Windows' },
  { value: 'macos', label: 'macOS' },
  { value: 'ios', label: 'iOS' },
  { value: 'android', label: 'Android' },
  { value: 'linux', label: 'Linux' },
  { value: 'harmony', label: 'HarmonyOS' },
]

/** 平台预置图标（data URI） */
export function platformIcon(p: string): string {
  const svg = svgs[p as ClientPlatform] || svgs.windows
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`
}

/** 客户端图标：未设置自定义图标时使用平台预置图标 */
export function clientIcon(c: Pick<ClientDownload, 'platform' | 'icon'>): string {
  return c.icon || platformIcon(c.platform)
}

/** 解析设置 client_downloads，格式错误时返回空列表 */
export function parseClients(s: unknown): ClientDownload[] {
  if (typeof s !== 'string' || !s.trim()) return []
  try {
    const v = JSON.parse(s)
    return Array.isArray(v) ? v : []
  } catch {
    return []
  }
}
