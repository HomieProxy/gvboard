package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/testutil"
)

const testKey = "base64:dGVzdC1rZXktZm9yLWd2Ym9hcmQtYXV0aA=="

func newUser(t *testing.T, db *gorm.DB, email string) *model.User {
	t.Helper()
	var u *model.User
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		u, _, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAuth(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) { testAuth(t, driver) })
	}
}

func testAuth(t *testing.T, driver string) {
	ctx := context.Background()
	db := testutil.DB(t, driver)
	s := New(db, testutil.Redis(t), testKey)
	u := newUser(t, db, "a@example.com")

	token, err := s.Issue(ctx, u.ID, "1.2.3.4", "UA")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Parse(ctx, token)
	if err != nil || id.ID != u.ID || id.Email != "a@example.com" || id.IsAdmin {
		t.Fatalf("解析凭证失败: %+v %v", id, err)
	}
	if _, err := s.Parse(ctx, "Bearer "+token); err != nil {
		t.Errorf("应支持 Bearer 前缀: %v", err)
	}

	sessions, err := s.Sessions(ctx, u.ID)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("会话数量不正确: %v %v", sessions, err)
	}
	for _, m := range sessions {
		if m.IP != "1.2.3.4" || m.UA != "UA" || m.LoginAt == 0 {
			t.Errorf("会话信息不正确: %+v", m)
		}
	}

	// 伪造与篡改
	other := New(db, s.cache, "another-key-0123456789")
	forged, _ := other.Issue(ctx, u.ID, "", "")
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, claims{ID: u.ID, Session: id.Session}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	hs512, _ := jwt.NewWithClaims(jwt.SigningMethodHS512, claims{ID: u.ID, Session: id.Session}).SignedString([]byte(testKey))
	for name, raw := range map[string]string{
		"空": "", "乱码": "abc.def.ghi", "其他密钥签发": forged, "none 算法": none, "HS512 算法": hs512, "篡改": token + "x",
	} {
		if _, err := s.Parse(ctx, raw); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s 的凭证应无效，实际 %v", name, err)
		}
	}

	// 封禁后已登录会话立即失效（决策 D16）
	db.Model(&model.User{}).Where("id = ?", u.ID).Update("banned", 1)
	if _, err := s.Parse(ctx, token); !errors.Is(err, ErrBanned) {
		t.Errorf("封禁用户应返回 ErrBanned，实际 %v", err)
	}
	db.Model(&model.User{}).Where("id = ?", u.ID).Update("banned", 0)

	// 删除会话后凭证立即失效
	token2, _ := s.Issue(ctx, u.ID, "", "")
	if err := s.RemoveSession(ctx, u.ID, id.Session); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Parse(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("删除会话后凭证应失效，实际 %v", err)
	}
	if _, err := s.Parse(ctx, token2); err != nil {
		t.Errorf("其他会话不应受影响: %v", err)
	}
	if err := s.RemoveAll(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Parse(ctx, token2); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("删除全部会话后凭证应失效，实际 %v", err)
	}

	// 用户被删除
	token3, _ := s.Issue(ctx, u.ID, "", "")
	db.Delete(&model.User{}, u.ID)
	if _, err := s.Parse(ctx, token3); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("用户删除后凭证应失效，实际 %v", err)
	}

	// 快捷登录码只能使用一次
	code, err := s.CreateTempToken(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ConsumeTempToken(ctx, code); got != 42 {
		t.Errorf("快捷登录码应对应用户 42，实际 %d", got)
	}
	if got, _ := s.ConsumeTempToken(ctx, code); got != 0 {
		t.Errorf("快捷登录码第二次使用应无效，实际 %d", got)
	}
	if got, _ := s.ConsumeTempToken(ctx, "not-exist"); got != 0 {
		t.Errorf("不存在的快捷登录码应无效，实际 %d", got)
	}

	testMiddleware(t, db, s)
}

func testMiddleware(t *testing.T, db *gorm.DB, s *Service) {
	ctx := context.Background()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	ok := func(c *gin.Context) { httpx.OK(c, Current(c).ID) }
	e.GET("/user", s.RequireUser(), ok)
	e.GET("/admin", s.RequireAdmin(), ok)
	e.GET("/missing", httpx.NotFound)

	do := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w
	}
	notFound := do("/missing", "")

	user := newUser(t, db, "user@example.com")
	admin := newUser(t, db, "admin@example.com")
	db.Model(&model.User{}).Where("id = ?", admin.ID).Update("is_admin", 1)
	userToken, _ := s.Issue(ctx, user.ID, "", "")
	adminToken, _ := s.Issue(ctx, admin.ID, "", "")

	if w := do("/user", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("未登录访问用户接口应返回 401，实际 %d", w.Code)
	}
	if w := do("/user", userToken); w.Code != http.StatusOK {
		t.Errorf("登录用户访问用户接口应返回 200，实际 %d %s", w.Code, w.Body)
	}
	for name, token := range map[string]string{"未登录": "", "普通用户": userToken, "无效凭证": "x"} {
		w := do("/admin", token)
		if w.Code != notFound.Code || w.Body.String() != notFound.Body.String() || w.Header().Get("Content-Type") != notFound.Header().Get("Content-Type") {
			t.Errorf("%s访问管理接口应返回统一 404，实际 %d", name, w.Code)
		}
	}
	if w := do("/admin", adminToken); w.Code != http.StatusOK {
		t.Errorf("管理员访问管理接口应返回 200，实际 %d", w.Code)
	}

	db.Model(&model.User{}).Where("id = ?", user.ID).Update("banned", 1)
	if w := do("/user", userToken); w.Code != http.StatusForbidden {
		t.Errorf("封禁用户访问用户接口应返回 403，实际 %d", w.Code)
	}
}
