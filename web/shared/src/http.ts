// 原生 API 请求封装：统一响应 {code, msg, data}，code 为 0 表示成功。

export class ApiError extends Error {
  status: number
  code: number
  constructor(status: number, code: number, msg: string) {
    super(msg)
    this.status = status
    this.code = code
  }
}

export interface Page<T> {
  total: number
  list: T[]
}

export interface HttpOptions {
  base: () => string
  token: () => string
  locale: () => string
  /** 认证失败（401）时调用 */
  onUnauthorized?: () => void
  /** 非 JSON 的 404（统一 404 页面）也视为认证失败：管理端对未登录 / 非管理员隐藏接口 */
  hiddenAsUnauthorized?: boolean
  /** 网络错误、服务器错误等非业务错误的提示文案 */
  networkError: () => string
}

type Query = Record<string, string | number | boolean | undefined | null>

function buildQuery(q?: Query): string {
  if (!q) return ''
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) {
    if (v !== undefined && v !== null && v !== '') p.append(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

export function createHttp(opt: HttpOptions) {
  async function raw(method: string, path: string, body?: unknown, query?: Query): Promise<Response> {
    const headers: Record<string, string> = { 'Content-Language': opt.locale() }
    const tok = opt.token()
    if (tok) headers.Authorization = `Bearer ${tok}`
    let payload: BodyInit | undefined
    if (body instanceof FormData) {
      payload = body
    } else if (body !== undefined) {
      headers['Content-Type'] = 'application/json'
      payload = JSON.stringify(body)
    }
    let res: Response
    try {
      res = await fetch(opt.base() + path + buildQuery(query), { method, headers, body: payload })
    } catch {
      throw new ApiError(0, -1, opt.networkError())
    }
    return res
  }

  async function request<T>(method: string, path: string, body?: unknown, query?: Query): Promise<T> {
    const res = await raw(method, path, body, query)
    let json: { code?: number; msg?: string; data?: T } | undefined
    try {
      json = await res.json()
    } catch {
      json = undefined
    }
    if (res.status === 401 || (opt.hiddenAsUnauthorized && res.status === 404 && !json)) opt.onUnauthorized?.()
    if (!json || typeof json.code !== 'number') {
      throw new ApiError(res.status, res.status || -1, opt.networkError())
    }
    if (json.code !== 0) {
      throw new ApiError(res.status, json.code, json.msg || opt.networkError())
    }
    return json.data as T
  }

  /** 下载文件（如导出 CSV），失败时按普通响应解析错误。 */
  async function download(method: string, path: string, body: unknown, filename: string): Promise<void> {
    const res = await raw(method, path, body)
    const type = res.headers.get('Content-Type') || ''
    if (!res.ok || type.includes('application/json')) {
      let msg = opt.networkError()
      let code = res.status
      try {
        const j = await res.json()
        msg = j.msg || msg
        code = j.code ?? code
      } catch {
        /* 忽略 */
      }
      if (res.status === 401 || (opt.hiddenAsUnauthorized && res.status === 404 && !type.includes('application/json'))) {
        opt.onUnauthorized?.()
      }
      throw new ApiError(res.status, code, msg)
    }
    const blob = await res.blob()
    const disposition = res.headers.get('Content-Disposition') || ''
    const m = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(disposition)
    saveBlob(blob, m ? decodeURIComponent(m[1]) : filename)
  }

  return {
    get: <T>(path: string, query?: Query) => request<T>('GET', path, undefined, query),
    post: <T>(path: string, body?: unknown, query?: Query) => request<T>('POST', path, body ?? {}, query),
    put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body ?? {}),
    patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body ?? {}),
    del: <T>(path: string, body?: unknown) => request<T>('DELETE', path, body),
    upload: <T>(path: string, form: FormData) => request<T>('POST', path, form),
    download,
  }
}

export type Http = ReturnType<typeof createHttp>

export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
