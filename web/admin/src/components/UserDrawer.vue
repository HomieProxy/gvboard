<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import {
  NDrawer, NDrawerContent, NTabs, NTabPane, NForm, NFormItem, NInput, NInputNumber, NSelect, NSwitch,
  NButton, NSpace, NDataTable, NSpin, NDescriptions, NDescriptionsItem, NTag, NProgress, type DataTableColumns,
} from 'naive-ui'
import { copyText, formatBytes, formatTime, nowUnix } from '@gv/shared'
import { admin } from '../api'
import { confirm, message, run } from '../ui'
import { actions, btn, confirmBtn, tag, toCents, toYuan } from '../helpers'
import type { Sub, UserDetail } from '../types'
import SubForm from './SubForm.vue'

const props = defineProps<{ userId: number | null }>()
const show = defineModel<boolean>('show', { required: true })
const emit = defineEmits<{ changed: [] }>()

const user = ref<UserDetail | null>(null)
const loading = ref(false)
const tab = ref('subs')

interface Account {
  email: string
  password: string
  balance: number | null
  commission_balance: number | null
  commission_type: 0 | 1 | 2
  commission_rate: number | null
  discount: number | null
  subscription_limit: number | null
  banned: boolean
  is_admin: boolean
  is_staff: boolean
  remarks: string
}
const acc = ref<Account | null>(null)
let accOrigin: Account | null = null
const inviter = ref('')

async function load() {
  if (!props.userId) return
  loading.value = true
  try {
    const u = await admin.get<UserDetail>(`/users/${props.userId}`)
    user.value = u
    acc.value = {
      email: u.email,
      password: '',
      balance: toYuan(u.balance),
      commission_balance: toYuan(u.commission_balance),
      commission_type: u.commission_type,
      commission_rate: u.commission_rate,
      discount: u.discount,
      subscription_limit: u.subscription_limit,
      banned: !!u.banned,
      is_admin: !!u.is_admin,
      is_staff: !!u.is_staff,
      remarks: u.remarks || '',
    }
    accOrigin = { ...acc.value }
    inviter.value = u.invite_user?.email || ''
  } catch (e) {
    message.error(String((e as Error).message))
  } finally {
    loading.value = false
  }
}

watch(show, (v) => {
  if (v) {
    user.value = null
    tab.value = 'subs'
    load()
  }
})

async function saveAccount() {
  if (!acc.value || !accOrigin) return
  const a = acc.value
  const o = accOrigin
  const body: Record<string, unknown> = {}
  if (a.email !== o.email) body.email = a.email
  if (a.password) body.password = a.password
  if (a.balance !== o.balance) body.balance = toCents(a.balance) ?? 0
  if (a.commission_balance !== o.commission_balance) body.commission_balance = toCents(a.commission_balance) ?? 0
  if (a.commission_type !== o.commission_type) body.commission_type = a.commission_type
  if (a.commission_rate !== o.commission_rate) body.commission_rate = a.commission_rate
  if (a.discount !== o.discount) body.discount = a.discount
  if (a.subscription_limit !== o.subscription_limit) body.subscription_limit = a.subscription_limit
  for (const k of ['banned', 'is_admin', 'is_staff'] as const) if (a[k] !== o[k]) body[k] = a[k] ? 1 : 0
  if (a.remarks !== o.remarks) body.remarks = a.remarks || null
  if (!Object.keys(body).length) {
    message.info('没有修改')
    return
  }
  if (await run(() => admin.put(`/users/${props.userId}`, body), '已保存')) {
    await load()
    emit('changed')
  }
}

async function saveInviter() {
  if (await run(() => admin.post(`/users/${props.userId}/inviter`, { invite_user_email: inviter.value.trim() }), '已保存')) {
    await load()
  }
}

function unbindTelegram() {
  confirm('确定解除该用户的 Telegram 绑定？', () => admin.put(`/users/${props.userId}`, { telegram_id: null })).then((ok) => {
    if (ok) load()
  })
}

function removeUser() {
  confirm(`确定删除用户 ${user.value?.email}？其订阅、订单、工单等将一并删除，且不可恢复。`, () => admin.del(`/users/${props.userId}`), '已删除').then(
    (ok) => {
      if (ok) {
        show.value = false
        emit('changed')
      }
    },
  )
}

// ---------- 订阅 ----------

const subForm = ref(false)
const editing = ref<Sub | null>(null)
function openSub(s: Sub | null) {
  editing.value = s
  subForm.value = true
}
async function onSubSaved() {
  await load()
  emit('changed')
}
async function subAction(path: string, ok: string) {
  if (await run(() => admin.post(path), ok)) onSubSaved()
}
async function removeSub(s: Sub) {
  if (await run(() => admin.del(`/subscriptions/${s.id}`), '已删除')) onSubSaved()
}

function usage(s: Sub) {
  const used = s.u + s.d
  const pct = s.transfer_enable ? Math.min(100, Math.round((used / s.transfer_enable) * 100)) : 0
  return h('div', { style: 'min-width:140px' }, [
    h('div', null, `${formatBytes(used)} / ${formatBytes(s.transfer_enable)}`),
    h(NProgress, { type: 'line', percentage: pct, showIndicator: false, height: 4, status: pct >= 90 ? 'error' : 'default' }),
  ])
}

function expire(s: Sub) {
  if (s.expired_at === null) return tag('长期有效', 'success')
  return s.expired_at > nowUnix() ? formatTime(s.expired_at) : tag(`已到期 ${formatTime(s.expired_at, false)}`, 'error')
}

const subCols: DataTableColumns<Sub> = [
  { type: 'expand', renderExpand: (s) => renderDevices(s) },
  {
    title: '订阅',
    key: 'id',
    render: (s) =>
      h(NSpace, { size: 4, vertical: true }, () => [
        h('span', null, [`#${s.id} ${s.name || ''}`]),
        h(NSpace, { size: 4 }, () => [s.is_primary ? tag('主订阅', 'info') : null, s.banned ? tag('已封禁', 'error') : null]),
      ]),
  },
  { title: '套餐', key: 'plan_name', render: (s) => s.plan_name || '-' },
  { title: '流量', key: 'transfer_enable', render: usage },
  { title: '到期', key: 'expired_at', render: expire },
  { title: '设备', key: 'alive_ip', render: (s) => `${s.alive_ip ?? 0} / ${s.device_limit ?? '∞'}` },
  { title: '近期 IP', key: 'share_ips', render: (s) => s.share_ips ?? 0 },
  {
    title: '操作',
    key: 'op',
    render: (s) =>
      actions(
        btn('编辑', () => openSub(s)),
        btn('复制链接', async () => message[(await copyText(s.subscribe_url || '')) ? 'success' : 'error']('已复制')),
        !s.is_primary ? btn('设为主订阅', () => subAction(`/subscriptions/${s.id}/primary`, '已设为主订阅'), 'info') : null,
        confirmBtn('重置链接', '重置后旧的订阅链接与 UUID 立即失效，确定？', () => subAction(`/subscriptions/${s.id}/reset-security`, '已重置'), 'warning'),
        !s.is_primary ? confirmBtn('删除', '确定删除该订阅？', () => removeSub(s)) : null,
      ),
  },
]

function renderDevices(s: Sub) {
  return h(NSpace, { vertical: true, size: 4 }, () => [
    h('div', { class: 'mono muted' }, `UUID ${s.uuid}　Token ${s.token}`),
    h('div', { class: 'mono muted', style: 'word-break:break-all' }, s.subscribe_url || ''),
    s.devices?.length
      ? h(
          NSpace,
          { size: 6 },
          () => s.devices!.map((d) => h(NTag, { size: 'small' }, () => `${d.ip}（${d.node}，${formatTime(d.update_at)}）`)),
        )
      : h('span', { class: 'muted' }, '当前没有在线设备'),
  ])
}

const commissionTypes = [
  { label: '跟随系统设置', value: 0 },
  { label: '循环返利', value: 1 },
  { label: '首次返利', value: 2 },
]
const title = computed(() => (user.value ? `${user.value.email}（#${user.value.id}）` : '用户详情'))
</script>

<template>
  <NDrawer v-model:show="show" :width="920" style="max-width: 100vw" placement="right">
    <NDrawerContent :title="title" closable>
      <NSpin :show="loading">
        <template v-if="user && acc">
          <NDescriptions size="small" :column="3" label-placement="left" bordered style="margin-bottom: 12px">
            <NDescriptionsItem label="注册时间">{{ formatTime(user.created_at) }}</NDescriptionsItem>
            <NDescriptionsItem label="最后登录">{{ formatTime(user.last_login_at) }}</NDescriptionsItem>
            <NDescriptionsItem label="邀请人">{{ user.invite_user ? `${user.invite_user.email} (#${user.invite_user.id})` : '-' }}</NDescriptionsItem>
          </NDescriptions>
          <NTabs v-model:value="tab" type="line">
            <NTabPane name="subs" :tab="`订阅（${user.subscriptions.length}）`">
              <div class="toolbar">
                <NButton size="small" type="primary" @click="openSub(null)">新开订阅</NButton>
                <span class="muted">管理员新开订阅不受用户订阅数量上限限制</span>
              </div>
              <NDataTable size="small" :columns="subCols" :data="user.subscriptions" :row-key="(s: Sub) => s.id" />
            </NTabPane>
            <NTabPane name="account" tab="账号">
              <NForm label-placement="left" label-width="110" style="max-width: 560px">
                <NFormItem label="邮箱"><NInput v-model:value="acc.email" /></NFormItem>
                <NFormItem label="新密码"><NInput v-model:value="acc.password" type="password" show-password-on="click" placeholder="留空不修改" /></NFormItem>
                <NFormItem label="余额（元）"><NInputNumber v-model:value="acc.balance" :precision="2" style="width: 100%" /></NFormItem>
                <NFormItem label="佣金余额（元）"><NInputNumber v-model:value="acc.commission_balance" :precision="2" style="width: 100%" /></NFormItem>
                <NFormItem label="佣金类型"><NSelect v-model:value="acc.commission_type" :options="commissionTypes" /></NFormItem>
                <NFormItem label="专属佣金比例 %"><NInputNumber v-model:value="acc.commission_rate" :min="0" :max="100" clearable placeholder="使用系统设置" style="width: 100%" /></NFormItem>
                <NFormItem label="专属折扣 %"><NInputNumber v-model:value="acc.discount" :min="0" :max="100" clearable placeholder="无" style="width: 100%" /></NFormItem>
                <NFormItem label="订阅数量上限"><NInputNumber v-model:value="acc.subscription_limit" :min="0" :max="1000" clearable placeholder="使用系统设置" style="width: 100%" /></NFormItem>
                <NFormItem label="状态">
                  <NSpace>
                    <span>封禁 <NSwitch v-model:value="acc.banned" size="small" /></span>
                    <span>管理员 <NSwitch v-model:value="acc.is_admin" size="small" /></span>
                    <span>客服 <NSwitch v-model:value="acc.is_staff" size="small" /></span>
                  </NSpace>
                </NFormItem>
                <NFormItem label="备注"><NInput v-model:value="acc.remarks" type="textarea" :autosize="{ minRows: 2, maxRows: 6 }" /></NFormItem>
                <NFormItem label=" ">
                  <NButton type="primary" @click="saveAccount">保存账号</NButton>
                </NFormItem>
                <NFormItem label="邀请人邮箱">
                  <NSpace>
                    <NInput v-model:value="inviter" placeholder="留空表示清除" style="width: 260px" />
                    <NButton @click="saveInviter">设置邀请人</NButton>
                  </NSpace>
                </NFormItem>
                <NFormItem label="Telegram">
                  <NSpace align="center">
                    <span>{{ user.telegram_id ? `已绑定 ${user.telegram_id}` : '未绑定' }}</span>
                    <NButton v-if="user.telegram_id" size="small" @click="unbindTelegram">解除绑定</NButton>
                  </NSpace>
                </NFormItem>
              </NForm>
            </NTabPane>
          </NTabs>
        </template>
      </NSpin>
      <template #footer>
        <NButton type="error" secondary :disabled="!user" @click="removeUser">删除用户</NButton>
      </template>
    </NDrawerContent>
  </NDrawer>
  <SubForm v-if="userId" v-model:show="subForm" :user-id="userId" :sub="editing" @saved="onSubSaved" />
</template>
