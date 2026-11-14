package legacy

import (
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gvboard/internal/adminstat"
	"gvboard/internal/model"
)

// ---------- 统计 ----------

func (m *Module) adminStatOverride(c *gin.Context) {
	o, err := adminstat.GetOverview(c.Request.Context(), m.DB, time.Now())
	m.respond(c, o, err)
}

// adminStatOrder 最近 31 天每日统计，每天 5 项，按日期升序（与原项目整体倒序后的顺序一致）。
func (m *Module) adminStatOrder(c *gin.Context) {
	days, err := adminstat.DailyStats(c.Request.Context(), m.DB, 31)
	if err != nil {
		m.fail(c, err)
		return
	}
	out := make([]gin.H, 0, len(days)*5)
	for _, d := range days {
		date := time.Unix(d.RecordAt, 0).Format("01-02")
		out = append(out,
			gin.H{"type": "佣金笔数(已发放)", "date": date, "value": d.CommissionCount},
			gin.H{"type": "佣金金额(已发放)", "date": date, "value": float64(d.CommissionTotal) / 100},
			gin.H{"type": "收款笔数", "date": date, "value": d.PaidCount},
			gin.H{"type": "收款金额", "date": date, "value": float64(d.PaidTotal) / 100},
			gin.H{"type": "注册人数", "date": date, "value": d.RegisterCount},
		)
	}
	ok(c, out)
}

func (m *Module) adminServerRank(day string) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := adminstat.ServerRanks(c.Request.Context(), m.DB, day, time.Now())
		m.respond(c, list, err)
	}
}

func (m *Module) adminUserRank(day string) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := adminstat.UserRanks(c.Request.Context(), m.DB, day, time.Now())
		m.respond(c, list, err)
	}
}

func (m *Module) adminStatUser(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	if v.required("user_id", "validation.required") {
		v.integer("user_id", "validation.integer")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	uid, _ := in.int("user_id")
	cur, size := in.page()
	page, err := adminstat.UserStats(c.Request.Context(), m.DB, adminstat.UserStatQuery{UserID: uid, Current: cur, PageSize: size})
	if err != nil {
		m.fail(c, err)
		return
	}
	paged(c, page.Data, page.Total)
}

// ---------- 系统 ----------

func (m *Module) adminSystemStatus(c *gin.Context) {
	st, err := adminstat.SystemStatus(c.Request.Context(), m.Cache, m.Queue, time.Now())
	if err != nil {
		m.fail(c, err)
		return
	}
	ok(c, gin.H{"schedule": st.Schedule, "horizon": st.Queue, "schedule_last_runtime": st.ScheduleLastRuntime})
}

// adminQueueStats 原项目为 Horizon 统计，这里由队列状态折算出同名字段。
func (m *Module) adminQueueStats(c *gin.Context) {
	stats, err := m.Queue.Stats()
	if err != nil {
		m.fail(c, err)
		return
	}
	workers, concurrency, err := m.Queue.Workers()
	if err != nil {
		m.fail(c, err)
		return
	}
	var failed, processed int
	var busiest any
	most := 0
	for _, s := range stats {
		failed += s.Failed
		processed += s.Processed
		if s.Processed > most {
			most, busiest = s.Processed, s.Queue
		}
	}
	now := time.Now().UTC()
	minutes := now.Hour()*60 + now.Minute() + 1
	ok(c, gin.H{
		"failedJobs":             failed,
		"jobsPerMinute":          processed / minutes,
		"pausedMasters":          0,
		"periods":                gin.H{"failedJobs": 10080, "recentJobs": 60},
		"processes":              concurrency,
		"queueWithMaxRuntime":    nil,
		"queueWithMaxThroughput": busiest,
		"recentJobs":             processed,
		"status":                 workers > 0,
		"wait":                   gin.H{},
	})
}

func (m *Module) adminQueueWorkload(c *gin.Context) {
	stats, err := m.Queue.Stats()
	if err != nil {
		m.fail(c, err)
		return
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].Queue < stats[j].Queue })
	out := make([]gin.H, 0, len(stats))
	for _, s := range stats {
		out = append(out, gin.H{"name": s.Queue, "length": s.Pending + s.Scheduled + s.Retry, "wait": 0, "processes": s.Active})
	}
	ok(c, out)
}

// adminQueueMasters 原项目直接返回 Horizon 的主进程列表（没有 data 包装），没有运行中的处理器时为空数组。
func (m *Module) adminQueueMasters(c *gin.Context) {
	workers, concurrency, err := m.Queue.Workers()
	if err != nil {
		m.fail(c, err)
		return
	}
	if workers == 0 {
		c.JSON(http.StatusOK, []any{})
		return
	}
	const name = "gvboard"
	c.JSON(http.StatusOK, gin.H{name: gin.H{
		"name": name, "status": "running",
		"supervisors": []gin.H{{
			"name": name + ":supervisor", "master": name, "status": "running",
			"processes": gin.H{"redis:default": concurrency},
		}},
	}})
}

var logConditions = []string{"in", "is", "not", "like", "lt", "gt"}

var logLikeEscaper = strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)

// adminSystemLog 系统日志，只允许按 level 筛选（与原项目一致）。
func (m *Module) adminSystemLog(c *gin.Context) {
	in := params(c)
	filters, _ := in.values("filter")
	v := newValidator(in)
	for i, f := range filters {
		fm, _ := f.(map[string]any)
		fi := input(fm)
		p := "filter." + strconv.Itoa(i) + "."
		if v.check(p+"key", fi.raw("key") != nil, "validation.required") {
			v.check(p+"key", fi.str("key") == "level", "validation.in")
		}
		if v.check(p+"condition", fi.raw("condition") != nil, "validation.required") {
			v.check(p+"condition", slices.Contains(logConditions, fi.str("condition")), "validation.in")
		}
		v.check(p+"value", newValidator(fi).required("value", ""), "validation.required")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	q := m.DB.WithContext(c.Request.Context()).Model(&model.Log{})
	for _, f := range filters {
		fi := input(f.(map[string]any))
		val := fi.str("value")
		switch fi.str("condition") {
		case "in":
			if list := fi.strList("value"); len(list) > 0 {
				q = q.Where("level IN ?", list)
			} else {
				q = q.Where("1 = 0")
			}
		case "is":
			q = q.Where("level = ?", val)
		case "not":
			q = q.Where("level <> ?", val)
		case "like":
			q = q.Where("level LIKE ? ESCAPE '!'", "%"+logLikeEscaper.Replace(val)+"%")
		case "lt":
			q = q.Where("level < ?", val)
		case "gt":
			q = q.Where("level > ?", val)
		}
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		m.fail(c, err)
		return
	}
	cur, _ := in.page()
	size := 10
	if n, valid := in.int("page_size"); valid && n > 10 {
		size = int(min(n, 100))
	}
	rows := []model.Log{}
	err := q.Order("created_at DESC").Order("id DESC").Offset((cur - 1) * size).Limit(size).Find(&rows).Error
	if err != nil {
		m.fail(c, err)
		return
	}
	paged(c, rows, total)
}
