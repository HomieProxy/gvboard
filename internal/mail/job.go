package mail

import (
	"context"
	"encoding/json"
	"html/template"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/model"
	"gvboard/internal/queue"
)

// TaskSendEmail 发送邮件任务。
const TaskSendEmail = "send_email"

// Payload 发送邮件任务参数，字段与原项目 SendEmailJob 一致。
type Payload struct {
	Email         string         `json:"email"`
	Subject       string         `json:"subject"`
	TemplateName  string         `json:"template_name"`
	TemplateValue map[string]any `json:"template_value"`
	// TrustedHTML 为 true 时 content 按 HTML 原样输出，仅用于管理员群发等可信内容。
	TrustedHTML bool `json:"trusted_html,omitempty"`
	// Server 不为空时只用 email_smtp_servers 中该序号的服务器发送（管理端测试单个服务器）。
	Server *int `json:"server,omitempty"`
}

// sendInterval 每封邮件发送前等待，与原项目一致，降低被邮件服务商限流的概率。
var sendInterval = 2 * time.Second

// Dispatcher 投递发送邮件任务。
type Dispatcher struct{ q *queue.Queue }

// Dispatch 投递到 send_email 队列。
func (d Dispatcher) Dispatch(ctx context.Context, p Payload) error {
	return d.q.Enqueue(ctx, TaskSendEmail, p, queue.QueueSendEmail)
}

// DispatchMass 投递到群发队列（优先级最低）。
func (d Dispatcher) DispatchMass(ctx context.Context, p Payload) error {
	return d.q.Enqueue(ctx, TaskSendEmail, p, queue.QueueSendEmailMass)
}

// RegisterTask 注册发送邮件任务处理器。
func RegisterTask(q *queue.Queue, m *Mailer, db *gorm.DB, log *zap.Logger) Dispatcher {
	log = log.Named("mail")
	q.Register(queue.Spec{Type: TaskSendEmail, Queue: queue.QueueSendEmail, Tries: 3, Timeout: 30 * time.Second},
		func(ctx context.Context, raw []byte) error {
			var p Payload
			if err := json.Unmarshal(raw, &p); err != nil {
				log.Error("邮件任务参数无效", zap.Error(err))
				return nil
			}
			select {
			case <-time.After(sendInterval):
			case <-ctx.Done():
				return ctx.Err()
			}
			m.Deliver(ctx, db, log, p)
			return nil
		})
	return Dispatcher{q: q}
}

// Deliver 立即发送并写入邮件日志，返回日志记录（发送失败时 Error 不为空）。
// 发送失败只记录错误、不重试，与原项目一致（避免重复发信）。
func (m *Mailer) Deliver(ctx context.Context, db *gorm.DB, log *zap.Logger, p Payload) model.MailLog {
	vars := p.TemplateValue
	if vars == nil {
		vars = map[string]any{}
	}
	if p.TrustedHTML {
		if s, ok := vars["content"].(string); ok {
			vars["content"] = template.HTML(s)
		}
	}
	theme := m.Theme()
	rec := model.MailLog{Email: p.Email, Subject: p.Subject, TemplateName: "mail." + theme + "." + p.TemplateName}
	server := -1
	if p.Server != nil {
		server = *p.Server
	}
	if err := m.SendVia(ctx, server, p.Email, p.Subject, theme, p.TemplateName, vars); err != nil {
		msg := err.Error()
		rec.Error = &msg
		log.Warn("邮件发送失败", zap.String("template", p.TemplateName), zap.Error(err))
	}
	if err := db.WithContext(ctx).Create(&rec).Error; err != nil {
		log.Error("写入邮件日志失败", zap.Error(err))
	}
	return rec
}
