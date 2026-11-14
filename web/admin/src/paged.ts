import { reactive, ref, type Ref } from 'vue'
import type { PaginationProps } from 'naive-ui'
import { errText, message } from './ui'

/** 远程分页表格：fetcher 返回 {rows, total}，页码 / 每页条数由分页组件驱动。 */
export function usePaged<T>(fetcher: (current: number, pageSize: number) => Promise<{ rows: T[]; total: number }>) {
  const rows = ref([]) as Ref<T[]>
  const loading = ref(false)
  const pagination = reactive<PaginationProps>({
    page: 1,
    pageSize: 20,
    itemCount: 0,
    showSizePicker: true,
    pageSizes: [10, 20, 50, 100],
    prefix: ({ itemCount }) => `共 ${itemCount} 条`,
    onUpdatePage: (p: number) => {
      pagination.page = p
      load()
    },
    onUpdatePageSize: (s: number) => {
      pagination.pageSize = s
      pagination.page = 1
      load()
    },
  })

  async function load() {
    loading.value = true
    try {
      const r = await fetcher(pagination.page || 1, pagination.pageSize || 20)
      rows.value = r.rows
      pagination.itemCount = r.total
    } catch (e) {
      message.error(errText(e))
    } finally {
      loading.value = false
    }
  }

  function reload() {
    pagination.page = 1
    return load()
  }

  return { rows, loading, pagination, load, reload }
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
