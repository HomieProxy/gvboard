// Package tasks 定时任务（对应原项目 app/Console/Commands，决策 D54）：每日统计、流量重置、日志清理、
// 到期 / 流量提醒、节点掉线检查。订单、佣金、工单、流量写入、自动续费由各自的业务包提供。
package tasks

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/mail"
	"gvboard/internal/scheduler"
	"gvboard/internal/setting"
	"gvboard/internal/telegram"
)

// MailDispatcher 投递邮件到发送队列。
type MailDispatcher interface {
	Dispatch(ctx context.Context, p mail.Payload) error
}

// Notifier Telegram 通知（telegram.Service），topic 为管理员话题群组中的类别（telegram.Topic*），
// build 按接收者语言生成 HTML 文本。
type Notifier interface {
	NotifyAdmins(ctx context.Context, topic string, staff bool, build func(locale string) string)
	NotifyUser(ctx context.Context, userID int64, kind telegram.NotifyKind, build func(locale string) string)
}

// Service 定时任务。
type Service struct {
	DB       *gorm.DB
	Cache    *cache.Cache
	Settings *setting.Service
	Mail     MailDispatcher
	Notify   Notifier // 可为 nil
	// Translate 按语言翻译文案，locale 为空时使用站点默认语言。
	Translate func(locale, key string, params map[string]any) string
	// CleanSubscribeLogs 清理过期的订阅请求日志（subscribe.Service.CleanLogs），可为 nil。
	CleanSubscribeLogs func(ctx context.Context) (int64, error)
	// CleanAuditLogs 清理过期的审计日志（audit.Service.CleanLogs），可为 nil。
	CleanAuditLogs func(ctx context.Context) (int64, error)
	Log            *zap.Logger
	Now            func() time.Time // 测试注入，nil 时为 time.Now
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *zap.Logger {
	if s.Log == nil {
		return zap.NewNop()
	}
	return s.Log
}

func (s *Service) tr(locale, key string, params map[string]any) string {
	if s.Translate == nil {
		return key
	}
	return s.Translate(locale, key, params)
}

// Jobs 本包的定时任务，执行时间与原项目 app/Console/Kernel.php 一致（节点掉线检查为 Gvboard 新增定时执行）。
func (s *Service) Jobs() []scheduler.Job {
	return []scheduler.Job{
		{Name: "statistics", Spec: "10 0 * * *", NoOverlap: true, Run: func(ctx context.Context) error {
			return s.Statistics(ctx, s.now().AddDate(0, 0, -1))
		}},
		{Name: "daily_report", Spec: "20 0 * * *", NoOverlap: true, Run: func(ctx context.Context) error {
			if !s.Settings.Bool("telegram_daily_report") {
				return nil
			}
			return s.DailyReport(ctx, s.now().AddDate(0, 0, -1))
		}},
		{Name: "reset_traffic", Spec: "0 0 * * *", NoOverlap: true, Run: func(ctx context.Context) error {
			_, err := s.ResetTraffic(ctx)
			return err
		}},
		{Name: "reset_log", Spec: "0 0 * * *", NoOverlap: true, Run: func(ctx context.Context) error {
			_, err := s.CleanLogs(ctx)
			return err
		}},
		{Name: "send_remind_mail", Spec: "30 11 * * *", NoOverlap: true, Run: func(ctx context.Context) error {
			_, err := s.Remind(ctx)
			return err
		}},
		{Name: "check_server", Spec: "*/5 * * * *", NoOverlap: true, Run: func(ctx context.Context) error {
			if !s.Settings.Bool("server_offline_notify") {
				return nil
			}
			_, err := s.CheckServers(ctx)
			return err
		}},
	}
}
