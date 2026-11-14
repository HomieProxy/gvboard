// 系统配置表单定义，键名与后端设置项一致（见 docs/接口参考/管理端-系统.md 1.2）。
// 分组、标题、说明文字与原版管理端保持一致。

export type ConfigKind = 'text' | 'textarea' | 'password' | 'int' | 'float' | 'switch' | 'select' | 'radio' | 'tags' | 'plan'

export interface ConfigField {
  key: string
  label: string
  kind: ConfigKind
  help?: string
  options?: { label: string; value: string | number }[]
  placeholder?: string
  /** 输入框右侧单位 */
  suffix?: string
  /** 与 key 同行显示的第二个输入框（如 APP 版本号 + 下载地址） */
  key2?: string
  placeholder2?: string
  /** 仅当该开关键为开启时显示 */
  showIf?: string
}

export interface ConfigGroup {
  name: string
  title: string
  /** 顶部提示 */
  notice?: string
  fields: ConfigField[]
}

const sel = (...pairs: [string | number, string][]) => pairs.map(([value, label]) => ({ value, label }))
const orderEvent = sel([0, '不执行任何动作'], [1, '重置用户流量'])
const lightDark = sel(['light', '亮'], ['dark', '暗'])

export const configGroups: ConfigGroup[] = [
  {
    name: 'site',
    title: '站点',
    fields: [
      { key: 'app_name', label: '站点名称', kind: 'text', help: '用于显示需要站点名称的地方。', placeholder: '请输入站点名称' },
      { key: 'app_description', label: '站点描述', kind: 'text', help: '用于显示需要站点描述的地方。', placeholder: '请输入站点描述' },
      { key: 'app_url', label: '站点网址', kind: 'text', help: '当前网站最新网址，将会在邮件等需要用于网址处体现。', placeholder: '请输入站点URL，末尾不要/' },
      { key: 'force_https', label: '强制HTTPS', kind: 'switch', help: '当站点没有使用HTTPS，CDN或反代开启强制HTTPS时需要开启。' },
      { key: 'logo', label: 'LOGO', kind: 'text', help: '用于显示需要LOGO的地方。', placeholder: '请输入LOGO URL，末尾不要/' },
      {
        key: 'subscribe_url',
        label: '订阅URL',
        kind: 'textarea',
        help: '用于订阅所使用，留空则为站点URL。如需多个订阅URL随机获取请使用逗号进行分割。',
        placeholder: '请输入订阅URL，末尾不要/。逗号分割支持多域名',
      },
      {
        key: 'subscribe_path',
        label: '订阅路径',
        kind: 'text',
        help: '用于订阅所使用，留空则为/api/v1/client/subscribe。如需更换不同的订阅路径请设置。',
      },
      { key: 'tos_url', label: '用户条款(TOS)URL', kind: 'text', help: '用于跳转到用户条款(TOS)', placeholder: '请输入用户条款URL，末尾不要/' },
      { key: 'stop_register', label: '停止新用户注册', kind: 'switch', help: '开启后任何人都将无法进行注册。' },
      { key: 'try_out_plan_id', label: '注册试用', kind: 'plan', help: '选择需要试用的订阅，如果没有选项请先前往订阅管理添加。', placeholder: '请选择试用订阅' },
      { key: 'try_out_hour', label: '试用时间(小时)', kind: 'float', placeholder: '请输入' },
      { key: 'currency', label: '货币单位', kind: 'text', help: '仅用于展示使用，更改后系统中所有的货币单位都将发生变更。', placeholder: 'CNY' },
      { key: 'currency_symbol', label: '货币符号', kind: 'text', help: '仅用于展示使用，更改后系统中所有的货币单位都将发生变更。', placeholder: '¥' },
    ],
  },
  {
    name: 'safe',
    title: '安全',
    fields: [
      { key: 'email_verify', label: '邮箱验证', kind: 'switch', help: '开启后将会强制要求用户进行邮箱验证。' },
      { key: 'email_gmail_limit_enable', label: '禁止使用Gmail多别名', kind: 'switch', help: '开启后Gmail多别名将无法注册。' },
      { key: 'safe_mode_enable', label: '安全模式', kind: 'switch', help: '开启后除了站点URL以外的绑定本站点的域名访问都将会被403。' },
      {
        key: 'secure_path',
        label: '后台路径',
        kind: 'text',
        help: '后台管理路径，修改后将会改变原有的admin路径。至少 16 位，只能包含字母、数字、_、-。',
        placeholder: 'admin',
      },
      { key: 'email_whitelist_enable', label: '邮箱后缀白名单', kind: 'switch', help: '开启后在名单中的邮箱后缀才允许进行注册。' },
      {
        key: 'email_whitelist_suffix',
        label: '白名单后缀',
        kind: 'tags',
        help: '如：qq.com,gmail.com。',
        placeholder: '输入后缀域名后回车',
        showIf: 'email_whitelist_enable',
      },
      { key: 'recaptcha_enable', label: '防机器人', kind: 'switch', help: '开启后将会使用Google reCAPTCHA防止机器人。' },
      { key: 'recaptcha_key', label: '密钥', kind: 'password', help: '在Google reCAPTCHA申请的密钥。', placeholder: '请输入', showIf: 'recaptcha_enable' },
      { key: 'recaptcha_site_key', label: '网站密钥', kind: 'text', help: '在Google reCAPTCHA申请的网站密钥。', placeholder: '请输入', showIf: 'recaptcha_enable' },
      {
        key: 'register_limit_by_ip_enable',
        label: 'IP注册限制',
        kind: 'switch',
        help: '开启后如果IP注册账户达到规则要求将会被限制注册，请注意IP判断可能因为CDN或前置代理导致问题。',
      },
      { key: 'register_limit_count', label: '次数', kind: 'int', help: '达到注册次数后开启惩罚。', placeholder: '请输入', showIf: 'register_limit_by_ip_enable' },
      {
        key: 'register_limit_expire',
        label: '惩罚时间(分钟)',
        kind: 'int',
        help: '需要等待惩罚时间过后才可以再次注册。',
        placeholder: '请输入',
        showIf: 'register_limit_by_ip_enable',
      },
      { key: 'password_limit_enable', label: '防爆破限制', kind: 'switch', help: '开启后如果该账户尝试登陆失败次数过多将会被限制。' },
      { key: 'password_limit_count', label: '次数', kind: 'int', help: '达到失败次数后开启惩罚。', placeholder: '请输入', showIf: 'password_limit_enable' },
      {
        key: 'password_limit_expire',
        label: '惩罚时间(分钟)',
        kind: 'int',
        help: '需要等待惩罚时间过后才可以再次登陆。',
        placeholder: '请输入',
        showIf: 'password_limit_enable',
      },
    ],
  },
  {
    name: 'subscribe',
    title: '订阅',
    fields: [
      { key: 'plan_change_enable', label: '允许用户更改订阅', kind: 'switch', help: '开启后用户将会可以对订阅计划进行变更。' },
      {
        key: 'reset_traffic_method',
        label: '月流量重置方式',
        kind: 'select',
        help: '全局流量重置方式，默认每月1号。可以在订阅管理为订阅单独设置。',
        placeholder: '请选择订阅重置方式',
        options: sel([0, '每月1号'], [1, '按月重置'], [2, '不重置'], [3, '每年1月1日'], [4, '按年重置']),
      },
      { key: 'surplus_enable', label: '开启折抵方案', kind: 'switch', help: '开启后用户更换订阅将会由系统对原有订阅进行折抵，方案参考文档。' },
      {
        key: 'allow_new_period',
        label: '允许提前开启流量周期',
        kind: 'switch',
        help: '开启后用户流量用尽时可以选择扣除订阅时长为代价重置流量，按月重置时扣除本周期剩余订阅时长，每月1号重置时扣除整月时间30天。',
      },
      { key: 'new_order_event_id', label: '当订阅新购时触发事件', kind: 'select', help: '新购订阅完成时将触发该任务。', placeholder: '请选择事件', options: orderEvent },
      { key: 'renew_order_event_id', label: '当订阅续费时触发事件', kind: 'select', help: '续费订阅完成时将触发该任务。', placeholder: '请选择事件', options: orderEvent },
      { key: 'change_order_event_id', label: '当订阅变更时触发事件', kind: 'select', help: '变更订阅完成时将触发该任务。', placeholder: '请选择事件', options: orderEvent },
      { key: 'show_info_to_server_enable', label: '在订阅中展示订阅信息', kind: 'switch', help: '开启后将会在用户订阅节点时输出订阅信息。' },
      {
        key: 'show_subscribe_method',
        label: '订阅链接生效模式',
        kind: 'select',
        help: '用户获取订阅链接后的有效期。',
        options: sel([0, '永久有效'], [1, '一次性有效'], [2, '限时有效']),
      },
      { key: 'show_subscribe_expire', label: '订阅链接有效时间(分钟)', kind: 'int', help: '订阅链接获取后经过该时间将失效。', placeholder: '请输入' },
    ],
  },
  {
    name: 'deposit',
    title: '充值',
    fields: [
      {
        key: 'deposit_bounus',
        label: '充值奖励',
        kind: 'tags',
        help: '充值一定金额可以获得的奖励。每项格式为 充值金额:奖励金额，如 50:18、100:38、200:88',
        placeholder: '如 50:18，回车添加',
      },
    ],
  },
  {
    name: 'ticket',
    title: '工单',
    fields: [
      {
        key: 'ticket_status',
        label: '工单设置',
        kind: 'select',
        help: '请选择工单的状态。',
        options: sel([0, '完全开放工单'], [1, '仅限有付费订单用户'], [2, '完全禁止工单']),
      },
    ],
  },
  {
    name: 'invite',
    title: '邀请&佣金',
    fields: [
      { key: 'invite_force', label: '开启强制邀请', kind: 'switch', help: '开启后只有被邀请的用户才可以进行注册。' },
      { key: 'invite_commission', label: '邀请佣金百分比', kind: 'int', help: '默认全局的佣金分配比例，你可以在用户管理单独配置单个比例。', placeholder: '请输入', suffix: '%' },
      { key: 'invite_gen_limit', label: '用户可创建邀请码上限', kind: 'int', placeholder: '请输入' },
      { key: 'invite_never_expire', label: '邀请码永不失效', kind: 'switch', help: '开启后邀请码被使用后将不会失效，否则使用过后即失效。' },
      {
        key: 'commission_first_time_enable',
        label: '佣金仅首次发放',
        kind: 'switch',
        help: '开启后被邀请人首次支付时才会产生佣金，可以在用户管理对用户进行单独配置。',
      },
      { key: 'commission_auto_check_enable', label: '佣金自动确认', kind: 'switch', help: '开启后佣金将会在订单完成3日后自动进行确认。' },
      { key: 'commission_withdraw_limit', label: '提现单申请门槛(元)', kind: 'float', help: '小于门槛金额的提现单将不会被提交。', placeholder: '请输入' },
      { key: 'commission_withdraw_method', label: '提现方式', kind: 'tags', help: '可以支持的提现方式。', placeholder: '如：支付宝、USDT、贝宝，回车添加' },
      { key: 'withdraw_close_enable', label: '关闭提现', kind: 'switch', help: '关闭后将禁止用户申请提现，且邀请佣金将会直接进入用户余额。' },
      {
        key: 'commission_distribution_enable',
        label: '三级分销',
        kind: 'switch',
        help: '开启后将佣金将按照设置的3成比例进行分成，三成比例合计请不要>100%。',
      },
      { key: 'commission_distribution_l1', label: '一级邀请人比例', kind: 'float', placeholder: '请输入比例如：50', suffix: '%', showIf: 'commission_distribution_enable' },
      { key: 'commission_distribution_l2', label: '二级邀请人比例', kind: 'float', placeholder: '请输入比例如：30', suffix: '%', showIf: 'commission_distribution_enable' },
      { key: 'commission_distribution_l3', label: '三级邀请人比例', kind: 'float', placeholder: '请输入比例如：20', suffix: '%', showIf: 'commission_distribution_enable' },
    ],
  },
  {
    name: 'frontend',
    title: '个性化',
    notice: '如果你采用前后分离的方式部署管理端，那么本页配置将不会生效。',
    fields: [
      { key: 'frontend_theme_sidebar', label: '边栏风格', kind: 'radio', options: lightDark },
      { key: 'frontend_theme_header', label: '头部风格', kind: 'radio', options: lightDark },
      { key: 'frontend_theme_color', label: '主题色', kind: 'select', options: sel(['default', '默认'], ['black', '黑色'], ['darkblue', '暗蓝色'], ['green', '奶绿色']) },
      { key: 'frontend_background_url', label: '背景', kind: 'text', help: '将会在后台登录页面进行展示。', placeholder: 'https://' },
    ],
  },
  {
    name: 'server',
    title: '节点',
    fields: [
      { key: 'server_api_url', label: '节点对接API地址', kind: 'text', help: 'v2node节点一键对接专用地址。', placeholder: '请输入' },
      { key: 'server_token', label: '通讯密钥', kind: 'password', help: '面板与节点通讯的密钥，以便数据不会被他人获取。', placeholder: '请输入' },
      { key: 'server_pull_interval', label: '节点拉取动作轮询间隔', kind: 'int', help: '节点从面板获取数据的间隔频率。', placeholder: '请输入', suffix: '秒' },
      { key: 'server_push_interval', label: '节点推送动作轮询间隔', kind: 'int', help: '节点推送数据到面板的间隔频率。', placeholder: '请输入', suffix: '秒' },
      {
        key: 'server_node_report_min_traffic',
        label: '节点用户流量上报最低阈值',
        kind: 'int',
        help: '每次推送动作仅累计使用流量高于阈值的用户信息会被上报，未上报流量会累计',
        placeholder: '请输入',
        suffix: 'Kb',
      },
      {
        key: 'server_device_online_min_traffic',
        label: '节点用户设备数统计最低阈值',
        kind: 'int',
        help: '每次推送动作仅上报流量高于阈值的在线设备IP地址会被节点统计',
        placeholder: '请输入',
        suffix: 'Kb',
      },
      { key: 'device_limit_mode', label: '全局设备数限制采用宽松模式', kind: 'switch', help: '开启后同一IP地址使用多个节点只统计为一个设备' },
      { key: 'server_offline_notify', label: '节点掉线通知', kind: 'switch', help: '开启后节点超过 30 分钟未上报时通过 Telegram 通知管理员。' },
    ],
  },
  {
    name: 'email',
    title: '邮件',
    notice: '本页配置保存后立即生效，无需重启服务。',
    fields: [
      { key: 'email_host', label: 'SMTP服务器地址', kind: 'text', help: '由邮件服务商提供的服务地址', placeholder: '请输入' },
      { key: 'email_port', label: 'SMTP服务端口', kind: 'int', help: '常见的端口有25, 465, 587', placeholder: '请输入' },
      { key: 'email_encryption', label: 'SMTP加密方式', kind: 'text', help: '465端口加密方式一般为SSL，587端口加密方式一般为TLS', placeholder: '请输入' },
      { key: 'email_username', label: 'SMTP账号', kind: 'text', help: '由邮件服务商提供的账号', placeholder: '请输入' },
      { key: 'email_password', label: 'SMTP密码', kind: 'password', help: '由邮件服务商提供的密码', placeholder: '请输入' },
      { key: 'email_from_address', label: '发件地址', kind: 'text', help: '由邮件服务商提供的发件地址', placeholder: '请输入' },
      { key: 'email_template', label: '邮件模板', kind: 'select', help: '你可以在文档查看如何自定义邮件模板', options: [] },
    ],
  },
  {
    name: 'telegram',
    title: 'Telegram',
    fields: [
      { key: 'telegram_bot_token', label: '机器人Token', kind: 'password', help: '请输入由Botfather提供的token。' },
      { key: 'telegram_bot_enable', label: '开启机器人通知', kind: 'switch', help: '开启后bot将会对绑定了telegram的管理员和用户进行基础通知。' },
      { key: 'telegram_discuss_link', label: '群组地址', kind: 'text', help: '填写后将会在用户端展示，或者被用于需要的地方。', placeholder: 'https://t.me/xxx' },
      {
        key: 'telegram_mode',
        label: '接收更新方式',
        kind: 'select',
        help: 'Webhook 需要站点可被 Telegram 访问；无法公网访问时使用长轮询。',
        options: sel(['webhook', 'Webhook'], ['polling', '长轮询']),
      },
      { key: 'telegram_api_url', label: 'Bot API 地址', kind: 'text', help: '可填写自建反向代理，留空使用官方地址。', placeholder: 'https://api.telegram.org' },
      { key: 'telegram_proxy', label: '代理', kind: 'password', help: '访问 Telegram 使用的代理，留空不使用。', placeholder: 'socks5://127.0.0.1:1080' },
      { key: 'telegram_webhook_secret', label: 'Webhook 密钥', kind: 'password', help: '留空时「一键设置」会自动生成。' },
      { key: 'telegram_welcome', label: '欢迎消息', kind: 'textarea', help: 'Telegram HTML，可使用 {app_name}，留空使用内置文案。' },
    ],
  },
  {
    name: 'app',
    title: 'APP',
    notice: '用于自有客户端(APP)的版本管理及更新',
    fields: [
      { key: 'windows_version', key2: 'windows_download_url', label: 'Windows', kind: 'text', help: 'Windows端版本号及下载地址', placeholder: '版本号', placeholder2: '下载地址' },
      { key: 'macos_version', key2: 'macos_download_url', label: 'macOS', kind: 'text', help: 'macOS端版本号及下载地址', placeholder: '版本号', placeholder2: '下载地址' },
      { key: 'android_version', key2: 'android_download_url', label: 'Android', kind: 'text', help: 'Android端版本号及下载地址', placeholder: '版本号', placeholder2: '下载地址' },
    ],
  },
  {
    name: 'gvboard',
    title: '多订阅与风控',
    fields: [
      { key: 'subscription_limit', label: '默认订阅数量上限', kind: 'int', help: '单个账号可拥有的订阅数，可在用户管理中为用户单独设置。' },
      { key: 'subscribe_log_enable', label: '记录订阅请求日志', kind: 'switch', help: '记录每次订阅拉取的 IP、UA 等信息，可在订阅风控中查看。' },
      { key: 'subscribe_log_retention_days', label: '日志保留天数', kind: 'int', help: '1-180', suffix: '天' },
      { key: 'subscribe_log_headers', label: '额外记录的请求头', kind: 'tags', help: '最多 10 个，如 X-Forwarded-For' },
      { key: 'subscribe_rate_window', label: '频率统计窗口', kind: 'int', help: '60-86400', suffix: '秒' },
      { key: 'subscribe_rate_sub_limit', label: '单订阅请求上限', kind: 'int', help: '窗口内最多请求次数，0 不限' },
      { key: 'subscribe_rate_ip_limit', label: '单 IP 请求上限', kind: 'int', help: '窗口内最多请求次数，0 不限' },
      { key: 'subscribe_rate_action', label: '超限动作', kind: 'select', options: sel(['empty', '返回空内容'], ['deny', '返回 404']) },
      { key: 'subscribe_share_ip_limit', label: '防分享 IP 数', kind: 'int', help: '单个订阅在窗口内允许的不同 IP 数，0 关闭' },
      { key: 'subscribe_share_window', label: '防分享窗口', kind: 'int', help: '1-168', suffix: '小时' },
      { key: 'subscribe_share_action', label: '防分享超限动作', kind: 'select', options: sel(['log', '仅记录'], ['empty', '返回空内容'], ['deny', '返回 404']) },
    ],
  },
]
