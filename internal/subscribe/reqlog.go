package subscribe

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/model"
)

const (
	// logBufferMax 内存中待写入的日志上限，数据库写入跟不上时丢弃新日志，不影响订阅下发。
	logBufferMax = 20000
	logFlushSize = 1000
	logInterval  = 2 * time.Second
	logBatchSize = 500
	// logCleanBatch 清理过期日志时每次删除的行数，避免长时间锁表。
	logCleanBatch = 5000
)

type ruleHit struct {
	n    int64
	last int64
}

// logBuffer 请求日志与规则命中数的内存缓冲，由 RunLogWriter 定期写入数据库。
type logBuffer struct {
	mu      sync.Mutex
	entries []model.SubscribeLog
	hits    map[int64]ruleHit
	dropped int64
	signal  chan struct{}
}

func (b *logBuffer) notify() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.signal == nil {
		b.signal = make(chan struct{}, 1)
	}
	return b.signal
}

// record 加入一条请求日志（未开启日志时只累计规则命中数）。
func (s *Service) record(e *model.SubscribeLog, hits []int64) {
	enabled := s.Settings.Bool("subscribe_log_enable")
	b := &s.logs
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(hits) > 0 {
		if b.hits == nil {
			b.hits = map[int64]ruleHit{}
		}
		for _, id := range hits {
			h := b.hits[id]
			h.n++
			h.last = e.CreatedAt
			b.hits[id] = h
		}
	}
	if !enabled {
		return
	}
	if len(b.entries) >= logBufferMax {
		b.dropped++
		return
	}
	b.entries = append(b.entries, *e)
	if len(b.entries) >= logFlushSize && b.signal != nil {
		select {
		case b.signal <- struct{}{}:
		default:
		}
	}
}

// RunLogWriter 定期把请求日志与规则命中数写入数据库；ctx 结束时写入剩余数据后返回。
func (s *Service) RunLogWriter(ctx context.Context) {
	signal := s.logs.notify()
	t := time.NewTicker(logInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			s.FlushLogs(fctx)
			cancel()
			return
		case <-t.C:
		case <-signal:
		}
		s.FlushLogs(ctx)
	}
}

// FlushLogs 立即写入缓冲中的日志与规则命中数。写入失败的日志直接丢弃（只影响日志，不影响下发）。
func (s *Service) FlushLogs(ctx context.Context) {
	b := &s.logs
	b.mu.Lock()
	entries, hits, dropped := b.entries, b.hits, b.dropped
	b.entries, b.hits, b.dropped = nil, nil, 0
	b.mu.Unlock()

	if dropped > 0 {
		s.log().Warn("订阅请求日志缓冲已满，部分日志被丢弃", zap.Int64("dropped", dropped))
	}
	if len(entries) > 0 {
		if err := s.DB.WithContext(ctx).CreateInBatches(entries, logBatchSize).Error; err != nil {
			s.log().Warn("写入订阅请求日志失败", zap.Int("count", len(entries)), zap.Error(err))
		}
	}
	for id, h := range hits {
		err := s.DB.WithContext(ctx).Model(&model.SubscribeRule{}).Where("id = ?", id).UpdateColumns(map[string]any{
			"hit_count":   gorm.Expr("hit_count + ?", h.n),
			"last_hit_at": h.last,
		}).Error
		if err != nil {
			s.log().Warn("更新订阅风控规则命中数失败", zap.Int64("rule_id", id), zap.Error(err))
		}
	}
}

func truncate(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// newLogEntry 请求日志的公共字段。额外记录的请求头由设置 subscribe_log_headers 指定。
func (s *Service) newLogEntry(sub *model.Subscription, a Access, client string) *model.SubscribeLog {
	e := &model.SubscribeLog{
		SubscriptionID: sub.ID,
		UserID:         sub.UserID,
		IP:             truncate(a.IP, 64),
		UserAgent:      truncate(a.UserAgent, 255),
		Client:         truncate(client, 32),
		CreatedAt:      s.now().Unix(),
	}
	if a.Token != "" {
		t := truncate(a.Token, 64)
		e.Token = &t
	}
	if a.Path != "" {
		p := truncate(a.Path, 128)
		e.Path = &p
	}
	if names := s.Settings.Strings("subscribe_log_headers"); len(names) > 0 {
		h := map[string]string{}
		for _, n := range names {
			if v := a.header(n); v != "" {
				h[n] = truncate(v, 255)
			}
		}
		if len(h) > 0 {
			if b, err := json.Marshal(h); err == nil {
				str := string(b)
				e.Headers = &str
			}
		}
	}
	return e
}

// countInvalid 无效 token 请求只按天计数，不记录明细（决策 D46）。
func (s *Service) countInvalid(ctx context.Context) {
	if !s.Settings.Bool("subscribe_log_enable") {
		return
	}
	days := s.Settings.Int("subscribe_log_retention_days")
	if days <= 0 {
		days = 30
	}
	k := s.Cache.KeyOf(cache.KeySubscribeInvalid, s.now().Format("20060102"))
	pipe := s.Cache.TxPipeline()
	pipe.Incr(ctx, k)
	pipe.Expire(ctx, k, time.Duration(days+1)*24*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		s.log().Warn("无效订阅 token 计数失败", zap.Error(err))
	}
}

// ---------- 管理端查询（接口在单元 14 挂载） ----------

// LogQuery 请求日志查询条件，零值表示不筛选。
type LogQuery struct {
	SubscriptionID int64  `form:"subscription_id"`
	UserID         int64  `form:"user_id"`
	IP             string `form:"ip"`
	Client         string `form:"client"`
	UserAgent      string `form:"user_agent"` // 包含匹配
	Result         *int   `form:"result"`
	RuleID         int64  `form:"rule_id"`
	Token          string `form:"token"`
	Since          int64  `form:"since"`
	Until          int64  `form:"until"`
	Current        int    `form:"current"`
	PageSize       int    `form:"page_size"`
}

// LogView 请求日志与所属用户的邮箱（用户已删除时为空）、订阅名称。
type LogView struct {
	model.SubscribeLog
	Email            string `json:"email"`
	SubscriptionName string `json:"subscription_name"`
}

// subscriptionNames 订阅显示名：有名称用名称，否则用其当前套餐名（与管理端其他页面一致）；订阅已删除时没有对应项。
func (s *Service) subscriptionNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var subs []model.Subscription
	if err := s.DB.WithContext(ctx).Select("id", "name", "plan_id").Where("id IN ?", ids).Find(&subs).Error; err != nil {
		return nil, err
	}
	var planIDs []int64
	for _, sub := range subs {
		if sub.Name == "" && sub.PlanID != nil {
			planIDs = append(planIDs, *sub.PlanID)
		}
	}
	plans := map[int64]string{}
	if len(planIDs) > 0 {
		var ps []model.Plan
		if err := s.DB.WithContext(ctx).Select("id", "name").Where("id IN ?", planIDs).Find(&ps).Error; err != nil {
			return nil, err
		}
		for _, p := range ps {
			plans[p.ID] = p.Name
		}
	}
	for _, sub := range subs {
		out[sub.ID] = sub.Name
		if sub.Name == "" && sub.PlanID != nil {
			out[sub.ID] = plans[*sub.PlanID]
		}
	}
	return out, nil
}

// LogPage 请求日志分页结果。
type LogPage struct {
	Data  []LogView `json:"data"`
	Total int64     `json:"total"`
}

// likeEscaper 使用 ! 作为 LIKE 转义符：反斜杠在 MySQL 与 PostgreSQL 字符串字面量中的含义不同。
var likeEscaper = strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)

// Logs 按条件分页查询请求日志，按时间倒序。
func (s *Service) Logs(ctx context.Context, q LogQuery) (*LogPage, error) {
	db := s.DB.WithContext(ctx).Model(&model.SubscribeLog{})
	if q.SubscriptionID > 0 {
		db = db.Where("subscription_id = ?", q.SubscriptionID)
	}
	if q.UserID > 0 {
		db = db.Where("user_id = ?", q.UserID)
	}
	if q.IP != "" {
		db = db.Where("ip = ?", q.IP)
	}
	if q.Client != "" {
		db = db.Where("client = ?", q.Client)
	}
	if q.UserAgent != "" {
		db = db.Where("user_agent LIKE ? ESCAPE '!'", "%"+likeEscaper.Replace(q.UserAgent)+"%")
	}
	if q.Result != nil {
		db = db.Where("result = ?", *q.Result)
	}
	if q.RuleID > 0 {
		db = db.Where("rule_id = ?", q.RuleID)
	}
	if q.Token != "" {
		db = db.Where("token = ?", q.Token)
	}
	if q.Since > 0 {
		db = db.Where("created_at >= ?", q.Since)
	}
	if q.Until > 0 {
		db = db.Where("created_at < ?", q.Until)
	}
	out := &LogPage{Data: []LogView{}}
	if err := db.Count(&out.Total).Error; err != nil {
		return nil, err
	}
	cur, size := q.Current, q.PageSize
	if cur < 1 {
		cur = 1
	}
	if size < 10 {
		size = 10
	}
	if size > 100 {
		size = 100
	}
	var rows []model.SubscribeLog
	if err := db.Order("id DESC").Offset((cur - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	subIDs := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
		subIDs = append(subIDs, r.SubscriptionID)
	}
	names, err := s.subscriptionNames(ctx, subIDs)
	if err != nil {
		return nil, err
	}
	var users []model.User
	if len(ids) > 0 {
		if err := s.DB.WithContext(ctx).Select("id", "email").Where("id IN ?", ids).Find(&users).Error; err != nil {
			return nil, err
		}
	}
	emails := make(map[int64]string, len(users))
	for _, u := range users {
		emails[u.ID] = u.Email
	}
	for _, r := range rows {
		out.Data = append(out.Data, LogView{SubscribeLog: r, Email: emails[r.UserID], SubscriptionName: names[r.SubscriptionID]})
	}
	return out, nil
}

// SubStat 单个订阅在一段时间内的请求统计，用于发现分享或异常拉取。
type SubStat struct {
	SubscriptionID   int64  `json:"subscription_id"`
	SubscriptionName string `json:"subscription_name" gorm:"-"`
	UserID           int64  `json:"user_id"`
	IPs              int64  `json:"ips"`
	Requests         int64  `json:"requests"`
	Blocked          int64  `json:"blocked"`
	LastAt           int64  `json:"last_at"`
}

// SubStats 按订阅汇总 since 之后的请求：不同 IP 数、请求数、被拦截数，按 IP 数（或请求数）倒序取前 limit 个。
func (s *Service) SubStats(ctx context.Context, since int64, orderBy string, limit int) ([]SubStat, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	order := "ips DESC"
	if orderBy == "requests" {
		order = "requests DESC"
	}
	var out []SubStat
	err := s.DB.WithContext(ctx).Model(&model.SubscribeLog{}).
		Select("subscription_id, MAX(user_id) AS user_id, COUNT(DISTINCT ip) AS ips, COUNT(*) AS requests, "+
			"COUNT(CASE WHEN result IN (2, 3, 4, 5) THEN 1 END) AS blocked, MAX(created_at) AS last_at").
		Where("created_at >= ?", since).
		Group("subscription_id").
		Order(order + ", subscription_id ASC").
		Limit(limit).
		Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []SubStat{}
	}
	ids := make([]int64, 0, len(out))
	for _, st := range out {
		ids = append(ids, st.SubscriptionID)
	}
	names, err := s.subscriptionNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].SubscriptionName = names[out[i].SubscriptionID]
	}
	return out, nil
}

// DayCount 某天的计数。
type DayCount struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// InvalidCounts 最近 days 天每天的无效 token 请求次数（含今天，按日期升序）。
func (s *Service) InvalidCounts(ctx context.Context, days int) ([]DayCount, error) {
	if days <= 0 || days > 180 {
		days = 30
	}
	now := s.now()
	keys := make([]string, days)
	out := make([]DayCount, days)
	for i := range days {
		d := now.AddDate(0, 0, i-days+1).Format("20060102")
		keys[i] = s.Cache.KeyOf(cache.KeySubscribeInvalid, d)
		out[i].Date = d
	}
	vals, err := s.Cache.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, v := range vals {
		if str, ok := v.(string); ok {
			out[i].Count, _ = strconv.ParseInt(str, 10, 64)
		}
	}
	return out, nil
}

// CleanLogs 删除超过保留天数的请求日志，分批删除避免长时间锁表，返回删除行数（定时任务在单元 15 注册）。
func (s *Service) CleanLogs(ctx context.Context) (int64, error) {
	days := s.Settings.Int("subscribe_log_retention_days")
	if days <= 0 {
		days = 30
	}
	before := s.now().Unix() - days*86400
	var total int64
	for {
		var ids []int64
		err := s.DB.WithContext(ctx).Model(&model.SubscribeLog{}).
			Where("created_at < ?", before).Order("id ASC").Limit(logCleanBatch).Pluck("id", &ids).Error
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		res := s.DB.WithContext(ctx).Where("id IN ?", ids).Delete(&model.SubscribeLog{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if len(ids) < logCleanBatch {
			return total, nil
		}
	}
}
