package syslog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/model"
	"gvboard/internal/testutil"
)

func TestSensitive(t *testing.T) {
	for _, k := range []string{
		"password", "new_password", "email_password", "telegram_bot_token", "server_token", "app_secret", "AppSecret",
		"alipay_private_key", "recaptcha_key", "recaptcha_site_key", "secure_path", "sql", "pkey", "stripe_sk_live",
		"uuid", "card_number", "Authorization", "sessionId", "api-key", "sign", "pass", "email_smtp_servers",
	} {
		if !Sensitive(k) {
			t.Errorf("%s 应脱敏", k)
		}
	}
	for _, k := range []string{"email", "id", "plan_id", "transfer_enable", "keyword", "monkey", "passport", "designer", "skip", "status", "email_smtp_mode"} {
		if Sensitive(k) {
			t.Errorf("%s 不应脱敏", k)
		}
	}
}

func TestMask(t *testing.T) {
	in := map[string]any{
		"email": "a@x.com",
		"user":  map[string]any{"password": map[string]any{"x": 1}, "name": "n", "token": nil},
		"list":  []any{map[string]any{"secret": "s", "id": 1}, "plain"},
	}
	want := map[string]any{
		"email": "a@x.com",
		"user":  map[string]any{"password": Masked, "name": "n", "token": nil},
		"list":  []any{map[string]any{"secret": Masked, "id": 1}, "plain"},
	}
	if got := Mask(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("脱敏错误：%v", got)
	}
	if in["user"].(map[string]any)["name"] != "n" || in["list"].([]any)[0].(map[string]any)["secret"] != "s" {
		t.Fatal("不应修改原数据")
	}
	q := Mask(map[string][]string{"auth_data": {"x"}, "ids": {"1", "2"}, "page": {"1"}})
	if !reflect.DeepEqual(q, map[string]any{"auth_data": Masked, "ids": []string{"1", "2"}, "page": "1"}) {
		t.Fatalf("参数脱敏错误：%v", q)
	}
}

func TestHideAdminPath(t *testing.T) {
	cases := [][3]string{
		{"/api/v1/:secure/users/:id", "/api/v1/abc/users/5", "/api/v1/{admin}/users/5"},
		{"/api/:secure/api/x", "/api/api/api/x", "/api/{admin}/api/x"},
		{"", "/x", "/x"},
	}
	for _, c := range cases {
		if got := hideAdminPath(c[0], c[1]); got != c[2] {
			t.Errorf("%s → %s，期望 %s", c[1], got, c[2])
		}
	}
}

func TestWriterBuffer(t *testing.T) {
	w := NewWriter(zap.NewNop())
	for range bufferSize + 3 {
		w.Add(model.Log{Title: "x"})
	}
	if w.Dropped() != 3 {
		t.Fatalf("缓冲区满应丢弃：%d", w.Dropped())
	}
	w.Flush(context.Background()) // 未设置数据库时不处理
	if len(w.ch) != bufferSize {
		t.Fatal("未设置数据库时日志应留在缓冲区")
	}

	w = NewWriter(zap.NewNop())
	long := strings.Repeat("中", textMax)
	w.Add(model.Log{Title: long, URI: strings.Repeat("u", 300), Method: "PROPPATCHXYZ", IP: ptr(strings.Repeat("1", 200))})
	l := <-w.ch
	if len(l.Title) > textMax || !utf8.ValidString(l.Title) || len(l.URI) != 255 || len(l.Method) != 11 || len(*l.IP) != 128 {
		t.Fatalf("截断错误：%d %d %d %d", len(l.Title), len(l.URI), len(l.Method), len(*l.IP))
	}
	if l.CreatedAt == 0 || l.UpdatedAt != l.CreatedAt {
		t.Fatal("应设置时间")
	}
}

func eachDriver(t *testing.T, f func(t *testing.T, db *gorm.DB)) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, d := range drivers {
		t.Run(d, func(t *testing.T) { f(t, testutil.DB(t, d)) })
	}
}

func logs(t *testing.T, w *Writer, db *gorm.DB) []model.Log {
	t.Helper()
	w.Flush(context.Background())
	var out []model.Log
	if err := db.Order("id").Find(&out).Error; err != nil {
		t.Fatal(err)
	}
	return out
}

func jsonOf(t *testing.T, s *string) map[string]any {
	t.Helper()
	var v map[string]any
	if s == nil || json.Unmarshal([]byte(*s), &v) != nil {
		t.Fatalf("不是 JSON 对象：%v", s)
	}
	return v
}

func TestCore(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *gorm.DB) {
		w := NewWriter(zap.NewNop())
		w.SetDB(db)
		log := zap.New(w.Core(zapcore.WarnLevel), zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)).Named("order")
		reqLog := log.With(zap.String(FieldMethod, "POST"), zap.String(FieldURI, "/api/v1/x"),
			zap.String(FieldIP, "1.2.3.4"), zap.String(FieldHost, "a.com"))
		log.Info("不记录")
		reqLog.Warn("慢查询", zap.String("sql", "SELECT * FROM v2_user WHERE password = 'x'"), zap.Int("rows", 3),
			zap.Any("cfg", map[string]any{"app_secret": "s", "mch_id": "m"}))
		log.Error("连接失败", zap.Error(errors.New("boom")))

		got := logs(t, w, db)
		if len(got) != 2 {
			t.Fatalf("应写入 2 条，实际 %d", len(got))
		}
		warn, errLog := got[0], got[1]
		if warn.Title != "慢查询" || *warn.Level != "WARNING" || warn.Method != "POST" || warn.URI != "/api/v1/x" ||
			*warn.IP != "1.2.3.4" || *warn.Host != "a.com" || warn.Data != nil {
			t.Fatalf("警告日志列错误：%+v", warn)
		}
		ctx := jsonOf(t, warn.Context)
		if ctx["sql"] != Masked || ctx["rows"] != float64(3) || ctx["logger"] != "order" || ctx["caller"] == nil ||
			ctx["cfg"].(map[string]any)["app_secret"] != Masked || ctx["cfg"].(map[string]any)["mch_id"] != "m" {
			t.Fatalf("警告日志内容错误：%v", ctx)
		}
		for _, k := range []string{FieldMethod, FieldURI, FieldIP, FieldHost, "stack"} {
			if _, ok := ctx[k]; ok {
				t.Errorf("context 不应包含 %s", k)
			}
		}
		ctx = jsonOf(t, errLog.Context)
		if *errLog.Level != "ERROR" || errLog.IP != nil || errLog.Method != "" || ctx["error"] != "boom" || ctx["stack"] == nil {
			t.Fatalf("错误日志错误：%+v %v", errLog, ctx)
		}
	})
}

func TestAudit(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		w := NewWriter(zap.NewNop())
		w.SetDB(db)
		as := auth.New(db, testutil.Redis(t), "base64:c3lzbG9nLWF1ZGl0LXRlc3Qta2V5LTAxMjM0NTY3ODk=")
		as.AdminHooks = []gin.HandlerFunc{w.Audit()}
		var admin, normal *model.User
		db.Transaction(func(tx *gorm.DB) error {
			admin, _, _ = account.Create(tx, account.NewUser{Email: "admin@x.com", Password: "password123"})
			normal, _, _ = account.Create(tx, account.NewUser{Email: "n@x.com", Password: "password123"})
			return tx.Model(admin).Update("is_admin", 1).Error
		})
		adminTok, _ := as.Issue(ctx, admin.ID, "", "")
		userTok, _ := as.Issue(ctx, normal.ID, "", "")

		gin.SetMode(gin.TestMode)
		r := gin.New()
		g := as.AdminGroup(r.Group("/api/v1"), func() string { return "sec0123456789" })
		echo := func(c *gin.Context) {
			b, _ := c.GetRawData()
			c.String(http.StatusOK, "%d", len(b))
		}
		g.POST("/users/:id", echo)
		g.PUT("/config", echo)
		g.GET("/users", echo)
		g.DELETE("/users/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		do := func(method, path, ct, body, tok string) int {
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			req.Header.Set("Authorization", tok)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			return rec.Code
		}
		do("POST", "/api/v1/sec0123456789/users/5?token=abc&from=list", "application/json",
			`{"email":"x@x.com","password":"newpass","balance":12.5,"nested":{"uuid":"u-1"}}`, adminTok)
		do("PUT", "/api/v1/sec0123456789/config", "application/x-www-form-urlencoded", "app_name=A&email_password=p", adminTok)
		do("PUT", "/api/v1/sec0123456789/config", "text/plain", "password=raw", adminTok)
		do("PUT", "/api/v1/sec0123456789/config", "application/json", `{"big":"`+strings.Repeat("a", auditBodyMax)+`"}`, adminTok)
		do("DELETE", "/api/v1/sec0123456789/users/7", "", "", adminTok)
		do("GET", "/api/v1/sec0123456789/users", "", "", adminTok)
		do("POST", "/api/v1/sec0123456789/users/5", "application/json", `{}`, userTok)
		do("POST", "/api/v1/wrongpath/users/5", "application/json", `{}`, adminTok)

		got := logs(t, w, db)
		if len(got) != 5 {
			t.Fatalf("应记录 5 条修改操作，实际 %d", len(got))
		}
		for _, l := range got {
			if strings.Contains(l.Title+l.URI, "sec0123456789") || (l.Data != nil && strings.Contains(*l.Data, "sec0123456789")) {
				t.Fatalf("不应记录管理端路径：%+v", l)
			}
		}
		post := got[0]
		if post.Title != "POST /api/v1/{admin}/users/5" || *post.Level != "INFO" || post.Method != "POST" || post.URI != "/api/v1/{admin}/users/5" {
			t.Fatalf("审计标题错误：%+v", post)
		}
		data := jsonOf(t, post.Data)
		wantData := map[string]any{
			"query": map[string]any{"token": Masked, "from": "list"},
			"body":  map[string]any{"email": "x@x.com", "password": Masked, "balance": 12.5, "nested": map[string]any{"uuid": Masked}},
		}
		if !reflect.DeepEqual(data, wantData) {
			t.Fatalf("审计参数错误：%v", data)
		}
		c := jsonOf(t, post.Context)
		if c["status"] != float64(200) || c["admin_id"] != float64(admin.ID) || c["admin_email"] != "admin@x.com" {
			t.Fatalf("审计上下文错误：%v", c)
		}
		if b := jsonOf(t, got[1].Data)["body"].(map[string]any); b["app_name"] != "A" || b["email_password"] != Masked {
			t.Fatalf("表单参数错误：%v", b)
		}
		if b := jsonOf(t, got[2].Data)["body"].(map[string]any); !reflect.DeepEqual(b, map[string]any{"bytes": float64(12)}) {
			t.Fatalf("无法解析的请求体只记录长度：%v", b)
		}
		if d := jsonOf(t, got[3].Data); d["body_truncated"] != true || d["body"].(map[string]any)["bytes"] != float64(auditBodyMax) {
			t.Fatalf("超长请求体错误：%v", d)
		}
		if got[4].Title != "DELETE /api/v1/{admin}/users/7" || got[4].Data != nil || jsonOf(t, got[4].Context)["status"] != float64(204) {
			t.Fatalf("删除操作记录错误：%+v", got[4])
		}
	})
}
