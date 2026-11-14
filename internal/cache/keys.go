package cache

import "fmt"

// KeyName 业务缓存键名。对应原项目 App\Utils\CacheKey 的键列表，只能使用这里定义的常量。
type KeyName string

const (
	KeyEmailVerifyCode              KeyName = "email_verify_code"              // 邮箱验证码
	KeyLastSendEmailVerifyTimestamp KeyName = "last_send_email_verify_ts"      // 最后一次发送邮箱验证码时间
	KeyServerOnlineUser             KeyName = "server_online_user"             // 节点在线用户数，唯一值为 "类型:节点ID"
	KeyServerLastCheckAt            KeyName = "server_last_check_at"           // 节点最后检查时间，唯一值同上
	KeyServerLastPushAt             KeyName = "server_last_push_at"            // 节点最后推送时间，唯一值同上
	KeyTempToken                    KeyName = "temp_token"                     // 临时令牌
	KeyLastSendEmailRemindTraffic   KeyName = "last_send_email_remind_traffic" // 最后发送流量提醒邮件
	KeyScheduleLastCheckAt          KeyName = "schedule_last_check_at"         // 定时任务最后运行时间
	KeyRegisterIPRateLimit          KeyName = "register_ip_rate_limit"         // 注册频率限制
	KeyLastSendLoginWithMailLinkTS  KeyName = "last_send_login_with_mail_link" // 最后一次发送邮件登录链接时间
	KeyPasswordErrorLimit           KeyName = "password_error_limit"           // 密码错误次数限制
	KeyUserSessions                 KeyName = "user_sessions"                  // 用户会话
	KeyForgetRequestLimit           KeyName = "forget_request_limit"           // 找回密码次数限制
	KeySendEmailCodeIPLimit         KeyName = "send_email_code_ip_limit"       // 发送邮箱验证码按 IP 限流（原项目 RateLimiter）
	KeyAliveIP                      KeyName = "alive_ip"                       // 订阅在线设备，唯一值为订阅 ID（原项目 ALIVE_IP_USER_）
	KeyAliveNodes                   KeyName = "alive_nodes"                    // 订阅在各节点的在线 IP（哈希，字段为"类型+节点ID"），唯一值为订阅 ID
	KeyAliveList                    KeyName = "alive_list"                     // 有设备数限制的订阅在线设备数列表（原项目 ALIVE_LIST）
	KeyTrafficUpload                KeyName = "traffic_upload"                 // 待写入数据库的上行流量（哈希，字段为订阅 ID）
	KeyTrafficDownload              KeyName = "traffic_download"               // 待写入数据库的下行流量
	KeyTrafficUploadPending         KeyName = "traffic_upload_pending"         // 正在写入数据库的上行流量，写入失败时保留到下次重试
	KeyTrafficDownloadPending       KeyName = "traffic_download_pending"       // 正在写入数据库的下行流量
	KeyTrafficResetLock             KeyName = "traffic_reset_lock"             // 流量重置进行中，期间暂停写入流量（原项目 traffic_reset_lock）
	KeySubscribeOTP                 KeyName = "subscribe_otp"                  // 一次性订阅 token：订阅 token → 一次性 token（原项目 otp_）
	KeySubscribeOTPToken            KeyName = "subscribe_otp_token"            // 一次性订阅 token：一次性 token → 订阅 token（原项目 otpn_）
	KeySubscribeTOTP                KeyName = "subscribe_totp"                 // 已验证的时效 token → 订阅 token（原项目 totp_）
	KeySubscribeRuleVersion         KeyName = "subscribe_rule_version"         // 订阅风控规则版本，规则修改后递增，各实例据此重新加载
	KeySubscribeRateSub             KeyName = "subscribe_rate_sub"             // 订阅请求次数，唯一值为"订阅ID:窗口序号"
	KeySubscribeRateIP              KeyName = "subscribe_rate_ip"              // 单 IP 订阅请求次数，唯一值为"IP:窗口序号"
	KeySubscribeShare               KeyName = "subscribe_share"                // 订阅最近请求的 IP（有序集合，分数为时间），唯一值为订阅 ID
	KeySubscribeInvalid             KeyName = "subscribe_invalid"              // 无效 token 请求次数，唯一值为日期 YYYYMMDD
	KeyTicketNotify                 KeyName = "ticket_notify"                  // 工单回复邮件通知节流，唯一值为用户 ID（原项目 ticket_sendEmailNotify_）
	KeyTelegramBind                 KeyName = "telegram_bind"                  // Telegram 一键绑定的一次性码 → 用户 ID
	KeyTelegramBot                  KeyName = "telegram_bot"                   // 机器人信息（getMe）缓存，唯一值为 token 摘要
	KeyTelegramUpdate               KeyName = "telegram_update"                // 已处理的 update_id（Webhook 重发去重）
	KeyTelegramOffset               KeyName = "telegram_offset"                // 长轮询的下一个 update_id
	KeyTelegramDraft                KeyName = "telegram_draft"                 // 机器人中购买套餐的草稿（含待输入的优惠券），唯一值为 Telegram 用户 ID
	KeyCancelledPaidNotify          KeyName = "cancelled_paid_notify"          // 已取消订单收到支付回调的通知去重，唯一值为订单号
)

// KeyOf 生成带前缀的业务键，unique 为空时只有键名。
func (c *Cache) KeyOf(name KeyName, unique any) string {
	if unique == nil || unique == "" {
		return c.Key(string(name))
	}
	return c.Key(fmt.Sprintf("%s:%v", name, unique))
}

// ServerKey 节点相关键的唯一值。
func ServerKey(serverType string, serverID int64) string {
	return fmt.Sprintf("%s:%d", serverType, serverID)
}
