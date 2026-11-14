// Package adminstat 管理端统计与系统状态（对应原项目 Admin/StatController、Admin/SystemController，决策 D53）。
package adminstat

import (
	"context"
	"sort"
	"time"

	"gorm.io/gorm"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/node"
)

const gb = 1073741824

var errInvalid = httpx.Validation("参数有误")

// Overview 管理端首页概况（对应原项目 admin/stat/getOverride），金额单位为分。
// 时间范围与原项目一致为 [开始, 当前时间)。在线数按订阅最近 10 分钟有流量上报统计（多订阅后流量记录在订阅上）。
type Overview struct {
	OnlineUser                int64 `json:"online_user"`         // 在线用户（任一订阅在线）
	OnlineSubscription        int64 `json:"online_subscription"` // 在线订阅
	DayIncome                 int64 `json:"day_income"`
	MonthIncome               int64 `json:"month_income"`
	LastMonthIncome           int64 `json:"last_month_income"`
	DayRegisterTotal          int64 `json:"day_register_total"`
	MonthRegisterTotal        int64 `json:"month_register_total"`
	TicketPendingTotal        int64 `json:"ticket_pending_total"`
	CommissionPendingTotal    int64 `json:"commission_pending_total"`
	CommissionMonthPayout     int64 `json:"commission_month_payout"`
	CommissionLastMonthPayout int64 `json:"commission_last_month_payout"`
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// GetOverview 计算概况。
func GetOverview(ctx context.Context, db *gorm.DB, now time.Time) (*Overview, error) {
	db = db.WithContext(ctx)
	day := dayStart(now).Unix()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	lastMonth := month.AddDate(0, -1, 0).Unix()
	cur := now.Unix()
	o := &Overview{}
	sum := func(dst *int64, m any, col string, where string, args ...any) error {
		return db.Model(m).Where(where, args...).Select("COALESCE(SUM(" + col + "), 0)").Scan(dst).Error
	}
	count := func(dst *int64, m any, where string, args ...any) error {
		return db.Model(m).Where(where, args...).Count(dst).Error
	}
	paid := "created_at >= ? AND created_at < ? AND status NOT IN (0, 2)"
	steps := []func() error{
		func() error { return sum(&o.DayIncome, &model.Order{}, "total_amount", paid, day, cur) },
		func() error { return sum(&o.MonthIncome, &model.Order{}, "total_amount", paid, month.Unix(), cur) },
		func() error {
			return sum(&o.LastMonthIncome, &model.Order{}, "total_amount", paid, lastMonth, month.Unix())
		},
		func() error {
			return sum(&o.CommissionMonthPayout, &model.CommissionLog{}, "get_amount", "created_at >= ? AND created_at < ?", month.Unix(), cur)
		},
		func() error {
			return sum(&o.CommissionLastMonthPayout, &model.CommissionLog{}, "get_amount", "created_at >= ? AND created_at < ?", lastMonth, month.Unix())
		},
		func() error {
			return count(&o.DayRegisterTotal, &model.User{}, "created_at >= ? AND created_at < ?", day, cur)
		},
		func() error {
			return count(&o.MonthRegisterTotal, &model.User{}, "created_at >= ? AND created_at < ?", month.Unix(), cur)
		},
		func() error { return count(&o.OnlineSubscription, &model.Subscription{}, "t >= ?", cur-600) },
		func() error {
			return db.Model(&model.Subscription{}).Where("t >= ?", cur-600).Distinct("user_id").Count(&o.OnlineUser).Error
		},
		func() error { return count(&o.TicketPendingTotal, &model.Ticket{}, "status = 0 AND reply_status = 0") },
		func() error {
			return count(&o.CommissionPendingTotal, &model.Order{},
				"commission_status = 0 AND invite_user_id IS NOT NULL AND status NOT IN (0, 2) AND commission_balance > 0")
		},
	}
	for _, f := range steps {
		if err := f(); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// DayStat 每日统计（对应原项目 admin/stat/getOrder，数据来自每日统计任务写入的 v2_stat），金额单位为分。
type DayStat struct {
	RecordAt        int64 `json:"record_at"`
	RegisterCount   int64 `json:"register_count"`
	PaidCount       int64 `json:"paid_count"`
	PaidTotal       int64 `json:"paid_total"`
	CommissionCount int64 `json:"commission_count"`
	CommissionTotal int64 `json:"commission_total"`
}

// DailyStats 最近 days 天的每日统计，按日期升序（原项目固定 31 天）。
func DailyStats(ctx context.Context, db *gorm.DB, days int) ([]DayStat, error) {
	var rows []model.Stat
	err := db.WithContext(ctx).Where("record_type = ?", "d").Order("record_at DESC").Limit(days).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]DayStat, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = DayStat{
			RecordAt: r.RecordAt, RegisterCount: r.RegisterCount, PaidCount: r.PaidCount, PaidTotal: r.PaidTotal,
			CommissionCount: r.CommissionCount, CommissionTotal: r.CommissionTotal,
		}
	}
	return out, nil
}

// rankRange today 为 [今天 0 点, 当前时间)，yesterday 为 [昨天 0 点, 今天 0 点)，与原项目一致。
func rankRange(day string, now time.Time) (int64, int64, error) {
	today := dayStart(now)
	switch day {
	case "today":
		return today.Unix(), now.Unix(), nil
	case "yesterday":
		return today.AddDate(0, 0, -1).Unix(), today.Unix(), nil
	}
	return 0, 0, errInvalid
}

// ServerRank 节点流量排行的一项，Total 单位为 GB。
type ServerRank struct {
	ServerID   int64   `json:"server_id"`
	ServerType string  `json:"server_type"`
	ServerName string  `json:"server_name"`
	U          int64   `json:"u"`
	D          int64   `json:"d"`
	Total      float64 `json:"total"`
}

// ServerRanks 节点流量前 15（对应原项目 getServerTodayRank / getServerLastRank）。
func ServerRanks(ctx context.Context, db *gorm.DB, day string, now time.Time) ([]ServerRank, error) {
	from, to, err := rankRange(day, now)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ServerID   int64
		ServerType string
		U, D       int64
	}
	err = db.WithContext(ctx).Model(&model.StatServer{}).Select("server_id, server_type, u, d").
		Where("record_at >= ? AND record_at < ? AND record_type = ?", from, to, "d").
		Order("u + d DESC").Limit(15).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]ServerRank, 0, len(rows))
	for _, r := range rows {
		typ := r.ServerType
		if typ == "v2ray" {
			typ = model.ServerTypeVmess
		}
		item := ServerRank{ServerID: r.ServerID, ServerType: r.ServerType, U: r.U, D: r.D, Total: float64(r.U+r.D) / gb}
		s, err := node.Find(ctx, db, typ, r.ServerID)
		if err != nil {
			return nil, err
		}
		if s != nil {
			item.ServerName = s.Common().Name
		}
		out = append(out, item)
	}
	return out, nil
}

// UserRank 用户流量排行的一项，Total 为按倍率折算后的 GB。
type UserRank struct {
	UserID int64   `json:"user_id"`
	Email  string  `json:"email"`
	U      int64   `json:"u"`
	D      int64   `json:"d"`
	Total  float64 `json:"total"`
}

// UserRanks 用户流量前 15（对应原项目 getUserTodayRank / getUserLastRank）：
// 取实际流量最高的 30 条记录，按倍率折算后按用户合并（与原项目一致，u、d 为合并后第一条记录的值）。
func UserRanks(ctx context.Context, db *gorm.DB, day string, now time.Time) ([]UserRank, error) {
	from, to, err := rankRange(day, now)
	if err != nil {
		return nil, err
	}
	var rows []model.StatUser
	err = db.WithContext(ctx).Select("user_id", "server_rate", "u", "d").
		Where("record_at >= ? AND record_at < ? AND record_type = ?", from, to, "d").
		Order("u + d DESC").Limit(30).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	index := map[int64]int{}
	var out []UserRank
	for _, r := range rows {
		total := float64(r.U+r.D) * r.ServerRate / gb
		if i, ok := index[r.UserID]; ok {
			out[i].Total += total
			continue
		}
		index[r.UserID] = len(out)
		out = append(out, UserRank{UserID: r.UserID, U: r.U, D: r.D, Total: total})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Total > out[j].Total })
	if len(out) > 15 {
		out = out[:15]
	}
	ids := make([]int64, len(out))
	for i := range out {
		ids[i] = out[i].UserID
	}
	emails := map[int64]string{}
	if len(ids) > 0 {
		var users []model.User
		if err := db.WithContext(ctx).Select("id", "email").Where("id IN ?", ids).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, u := range users {
			emails[u.ID] = u.Email
		}
	}
	for i := range out {
		if e, ok := emails[out[i].UserID]; ok {
			out[i].Email = e
		} else {
			out[i].Email = "null"
		}
	}
	if out == nil {
		out = []UserRank{}
	}
	return out, nil
}

// UserStatQuery 用户流量记录查询（对应原项目 admin/stat/getStatUser），可按订阅筛选。
type UserStatQuery struct {
	UserID         int64 `form:"user_id"`
	SubscriptionID int64 `form:"subscription_id"`
	Current        int   `form:"current"`
	PageSize       int   `form:"page_size"`
}

// UserStatPage 用户流量记录分页。
type UserStatPage struct {
	Data  []model.StatUser `json:"data"`
	Total int64            `json:"total"`
}

// UserStats 用户流量记录，按日期倒序，每页 10～100 条。
func UserStats(ctx context.Context, db *gorm.DB, q UserStatQuery) (*UserStatPage, error) {
	if q.UserID <= 0 {
		return nil, errInvalid
	}
	b := db.WithContext(ctx).Model(&model.StatUser{}).Where("user_id = ?", q.UserID)
	if q.SubscriptionID > 0 {
		b = b.Where("subscription_id = ?", q.SubscriptionID)
	}
	out := &UserStatPage{Data: []model.StatUser{}}
	if err := b.Count(&out.Total).Error; err != nil {
		return nil, err
	}
	size := min(max(q.PageSize, 10), 100)
	cur := max(q.Current, 1)
	err := b.Order("record_at DESC").Order("id DESC").Offset((cur - 1) * size).Limit(size).Find(&out.Data).Error
	return out, err
}
