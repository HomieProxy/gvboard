package adminstat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/model"
	"gvboard/internal/queue"
	"gvboard/internal/realip"
)

// scheduleTimeout 定时任务心跳超过该时间未更新即视为未运行（与原项目一致为 120 秒）。
const scheduleTimeout = 120

// QueueInfo 队列状态来源（queue.Queue）。
type QueueInfo interface {
	Stats() ([]queue.Stats, error)
	Workers() (servers, concurrency int, err error)
}

// Status 系统状态（对应原项目 admin/system/getSystemStatus）。
type Status struct {
	Schedule            bool   `json:"schedule"`              // 定时任务在运行
	ScheduleLastRuntime *int64 `json:"schedule_last_runtime"` // 定时任务最后运行时间
	Queue               bool   `json:"queue"`                 // 有队列处理器在运行（原项目 horizon）
	QueueWorkers        int    `json:"queue_workers"`         // 队列处理器进程数
	QueueConcurrency    int    `json:"queue_concurrency"`     // 总并发数
	SysLogDropped       int64  `json:"syslog_dropped"`        // 本实例因缓冲区满丢弃的系统日志数
	// RealIP 本实例最近 10～20 分钟的真实 IP 识别情况，warning 为 true 时可能没有把 CDN 的 IP 段加入可信代理。
	RealIP *realip.Report `json:"real_ip"`
}

// SystemStatus 读取定时任务心跳与队列处理器。
func SystemStatus(ctx context.Context, c *cache.Cache, q QueueInfo, now time.Time) (*Status, error) {
	st := &Status{}
	last, err := c.Get(ctx, c.KeyOf(cache.KeyScheduleLastCheckAt, nil)).Int64()
	switch {
	case err == nil:
		st.ScheduleLastRuntime = &last
		st.Schedule = now.Unix()-scheduleTimeout < last
	case !errors.Is(err, redis.Nil):
		return nil, err
	}
	if st.QueueWorkers, st.QueueConcurrency, err = q.Workers(); err != nil {
		return nil, err
	}
	st.Queue = st.QueueWorkers > 0
	return st, nil
}

// LogQuery 系统日志查询（对应原项目 admin/system/getSystemLog，只能按级别筛选），另可按标题关键字筛选。
type LogQuery struct {
	Level    string `form:"level"`
	Keyword  string `form:"keyword"`
	Current  int    `form:"current"`
	PageSize int    `form:"page_size"`
}

// LogPage 系统日志分页。
type LogPage struct {
	Data  []model.Log `json:"data"`
	Total int64       `json:"total"`
}

// Levels 系统日志级别（与原项目 Monolog 级别名一致）。
var Levels = []string{"DEBUG", "INFO", "NOTICE", "WARNING", "ERROR", "CRITICAL", "ALERT", "EMERGENCY"}

// likeEscaper 使用 ! 作为 LIKE 转义符（反斜杠在 MySQL 与 PostgreSQL 中含义不同）。
var likeEscaper = strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)

// Logs 系统日志，按时间倒序，每页 10～100 条。
func Logs(ctx context.Context, db *gorm.DB, q LogQuery) (*LogPage, error) {
	b := db.WithContext(ctx).Model(&model.Log{})
	if q.Level != "" {
		level := strings.ToUpper(q.Level)
		if !slices.Contains(Levels, level) {
			return nil, errInvalid
		}
		b = b.Where("level = ?", level)
	}
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		b = b.Where("title LIKE ? ESCAPE '!'", "%"+likeEscaper.Replace(kw)+"%")
	}
	out := &LogPage{Data: []model.Log{}}
	if err := b.Count(&out.Total).Error; err != nil {
		return nil, err
	}
	size := min(max(q.PageSize, 10), 100)
	cur := max(q.Current, 1)
	err := b.Order("created_at DESC").Order("id DESC").Offset((cur - 1) * size).Limit(size).Find(&out.Data).Error
	return out, err
}
