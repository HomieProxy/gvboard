package adminstat

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/httpx"
	"gvboard/internal/realip"
	"gvboard/internal/scheduler"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/syslog"
)

// Module 统计与系统状态路由。
type Module struct {
	DB       *gorm.DB
	Cache    *cache.Cache
	Settings *setting.Service
	Queue    QueueInfo
	SysLog   *syslog.Writer // 可为 nil
	Jobs     JobRunner      // 可为 nil，此时任务接口不挂载
	Auth     *auth.Service
	RealIP   *realip.Resolver // 可为 nil
	Now      func() time.Time // 测试注入，nil 时为 time.Now
	// Version、BuildTime 当前运行的程序版本与构建时间（构建时注入，本地编译为 dev 与空）。
	Version   string
	BuildTime string
}

// JobRunner 定时任务列表与手动执行（scheduler.Scheduler）。
type JobRunner interface {
	List(ctx context.Context) ([]scheduler.JobInfo, error)
	Start(ctx context.Context, name string) error
}

var (
	errJobNotFound = httpx.NotFoundErr("任务不存在")
	errJobRunning  = httpx.Conflict("任务正在执行，请稍后再试")
)

// Register 挂载管理端接口（{api}/{secure_path}）：
//
//	GET stat/overview                      首页概况
//	GET stat/orders                        最近 31 天每日统计
//	GET stat/server-rank?day=today|yesterday 节点流量排行
//	GET stat/user-rank?day=today|yesterday   用户流量排行
//	GET stat/user?user_id&subscription_id&current&page_size 用户流量记录
//	GET system/status                      定时任务与队列状态
//	GET system/queue                       各队列任务数
//	GET system/logs?level&keyword&current&page_size 系统日志
//	GET system/jobs                        定时任务列表与最近一次执行状态
//	POST system/jobs/:name/run             立即执行一次定时任务（后台执行）
func (m *Module) Register(r *server.Router) {
	a := m.Auth.AdminGroup(r.API, func() string { return m.Settings.String("secure_path") })
	a.GET("/stat/overview", func(c *gin.Context) { reply(c)(GetOverview(c.Request.Context(), m.DB, m.now())) })
	a.GET("/stat/orders", func(c *gin.Context) { reply(c)(DailyStats(c.Request.Context(), m.DB, 31)) })
	a.GET("/stat/server-rank", func(c *gin.Context) {
		reply(c)(ServerRanks(c.Request.Context(), m.DB, c.Query("day"), m.now()))
	})
	a.GET("/stat/user-rank", func(c *gin.Context) {
		reply(c)(UserRanks(c.Request.Context(), m.DB, c.Query("day"), m.now()))
	})
	a.GET("/stat/user", func(c *gin.Context) {
		var q UserStatQuery
		if c.ShouldBindQuery(&q) != nil {
			httpx.Fail(c, errInvalid)
			return
		}
		reply(c)(UserStats(c.Request.Context(), m.DB, q))
	})
	a.GET("/system/status", m.status)
	a.GET("/system/version", func(c *gin.Context) {
		httpx.OK(c, gin.H{"version": m.Version, "build_time": m.BuildTime})
	})
	a.GET("/system/queue", func(c *gin.Context) { reply(c)(m.Queue.Stats()) })
	a.GET("/system/logs", func(c *gin.Context) {
		var q LogQuery
		if c.ShouldBindQuery(&q) != nil {
			httpx.Fail(c, errInvalid)
			return
		}
		reply(c)(Logs(c.Request.Context(), m.DB, q))
	})
	if m.Jobs != nil {
		a.GET("/system/jobs", func(c *gin.Context) { reply(c)(m.Jobs.List(c.Request.Context())) })
		a.POST("/system/jobs/:name/run", m.runJob)
	}
}

func (m *Module) runJob(c *gin.Context) {
	err := m.Jobs.Start(c.Request.Context(), c.Param("name"))
	switch {
	case errors.Is(err, scheduler.ErrJobNotFound):
		httpx.Fail(c, errJobNotFound)
	case errors.Is(err, scheduler.ErrJobRunning):
		httpx.Fail(c, errJobRunning)
	case err != nil:
		httpx.Fail(c, err)
	default:
		httpx.OK(c, true)
	}
}

func (m *Module) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Module) status(c *gin.Context) {
	st, err := SystemStatus(c.Request.Context(), m.Cache, m.Queue, m.now())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if m.SysLog != nil {
		st.SysLogDropped = m.SysLog.Dropped()
	}
	if m.RealIP != nil {
		rep := m.RealIP.Report()
		st.RealIP = &rep
	}
	httpx.OK(c, st)
}

func reply(c *gin.Context) func(any, error) {
	return func(v any, err error) {
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, v)
	}
}
