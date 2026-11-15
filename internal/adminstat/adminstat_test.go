package adminstat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/queue"
	"gvboard/internal/scheduler"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/testutil"
)

type fakeQueue struct {
	workers, concurrency int
	err                  error
}

func (f *fakeQueue) Stats() ([]queue.Stats, error) {
	return []queue.Stats{{Queue: "order_handle", Pending: 2}}, nil
}

func (f *fakeQueue) Workers() (int, int, error) { return f.workers, f.concurrency, f.err }

type env struct {
	db    *gorm.DB
	cache *cache.Cache
	st    *setting.Service
	auth  *auth.Service
	now   time.Time
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &env{
		db: db, cache: c, st: st, auth: auth.New(db, c, "base64:YWRtaW5zdGF0LXRlc3Qta2V5LTAxMjM0NTY3ODk="),
		now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.Local),
	}
}

func eachDriver(t *testing.T, f func(t *testing.T, e *env)) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, d := range drivers {
		t.Run(d, func(t *testing.T) { f(t, newEnv(t, d)) })
	}
}

func (e *env) create(t *testing.T, v any) {
	t.Helper()
	if err := e.db.Create(v).Error; err != nil {
		t.Fatal(err)
	}
}

// user 创建用户（含主订阅），created 为注册时间。
func (e *env) user(t *testing.T, email string, created int64) *model.User {
	t.Helper()
	u, _, err := account.Create(e.db, account.NewUser{Email: email, Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.db.Model(u).Update("created_at", created).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func ptr[T any](v T) *T { return &v }

func TestOverview(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		now := e.now.Unix()
		nov2 := time.Date(2026, 11, 2, 8, 0, 0, 0, time.Local).Unix()
		oct := time.Date(2026, 10, 15, 8, 0, 0, 0, time.Local).Unix()
		a := e.user(t, "a@x.com", now-3600)
		b := e.user(t, "b@x.com", time.Date(2026, 11, 4, 0, 0, 0, 0, time.Local).Unix())
		e.user(t, "c@x.com", oct)
		e.user(t, "future@x.com", now+60)

		orders := []model.Order{
			{UserID: a.ID, TradeNo: "T1", TotalAmount: 1000, Status: 3, CreatedAt: now - 60,
				InviteUserID: &b.ID, CommissionBalance: 100},
			{UserID: a.ID, TradeNo: "T2", TotalAmount: 500, Status: 0, CreatedAt: now - 60},
			{UserID: a.ID, TradeNo: "T3", TotalAmount: 700, Status: 2, CreatedAt: now - 60},
			{UserID: b.ID, TradeNo: "T4", TotalAmount: 2000, Status: 1, CreatedAt: nov2},
			{UserID: b.ID, TradeNo: "T5", TotalAmount: 4000, Status: 4, CreatedAt: oct},
			{UserID: b.ID, TradeNo: "T6", TotalAmount: 999, Status: 3, CreatedAt: now + 10},
			{UserID: b.ID, TradeNo: "T7", TotalAmount: 10, Status: 3, CreatedAt: now - 60,
				InviteUserID: &a.ID, CommissionStatus: 2, CommissionBalance: 1},
		}
		for i := range orders {
			orders[i].Period = "month_price"
			orders[i].PlanID = 1
			e.create(t, &orders[i])
		}
		e.create(t, &model.CommissionLog{InviteUserID: b.ID, UserID: a.ID, TradeNo: "C1", OrderAmount: 1, GetAmount: 50, CreatedAt: nov2})
		e.create(t, &model.CommissionLog{InviteUserID: b.ID, UserID: a.ID, TradeNo: "C2", OrderAmount: 1, GetAmount: 70, CreatedAt: oct})

		// a 的主订阅在线；b 的主订阅与附加订阅都在线（只算一个在线用户）；另一个附加订阅已超过 10 分钟
		e.db.Model(&model.Subscription{}).Where("user_id IN ?", []int64{a.ID, b.ID}).Update("t", now-60)
		e.create(t, &model.Subscription{UserID: b.ID, Name: "s2", UUID: "uuid-s2", Token: "token-s2", T: now - 30})
		e.create(t, &model.Subscription{UserID: b.ID, Name: "s3", UUID: "uuid-s3", Token: "token-s3", T: now - 700})

		e.create(t, &model.Ticket{UserID: a.ID, Subject: "1", Status: 0, ReplyStatus: 0})
		e.create(t, &model.Ticket{UserID: a.ID, Subject: "2", Status: 0, ReplyStatus: 1})
		e.create(t, &model.Ticket{UserID: a.ID, Subject: "3", Status: 1, ReplyStatus: 0})

		o, err := GetOverview(context.Background(), e.db, e.now)
		if err != nil {
			t.Fatal(err)
		}
		want := Overview{
			OnlineUser: 2, OnlineSubscription: 3, DayIncome: 1010, MonthIncome: 3010, LastMonthIncome: 4000,
			DayRegisterTotal: 1, MonthRegisterTotal: 2, TicketPendingTotal: 1, CommissionPendingTotal: 1,
			CommissionMonthPayout: 50, CommissionLastMonthPayout: 70,
		}
		if *o != want {
			t.Fatalf("概况错误：\n实际 %+v\n期望 %+v", *o, want)
		}
	})
}

func TestDailyAndRanks(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		today := dayStart(e.now).Unix()
		yesterday := today - 86400
		for i, at := range []int64{today - 3*86400, yesterday, today - 2*86400} {
			e.create(t, &model.Stat{RecordAt: at, RecordType: "d", PaidCount: int64(i + 1), PaidTotal: 100, TransferUsedTotal: "0"})
		}
		e.create(t, &model.Stat{RecordAt: today, RecordType: "m", PaidCount: 99, TransferUsedTotal: "0"})
		days, err := DailyStats(ctx, e.db, 2)
		if err != nil || len(days) != 2 || days[0].RecordAt != today-2*86400 || days[1].RecordAt != yesterday || days[1].PaidCount != 2 {
			t.Fatalf("每日统计错误：%+v %v", days, err)
		}

		vm := model.ServerVmess{ServerBase: model.ServerBase{
			GroupID: model.IntList{1}, RouteID: model.IntList{}, Name: "节点A", Host: "a.example.com", Port: "443",
			ServerPort: 443, Tags: model.StrList{}, Rate: "1", Show: 1,
		}, Network: "tcp"}
		e.create(t, &vm)
		e.create(t, &model.StatServer{ServerID: vm.ID, ServerType: "v2ray", U: gb, D: gb, RecordType: "d", RecordAt: today})
		e.create(t, &model.StatServer{ServerID: 999, ServerType: "trojan", U: 1, D: 1, RecordType: "d", RecordAt: today})
		e.create(t, &model.StatServer{ServerID: vm.ID, ServerType: "vmess", U: 5, D: 5, RecordType: "d", RecordAt: yesterday})
		e.create(t, &model.StatServer{ServerID: vm.ID, ServerType: "vmess", U: 9 * gb, D: 0, RecordType: "d", RecordAt: e.now.Unix() + 60})
		rank, err := ServerRanks(ctx, e.db, "today", e.now)
		if err != nil || len(rank) != 2 || rank[0].ServerName != "节点A" || rank[0].Total != 2 || rank[1].ServerName != "" {
			t.Fatalf("今日节点排行错误：%+v %v", rank, err)
		}
		if rank, _ = ServerRanks(ctx, e.db, "yesterday", e.now); len(rank) != 1 || rank[0].U != 5 {
			t.Fatalf("昨日节点排行错误：%+v", rank)
		}

		a := e.user(t, "a@x.com", today)
		b := e.user(t, "b@x.com", today)
		// a：两条记录按倍率折算后合并为 1×1 + 1×2 = 3 GB；b：4 GB×0.5 = 2 GB；已删除的用户邮箱为 null
		e.create(t, &model.StatUser{UserID: a.ID, ServerRate: 1, U: gb, RecordType: "d", RecordAt: today})
		e.create(t, &model.StatUser{UserID: a.ID, ServerRate: 2, D: gb, RecordType: "d", RecordAt: today, SubscriptionID: ptr(int64(77))})
		e.create(t, &model.StatUser{UserID: b.ID, ServerRate: 0.5, U: 4 * gb, RecordType: "d", RecordAt: today})
		e.create(t, &model.StatUser{UserID: 424242, ServerRate: 1, U: gb / 2, RecordType: "d", RecordAt: today})
		e.create(t, &model.StatUser{UserID: b.ID, ServerRate: 1, U: 100 * gb, RecordType: "d", RecordAt: yesterday})
		users, err := UserRanks(ctx, e.db, "today", e.now)
		if err != nil || len(users) != 3 {
			t.Fatalf("今日用户排行错误：%+v %v", users, err)
		}
		if users[0].Email != "a@x.com" || users[0].Total != 3 || users[1].Email != "b@x.com" || users[1].Total != 2 || users[2].Email != "null" {
			t.Fatalf("今日用户排行错误：%+v", users)
		}
		if users, _ = UserRanks(ctx, e.db, "yesterday", e.now); len(users) != 1 || users[0].Total != 100 {
			t.Fatalf("昨日用户排行错误：%+v", users)
		}
		if _, err := UserRanks(ctx, e.db, "week", e.now); !errors.Is(err, errInvalid) {
			t.Fatalf("无效的日期应报错：%v", err)
		}

		page, err := UserStats(ctx, e.db, UserStatQuery{UserID: a.ID})
		if err != nil || page.Total != 2 || len(page.Data) != 2 {
			t.Fatalf("用户流量记录错误：%+v %v", page, err)
		}
		if page, _ = UserStats(ctx, e.db, UserStatQuery{UserID: a.ID, SubscriptionID: 77}); page.Total != 1 || page.Data[0].ServerRate != 2 {
			t.Fatalf("按订阅筛选错误：%+v", page)
		}
		if _, err := UserStats(ctx, e.db, UserStatQuery{}); !errors.Is(err, errInvalid) {
			t.Fatal("缺少用户应报错")
		}
	})
}

func TestStatusAndLogs(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		q := &fakeQueue{}
		st, err := SystemStatus(ctx, e.cache, q, e.now)
		if err != nil || st.Schedule || st.ScheduleLastRuntime != nil || st.Queue {
			t.Fatalf("无心跳时状态错误：%+v %v", st, err)
		}
		key := e.cache.KeyOf(cache.KeyScheduleLastCheckAt, nil)
		e.cache.Set(ctx, key, e.now.Unix()-60, 0)
		q.workers, q.concurrency = 2, 20
		if st, _ = SystemStatus(ctx, e.cache, q, e.now); !st.Schedule || *st.ScheduleLastRuntime != e.now.Unix()-60 || !st.Queue || st.QueueConcurrency != 20 {
			t.Fatalf("运行中状态错误：%+v", st)
		}
		e.cache.Set(ctx, key, e.now.Unix()-121, 0)
		if st, _ = SystemStatus(ctx, e.cache, q, e.now); st.Schedule {
			t.Fatal("心跳超时应为未运行")
		}
		q.err = errors.New("redis down")
		if _, err := SystemStatus(ctx, e.cache, q, e.now); err == nil {
			t.Fatal("队列状态读取失败应报错")
		}

		for i, title := range []string{"慢查询 50%_done", "慢查询 50 done", "POST /api/{admin}/x", "连接失败"} {
			level := []string{"WARNING", "WARNING", "INFO", "ERROR"}[i]
			e.create(t, &model.Log{Title: title, Level: &level, CreatedAt: int64(1000 + i)})
		}
		page, err := Logs(ctx, e.db, LogQuery{})
		if err != nil || page.Total != 4 || page.Data[0].Title != "连接失败" {
			t.Fatalf("日志列表错误：%+v %v", page, err)
		}
		if page, _ = Logs(ctx, e.db, LogQuery{Level: "warning"}); page.Total != 2 {
			t.Fatalf("按级别筛选错误：%d", page.Total)
		}
		if page, _ = Logs(ctx, e.db, LogQuery{Keyword: "50%_"}); page.Total != 1 || page.Data[0].Title != "慢查询 50%_done" {
			t.Fatalf("关键字中的通配符应按字面匹配：%+v", page)
		}
		if page, _ = Logs(ctx, e.db, LogQuery{Keyword: "!"}); page.Total != 0 {
			t.Fatalf("转义符应按字面匹配：%d", page.Total)
		}
		if _, err := Logs(ctx, e.db, LogQuery{Level: "TRACE"}); !errors.Is(err, errInvalid) {
			t.Fatal("无效级别应报错")
		}
		if page, _ = Logs(ctx, e.db, LogQuery{PageSize: 1}); len(page.Data) != 4 {
			t.Fatalf("每页至少 10 条：%d", len(page.Data))
		}
	})
}

func TestHTTP(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		const secure = "adminstat0123456789"
		if err := e.st.Set(ctx, map[string]any{"secure_path": secure}); err != nil {
			t.Fatal(err)
		}
		admin := e.user(t, "admin@x.com", e.now.Unix()-60)
		normal := e.user(t, "n@x.com", e.now.Unix()-60)
		e.db.Model(admin).Update("is_admin", 1)
		adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
		userTok, _ := e.auth.Issue(ctx, normal.ID, "", "")
		cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
		r, err := server.NewRouter(cfg, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		jobs := &fakeJobs{}
		r.Mount(&Module{DB: e.db, Cache: e.cache, Settings: e.st, Queue: &fakeQueue{workers: 1}, Jobs: jobs, Auth: e.auth, RealIP: r.RealIP, Now: func() time.Time { return e.now }})
		do := func(method, path, tok string) (int, map[string]any) {
			req := httptest.NewRequest(method, "/api/t1/"+path, nil)
			req.Header.Set("Authorization", tok)
			w := httptest.NewRecorder()
			r.Engine.ServeHTTP(w, req)
			var body map[string]any
			json.Unmarshal(w.Body.Bytes(), &body)
			return w.Code, body
		}
		get := func(path, tok string) (int, map[string]any) { return do("GET", path, tok) }
		paths := []string{
			"stat/overview", "stat/orders", "stat/server-rank?day=today", "stat/user-rank?day=yesterday",
			fmt.Sprintf("stat/user?user_id=%d", admin.ID), "system/status", "system/queue", "system/logs?level=ERROR",
			"system/jobs",
		}
		for _, p := range paths {
			if code, _ := get(secure+"/"+p, userTok); code != http.StatusNotFound {
				t.Fatalf("%s 普通用户应 404，实际 %d", p, code)
			}
			if code, body := get(secure+"/"+p, adminTok); code != 200 || body["data"] == nil {
				t.Fatalf("%s 错误：%d %v", p, code, body)
			}
		}
		if _, body := get(secure+"/stat/overview", adminTok); body["data"].(map[string]any)["day_register_total"] != float64(2) {
			t.Fatalf("概况错误：%v", body)
		}
		if _, body := get(secure+"/system/status", adminTok); body["data"].(map[string]any)["queue"] != true {
			t.Fatalf("系统状态错误：%v", body)
		}
		if _, body := get(secure+"/system/status", adminTok); body["data"].(map[string]any)["real_ip"].(map[string]any)["warning"] != false {
			t.Fatalf("真实 IP 识别状态错误：%v", body)
		}
		for _, p := range []string{"stat/server-rank", "stat/user", "stat/user?user_id=x", "system/logs?level=x"} {
			if code, _ := get(secure+"/"+p, adminTok); code != http.StatusUnprocessableEntity {
				t.Fatalf("%s 参数错误应 422，实际 %d", p, code)
			}
		}
		var he *httpx.Error
		if !errors.As(errInvalid, &he) || !strings.Contains(he.Msg, "参数有误") {
			t.Fatal("参数错误文案不正确")
		}

		if code, _ := do("POST", secure+"/system/jobs/stat/run", userTok); code != http.StatusNotFound || len(jobs.started) != 0 {
			t.Fatalf("普通用户手动执行任务应 404，实际 %d", code)
		}
		for name, want := range map[string]int{"stat": 200, "missing": http.StatusNotFound, "busy": http.StatusConflict} {
			if code, _ := do("POST", secure+"/system/jobs/"+name+"/run", adminTok); code != want {
				t.Fatalf("手动执行 %s 应返回 %d，实际 %d", name, want, code)
			}
		}
		if len(jobs.started) != 1 || jobs.started[0] != "stat" {
			t.Fatalf("手动执行记录有误：%v", jobs.started)
		}
		if _, body := get(secure+"/system/jobs", adminTok); body["data"].([]any)[0].(map[string]any)["name"] != "stat" {
			t.Fatalf("任务列表错误：%v", body)
		}
	})
}

type fakeJobs struct{ started []string }

func (f *fakeJobs) List(context.Context) ([]scheduler.JobInfo, error) {
	return []scheduler.JobInfo{{Name: "stat", Spec: "* * * * *"}}, nil
}

func (f *fakeJobs) Start(_ context.Context, name string) error {
	switch name {
	case "stat":
		f.started = append(f.started, name)
		return nil
	case "busy":
		return scheduler.ErrJobRunning
	}
	return scheduler.ErrJobNotFound
}
