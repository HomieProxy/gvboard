<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NCard, NForm, NFormItem, NInput, NButton } from 'naive-ui'
import { cfg, http, admin, setToken } from '../api'
import { message, errText } from '../ui'

const route = useRoute()
const router = useRouter()
const form = reactive({ email: '', password: '' })
const loading = ref(false)

async function submit() {
  if (!form.email || !form.password) {
    message.warning('请输入邮箱和密码')
    return
  }
  loading.value = true
  try {
    const res = await http.post<{ token: string; is_admin: boolean }>('/auth/login', form)
    if (!res.is_admin) {
      message.error('该账号不是管理员')
      return
    }
    setToken(res.token)
    // 确认管理端路径与登录状态有效
    await admin.get('/system/status')
    const to = typeof route.query.redirect === 'string' ? route.query.redirect : '/dashboard'
    router.replace(to)
  } catch (e) {
    message.error(errText(e))
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="wrap">
    <NCard :title="cfg.site.name || '管理后台'" style="width: 360px">
      <NForm @submit.prevent="submit">
        <NFormItem label="邮箱">
          <NInput v-model:value="form.email" placeholder="管理员邮箱" autocomplete="username" />
        </NFormItem>
        <NFormItem label="密码">
          <NInput
            v-model:value="form.password"
            type="password"
            show-password-on="click"
            placeholder="密码"
            autocomplete="current-password"
            @keyup.enter="submit"
          />
        </NFormItem>
        <NButton type="primary" block :loading="loading" attr-type="submit">登录</NButton>
      </NForm>
    </NCard>
  </div>
</template>

<style scoped>
.wrap { min-height: 100%; display: flex; align-items: center; justify-content: center; padding: 16px; box-sizing: border-box; }
</style>
