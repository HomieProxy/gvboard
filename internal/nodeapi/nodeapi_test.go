package nodeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/model"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/testutil"
)

// ---------- 不依赖数据库的测试 ----------

// decodeAny 解析 JSON 用于语义比较（忽略键顺序与转义差异）。
func decodeAny(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		t.Fatalf("JSON 无效: %v: %s", err, b)
	}
	return v
}

func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := encode(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func strp(s string) *string { return &s }
func i64p(n int64) *int64   { return &n }

// TestPHPVector 与原项目 PHP 代码的输出对照（testdata/php_vector.json 由 php:8.2 生成）。
func TestPHPVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/php_vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var vec struct {
		ServerKey []struct {
			CreatedAt int64 `json:"created_at"`
			Len       int   `json:"len"`
			Key       string
		} `json:"server_key"`
		PHPArray []struct{ In, Out string } `json:"php_array"`
		V2ray    []struct {
			ServerPort      int64   `json:"server_port"`
			Network         string  `json:"network"`
			TLS             int     `json:"tls"`
			NetworkSettings *string `json:"networkSettings"`
			DNSSettings     *string `json:"dnsSettings"`
			RuleSettings    *string `json:"ruleSettings"`
			TLSSettings     *string `json:"tlsSettings"`
			Log             int     `json:"log"`
			LocalPort       int64   `json:"local_port"`
			Out             string  `json:"out"`
		} `json:"v2ray"`
		Trojan []struct {
			LocalPort  int64   `json:"local_port"`
			ServerPort int64   `json:"server_port"`
			ServerName *string `json:"server_name"`
			Host       string  `json:"host"`
			Out        string  `json:"out"`
		} `json:"trojan"`
	}
	if err := json.Unmarshal(raw, &vec); err != nil {
		t.Fatal(err)
	}
	for _, c := range vec.ServerKey {
		if got := serverKey(c.CreatedAt, c.Len); got != c.Key {
			t.Errorf("serverKey(%d,%d)=%s，期望 %s", c.CreatedAt, c.Len, got, c.Key)
		}
	}
	for _, c := range vec.PHPArray {
		got := mustEncode(t, phpArray(model.JSON(c.In)))
		if !reflect.DeepEqual(decodeAny(t, got), decodeAny(t, []byte(c.Out))) {
			t.Errorf("phpArray(%s)=%s，期望 %s", c.In, got, c.Out)
		}
	}
	js := func(p *string) model.JSON {
		if p == nil {
			return nil
		}
		return model.JSON(*p)
	}
	for i, c := range vec.V2ray {
		srv := &model.ServerVmess{
			ServerBase: model.ServerBase{ServerPort: c.ServerPort}, Network: c.Network, TLS: c.TLS,
			NetworkSettings: js(c.NetworkSettings), DNSSettings: js(c.DNSSettings),
			RuleSettings: js(c.RuleSettings), TLSSettings: js(c.TLSSettings),
		}
		got := mustEncode(t, v2rayConfig(srv, c.LocalPort, c.Log == 1))
		if !reflect.DeepEqual(decodeAny(t, got), decodeAny(t, []byte(c.Out))) {
			t.Errorf("V2Ray 配置 %d 不一致\nGo:  %s\nPHP: %s", i, got, c.Out)
		}
	}
	for i, c := range vec.Trojan {
		srv := &model.ServerTrojan{ServerBase: model.ServerBase{ServerPort: c.ServerPort, Host: c.Host}, ServerName: c.ServerName}
		got := mustEncode(t, trojanConfig(srv, c.LocalPort))
		if !reflect.DeepEqual(decodeAny(t, got), decodeAny(t, []byte(c.Out))) {
			t.Errorf("Trojan 配置 %d 不一致\nGo:  %s\nPHP: %s", i, got, c.Out)
		}
	}
}

func TestParse(t *testing.T) {
	d, n, err := ParseUniTraffic([]byte(`{"1":[100,200],"2":[1.9,"3"],"x":[1,1],"3":5,"4":[-5,7]}`))
	if err != nil || n != 5 {
		t.Fatalf("解析失败: %v %d", err, n)
	}
	want := Traffic{1: {100, 200}, 2: {1, 3}, 4: {0, 7}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("流量解析错误: %v", d)
	}
	for _, s := range []string{"", "  ", "[]", "{}"} {
		if d, n, err := ParseUniTraffic([]byte(s)); err != nil || len(d) != 0 || n != 0 {
			t.Errorf("%q 应视为空: %v %v", s, d, err)
		}
	}
	for _, s := range []string{"{", "[1]", "1", `"a"`} {
		if _, _, err := ParseUniTraffic([]byte(s)); err == nil {
			t.Errorf("%q 应报错", s)
		}
	}
	d, n, err = ParseLegacyTraffic([]byte(`[{"user_id":1,"u":5,"d":6},{"user_id":"2","u":1},{"u":1},{"user_id":1,"u":7,"d":8}]`))
	if err != nil || n != 4 || !reflect.DeepEqual(d, Traffic{1: {7, 8}, 2: {1, 0}}) {
		t.Fatalf("旧版流量解析错误: %v %d %v", d, n, err)
	}
	if _, _, err := ParseLegacyTraffic([]byte(`{}`)); err == nil {
		t.Fatal("旧版非数组应报错")
	}
	a, err := ParseAlive([]byte(`{"1":["1.1.1.1_3","2.2.2.2_3"],"2":"x","y":["a"],"3":[]}`))
	if err != nil || !reflect.DeepEqual(a, map[int64][]string{1: {"1.1.1.1_3", "2.2.2.2_3"}, 3: {}}) {
		t.Fatalf("在线数据解析错误: %v %v", a, err)
	}
	if _, err := ParseAlive([]byte(`[1]`)); err == nil {
		t.Fatal("在线数据非对象应报错")
	}
}

func TestWhitelist(t *testing.T) {
	ps, err := ParseWhitelist([]string{" 10.0.0.0/8 ", "192.0.2.7", "", "2001:db8::/32"})
	if err != nil || len(ps) != 3 {
		t.Fatalf("解析失败: %v %v", ps, err)
	}
	for _, bad := range []string{"10.0.0.0/33", "abc"} {
		if _, err := ParseWhitelist([]string{bad}); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}

// ---------- 数据库测试 ----------

const token = "node-token-0123456789"

type env struct {
	*Service
	auth *auth.Service
	now  time.Time
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	ctx := context.Background()
	if err := st.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ctx, map[string]any{"server_token": token}); err != nil {
		t.Fatal(err)
	}
	e := &env{now: time.Date(2027, 3, 15, 12, 0, 0, 0, time.Local), auth: auth.New(db, c, "base64:bm9kZS10ZXN0LWtleS0wMTIzNDU2Nzg5YWJjZGVm")}
	e.Service = &Service{DB: db, Cache: c, Settings: st, Now: func() time.Time { return e.now }}
	return e
}

func (e *env) set(t *testing.T, kv map[string]any) {
	t.Helper()
	if err := e.Settings.Set(context.Background(), kv); err != nil {
		t.Fatal(err)
	}
}

// user 创建用户及主订阅，返回用户与主订阅。
func (e *env) user(t *testing.T, email string, edit func(s *model.Subscription)) (*model.User, *model.Subscription) {
	t.Helper()
	var u *model.User
	var s *model.Subscription
	err := e.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		u, s, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e.editSub(t, s, edit)
	return u, s
}

// sub 为用户新增一个订阅。
func (e *env) sub(t *testing.T, uid int64, edit func(s *model.Subscription)) *model.Subscription {
	t.Helper()
	var maxID int64
	e.DB.Model(&model.Subscription{}).Select("COALESCE(MAX(id), 0)").Scan(&maxID)
	s := &model.Subscription{ID: maxID + 1000, UserID: uid, UUID: fmt.Sprintf("uuid-%d", maxID+1000), Token: fmt.Sprintf("tok%d", maxID+1000)}
	if err := e.DB.Create(s).Error; err != nil {
		t.Fatal(err)
	}
	e.editSub(t, s, edit)
	return s
}

// editSub 默认设为可用（权限组 1、100 GB、一天后到期），再按 edit 修改。
func (e *env) editSub(t *testing.T, s *model.Subscription, edit func(s *model.Subscription)) {
	t.Helper()
	g := int64(1)
	exp := e.now.Unix() + 86400
	s.GroupID, s.TransferEnable, s.ExpiredAt = &g, 100<<30, &exp
	if edit != nil {
		edit(s)
	}
	err := e.DB.Model(s).Select("group_id", "transfer_enable", "u", "d", "expired_at", "banned", "device_limit", "speed_limit").Updates(s).Error
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) createNode(t *testing.T, srv any) {
	t.Helper()
	if err := e.DB.Create(srv).Error; err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}
}

func baseNode(name string, groups ...int64) model.ServerBase {
	return model.ServerBase{Name: name, GroupID: groups, Host: "h.example.com", Port: "443", ServerPort: 8443, Rate: "1.5", Show: 1}
}

func TestNodeAPI(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("可用订阅", func(t *testing.T) { testUsers(t, newEnv(t, driver)) })
			t.Run("流量累计与写入", func(t *testing.T) { testTraffic(t, newEnv(t, driver)) })
			t.Run("并发上报", func(t *testing.T) { testConcurrent(t, newEnv(t, driver)) })
			t.Run("并发统计", func(t *testing.T) { testConcurrentStat(t, newEnv(t, driver)) })
			t.Run("在线IP", func(t *testing.T) { testAlive(t, newEnv(t, driver)) })
			t.Run("节点配置", func(t *testing.T) { testConfig(t, newEnv(t, driver)) })
			t.Run("HTTP", func(t *testing.T) { testHTTP(t, newEnv(t, driver)) })
		})
	}
}

func ids(users []User) []int64 {
	out := make([]int64, len(users))
	for i, u := range users {
		out[i] = u.ID
	}
	return out
}

func testUsers(t *testing.T, e *env) {
	ctx := context.Background()
	_, ok := e.user(t, "ok@x.com", func(s *model.Subscription) { s.SpeedLimit = i64p(0); s.DeviceLimit = i64p(3) })
	_, g2 := e.user(t, "g2@x.com", func(s *model.Subscription) { g := int64(2); s.GroupID = &g })
	_, forever := e.user(t, "forever@x.com", func(s *model.Subscription) { s.ExpiredAt = nil })
	e.user(t, "expired@x.com", func(s *model.Subscription) { s.ExpiredAt = i64p(e.now.Unix() - 1) })
	e.user(t, "zero@x.com", func(s *model.Subscription) { s.ExpiredAt = i64p(0) })
	e.user(t, "used@x.com", func(s *model.Subscription) { s.U, s.D = 50<<30, 50<<30 })
	e.user(t, "subban@x.com", func(s *model.Subscription) { s.Banned = 1 })
	banned, _ := e.user(t, "ban@x.com", nil)
	e.DB.Model(banned).Update("banned", 1)
	extra := e.sub(t, ok.UserID, nil)
	e.sub(t, banned.ID, nil)

	users, err := e.Users(ctx, model.IntList{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{ok.ID, g2.ID, forever.ID, extra.ID}; !reflect.DeepEqual(ids(users), want) {
		t.Fatalf("可用订阅 %v，期望 %v", ids(users), want)
	}
	if users[0].SpeedLimit == nil || *users[0].SpeedLimit != 0 || *users[0].DeviceLimit != 3 || users[0].UUID != ok.UUID {
		t.Fatalf("字段错误: %+v", users[0])
	}
	b := mustEncode(t, obj{{"users", users}})
	if !strings.Contains(string(b), `"speed_limit":0,"device_limit":3`) || strings.Count(string(b), "speed_limit") != 1 {
		t.Fatalf("空值应省略、0 应保留: %s", b)
	}
	if users, _ := e.Users(ctx, model.IntList{2}); !reflect.DeepEqual(ids(users), []int64{g2.ID}) {
		t.Fatalf("按权限组过滤错误: %v", ids(users))
	}
	if users, err := e.Users(ctx, nil); err != nil || len(users) != 0 || users == nil {
		t.Fatalf("无权限组应返回空数组: %v %v", users, err)
	}
}

func (e *env) reload(t *testing.T, s *model.Subscription) *model.Subscription {
	t.Helper()
	var out model.Subscription
	if err := e.DB.Take(&out, s.ID).Error; err != nil {
		t.Fatal(err)
	}
	return &out
}

func statUsers(t *testing.T, db *gorm.DB) []model.StatUser {
	t.Helper()
	var rows []model.StatUser
	if err := db.Order("subscription_id").Order("server_rate").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func testTraffic(t *testing.T, e *env) {
	ctx := context.Background()
	u, s1 := e.user(t, "a@x.com", nil)
	s2 := e.sub(t, u.ID, nil)
	srv := &model.ServerVless{ServerBase: baseNode("v", 1), Network: "tcp"}
	e.createNode(t, srv)

	data := Traffic{s1.ID: {100, 200}, s2.ID: {10, 0}, 999999: {5, 5}}
	for i := 0; i < 2; i++ {
		if err := e.Record(ctx, model.ServerTypeVless, srv, data); err != nil {
			t.Fatal(err)
		}
	}
	up := e.Cache.HGetAll(ctx, e.Cache.KeyOf(cache.KeyTrafficUpload, nil)).Val()
	if up[strconv.FormatInt(s1.ID, 10)] != "300" {
		t.Fatalf("应乘倍率累加: %v", up)
	}
	rows := statUsers(t, e.DB)
	if len(rows) != 2 || rows[0].U != 200 || rows[0].D != 400 || rows[0].UserID != u.ID || rows[0].ServerRate != 1.5 ||
		rows[0].RecordAt != time.Date(2027, 3, 15, 0, 0, 0, 0, time.Local).Unix() || rows[0].RecordType != "d" {
		t.Fatalf("用户统计错误（应为原始流量、按订阅累加、跳过不存在的订阅）: %+v", rows)
	}
	var ss []model.StatServer
	e.DB.Find(&ss)
	if len(ss) != 1 || ss[0].U != 230 || ss[0].D != 410 || ss[0].ServerType != model.ServerTypeVless || ss[0].ServerID != srv.ID {
		t.Fatalf("节点统计错误: %+v", ss)
	}

	// 流量重置进行中时不写入。
	e.Cache.Set(ctx, e.Cache.KeyOf(cache.KeyTrafficResetLock, nil), 1, time.Minute)
	if err := e.FlushTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	if e.reload(t, s1).U != 0 {
		t.Fatal("重置锁存在时不应写入")
	}
	e.Cache.Del(ctx, e.Cache.KeyOf(cache.KeyTrafficResetLock, nil))

	// 上次失败遗留的待写入数据与本次合并。
	e.Cache.HSet(ctx, e.Cache.KeyOf(cache.KeyTrafficUploadPending, nil), strconv.FormatInt(s2.ID, 10), 7, "bad", "x")
	e.now = e.now.Add(time.Minute)
	if err := e.FlushTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	r1, r2 := e.reload(t, s1), e.reload(t, s2)
	if r1.U != 300 || r1.D != 600 || r1.T != e.now.Unix() || r2.U != 37 || r2.D != 0 {
		t.Fatalf("写入结果错误: s1=%d/%d t=%d s2=%d/%d", r1.U, r1.D, r1.T, r2.U, r2.D)
	}
	for _, k := range []cache.KeyName{cache.KeyTrafficUpload, cache.KeyTrafficDownload, cache.KeyTrafficUploadPending, cache.KeyTrafficDownloadPending} {
		if n := e.Cache.Exists(ctx, e.Cache.KeyOf(k, nil)).Val(); n != 0 {
			t.Fatalf("%s 应已清空", k)
		}
	}
	var stale int64
	e.DB.Model(&model.Subscription{}).Where("id = ?", 999999).Count(&stale)
	if stale != 0 {
		t.Fatal("不存在的订阅不应被创建")
	}

	// 大流量（超过 32 位整数）。
	big := int64(5) << 40
	if err := e.Record(ctx, model.ServerTypeVless, srv, Traffic{s1.ID: {big, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := e.FlushTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.reload(t, s1).U; got != 300+big*3/2 {
		t.Fatalf("大流量写入错误: %d", got)
	}
}

func testConcurrent(t *testing.T, e *env) {
	ctx := context.Background()
	_, s1 := e.user(t, "a@x.com", nil)
	_, s2 := e.user(t, "b@x.com", nil)
	srv := &model.ServerTrojan{ServerBase: baseNode("t", 1)}
	srv.Rate = "1"
	e.createNode(t, srv)
	const workers, rounds = 8, 10
	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds+rounds)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				errs <- e.Record(ctx, model.ServerTypeTrojan, srv, Traffic{s1.ID: {1, 2}, s2.ID: {3, 4}})
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			errs <- e.FlushTraffic(ctx)
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := e.FlushTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	r1, r2 := e.reload(t, s1), e.reload(t, s2)
	const n = workers * rounds
	if r1.U != n || r1.D != 2*n || r2.U != 3*n || r2.D != 4*n {
		t.Fatalf("并发后流量不准确: %d/%d %d/%d", r1.U, r1.D, r2.U, r2.D)
	}
	rows := statUsers(t, e.DB)
	if len(rows) != 2 || rows[0].U != n || rows[1].D != 4*n {
		t.Fatalf("并发后统计不准确: %+v", rows)
	}
}

// testConcurrentStat 多个统计任务并发插入相互重叠的新统计行（MySQL 间隙锁会死锁），应全部成功且不重复计数。
func testConcurrentStat(t *testing.T, e *env) {
	ctx := context.Background()
	u, first := e.user(t, "stat@x.com", nil)
	ids := []int64{first.ID}
	for len(ids) < 120 {
		ids = append(ids, e.sub(t, u.ID, nil).ID)
	}
	const workers, rounds, width = 10, 5, 60
	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range rounds {
				data := Traffic{}
				for k := range width {
					data[ids[(w*7+k)%len(ids)]] = [2]int64{1, 2}
				}
				errs <- e.Stat(ctx, statTask{ServerID: 1, ServerType: model.ServerTypeTrojan, Rate: 1, RecordAt: int64(r+1) * 86400, Data: data})
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发统计不应失败: %v", err)
		}
	}
	var sum struct{ U, D int64 }
	e.DB.Model(&model.StatUser{}).Select("COALESCE(SUM(u), 0) AS u, COALESCE(SUM(d), 0) AS d").Scan(&sum)
	if want := int64(workers * rounds * width); sum.U != want || sum.D != 2*want {
		t.Fatalf("并发统计总量不准确: %+v，应为 %d", sum, want)
	}
	var server struct{ U int64 }
	e.DB.Model(&model.StatServer{}).Select("COALESCE(SUM(u), 0) AS u").Scan(&server)
	if server.U != int64(workers*rounds*width) {
		t.Fatalf("并发节点统计不准确: %d", server.U)
	}
}

func aliveOf(t *testing.T, e *env, id int64) int64 {
	t.Helper()
	var c aliveCount
	ok, err := e.Cache.GetJSON(context.Background(), e.Cache.KeyOf(cache.KeyAliveIP, id), &c)
	if err != nil || !ok {
		t.Fatalf("读取在线数失败: %v %v", ok, err)
	}
	return c.AliveIP
}

func testAlive(t *testing.T, e *env) {
	ctx := context.Background()
	_, s1 := e.user(t, "a@x.com", func(s *model.Subscription) { s.DeviceLimit = i64p(2) })
	_, s2 := e.user(t, "b@x.com", func(s *model.Subscription) { s.DeviceLimit = i64p(1); s.ExpiredAt = i64p(1) })
	_, s3 := e.user(t, "c@x.com", nil)
	_, s4 := e.user(t, "d@x.com", func(s *model.Subscription) { s.DeviceLimit = i64p(5) })

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.Alive(ctx, "vless", 1, map[int64][]string{s1.ID: {"1.1.1.1_1", "2.2.2.2_1"}, s2.ID: {"3.3.3.3_1"}, s3.ID: {"4.4.4.4_1"}}))
	must(e.Alive(ctx, "trojan", 1, map[int64][]string{s1.ID: {"1.1.1.1_2"}}))
	if n := aliveOf(t, e, s1.ID); n != 3 {
		t.Fatalf("按连接累加应为 3，实际 %d", n)
	}
	e.set(t, map[string]any{"device_limit_mode": 1})
	must(e.Alive(ctx, "trojan", 1, map[int64][]string{s1.ID: {"1.1.1.1_2"}}))
	if n := aliveOf(t, e, s1.ID); n != 2 {
		t.Fatalf("按 IP 去重应为 2，实际 %d", n)
	}
	// 101 秒后只有 trojan 节点上报，vless 节点的数据不再计入。
	e.now = e.now.Add(101 * time.Second)
	must(e.Alive(ctx, "trojan", 1, map[int64][]string{s1.ID: {"9.9.9.9_2"}}))
	if n := aliveOf(t, e, s1.ID); n != 1 {
		t.Fatalf("过期节点应被清理，实际 %d", n)
	}
	if f := e.Cache.HKeys(ctx, e.Cache.KeyOf(cache.KeyAliveNodes, s1.ID)).Val(); !reflect.DeepEqual(f, []string{"trojan1"}) {
		t.Fatalf("过期节点字段应删除: %v", f)
	}

	list, err := e.AliveList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(list, map[int64]int64{s1.ID: 1}) {
		t.Fatalf("在线列表应只含有设备限制、可用且有在线数据的订阅: %v", list)
	}
	must(e.Alive(ctx, "vless", 2, map[int64][]string{s4.ID: {"5.5.5.5_2"}}))
	if list, _ := e.AliveList(ctx); len(list) != 1 {
		t.Fatalf("在线列表应缓存 60 秒: %v", list)
	}
	e.Cache.Del(ctx, e.Cache.KeyOf(cache.KeyAliveList, nil))
	if list, _ := e.AliveList(ctx); !reflect.DeepEqual(list, map[int64]int64{s1.ID: 1, s4.ID: 1}) {
		t.Fatalf("缓存过期后应重新计算: %v", list)
	}

	// 多个节点并发上报同一订阅，最终计数包含全部节点。
	e.set(t, map[string]any{"device_limit_mode": 0})
	var wg sync.WaitGroup
	for i := 1; i <= 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				_ = e.Alive(ctx, "vmess", int64(i), map[int64][]string{s3.ID: {fmt.Sprintf("10.0.0.%d_%d", i, i)}})
			}
		}(i)
	}
	wg.Wait()
	must(e.Alive(ctx, "vmess", 1, map[int64][]string{s3.ID: {"10.0.0.1_1"}}))
	if n := aliveOf(t, e, s3.ID); n != 6 {
		t.Fatalf("并发上报后应为 6，实际 %d", n)
	}
}

func configOf(t *testing.T, e *env, srv interface {
	Common() *model.ServerBase
	TableName() string
}) map[string]any {
	t.Helper()
	o, err := e.UniConfig(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	return decodeAny(t, mustEncode(t, o)).(map[string]any)
}

func testConfig(t *testing.T, e *env) {
	ctx := context.Background()
	e.set(t, map[string]any{"server_push_interval": 30, "server_pull_interval": 90, "server_node_report_min_traffic": 5, "server_device_online_min_traffic": 7})
	r1 := &model.ServerRoute{Remarks: "a", Match: `["x.com","y.com"]`, Action: "block"}
	r2 := &model.ServerRoute{Remarks: "b", Match: `{"1":"z"}`, Action: "dns", ActionValue: strp("1.1.1.1")}
	r3 := &model.ServerRoute{Remarks: "c", Match: `plain`, Action: "block"}
	for _, r := range []*model.ServerRoute{r1, r2, r3} {
		if err := e.DB.Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}

	ss := &model.ServerShadowsocks{ServerBase: baseNode("ss", 1), Cipher: "2022-blake3-aes-128-gcm", ObfsSettings: model.JSON(`{}`)}
	ss.RouteID = model.IntList{r3.ID, 99999, r1.ID, r2.ID, r1.ID}
	e.createNode(t, ss)
	c := configOf(t, e, ss)
	if c["server_key"] != serverKey(ss.CreatedAt, 16) || c["cipher"] != ss.Cipher || !reflect.DeepEqual(c["obfs_settings"], []any{}) {
		t.Fatalf("Shadowsocks 配置错误: %v", c)
	}
	if !reflect.DeepEqual(c["base_config"], map[string]any{"push_interval": json.Number("30"), "pull_interval": json.Number("90")}) {
		t.Fatalf("base_config 错误: %v", c["base_config"])
	}
	routes := c["routes"].([]any)
	if len(routes) != 3 || routes[0].(map[string]any)["match"] != "plain" ||
		!reflect.DeepEqual(routes[1].(map[string]any)["match"], []any{"x.com", "y.com"}) ||
		!reflect.DeepEqual(routes[2].(map[string]any)["match"], map[string]any{"1": "z"}) ||
		routes[2].(map[string]any)["action_value"] != "1.1.1.1" {
		t.Fatalf("路由应按 route_id 顺序、去重并跳过不存在的: %v", routes)
	}

	hy := &model.ServerHysteria{ServerBase: baseNode("hy", 1), Version: 2, Obfs: strp("salamander"), ObfsPassword: strp("pw")}
	e.createNode(t, hy)
	c = configOf(t, e, hy)
	if c["ignore_client_bandwidth"] != true || c["obfs-password"] != "pw" || c["obfs"] != "salamander" || c["routes"] != nil {
		t.Fatalf("Hysteria2 配置错误: %v", c)
	}
	if _, has := c["routes"]; has {
		t.Fatal("没有路由时不应输出 routes")
	}
	hy.Version, hy.UpMbps = 1, 100
	c = configOf(t, e, hy)
	if c["obfs"] != "pw" || c["ignore_client_bandwidth"] != nil {
		t.Fatalf("Hysteria1 配置错误: %v", c)
	}
	tuic := &model.ServerTuic{ServerBase: baseNode("tuic", 1), ZeroRTTHandshake: 1}
	if c = configOf(t, e, tuic); c["zero_rtt_handshake"] != true {
		t.Fatalf("TUIC 配置错误: %v", c)
	}
	vmess := &model.ServerVmess{ServerBase: baseNode("vm", 1), TLS: 1, Network: "ws", NetworkSettings: model.JSON(`{"path":"/a"}`)}
	if c = configOf(t, e, vmess); c["tls"] != json.Number("1") || !reflect.DeepEqual(c["networkSettings"], map[string]any{"path": "/a"}) {
		t.Fatalf("VMess 配置错误: %v", c)
	}
	v2 := &model.ServerV2node{ServerBase: baseNode("v2", 1), ListenIP: "0.0.0.0", Protocol: "shadowsocks", Cipher: strp("2022-blake3-aes-256-gcm")}
	if c = configOf(t, e, v2); len(c) != 1 || c["base_config"] == nil {
		t.Fatalf("v2node 通过 UniProxy 只返回 base_config: %v", c)
	}
	e.createNode(t, v2)
	o, err := e.V2Config(ctx, v2)
	if err != nil {
		t.Fatal(err)
	}
	c = decodeAny(t, mustEncode(t, o)).(map[string]any)
	if c["server_key"] != serverKey(v2.CreatedAt, 32) || c["ignore_client_bandwidth"] != true || c["zero_rtt_handshake"] != false ||
		c["base_config"].(map[string]any)["device_online_min_traffic"] != json.Number("7") || c["listen_ip"] != "0.0.0.0" {
		t.Fatalf("v2 config 错误: %v", c)
	}
}

func testHTTP(t *testing.T, e *env) {
	ctx := context.Background()
	u, s1 := e.user(t, "a@x.com", func(s *model.Subscription) { s.DeviceLimit = i64p(2) })
	other, os1 := e.user(t, "b@x.com", nil)
	s2 := e.sub(t, u.ID, nil)
	vless := &model.ServerVless{ServerBase: baseNode("v", 1), Network: "tcp"}
	vmess := &model.ServerVmess{ServerBase: baseNode("vm", 1), Network: "tcp"}
	trojan := &model.ServerTrojan{ServerBase: baseNode("tj", 1)}
	ss := &model.ServerShadowsocks{ServerBase: baseNode("ss", 1), Cipher: "aes-128-gcm"}
	v2 := &model.ServerV2node{ServerBase: baseNode("v2", 1), ListenIP: "0.0.0.0", Protocol: "vless"}
	hy := &model.ServerHysteria{ServerBase: baseNode("hy", 1), Version: 2}
	for _, n := range []any{vless, vmess, trojan, ss, v2, hy} {
		e.createNode(t, n)
	}

	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{Svc: e.Service, Auth: e.auth})
	type req struct {
		method, path, body string
		header             map[string]string
	}
	do := func(q req) *httptest.ResponseRecorder {
		hr := httptest.NewRequest(q.method, q.path, strings.NewReader(q.body))
		hr.Header.Set("Content-Type", "application/json")
		for k, v := range q.header {
			hr.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, hr)
		return w
	}
	get := func(path string, h map[string]string) *httptest.ResponseRecorder { return do(req{"GET", path, "", h}) }
	uni := func(action, typ string, id int64) string {
		return fmt.Sprintf("/api/v1/server/UniProxy/%s?token=%s&node_type=%s&node_id=%d", action, token, typ, id)
	}

	notFound := get("/api/v1/server/definitely-not-exist", nil)
	for _, p := range []string{
		fmt.Sprintf("/api/v1/server/UniProxy/user?node_type=vless&node_id=%d", vless.ID),
		fmt.Sprintf("/api/v1/server/UniProxy/user?token=wrong&node_type=vless&node_id=%d", vless.ID),
		uni("user", "vless", 99999), uni("user", "socks", vless.ID), uni("nothing", "vless", vless.ID),
		"/api/v1/server/Unknown/user?token=" + token, "/api/v1/server/uniproxy/user?token=" + token + "&node_type=vless&node_id=1",
		"/api/v2/server/config?token=wrong&node_id=1", "/api/v2/server/config?token=" + token + "&node_id=99999",
		"/api/v1/server/Deepbwork/user?token=" + token + "&node_id=99999",
		fmt.Sprintf("/api/v1/server/Deepbwork/config?token=%s&node_id=%d", token, vmess.ID),
		fmt.Sprintf("/api/v1/server/ShadowsocksTidalab/config?token=%s&node_id=%d", token, ss.ID),
	} {
		if w := get(p, nil); w.Code != http.StatusNotFound || w.Body.String() != notFound.Body.String() {
			t.Errorf("%s 应返回统一 404，实际 %d %s", p, w.Code, w.Body)
		}
	}

	// 用户列表：JSON、ETag、msgpack、类型别名、方法名大小写。
	w := get(uni("user", "vless", vless.ID), nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("用户列表失败: %d %s", w.Code, w.Body)
	}
	var ur struct{ Users []User }
	json.Unmarshal(w.Body.Bytes(), &ur)
	if !reflect.DeepEqual(ids(ur.Users), []int64{s1.ID, os1.ID, s2.ID}) {
		t.Fatalf("用户列表错误: %s", w.Body)
	}
	tag := w.Header().Get("ETag")
	if w := get(uni("user", "vless", vless.ID), map[string]string{"If-None-Match": tag}); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("ETag 相同应返回 304: %d", w.Code)
	}
	var last int64
	e.Cache.GetJSON(ctx, e.Cache.KeyOf(cache.KeyServerLastCheckAt, cache.ServerKey("vless", vless.ID)), &last)
	if last != e.now.Unix() {
		t.Fatal("应记录最后检查时间")
	}
	w = get(uni("USER", "vless", vless.ID), map[string]string{"X-Response-Format": "msgpack"})
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-msgpack" {
		t.Fatalf("msgpack 失败: %d", w.Code)
	}
	var mp struct {
		Users []map[string]any `msgpack:"users"`
	}
	if err := msgpack.Unmarshal(w.Body.Bytes(), &mp); err != nil || len(mp.Users) != 3 || mp.Users[0]["uuid"] != s1.UUID {
		t.Fatalf("msgpack 内容错误: %v %v", mp, err)
	}
	if _, has := mp.Users[1]["device_limit"]; has {
		t.Fatal("msgpack 中空值应省略")
	}
	if w := get(uni("user", "hysteria2", hy.ID), nil); w.Code != 200 {
		t.Fatalf("hysteria2 应映射为 hysteria: %d", w.Code)
	}
	if w := get(uni("user", "v2ray", vmess.ID), nil); w.Code != 200 {
		t.Fatalf("v2ray 应映射为 vmess: %d", w.Code)
	}
	if w := get(fmt.Sprintf("/api/v1/server/uniProxy/user?token=%s&node_type=vless&node_id=%d", token, vless.ID), nil); w.Code != 200 {
		t.Fatalf("类名首字母小写应可访问: %d", w.Code)
	}
	// 表单提交 token。
	fr := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/server/UniProxy/user?node_type=vless&node_id=%d", vless.ID), strings.NewReader("token="+token))
	fr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	fw := httptest.NewRecorder()
	r.Engine.ServeHTTP(fw, fr)
	if fw.Code != 200 {
		t.Fatalf("表单 token 应可用: %d", fw.Code)
	}

	// 上报流量与在线。
	w = do(req{"POST", uni("push", "vless", vless.ID), fmt.Sprintf(`{"%d":[10,20],"%d":[1,1]}`, s1.ID, s2.ID), nil})
	if w.Code != 200 || w.Body.String() != `{"data":true}` {
		t.Fatalf("上报流量失败: %d %s", w.Code, w.Body)
	}
	var online int64
	e.Cache.GetJSON(ctx, e.Cache.KeyOf(cache.KeyServerOnlineUser, cache.ServerKey("vless", vless.ID)), &online)
	if online != 2 {
		t.Fatalf("在线人数应为 2: %d", online)
	}
	if w := do(req{"POST", uni("push", "vless", vless.ID), `{bad`, nil}); w.Code != 400 || !strings.Contains(w.Body.String(), "Invalid traffic data") {
		t.Fatalf("非法数据应返回 400: %d %s", w.Code, w.Body)
	}
	if w := do(req{"POST", uni("push", "vless", vless.ID), ``, nil}); w.Code != 200 {
		t.Fatalf("空上报应成功: %d", w.Code)
	}
	w = do(req{"POST", uni("alive", "vless", vless.ID), fmt.Sprintf(`{"%d":["1.1.1.1_1","2.2.2.2_1"]}`, s1.ID), nil})
	if w.Code != 200 || aliveOf(t, e, s1.ID) != 2 {
		t.Fatalf("上报在线失败: %d %s", w.Code, w.Body)
	}
	if w := do(req{"POST", uni("alive", "vless", vless.ID), `"x"`, nil}); w.Code != 400 {
		t.Fatalf("在线数据格式错误应返回 400: %d", w.Code)
	}
	w = get(uni("alivelist", "vless", vless.ID), nil)
	if w.Code != 200 || w.Body.String() != fmt.Sprintf(`{"alive":{"%d":2}}`, s1.ID) {
		t.Fatalf("在线列表错误: %s", w.Body)
	}

	// 节点配置。
	w = get(uni("config", "hysteria2", hy.ID), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"obfs-password":null`) {
		t.Fatalf("配置失败: %d %s", w.Code, w.Body)
	}
	if w2 := get(uni("config", "hysteria2", hy.ID), map[string]string{"If-None-Match": w.Header().Get("ETag")}); w2.Code != http.StatusNotModified {
		t.Fatalf("配置 ETag 相同应返回 304: %d", w2.Code)
	}
	v2path := fmt.Sprintf("/api/v2/server/config?token=%s&node_id=%d", token, v2.ID)
	w = get(v2path, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"protocol":"vless"`) {
		t.Fatalf("v2 config 失败: %d %s", w.Code, w.Body)
	}
	tag = w.Header().Get("ETag")
	if w := get(v2path, map[string]string{"If-None-Match": tag}); w.Code != 200 {
		t.Fatal("v2 config 带引号的 ETag 与原项目一样不命中")
	}
	if w := get(v2path, map[string]string{"If-None-Match": strings.Trim(tag, `"`)}); w.Code != http.StatusNotModified || w.Header().Get("ETag") != tag {
		t.Fatalf("v2 config ETag 完全相同时应返回 304: %d", w.Code)
	}

	// 旧版接口。
	w = get(fmt.Sprintf("/api/v1/server/Deepbwork/user?token=%s&node_id=%d", token, vmess.ID), nil)
	want := fmt.Sprintf(`{"id":%d,"speed_limit":null,"device_limit":2,"v2ray_user":{"uuid":"%s","email":"%s@v2board.user","alter_id":0,"level":0}}`, s1.ID, s1.UUID, s1.UUID)
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), `{"msg":"ok","data":[`+want) {
		t.Fatalf("Deepbwork 用户错误: %s", w.Body)
	}
	w = do(req{"POST", fmt.Sprintf("/api/v1/server/Deepbwork/submit?token=%s&node_id=%d", token, vmess.ID), fmt.Sprintf(`[{"user_id":%d,"u":4,"d":6}]`, s2.ID), nil})
	if w.Code != 200 || w.Body.String() != `{"ret":1,"msg":"ok"}` {
		t.Fatalf("Deepbwork 上报错误: %s", w.Body)
	}
	w = get(fmt.Sprintf("/api/v1/server/Deepbwork/config?token=%s&node_id=%d&local_port=10085", token, vmess.ID), nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=UTF-8" || !strings.Contains(w.Body.String(), `"port":10085`) {
		t.Fatalf("Deepbwork 配置错误: %d %s", w.Code, w.Body)
	}
	w = get(fmt.Sprintf("/api/v1/server/TrojanTidalab/user?token=%s&node_id=%d", token, trojan.ID), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), fmt.Sprintf(`"trojan_user":{"password":"%s"}`, s1.UUID)) {
		t.Fatalf("TrojanTidalab 用户错误: %s", w.Body)
	}
	w = get(fmt.Sprintf("/api/v1/server/TrojanTidalab/config?token=%s&node_id=%d&local_port=10000", token, trojan.ID), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sni":"h.example.com"`) {
		t.Fatalf("TrojanTidalab 配置错误: %s", w.Body)
	}
	w = get(fmt.Sprintf("/api/v1/server/ShadowsocksTidalab/user?token=%s&node_id=%d", token, ss.ID), nil)
	want = fmt.Sprintf(`{"data":[{"id":%d,"port":8443,"cipher":"aes-128-gcm","secret":"%s"}`, s1.ID, s1.UUID)
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), want) || w.Header().Get("ETag") == "" {
		t.Fatalf("ShadowsocksTidalab 用户错误: %s", w.Body)
	}
	w = do(req{"POST", fmt.Sprintf("/api/v1/server/ShadowsocksTidalab/submit?token=%s&node_id=%d", token, ss.ID), `[]`, nil})
	if w.Code != 200 || w.Body.String() != `{"ret":1,"msg":"ok"}` {
		t.Fatalf("ShadowsocksTidalab 上报错误: %s", w.Body)
	}

	// IP 白名单。
	allow, _ := ParseWhitelist([]string{"10.0.0.0/8"})
	r2, _ := server.NewRouter(cfg, zap.NewNop())
	r2.Mount(&Module{Svc: e.Service, Auth: e.auth, Allow: allow})
	for _, c := range []struct {
		ip   string
		code int
	}{{"192.0.2.1:1000", 404}, {"10.1.2.3:1000", 200}} {
		hr := httptest.NewRequest("GET", uni("user", "vless", vless.ID), nil)
		hr.RemoteAddr = c.ip
		w := httptest.NewRecorder()
		r2.Engine.ServeHTTP(w, hr)
		if w.Code != c.code {
			t.Errorf("来源 %s 应返回 %d，实际 %d", c.ip, c.code, w.Code)
		}
	}

	// 用户流量明细。
	e.DB.Create(&model.StatUser{UserID: u.ID, SubscriptionID: &s1.ID, ServerRate: 1, U: 1, D: 1, RecordType: "d", RecordAt: e.now.AddDate(0, -1, 0).Unix()})
	e.DB.Create(&model.StatUser{UserID: other.ID, SubscriptionID: &os1.ID, ServerRate: 1, U: 1, D: 1, RecordType: "d", RecordAt: e.now.Unix()})
	tok, _ := e.auth.Issue(ctx, u.ID, "", "")
	logs := func(q string) []TrafficLog {
		t.Helper()
		w := get("/api/t1/user/traffic-log"+q, map[string]string{"Authorization": tok})
		if w.Code != 200 {
			t.Fatalf("流量明细失败: %d %s", w.Code, w.Body)
		}
		var resp struct{ Data []TrafficLog }
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.Data
	}
	all := logs("")
	if len(all) != 2 || all[0].UserID != u.ID || all[0].ServerRate != 1.5 {
		t.Fatalf("应返回本人本月全部订阅的记录: %+v", all)
	}
	if one := logs(fmt.Sprintf("?subscription_id=%d", s2.ID)); len(one) != 1 || *one[0].SubscriptionID != s2.ID || one[0].U != 5 {
		t.Fatalf("按订阅筛选错误: %+v", one)
	}
	if none := logs(fmt.Sprintf("?subscription_id=%d", os1.ID)); len(none) != 0 {
		t.Fatalf("不应返回他人订阅的记录: %+v", none)
	}
	if w := get("/api/t1/user/traffic-log?subscription_id=abc", map[string]string{"Authorization": tok}); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("非法参数应返回 422: %d", w.Code)
	}
	if w := get("/api/t1/user/traffic-log", nil); w.Code == 200 {
		t.Fatal("未登录不应访问")
	}
}
