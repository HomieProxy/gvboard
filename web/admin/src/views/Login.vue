<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Loader2Icon } from '@lucide/vue'
import { Button } from '@gv/shared/ui/button'
import { Input } from '@gv/shared/ui/input'
import { Label } from '@gv/shared/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@gv/shared/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@gv/shared/ui/dialog'
import { cfg, http, admin, setToken } from '../api'
import { message, errText } from '../ui'

const route = useRoute()
const router = useRouter()
const form = reactive({ email: '', password: '' })
const loading = ref(false)
const forgot = ref(false)

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
  <div class="flex min-h-svh items-center justify-center bg-muted/40 p-4">
    <Card class="w-full max-w-sm">
      <CardHeader class="text-center">
        <img v-if="cfg.site.logo" :src="cfg.site.logo" alt="" class="mx-auto mb-2 h-10 object-contain" />
        <CardTitle class="text-xl">{{ cfg.site.name || '管理中心' }}</CardTitle>
        <CardDescription>登录到管理中心</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="grid gap-4" @submit.prevent="submit">
          <div class="grid gap-2">
            <Label for="email">邮箱</Label>
            <Input id="email" v-model="form.email" type="email" autocomplete="username" />
          </div>
          <div class="grid gap-2">
            <div class="flex items-center">
              <Label for="password">密码</Label>
              <button type="button" class="ml-auto text-sm text-muted-foreground hover:text-primary" @click="forgot = true">忘记密码</button>
            </div>
            <Input id="password" v-model="form.password" type="password" autocomplete="current-password" />
          </div>
          <Button type="submit" class="w-full" :disabled="loading">
            <Loader2Icon v-if="loading" class="animate-spin" />
            登入
          </Button>
        </form>
      </CardContent>
    </Card>

    <Dialog v-model:open="forgot">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>忘记密码</DialogTitle>
          <DialogDescription>在服务器上执行命令找回密码</DialogDescription>
        </DialogHeader>
        <pre class="rounded-md bg-muted px-3 py-2 font-mono text-sm">gvboard reset-password 管理员邮箱</pre>
        <DialogFooter><Button @click="forgot = false">我知道了</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
