package tasks

import (
	"context"
	"html"
	"strconv"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/telegram"
)

// remindTrafficTTL 同一订阅的流量提醒间隔（与原项目一致为 24 小时）。
const remindTrafficTTL = 24 * time.Hour

// RemindResult 提醒数量。
type RemindResult struct {
	Expire  int `json:"expire"`
	Traffic int `json:"traffic"`
}

type remindRow struct {
	model.Subscription
	Email string
}

// Remind 到期与流量提醒（原项目 send:remindMail，每天 11:30），按订阅发送（决策 D54）：
//   - 到期提醒：开启到期提醒、24 小时内到期的订阅；
//   - 流量提醒：开启流量提醒、未到期且已用流量达到 95%（未用完）的订阅，同一订阅 24 小时内只提醒一次。
//
// 邮件注明订阅名称；已绑定 Telegram 的用户同时按通知设置收到提醒。
func (s *Service) Remind(ctx context.Context) (*RemindResult, error) {
	now := s.now().Unix()
	r := &RemindResult{}
	base := func() *gorm.DB {
		return s.DB.WithContext(ctx).Table("v2_subscription AS s").
			Select("s.*, u.email").Joins("JOIN v2_user u ON u.id = s.user_id")
	}
	var expiring []remindRow
	if err := base().Where("s.remind_expire = 1 AND s.expired_at IS NOT NULL AND s.expired_at > ? AND s.expired_at < ?", now, now+86400).
		Order("s.id").Scan(&expiring).Error; err != nil {
		return r, err
	}
	for _, row := range expiring {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		s.sendRemind(ctx, row, mail.TplRemindExpire, "The service in :app_name is about to expire", "telegram.remind_expire")
		r.Expire++
	}

	var traffic []remindRow
	if err := base().Where("s.remind_traffic = 1 AND (s.expired_at IS NULL OR s.expired_at >= ?) AND s.transfer_enable > 0"+
		" AND (s.u + s.d) * 100 >= s.transfer_enable * 95 AND s.u + s.d < s.transfer_enable", now).
		Order("s.id").Scan(&traffic).Error; err != nil {
		return r, err
	}
	for _, row := range traffic {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		ok, err := s.Cache.SetNX(ctx, s.Cache.KeyOf(cache.KeyLastSendEmailRemindTraffic, row.ID), 1, remindTrafficTTL).Result()
		if err != nil {
			return r, err
		}
		if !ok {
			continue
		}
		s.sendRemind(ctx, row, mail.TplRemindTraffic, "The traffic usage in :app_name has reached 95%", "telegram.remind_traffic")
		r.Traffic++
	}
	return r, nil
}

// sendRemind 发送一个订阅的提醒邮件与 Telegram 提醒。
func (s *Service) sendRemind(ctx context.Context, row remindRow, tpl, subjectKey, tgKey string) {
	name := s.Settings.String("app_name")
	sub := subName(&row.Subscription)
	err := s.Mail.Dispatch(ctx, mail.Payload{
		Email:        row.Email,
		Subject:      s.tr("", subjectKey, map[string]any{"app_name": name}),
		TemplateName: tpl,
		TemplateValue: map[string]any{
			"name": name, "url": s.Settings.String("app_url"), "subscription": sub,
		},
	})
	if err != nil {
		s.log().Error("投递提醒邮件失败", zap.Int64("subscription_id", row.ID), zap.Error(err))
	}
	if s.Notify == nil {
		return
	}
	params := map[string]any{
		"name":    "<b>" + html.EscapeString(sub) + "</b>",
		"expired": "",
		"used":    flow(row.U + row.D),
		"total":   flow(row.TransferEnable),
	}
	if row.ExpiredAt != nil {
		params["expired"] = time.Unix(*row.ExpiredAt, 0).Format(time.DateTime)
	}
	s.Notify.NotifyUser(ctx, row.UserID, telegram.NotifyRemind, func(locale string) string {
		return s.tr(locale, tgKey, params)
	})
}

// subName 订阅显示名称：未命名时为 #ID（与 Telegram 机器人一致）。
func subName(sub *model.Subscription) string {
	if sub.Name != "" {
		return sub.Name
	}
	return "#" + strconv.FormatInt(sub.ID, 10)
}

// flow 流量显示为 GB，保留两位小数。
func flow(b int64) string {
	return strconv.FormatFloat(float64(b)/(1<<30), 'f', 2, 64) + " GB"
}
