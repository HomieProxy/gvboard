// Package queue 异步任务队列（基于 Redis），替代原项目的 Laravel Horizon。
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"gvboard/internal/config"
)

// 队列名与原项目一致。实际使用时会加上 redis.prefix，避免同一 Redis 中多个站点互相处理任务。
const (
	QueueOrderHandle   = "order_handle"
	QueueTrafficFetch  = "traffic_fetch"
	QueueStat          = "stat"
	QueueSendEmail     = "send_email"
	QueueSendEmailMass = "send_email_mass"
	QueueSendTelegram  = "send_telegram"
	QueuePlugin        = "plugin" // Gvboard 新增：插件事件
)

// queueWeights 处理优先级。群发邮件优先级最低，避免大量群发时验证码邮件被积压。
var queueWeights = map[string]int{
	QueueOrderHandle:   6,
	QueueTrafficFetch:  4,
	QueueStat:          4,
	QueueSendEmail:     3,
	QueueSendTelegram:  3,
	QueuePlugin:        2,
	QueueSendEmailMass: 1,
}

// Spec 任务类型定义。
type Spec struct {
	Type    string        // 任务类型
	Queue   string        // 默认队列
	Tries   int           // 最多执行次数（含首次），对应原项目 $tries
	Timeout time.Duration // 单次执行超时，对应原项目 $timeout
}

// Handler 任务处理函数，payload 为入队时的 JSON。
type Handler func(ctx context.Context, payload []byte) error

// Queue 任务队列客户端与处理器。
type Queue struct {
	prefix   string
	redisOpt asynq.RedisClientOpt
	client   *asynq.Client
	log      *zap.Logger

	mu       sync.RWMutex
	specs    map[string]Spec
	handlers map[string]Handler
}

// New 创建队列。不会立即建立处理器，Run 时才开始处理任务。
func New(c config.RedisConfig, log *zap.Logger) *Queue {
	opt := asynq.RedisClientOpt{Addr: c.Addr, Password: c.Password, DB: c.DB}
	return &Queue{
		prefix:   c.Prefix,
		redisOpt: opt,
		client:   asynq.NewClient(opt),
		log:      log.Named("queue"),
		specs:    map[string]Spec{},
		handlers: map[string]Handler{},
	}
}

// Name 返回实际使用的队列名。
func (q *Queue) Name(queue string) string { return q.prefix + queue }

// Register 注册任务类型及其处理函数。重复注册会 panic，属于编程错误。
func (q *Queue) Register(s Spec, h Handler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, dup := q.specs[s.Type]; dup {
		panic("任务类型重复注册: " + s.Type)
	}
	if _, ok := queueWeights[s.Queue]; !ok {
		panic("未知队列: " + s.Queue)
	}
	if s.Tries < 1 {
		s.Tries = 1
	}
	q.specs[s.Type] = s
	q.handlers[s.Type] = h
}

// Enqueue 投递任务。queueName 为空时使用任务类型的默认队列。
func (q *Queue) Enqueue(ctx context.Context, taskType string, payload any, queueName string, opts ...asynq.Option) error {
	q.mu.RLock()
	s, ok := q.specs[taskType]
	q.mu.RUnlock()
	if !ok {
		return fmt.Errorf("未注册的任务类型: %s", taskType)
	}
	if queueName == "" {
		queueName = s.Queue
	}
	if _, ok := queueWeights[queueName]; !ok {
		return fmt.Errorf("未知队列: %s", queueName)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	base := []asynq.Option{asynq.Queue(q.Name(queueName)), asynq.MaxRetry(s.Tries - 1)}
	if s.Timeout > 0 {
		base = append(base, asynq.Timeout(s.Timeout))
	}
	_, err = q.client.EnqueueContext(ctx, asynq.NewTask(taskType, b), append(base, opts...)...)
	return err
}

// Run 开始处理任务，阻塞直到 ctx 结束，退出前等待正在执行的任务完成。
func (q *Queue) Run(ctx context.Context, concurrency int) error {
	queues := make(map[string]int, len(queueWeights))
	for name, w := range queueWeights {
		queues[q.Name(name)] = w
	}
	srv := asynq.NewServer(q.redisOpt, asynq.Config{
		Concurrency:     concurrency,
		Queues:          queues,
		Logger:          asynqLogger{q.log.Sugar()},
		ShutdownTimeout: 30 * time.Second,
		ErrorHandler: asynq.ErrorHandlerFunc(func(_ context.Context, t *asynq.Task, err error) {
			q.log.Warn("任务执行失败", zap.String("type", t.Type()), zap.Error(err))
		}),
	})

	mux := asynq.NewServeMux()
	q.mu.RLock()
	for typ, h := range q.handlers {
		h := h
		mux.HandleFunc(typ, func(ctx context.Context, t *asynq.Task) (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("任务 panic: %v", r)
				}
			}()
			return h(ctx, t.Payload())
		})
	}
	q.mu.RUnlock()

	if err := srv.Start(mux); err != nil {
		return fmt.Errorf("启动队列处理器失败: %w", err)
	}
	q.log.Info("队列处理器已启动", zap.Int("concurrency", concurrency))
	<-ctx.Done()
	srv.Shutdown()
	return nil
}

// Stats 单个队列的状态，供管理端"队列状态"使用。
type Stats struct {
	Queue     string `json:"queue"`
	Pending   int    `json:"pending"`
	Active    int    `json:"active"`
	Scheduled int    `json:"scheduled"`
	Retry     int    `json:"retry"`
	Failed    int    `json:"failed"`
	Processed int    `json:"processed"` // 当天处理数
}

// Stats 返回全部队列状态。
func (q *Queue) Stats() ([]Stats, error) {
	insp := asynq.NewInspector(q.redisOpt)
	defer insp.Close()
	existing, err := insp.Queues()
	if err != nil {
		return nil, err
	}
	exists := make(map[string]bool, len(existing))
	for _, n := range existing {
		exists[n] = true
	}
	out := make([]Stats, 0, len(queueWeights))
	for _, name := range []string{QueueOrderHandle, QueueTrafficFetch, QueueStat, QueueSendEmail, QueueSendEmailMass, QueueSendTelegram, QueuePlugin} {
		if !exists[q.Name(name)] {
			out = append(out, Stats{Queue: name})
			continue
		}
		info, err := insp.GetQueueInfo(q.Name(name))
		if err != nil {
			return nil, err
		}
		out = append(out, Stats{
			Queue: name, Pending: info.Pending, Active: info.Active, Scheduled: info.Scheduled,
			Retry: info.Retry, Failed: info.Archived, Processed: info.Processed,
		})
	}
	return out, nil
}

// Workers 正在处理本站队列的处理器进程数（多实例部署时为全部实例之和）及其总并发数。
func (q *Queue) Workers() (servers, concurrency int, err error) {
	insp := asynq.NewInspector(q.redisOpt)
	defer insp.Close()
	list, err := insp.Servers()
	if err != nil {
		return 0, 0, err
	}
	for _, s := range list {
		if s.Status != "active" {
			continue
		}
		if _, ok := s.Queues[q.Name(QueueOrderHandle)]; ok {
			servers++
			concurrency += s.Concurrency
		}
	}
	return servers, concurrency, nil
}

// Close 关闭投递客户端。
func (q *Queue) Close() error { return q.client.Close() }

type asynqLogger struct{ s *zap.SugaredLogger }

func (l asynqLogger) Debug(args ...any) { l.s.Debug(args...) }
func (l asynqLogger) Info(args ...any)  { l.s.Info(args...) }
func (l asynqLogger) Warn(args ...any)  { l.s.Warn(args...) }
func (l asynqLogger) Error(args ...any) { l.s.Error(args...) }
func (l asynqLogger) Fatal(args ...any) { l.s.Error(args...) }
