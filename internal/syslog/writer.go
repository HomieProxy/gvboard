package syslog

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"gvboard/internal/model"
)

const (
	bufferSize    = 2000
	batchSize     = 100
	flushInterval = time.Second
	// textMax v2_log 中 text 列的上限（65535 字节），留出余量。
	textMax = 60000
)

// Writer 异步批量写入 v2_log。缓冲区满时丢弃新日志（计数），不阻塞业务。
type Writer struct {
	log     *zap.Logger // 写入失败时记录到这里（不经过系统日志，避免循环）
	mu      sync.RWMutex
	db      *gorm.DB
	ch      chan model.Log
	dropped atomic.Int64
	flushMu sync.Mutex
	now     func() time.Time
}

// NewWriter 创建写入器。log 为不写入系统日志的基础日志。
func NewWriter(log *zap.Logger) *Writer {
	return &Writer{log: log.Named("syslog"), ch: make(chan model.Log, bufferSize), now: time.Now}
}

// SetDB 设置数据库。设置前的日志留在缓冲区中。写入使用静默的数据库日志，避免写日志产生的慢查询再写入日志。
func (w *Writer) SetDB(db *gorm.DB) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if db == nil {
		w.db = nil
		return
	}
	w.db = db.Session(&gorm.Session{Logger: gormlogger.Discard})
}

// Dropped 因缓冲区满被丢弃的日志数。
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

// Add 加入一条日志（不阻塞）。各文本列按 v2_log 列长截断。
func (w *Writer) Add(l model.Log) {
	l.Title = truncate(l.Title, textMax)
	l.URI = truncate(l.URI, 255)
	l.Method = truncate(l.Method, 11)
	l.Level = truncPtr(l.Level, 11)
	l.Host = truncPtr(l.Host, 255)
	l.IP = truncPtr(l.IP, 128)
	l.Data = truncPtr(l.Data, textMax)
	l.Context = truncPtr(l.Context, textMax)
	if l.CreatedAt == 0 {
		l.CreatedAt = w.now().Unix()
	}
	l.UpdatedAt = l.CreatedAt
	select {
	case w.ch <- l:
	default:
		w.dropped.Add(1)
	}
}

// Run 定时批量写入，阻塞直到 ctx 结束，退出前写入缓冲区中剩余的日志。
func (w *Writer) Run(ctx context.Context) {
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			w.Flush(fctx)
			cancel()
			return
		case <-t.C:
			w.Flush(ctx)
		}
	}
}

// Flush 立即写入缓冲区中的全部日志。数据库未设置时不处理。
func (w *Writer) Flush(ctx context.Context) {
	w.flushMu.Lock()
	defer w.flushMu.Unlock()
	w.mu.RLock()
	db := w.db
	w.mu.RUnlock()
	if db == nil {
		return
	}
	for {
		batch := make([]model.Log, 0, batchSize)
	fill:
		for len(batch) < batchSize {
			select {
			case l := <-w.ch:
				batch = append(batch, l)
			default:
				break fill
			}
		}
		if len(batch) == 0 {
			return
		}
		if err := db.WithContext(ctx).Create(&batch).Error; err != nil {
			w.log.Warn("写入系统日志失败", zap.Int("count", len(batch)), zap.Error(err))
			return
		}
		if len(batch) < batchSize {
			return
		}
	}
}

// truncate 截断到 max 字节以内，不截断多字节字符。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func truncPtr(p *string, max int) *string {
	if p == nil {
		return nil
	}
	s := truncate(*p, max)
	return &s
}

func ptr(s string) *string { return &s }

// jsonText 编码为 JSON，超长时截断（截断后不再是合法 JSON，仅供查看）。
func jsonText(v any) *string {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return ptr(truncate(string(b), textMax))
}
