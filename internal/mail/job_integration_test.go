package mail

// 队列 + 邮件任务集成测试。需要环境变量：
//   GVB_TEST_REDIS_ADDR  例如 127.0.0.1:63790
//   GVB_TEST_MYSQL_DSN   例如 root:password@tcp(127.0.0.1:33080)/?charset=utf8mb4

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gvboard/internal/config"
	"gvboard/internal/migrate"
	"gvboard/internal/model"
	"gvboard/internal/queue"
)

func TestSendEmailJob(t *testing.T) {
	redisAddr, dsn := os.Getenv("GVB_TEST_REDIS_ADDR"), os.Getenv("GVB_TEST_MYSQL_DSN")
	if redisAddr == "" || dsn == "" {
		t.Skip("未设置 GVB_TEST_REDIS_ADDR / GVB_TEST_MYSQL_DSN，跳过")
	}
	db := openMailLogDB(t, dsn)

	prefix := fmt.Sprintf("gvbtest%d:", time.Now().UnixNano())
	rc := config.RedisConfig{Addr: redisAddr, Prefix: prefix}
	q := queue.New(rc, zap.NewNop())
	t.Cleanup(func() {
		q.Close()
		insp := asynq.NewInspector(asynq.RedisClientOpt{Addr: redisAddr})
		for _, n := range []string{queue.QueueSendEmail, queue.QueueSendEmailMass} {
			_ = insp.DeleteQueue(q.Name(n), true)
		}
		insp.Close()
	})

	m := newMailer(t, fakeSettings{"email_host": "smtp.x.com", "email_from_address": "a@x.com", "app_name": "站"})
	var mu sync.Mutex
	calls := map[string]int{}
	m.SetTransport(func(_ context.Context, _ SMTPConfig, to, _, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		calls[to]++
		if strings.HasPrefix(to, "fail") {
			return errors.New("550 mailbox unavailable")
		}
		return nil
	})
	sendInterval = 0
	d := RegisterTask(q, m, db, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = q.Run(ctx, 2); close(done) }()
	defer func() { cancel(); <-done }()

	if err := d.Dispatch(ctx, Payload{Email: "ok@x.com", Subject: "验证码", TemplateName: TplVerify, TemplateValue: map[string]any{"code": "1"}}); err != nil {
		t.Fatal(err)
	}
	if err := d.DispatchMass(ctx, Payload{Email: "fail@x.com", Subject: "群发", TemplateName: TplNotify, TemplateValue: map[string]any{"content": "<b>x</b>"}, TrustedHTML: true}); err != nil {
		t.Fatal(err)
	}

	var logs []model.MailLog
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		db.Order("email desc").Find(&logs)
		if len(logs) == 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(logs) != 2 {
		t.Fatalf("应写入 2 条邮件日志，实际 %d", len(logs))
	}
	if logs[0].Email != "ok@x.com" || logs[0].Error != nil || logs[0].TemplateName != "mail.default.verify" {
		t.Errorf("成功邮件日志错误: %+v", logs[0])
	}
	if logs[1].Email != "fail@x.com" || logs[1].Error == nil || !strings.Contains(*logs[1].Error, "550") {
		t.Errorf("失败邮件应记录错误原因: %+v", logs[1])
	}

	time.Sleep(time.Second)
	mu.Lock()
	defer mu.Unlock()
	if calls["fail@x.com"] != 1 || calls["ok@x.com"] != 1 {
		t.Errorf("发送失败不应重试（避免重复发信），实际调用次数 %v", calls)
	}

	if err := q.Enqueue(ctx, "no_such_task", nil, ""); err == nil {
		t.Error("未注册的任务类型应报错")
	}
	if err := q.Enqueue(ctx, TaskSendEmail, nil, "no_such_queue"); err == nil {
		t.Error("未知队列应报错")
	}
	stats, err := q.Stats()
	if err != nil || len(stats) != 7 {
		t.Errorf("队列状态: %v %v", stats, err)
	}
}

func openMailLogDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(mysql.Open(dsn), cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("gvb_test_mail_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE " + name) })
	db, err := gorm.Open(mysql.Open(strings.Replace(dsn, "/?", "/"+name+"?", 1)), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range migrate.Schema() {
		if tb.Name != "v2_mail_log" {
			continue
		}
		stmts, err := migrate.CreateTableSQL(config.DriverMySQL, tb)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range stmts {
			if err := db.Exec(s).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	return db
}
