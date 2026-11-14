import { reactive, ref, type Ref } from 'vue'
import { errText, message } from './ui'

export interface Pager {
  page: number
  pageSize: number
  total: number
}

/** 远程分页表格：fetcher 返回 {rows, total}；切换页码 / 每页条数调用 setPage / setPageSize。 */
export function usePaged<T>(fetcher: (current: number, pageSize: number) => Promise<{ rows: T[]; total: number }>, pageSize = 20) {
  const rows = ref([]) as Ref<T[]>
  const loading = ref(false)
  const pager = reactive<Pager>({ page: 1, pageSize, total: 0 })

  async function load() {
    loading.value = true
    try {
      const r = await fetcher(pager.page, pager.pageSize)
      rows.value = r.rows
      pager.total = r.total
    } catch (e) {
      message.error(errText(e))
    } finally {
      loading.value = false
    }
  }

  function reload() {
    pager.page = 1
    return load()
  }

  function setPage(p: number) {
    pager.page = p
    return load()
  }

  function setPageSize(s: number) {
    pager.pageSize = s
    pager.page = 1
    return load()
  }

  return { rows, loading, pager, load, reload, setPage, setPageSize }
}

/** 一次性加载的列表 */
export function useList<T>(fetcher: () => Promise<T[]>) {
  const rows = ref([]) as Ref<T[]>
  const loading = ref(false)
  async function load() {
    loading.value = true
    try {
      rows.value = (await fetcher()) || []
    } catch (e) {
      message.error(errText(e))
    } finally {
      loading.value = false
    }
  }
  return { rows, loading, load }
}
