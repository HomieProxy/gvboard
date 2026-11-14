// Package app 负责组装应用依赖（配置、日志、数据库、缓存、设置）。
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gorm.io/gorm"

	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/database"
	"gvboard/internal/i18n"
	"gvboard/internal/logger"
	"gvboard/internal/mail"
	"gvboard/internal/queue"
	"gvboard/internal/scheduler"
	"gvboard/internal/setting"
	"gvboard/internal/syslog"
)

// App 应用依赖容器。
type App struct {
	Config   *config.Config
	Log      *zap.Logger
	DB       *gorm.DB
	Cache    *cache.Cache // WithoutCache 时为 nil
	Settings *setting.Service
	I18n     *i18n.Translator
	Mailer   *mail.Mailer
	// 以下依赖 Redis，WithoutCache 时为 nil / 零值
	Queue     *queue.Queue
	Mail      mail.Dispatcher
	Scheduler *scheduler.Scheduler
	Auth      *auth.Service
	SysLog    *syslog.Writer // 不连接 Redis 时为 nil
}

type options struct {
	noCache      bool
	skipSettings bool
}

// Option 初始化选项。
type Option func(*options)

// WithoutCache 不连接 Redis（迁移、导入等命令行工具使用）。
func WithoutCache() Option { return func(o *options) { o.noCache = true } }

// SkipSettingsLoad 不加载设置（执行迁移前设置表可能尚不存在）。
func SkipSettingsLoad() Option { return func(o *options) { o.skipSettings = true } }

// New 加载配置并初始化依赖。任一依赖失败时释放已打开的资源。
func New(cfgPath string, opts ...Option) (*App, error) {
	var o options
	for _, fn := range opts {
		fn(&o)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(cfg.App.Timezone)
	if err != nil {
		return nil, err
	}
	time.Local = loc
	// 插件在创建主路由之前就会建立自己的 gin 引擎，模式必须在此统一设置，否则会输出调试路由表
	if cfg.App.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	log, err := logger.New(cfg.Log)
	if err != nil {
		return nil, err
	}
	a := &App{Config: cfg}
	// 警告及以上日志另写入 v2_log（决策 D53）。命令行工具（不连接 Redis）不写入。
	if !o.noCache {
		a.SysLog = syslog.NewWriter(log)
		log = zap.New(zapcore.NewTee(log.Core(), a.SysLog.Core(zapcore.WarnLevel)), zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	}
	zap.ReplaceGlobals(log)
	a.Log = log
	if a.DB, err = database.Open(cfg.Database, log, cfg.App.Debug); err != nil {
		a.Close()
		return nil, err
	}
	if a.SysLog != nil {
		a.SysLog.SetDB(a.DB)
	}
	if !o.noCache {
		if a.Cache, err = cache.Open(cfg.Redis); err != nil {
			a.Close()
			return nil, err
		}
	}
	a.Settings = setting.New(a.DB, a.Cache, log)
	if !o.skipSettings {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Settings.Load(ctx); err != nil {
			a.Close()
			return nil, errors.Join(err, errors.New("请先执行 gvboard migrate"))
		}
	}

	if a.I18n, err = i18n.New(cfg.App.Locale); err != nil {
		a.Close()
		return nil, fmt.Errorf("app.locale: %w", err)
	}
	if a.Mailer, err = mail.New(a.Settings); err != nil {
		a.Close()
		return nil, err
	}
	if a.Cache != nil {
		a.Queue = queue.New(cfg.Redis, log)
		a.Mail = mail.RegisterTask(a.Queue, a.Mailer, a.DB, log)
		beatKey := a.Cache.KeyOf(cache.KeyScheduleLastCheckAt, nil)
		a.Scheduler = scheduler.New(a.Cache, log, func(ctx context.Context, now time.Time) {
			_ = a.Cache.Set(ctx, beatKey, now.Unix(), 0).Err()
		})
		a.Auth = auth.New(a.DB, a.Cache, cfg.App.Key)
	}
	return a, nil
}

// Close 释放资源。
func (a *App) Close() error {
	var errs []error
	if a.SysLog != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		a.SysLog.Flush(ctx)
		cancel()
	}
	if a.Queue != nil {
		errs = append(errs, a.Queue.Close())
	}
	if a.Cache != nil {
		errs = append(errs, a.Cache.Close())
	}
	if a.DB != nil {
		errs = append(errs, database.Close(a.DB))
	}
	if a.Log != nil {
		_ = a.Log.Sync()
	}
	return errors.Join(errs...)
}
