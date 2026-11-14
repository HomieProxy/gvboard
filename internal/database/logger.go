package database

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// gormLogger 把 GORM 日志转发到 zap。SQL 语句只在 Info 级别（调试模式）输出，避免日志泄露数据。
type gormLogger struct {
	log           *zap.Logger
	level         gormlogger.LogLevel
	slowThreshold time.Duration
}

func newGormLogger(log *zap.Logger, level gormlogger.LogLevel, slow time.Duration) gormlogger.Interface {
	return &gormLogger{log: log.Named("db").WithOptions(zap.AddCallerSkip(3)), level: level, slowThreshold: slow}
}

func (l *gormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	n := *l
	n.level = level
	return &n
}

// ParamsFilter 失败与慢查询日志中的 SQL 只保留占位符，不写入参数值（邮箱、密码哈希、令牌、支付配置等）；
// 调试模式（Info 级别）保留参数便于排查。
func (l *gormLogger) ParamsFilter(_ context.Context, sql string, params ...any) (string, []any) {
	if l.level >= gormlogger.Info {
		return sql, params
	}
	return sql, nil
}

func (l *gormLogger) Info(_ context.Context, msg string, args ...any) {
	if l.level >= gormlogger.Info {
		l.log.Sugar().Infof(msg, args...)
	}
}

func (l *gormLogger) Warn(_ context.Context, msg string, args ...any) {
	if l.level >= gormlogger.Warn {
		l.log.Sugar().Warnf(msg, args...)
	}
}

func (l *gormLogger) Error(_ context.Context, msg string, args ...any) {
	if l.level >= gormlogger.Error {
		l.log.Sugar().Errorf(msg, args...)
	}
}

func (l *gormLogger) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= gormlogger.Silent {
		return
	}
	elapsed := time.Since(begin)
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && l.level >= gormlogger.Error:
		sql, rows := fc()
		l.log.Error("SQL 执行失败", zap.Error(err), zap.Duration("elapsed", elapsed), zap.Int64("rows", rows), zap.String("sql", sql))
	case l.slowThreshold > 0 && elapsed > l.slowThreshold && l.level >= gormlogger.Warn:
		sql, rows := fc()
		l.log.Warn("慢查询", zap.Duration("elapsed", elapsed), zap.Int64("rows", rows), zap.String("sql", sql))
	case l.level >= gormlogger.Info:
		sql, rows := fc()
		l.log.Debug("SQL", zap.Duration("elapsed", elapsed), zap.Int64("rows", rows), zap.String("sql", sql))
	}
}
