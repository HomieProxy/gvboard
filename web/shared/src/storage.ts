// 本地存储，键名带前缀以区分用户端与管理端。

export function createStore(prefix: string) {
  const key = (k: string) => `${prefix}.${k}`
  return {
    get(k: string): string {
      try {
        return localStorage.getItem(key(k)) || ''
      } catch {
        return ''
      }
    },
    set(k: string, v: string) {
      try {
        if (v) localStorage.setItem(key(k), v)
        else localStorage.removeItem(key(k))
      } catch {
        /* 隐私模式等情况下忽略 */
      }
    },
  }
}
