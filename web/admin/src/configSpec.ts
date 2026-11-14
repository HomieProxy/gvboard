// 系统配置表单定义，键名与后端设置项一致（见 docs/接口参考/管理端-系统.md 1.2）。

export type ConfigKind = 'text' | 'textarea' | 'password' | 'int' | 'float' | 'switch' | 'select' | 'tags'

export interface ConfigField {
  key: string
  label: string
  kind: ConfigKind
  help?: string
  options?: { label: string; value: string | number }[]
  placeholder?: string
}

export interface ConfigGroup {
  name: string
  title: string
  fields: ConfigField[]
}

const sel = (...pairs: [string | number, string][]) => pairs.map(([value, label]) => ({ value, label }))
const orderEvent = sel([0, '不执行任何动作'], [1, '重置用户流量'])

export const configGroups: ConfigGroup[] = [
  {
    name: 'site',
    title: '站点',
    fields: [
      { key: 'app_name', label: '站点名称', kind: 'text' },
      { key: 'app_description', label: '站点描述', kind: 'text' },
      { key: 'app_url', label: '站点网址', kind: 'text', help: '邮件、支付回调等处使用，末尾不要带 /', placeholder: 'https://example.com' },
      { key: 'logo', label: 'LOGO 地址', kind: 'text', placeholder: 'https://' },
      { key: 'force_https', label: '强制 HTTPS', kind: 'switch', help: '站点未使用 HTTPS 但 CDN / 反向代理开启了强制 HTTPS 时开启' },
      { key: 'stop_register', label: '停止新用户注册', kind: 'switch' },
      { key: 'subscribe_url', label: '订阅 URL', kind: 'text', help: '留空使用站点网址；多个用英文逗号分隔，下发时随机取一个' },
      { key: 'subscribe_path', label: '订阅路径', kind: 'text', help: '形如 /a/b，每段只能包含字母、数字、_、-' },
      { key: 'try_out_plan_id', label: '注册试用套餐 ID', kind: 'int', help: '0 为关闭试用' },
      { key: 'try_out_hour', label: '试用时长（小时）', kind: 'float' },
      { key: 'tos_url', label: '用户条款地址', kind: 'text', placeholder: 'https://' },
      { key: 'currency', label: '货币单位', kind: 'text', help: '仅用于展示，如 CNY' },
      { key: 'currency_symbol', label: '货币符号', kind: 'text', help: '仅用于展示，如 ¥' },
    ],
  },
  {
    name: 'safe',
    title: '安全',
    fields: [
      { key: 'secure_path', label: '管理端路径', kind: 'text', help: '至少 16 位，只能包含字母、数字、_、-。修改后管理端地址立即变化，保存后会自动跳转到新地址' },
      { key: 'email_verify', label: '注册邮箱验证', kind: 'switch' },
      { key: 'safe_mode_enable', label: '安全模式', kind: 'switch', help: '开启后除站点网址外，其它绑定本站的域名访问都将被拒绝' },
      { key: 'email_whitelist_enable', label: '邮箱后缀白名单', kind: 'switch' },
      { key: 'email_whitelist_suffix', label: '白名单后缀', kind: 'tags' },
      { key: 'email_gmail_limit_enable', label: '禁止 Gmail 多别名', kind: 'switch' },
      { key: 'recaptcha_enable', label: '防机器人（reCAPTCHA）', kind: 'switch' },
      { key: 'recaptcha_site_key', label: 'reCAPTCHA 网站密钥', kind: 'text' },
      { key: 'recaptcha_key', label: 'reCAPTCHA 密钥', kind: 'password' },
      { key: 'register_limit_by_ip_enable', label: 'IP 注册限制', kind: 'switch', help: '注意 CDN / 前置代理可能影响 IP 判断' },
      { key: 'register_limit_count', label: 'IP 注册次数上限', kind: 'int' },
      { key: 'register_limit_expire', label: 'IP 注册惩罚时间（分钟）', kind: 'int' },
      { key: 'password_limit_enable', label: '防密码爆破', kind: 'switch' },
      { key: 'password_limit_count', label: '密码错误次数上限', kind: 'int' },
      { key: 'password_limit_expire', label: '密码错误惩罚时间（分钟）', kind: 'int' },
    ],
  },
  {
    name: 'subscribe',
    title: '订阅',
    fields: [
      { key: 'plan_change_enable', label: '允许用户更换套餐', kind: 'switch' },
      {
        key: 'reset_traffic_method',
        label: '流量重置方式',
        kind: 'select',
        options: sel([0, '每月 1 号'], [1, '按到期日每月'], [2, '不重置'], [3, '每年 1 月 1 日'], [4, '按到期日每年']),
        help: '套餐可单独设置',
      },
      { key: 'surplus_enable', label: '更换套餐时折抵剩余价值', kind: 'switch' },
      { key: 'allow_new_period', label: '允许提前开始新的流量周期', kind: 'switch', help: '流量用尽时扣除订阅时长立即重置流量' },
      { key: 'new_order_event_id', label: '新购完成时', kind: 'select', options: orderEvent },
      { key: 'renew_order_event_id', label: '续费完成时', kind: 'select', options: orderEvent },
      { key: 'change_order_event_id', label: '更换套餐完成时', kind: 'select', options: orderEvent },
      { key: 'show_info_to_server_enable', label: '在订阅中显示订阅信息', kind: 'switch', help: '把剩余流量、到期时间作为节点名输出' },
      { key: 'show_subscribe_method', label: '订阅链接生效模式', kind: 'select', options: sel([0, '永久有效'], [1, '一次性有效'], [2, '限时有效']) },
      { key: 'show_subscribe_expire', label: '限时有效时间（分钟）', kind: 'int' },
    ],
  },
  {
    name: 'invite',
    title: '邀请与佣金',
    fields: [
      { key: 'invite_force', label: '强制邀请注册', kind: 'switch' },
      { key: 'invite_commission', label: '默认佣金比例（%）', kind: 'int' },
      { key: 'invite_gen_limit', label: '用户可创建邀请码数', kind: 'int' },
      { key: 'invite_never_expire', label: '邀请码永不失效', kind: 'switch', help: '关闭时邀请码使用一次即失效' },
      { key: 'commission_first_time_enable', label: '佣金仅首次发放', kind: 'switch' },
      { key: 'commission_auto_check_enable', label: '佣金自动确认', kind: 'switch', help: '订单完成 3 日后自动确认' },
      { key: 'commission_withdraw_limit', label: '最低提现金额（元）', kind: 'float' },
      { key: 'commission_withdraw_method', label: '提现方式', kind: 'tags' },
      { key: 'withdraw_close_enable', label: '关闭提现', kind: 'switch' },
      { key: 'commission_distribution_enable', label: '三级分销', kind: 'switch', help: '三级比例合计不要超过 100%' },
      { key: 'commission_distribution_l1', label: '一级比例（%）', kind: 'float' },
      { key: 'commission_distribution_l2', label: '二级比例（%）', kind: 'float' },
      { key: 'commission_distribution_l3', label: '三级比例（%）', kind: 'float' },
    ],
  },
  {
    name: 'deposit',
    title: '充值',
    fields: [{ key: 'deposit_bounus', label: '充值奖励档位', kind: 'tags', help: '每项格式为“充值金额:奖励金额”（元），如 100:10' }],
  },
  {
    name: 'ticket',
    title: '工单',
    fields: [{ key: 'ticket_status', label: '工单开放策略', kind: 'select', options: sel([0, '完全开放'], [1, '仅限有付费订单的用户'], [2, '禁止新建工单']) }],
  },
  {
    name: 'frontend',
    title: '外观',
    fields: [
      { key: 'frontend_theme_sidebar', label: '管理端边栏', kind: 'select', options: sel(['light', '亮色'], ['dark', '暗色']) },
      { key: 'frontend_theme_header', label: '管理端顶栏', kind: 'select', options: sel(['light', '亮色'], ['dark', '暗色']) },
      { key: 'frontend_theme_color', label: '管理端主题色', kind: 'select', options: sel(['default', '默认'], ['darkblue', '暗蓝色'], ['black', '黑色'], ['green', '奶绿色']) },
      { key: 'frontend_background_url', label: '登录页背景图', kind: 'text', placeholder: 'https://' },
    ],
  },
  {
    name: 'server',
    title: '节点',
    fields: [
      { key: 'server_token', label: '通讯密钥', kind: 'password', help: '面板与节点通讯的密钥，至少 16 位' },
      { key: 'server_api_url', label: '节点对接 API 地址', kind: 'text', help: 'v2node 一键安装命令使用，留空使用站点网址' },
      { key: 'server_pull_interval', label: '节点拉取间隔（秒）', kind: 'int' },
      { key: 'server_push_interval', label: '节点上报间隔（秒）', kind: 'int' },
      { key: 'server_node_report_min_traffic', label: '流量上报最低阈值（字节）', kind: 'int' },
      { key: 'server_device_online_min_traffic', label: '在线设备统计最低流量（字节）', kind: 'int' },
      { key: 'device_limit_mode', label: '设备数限制模式', kind: 'select', options: sel([0, '严格'], [1, '宽松（同一 IP 多节点只算一个设备）']) },
      { key: 'server_offline_notify', label: '节点掉线通知管理员', kind: 'switch' },
    ],
  },
  {
    name: 'email',
    title: '邮件',
    fields: [
      { key: 'email_template', label: '邮件模板', kind: 'select', options: [] },
      { key: 'email_host', label: 'SMTP 服务器', kind: 'text' },
      { key: 'email_port', label: 'SMTP 端口', kind: 'int', help: '常见 25 / 465 / 587' },
      { key: 'email_encryption', label: '加密方式', kind: 'select', options: sel(['', '无'], ['ssl', 'SSL'], ['tls', 'TLS']) },
      { key: 'email_username', label: 'SMTP 账号', kind: 'text' },
      { key: 'email_password', label: 'SMTP 密码', kind: 'password' },
      { key: 'email_from_address', label: '发件地址', kind: 'text' },
    ],
  },
  {
    name: 'telegram',
    title: 'Telegram',
    fields: [
      { key: 'telegram_bot_enable', label: '启用机器人', kind: 'switch' },
      { key: 'telegram_bot_token', label: '机器人 Token', kind: 'password' },
      { key: 'telegram_mode', label: '接收更新方式', kind: 'select', options: sel(['webhook', 'Webhook'], ['polling', '长轮询']) },
      { key: 'telegram_api_url', label: 'Bot API 地址', kind: 'text', help: '可填写自建反向代理' },
      { key: 'telegram_proxy', label: '代理', kind: 'password', placeholder: 'socks5://127.0.0.1:1080，留空不用代理' },
      { key: 'telegram_webhook_secret', label: 'Webhook 密钥', kind: 'password', help: '留空时“应用设置”会自动生成' },
      { key: 'telegram_discuss_link', label: '群组地址', kind: 'text', placeholder: 'https://t.me/xxx' },
      { key: 'telegram_welcome', label: '欢迎消息', kind: 'textarea', help: 'Telegram HTML，可使用 {app_name}，留空使用内置文案' },
    ],
  },
  {
    name: 'app',
    title: '客户端',
    fields: [
      { key: 'windows_version', label: 'Windows 版本', kind: 'text' },
      { key: 'windows_download_url', label: 'Windows 下载地址', kind: 'text' },
      { key: 'macos_version', label: 'macOS 版本', kind: 'text' },
      { key: 'macos_download_url', label: 'macOS 下载地址', kind: 'text' },
      { key: 'android_version', label: 'Android 版本', kind: 'text' },
      { key: 'android_download_url', label: 'Android 下载地址', kind: 'text' },
    ],
  },
  {
    name: 'gvboard',
    title: '多订阅与风控',
    fields: [
      { key: 'subscription_limit', label: '默认订阅数量上限', kind: 'int', help: '单个账号可拥有的订阅数，用户可单独设置' },
      { key: 'subscribe_log_enable', label: '记录订阅请求日志', kind: 'switch' },
      { key: 'subscribe_log_retention_days', label: '日志保留天数', kind: 'int', help: '1-180' },
      { key: 'subscribe_log_headers', label: '额外记录的请求头', kind: 'tags', help: '最多 10 个，如 X-Forwarded-For' },
      { key: 'subscribe_rate_window', label: '频率统计窗口（秒）', kind: 'int', help: '60-86400' },
      { key: 'subscribe_rate_sub_limit', label: '单订阅请求上限', kind: 'int', help: '窗口内最多请求次数，0 不限' },
      { key: 'subscribe_rate_ip_limit', label: '单 IP 请求上限', kind: 'int', help: '0 不限' },
      { key: 'subscribe_rate_action', label: '超限动作', kind: 'select', options: sel(['empty', '返回空内容'], ['deny', '返回 404']) },
      { key: 'subscribe_share_ip_limit', label: '防分享 IP 数', kind: 'int', help: '单个订阅在窗口内允许的不同 IP 数，0 关闭' },
      { key: 'subscribe_share_window', label: '防分享窗口（小时）', kind: 'int', help: '1-168' },
      { key: 'subscribe_share_action', label: '防分享超限动作', kind: 'select', options: sel(['log', '仅记录'], ['empty', '返回空内容'], ['deny', '返回 404']) },
    ],
  },
]
