package node

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/testutil"
)

// ---------- 不依赖数据库的测试 ----------

func TestParams(t *testing.T) {
	p, ok := parseParams([]byte(`{"name":"  a ","host":"","n":{"x":" ","y":[" b ",""]},"num":1.50}`))
	if !ok {
		t.Fatal("解析失败")
	}
	if p.m["name"] != "a" || p.m["host"] != nil || !p.has("host") || p.isset("host") {
		t.Fatalf("去空白 / 空串转 null 错误: %#v", p.m)
	}
	n := p.m["n"].(map[string]any)
	if n["x"] != nil || n["y"].([]any)[0] != "b" || n["y"].([]any)[1] != nil {
		t.Fatalf("嵌套字段处理错误: %#v", n)
	}
	if !p.isNumeric("num") || p.isInteger("num") {
		t.Fatal("数字判断错误")
	}
	if _, ok := parseParams([]byte(`[1]`)); ok {
		t.Fatal("非对象应失败")
	}
	for _, c := range []struct {
		in   any
		want int64
	}{{json.Number("12"), 12}, {json.Number("1.9"), 1}, {"12abc", 12}, {"abc", 0}, {true, 1}, {"-3", -3}} {
		if got := phpInt(c.in); got != c.want {
			t.Errorf("phpInt(%v)=%d，期望 %d", c.in, got, c.want)
		}
	}
	for _, c := range []struct {
		in   any
		want bool
	}{{"true", true}, {"On", true}, {json.Number("1"), true}, {"0", false}, {"abc", false}, {false, false}, {true, true}} {
		if got := phpBool(c.in); got != c.want {
			t.Errorf("phpBool(%v)=%v", c.in, got)
		}
	}
	for _, c := range []struct {
		in   any
		want bool
	}{{nil, true}, {"0", true}, {"", true}, {json.Number("0"), true}, {[]any{}, true}, {"a", false}, {json.Number("1"), false}} {
		if got := phpEmpty(c.in); got != c.want {
			t.Errorf("phpEmpty(%v)=%v", c.in, got)
		}
	}
}

func TestKeys(t *testing.T) {
	priv, pub, err := x25519Pair()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := x25519Public(priv); !ok || got != pub || len(priv) != 43 {
		t.Fatalf("公钥推导错误")
	}
	if _, ok := x25519Public("!!"); ok {
		t.Fatal("非法私钥应失败")
	}

	key, cfg, err := echKeyPair("cover.example.com")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := base64.StdEncoding.DecodeString(cfg)
	k, _ := base64.StdEncoding.DecodeString(key)
	be := binary.BigEndian
	if be.Uint16(c[0:]) != 0xfe0d || int(be.Uint16(c[2:])) != len(c)-4 || be.Uint16(c[5:]) != 0x0020 || be.Uint16(c[7:]) != 32 {
		t.Fatalf("ECHConfig 头部错误: %x", c[:9])
	}
	sni := "cover.example.com"
	tail := c[len(c)-2-len(sni)-1:]
	if int(tail[0]) != len(sni) || string(tail[1:1+len(sni)]) != sni || be.Uint16(tail[len(tail)-2:]) != 0 {
		t.Fatalf("public_name 错误")
	}
	if int(be.Uint16(k[0:])) != len(c) || string(k[2:2+len(c)]) != string(c) {
		t.Fatal("ECHKeys 中的配置与 ECHConfigList 不一致")
	}
	rest := k[2+len(c):]
	if be.Uint16(rest[0:]) != 1 || rest[2] != c[4] || be.Uint16(rest[3:]) != 32 || len(rest) != 5+32 {
		t.Fatalf("ECHKeys 密钥部分错误: %x", rest[:5])
	}

	now := time.Unix(1_800_000_000, 0)
	certPEM, keyPEM, pin, err := selfSignedCert("node.example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blk.Bytes)
	if cert.Subject.CommonName != "node.example.com" || !cert.NotAfter.Equal(now.AddDate(0, 0, 3650)) || hex.EncodeToString(sum[:]) != pin {
		t.Fatal("证书内容或指纹错误")
	}
	kb, _ := pem.Decode([]byte(keyPEM))
	if kb.Type != "PRIVATE KEY" {
		t.Fatalf("私钥格式错误: %s", kb.Type)
	}
	if _, err := x509.ParsePKCS8PrivateKey(kb.Bytes); err != nil {
		t.Fatal(err)
	}
}

// TestPHPVector 与原项目 PHP 实现对比（testdata 由 php:8.2 + 原项目 Helper 生成）：
// X25519 公钥推导一致；ECH 密钥除随机的 config_id、公钥、私钥外逐字节一致。
func TestPHPVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/php_vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		X25519 []struct{ Private, Public string }
		ECH    []struct{ SNI, Key, Config string }
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, x := range v.X25519 {
		if got, ok := x25519Public(x.Private); !ok || got != x.Public {
			t.Errorf("公钥推导与 PHP 不一致: %s", x.Private)
		}
	}
	// mask 把随机部分清零：配置中 [4] 为 config_id、[9,41) 为公钥；密钥列表末尾为 config_id 与私钥。
	maskCfg := func(b []byte) []byte {
		b = append([]byte{}, b...)
		b[4] = 0
		clear(b[9:41])
		return b
	}
	maskKey := func(b []byte, cfgLen int) []byte {
		b = append([]byte{}, b...)
		copy(b[2:], maskCfg(b[2:2+cfgLen]))
		rest := b[2+cfgLen:]
		rest[2] = 0
		clear(rest[5:])
		return b
	}
	for _, e := range v.ECH {
		phpCfg, _ := base64.StdEncoding.DecodeString(e.Config)
		phpKey, _ := base64.StdEncoding.DecodeString(e.Key)
		key, cfg, err := echKeyPair(e.SNI)
		if err != nil {
			t.Fatal(err)
		}
		goCfg, _ := base64.StdEncoding.DecodeString(cfg)
		goKey, _ := base64.StdEncoding.DecodeString(key)
		if len(goCfg) != len(phpCfg) || len(goKey) != len(phpKey) {
			t.Fatalf("%s 长度不一致", e.SNI)
		}
		if hex.EncodeToString(maskCfg(goCfg)) != hex.EncodeToString(maskCfg(phpCfg)) ||
			hex.EncodeToString(maskKey(goKey, len(goCfg))) != hex.EncodeToString(maskKey(phpKey, len(phpCfg))) {
			t.Errorf("%s ECH 结构与 PHP 不一致", e.SNI)
		}
		// PHP 生成的私钥与配置中的公钥匹配，说明清零的位置确实是密钥
		priv := phpKey[len(phpKey)-32:]
		pub, _ := x25519Public(base64.RawURLEncoding.EncodeToString(priv))
		if pub != base64.RawURLEncoding.EncodeToString(phpCfg[9:41]) {
			t.Errorf("%s 向量中的公私钥位置判断错误", e.SNI)
		}
	}
}

// ---------- 数据库测试 ----------

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
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &env{now: time.Unix(1_800_000_000, 0), auth: auth.New(db, c, "base64:bm9kZS10ZXN0LWtleS0wMTIzNDU2Nzg5YWJjZGVm")}
	e.Service = &Service{DB: db, Cache: c, Settings: st, Now: func() time.Time { return e.now }}
	return e
}

func wantErr(t *testing.T, err error, want *httpx.Error) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != want.Msg {
		t.Fatalf("期望错误 %q，实际 %v", want.Msg, err)
	}
}

func wantMsg(t *testing.T, err error, msg string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != msg {
		t.Fatalf("期望错误 %q，实际 %v", msg, err)
	}
}

func (e *env) save(t *testing.T, typ string, id int64, body map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	if err := e.Save(context.Background(), typ, id, b); err != nil {
		t.Fatalf("保存 %s 失败: %v", typ, err)
	}
}

func (e *env) last(t *testing.T, typ string) Server {
	t.Helper()
	rows, err := List(context.Background(), e.DB, typ)
	if err != nil || len(rows) == 0 {
		t.Fatalf("读取 %s 失败: %v", typ, err)
	}
	return rows[len(rows)-1]
}

func base(extra map[string]any) map[string]any {
	m := map[string]any{"name": "节点", "group_id": []any{1, "2"}, "host": "a.example.com", "port": 443, "server_port": "8443", "rate": "1.5"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func settings(t *testing.T, j model.JSON) map[string]any {
	t.Helper()
	var m map[string]any
	if err := j.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNode(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("权限组", func(t *testing.T) { testGroups(t, newEnv(t, driver)) })
			t.Run("路由", func(t *testing.T) { testRoutes(t, newEnv(t, driver)) })
			t.Run("各类节点", func(t *testing.T) { testTypes(t, newEnv(t, driver)) })
			t.Run("校验", func(t *testing.T) { testValidate(t, newEnv(t, driver)) })
			t.Run("修改只写入提交的列", func(t *testing.T) { testPartialUpdate(t, newEnv(t, driver)) })
			t.Run("v2node 补全", func(t *testing.T) { testV2node(t, newEnv(t, driver)) })
			t.Run("显示复制删除排序", func(t *testing.T) { testOps(t, newEnv(t, driver)) })
			t.Run("总览", func(t *testing.T) { testOverview(t, newEnv(t, driver)) })
			t.Run("HTTP", func(t *testing.T) { testHTTP(t, newEnv(t, driver)) })
		})
	}
}

func testGroups(t *testing.T, e *env) {
	ctx := context.Background()
	_, err := e.SaveGroup(ctx, 0, "  ")
	wantErr(t, err, errGroupName)
	g1, err := e.SaveGroup(ctx, 0, " A ")
	if err != nil || g1.Name != "A" {
		t.Fatalf("新建失败: %v", err)
	}
	g2, _ := e.SaveGroup(ctx, 0, "B")
	if g, err := e.SaveGroup(ctx, g2.ID, "B2"); err != nil || g.Name != "B2" {
		t.Fatalf("修改失败: %v", err)
	}
	_, err = e.SaveGroup(ctx, 999999, "x")
	wantErr(t, err, errGroupNotExist)

	// 订阅数：同一用户两个订阅都计入
	var u *model.User
	var sub *model.Subscription
	e.DB.Transaction(func(tx *gorm.DB) error {
		u, sub, err = account.Create(tx, account.NewUser{Email: "g@x.com", Password: "password123"})
		return err
	})
	e.DB.Model(sub).Update("group_id", g1.ID)
	e.DB.Create(&model.Subscription{UserID: u.ID, Name: "二", GroupID: &g1.ID, UUID: "uuid-2", Token: "token-2"})
	e.save(t, model.ServerTypeTrojan, 0, base(map[string]any{"group_id": []any{g1.ID}, "network": "tcp"}))
	e.save(t, model.ServerTypeV2node, 0, base(map[string]any{"group_id": []any{g1.ID, g2.ID}, "protocol": "vmess", "tls": 0, "network": "tcp", "disable_sni": 0, "zero_rtt_handshake": 0}))
	gs, err := e.Groups(ctx)
	if err != nil || len(gs) != 2 {
		t.Fatalf("列表错误: %v", err)
	}
	if gs[0].UserCount != 2 || gs[0].ServerCount != 2 || gs[1].UserCount != 0 || gs[1].ServerCount != 1 {
		t.Fatalf("统计错误: %+v", gs)
	}

	// 删除检查：Trojan 节点在用（原项目漏查，D32）→ 套餐在用 → 订阅在用
	wantErr(t, e.DeleteGroup(ctx, g1.ID), errGroupUsedByNode)
	e.DB.Where("1 = 1").Delete(&model.ServerTrojan{})
	e.DB.Where("1 = 1").Delete(&model.ServerV2node{})
	e.DB.Create(&model.Plan{GroupID: g1.ID, TransferEnable: 1, Name: "P"})
	wantErr(t, e.DeleteGroup(ctx, g1.ID), errGroupUsedByPlan)
	e.DB.Where("1 = 1").Delete(&model.Plan{})
	wantErr(t, e.DeleteGroup(ctx, g1.ID), errGroupUsedBySub)
	e.DB.Model(&model.Subscription{}).Where("user_id = ?", u.ID).Update("group_id", nil)
	if err := e.DeleteGroup(ctx, g1.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.DeleteGroup(ctx, g1.ID), errGroupNotExist)
}

func testRoutes(t *testing.T, e *env) {
	ctx := context.Background()
	wantErr(t, e.SaveRoute(ctx, 0, RouteInput{Action: "block", Match: []string{"a"}}), errRouteRemarks)
	wantErr(t, e.SaveRoute(ctx, 0, RouteInput{Remarks: "r", Action: "drop", Match: []string{"a"}}), errRouteAction)
	wantErr(t, e.SaveRoute(ctx, 0, RouteInput{Remarks: "r", Action: "block"}), errRouteMatch)
	if err := e.SaveRoute(ctx, 0, RouteInput{Remarks: "r1", Action: "block", Match: []string{"a", "", "0", " b "}}); err != nil {
		t.Fatal(err)
	}
	v := " "
	if err := e.SaveRoute(ctx, 0, RouteInput{Remarks: "r2", Action: "default_out", Match: []string{"x"}, ActionValue: &v}); err != nil {
		t.Fatal(err)
	}
	rs, err := e.Routes(ctx)
	if err != nil || len(rs) != 2 {
		t.Fatalf("列表错误: %v", err)
	}
	if m, _ := json.Marshal(rs[0].Match); string(m) != `["a","b"]` {
		t.Fatalf("匹配值应去掉空项并保持数组: %s", m)
	}
	if m, _ := json.Marshal(rs[1].Match); string(m) != `[]` || rs[1].ActionValue != nil {
		t.Fatalf("default_out 匹配值应为空数组: %s", m)
	}
	val := "1.1.1.1"
	if err := e.SaveRoute(ctx, rs[0].ID, RouteInput{Remarks: "r1b", Action: "dns", Match: []string{"c"}, ActionValue: &val}); err != nil {
		t.Fatal(err)
	}
	var r model.ServerRoute
	e.DB.Take(&r, rs[0].ID)
	if r.Remarks != "r1b" || r.Action != "dns" || r.Match != `["c"]` || *r.ActionValue != val {
		t.Fatalf("修改错误: %+v", r)
	}
	// 历史数据中不是 JSON 数组的匹配值原样返回
	e.DB.Model(&r).Update("match", "plain")
	if rs, _ = e.Routes(ctx); rs[0].Match != "plain" {
		t.Fatalf("非数组匹配值应原样返回: %v", rs[0].Match)
	}
	wantErr(t, e.SaveRoute(ctx, 999999, RouteInput{Remarks: "x", Action: "block", Match: []string{"a"}}), errRouteNotExist)
	if err := e.DeleteRoute(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.DeleteRoute(ctx, r.ID), errRouteNotExist)
}

func testTypes(t *testing.T, e *env) {
	cases := map[string]map[string]any{
		model.ServerTypeShadowsocks: {"cipher": "2022-blake3-aes-128-gcm", "obfs": "http", "obfs_settings": map[string]any{"host": "h", "path": "/"}},
		model.ServerTypeVmess:       {"tls": 1, "network": "ws", "networkSettings": map[string]any{"path": "/ws", "security": "auto"}, "tlsSettings": map[string]any{"serverName": "s"}},
		model.ServerTypeTrojan:      {"network": "grpc", "network_settings": map[string]any{"serviceName": "g"}, "allow_insecure": 1, "server_name": "sni"},
		model.ServerTypeTuic:        {"insecure": 0, "disable_sni": 1, "zero_rtt_handshake": "1", "congestion_control": "bbr"},
		model.ServerTypeHysteria:    {"version": 2, "insecure": 0, "up_mbps": "100.6"},
		model.ServerTypeVless:       {"tls": 0, "network": "tcp", "flow": "xtls-rprx-vision"},
		model.ServerTypeAnytls:      {"insecure": 1, "padding_scheme": `["stop=8","0=30-30"]`},
		model.ServerTypeV2node:      {"protocol": "vless", "tls": 0, "network": "tcp", "disable_sni": 0, "zero_rtt_handshake": 0},
	}
	for typ, extra := range cases {
		e.save(t, typ, 0, base(extra))
		c := e.last(t, typ).Common()
		if c.Name != "节点" || c.Port != "443" || c.ServerPort != 8443 || c.Rate != "1.5" || len(c.GroupID) != 2 || c.GroupID[1] != 2 ||
			c.Show != 0 || c.CreatedAt != e.now.Unix() {
			t.Errorf("%s 共有字段错误: %+v", typ, c)
		}
	}
	ss := e.last(t, model.ServerTypeShadowsocks).(*model.ServerShadowsocks)
	if ss.Cipher != "2022-blake3-aes-128-gcm" || settings(t, ss.ObfsSettings)["path"] != "/" {
		t.Errorf("SS 字段错误")
	}
	vm := e.last(t, model.ServerTypeVmess).(*model.ServerVmess)
	if vm.TLS != 1 || vm.Network != "ws" || settings(t, vm.NetworkSettings)["path"] != "/ws" || settings(t, vm.TLSSettings)["serverName"] != "s" {
		t.Errorf("VMess 字段错误")
	}
	tj := e.last(t, model.ServerTypeTrojan).(*model.ServerTrojan)
	if *tj.Network != "grpc" || tj.AllowInsecure != 1 || *tj.ServerName != "sni" {
		t.Errorf("Trojan 字段错误")
	}
	tu := e.last(t, model.ServerTypeTuic).(*model.ServerTuic)
	if tu.DisableSNI != 1 || tu.ZeroRTTHandshake != 1 || *tu.CongestionControl != "bbr" {
		t.Errorf("TUIC 字段错误")
	}
	hy := e.last(t, model.ServerTypeHysteria).(*model.ServerHysteria)
	if hy.Version != 2 || hy.UpMbps != 101 || hy.DownMbps != 0 || hy.ObfsPassword != nil {
		t.Errorf("Hysteria 字段错误: %+v", hy)
	}
	vl := e.last(t, model.ServerTypeVless).(*model.ServerVless)
	if vl.Flow == nil || *vl.Flow != "xtls-rprx-vision" {
		t.Errorf("VLESS tcp 应保留流控")
	}
	at := e.last(t, model.ServerTypeAnytls).(*model.ServerAnytls)
	if string(at.PaddingScheme) != `["stop=8","0=30-30"]` {
		t.Errorf("AnyTLS 填充方案错误: %s", at.PaddingScheme)
	}
	v2 := e.last(t, model.ServerTypeV2node).(*model.ServerV2node)
	if v2.ListenIP != "0.0.0.0" || v2.Protocol != "vless" {
		t.Errorf("v2node 默认监听地址错误: %q", v2.ListenIP)
	}

	// Hysteria 开启混淆未填密码：随机 16 位（D34），两次不同
	e.save(t, model.ServerTypeHysteria, 0, base(map[string]any{"version": 2, "insecure": 0, "obfs": "salamander"}))
	p1 := e.last(t, model.ServerTypeHysteria).(*model.ServerHysteria).ObfsPassword
	e.save(t, model.ServerTypeHysteria, 0, base(map[string]any{"version": 2, "insecure": 0, "obfs": "salamander"}))
	p2 := e.last(t, model.ServerTypeHysteria).(*model.ServerHysteria).ObfsPassword
	if p1 == nil || p2 == nil || len(*p1) != 16 || *p1 == *p2 || *p1 == "ZDQxZDhjZDk4ZjAwYjIwNA==" {
		t.Errorf("混淆密码应随机生成")
	}
	e.save(t, model.ServerTypeHysteria, 0, base(map[string]any{"version": 2, "insecure": 0, "obfs": "salamander", "obfs_password": "mine"}))
	if p := e.last(t, model.ServerTypeHysteria).(*model.ServerHysteria).ObfsPassword; *p != "mine" {
		t.Errorf("应保留填写的混淆密码")
	}

	// VLESS：非 tcp 清除流控；REALITY 生成密钥；xhttp extra 类型转换；mlkem 补全
	e.save(t, model.ServerTypeVless, 0, base(map[string]any{
		"tls": 2, "network": "xhttp", "flow": "xtls-rprx-vision", "encryption": "mlkem768x25519plus",
		"encryption_settings": map[string]any{"rtt": "1rtt"},
		"network_settings": map[string]any{"extra": map[string]any{"noGRPCHeader": "true", "noSSEHeader": "0", "xPaddingObfsMode": "true",
			"scMaxBufferedPosts": "30", "xmux": map[string]any{"hKeepAlivePeriod": "5"}, "downloadSettings": map[string]any{"port": "443"}}},
	}))
	vl = e.last(t, model.ServerTypeVless).(*model.ServerVless)
	ts := settings(t, vl.TLSSettings)
	pub, ok := x25519Public(ts["private_key"].(string))
	sum := fmt.Sprintf("%x", sha1Sum(ts["private_key"].(string)))
	if vl.Flow != nil || !ok || ts["public_key"] != pub || ts["short_id"] != sum[:8] || ts["server_port"] != "443" {
		t.Errorf("REALITY 补全错误: %v", ts)
	}
	extra := settings(t, vl.NetworkSettings)["extra"].(map[string]any)
	if extra["noGRPCHeader"] != true || extra["noSSEHeader"] != false || extra["xPaddingObfsMode"] != "true" ||
		extra["scMaxBufferedPosts"] != float64(30) || extra["xmux"].(map[string]any)["hKeepAlivePeriod"] != float64(5) ||
		extra["downloadSettings"].(map[string]any)["port"] != float64(443) {
		t.Errorf("xhttp extra 转换错误: %v", extra)
	}
	es := settings(t, vl.EncryptionSettings)
	if es["ticket"] != "0s" || es["private_key"] == nil || es["password"] == nil || es["mode"] != nil {
		t.Errorf("mlkem 补全错误: %v", es)
	}
	// 提供了私钥时公钥由私钥推导
	e.save(t, model.ServerTypeVless, 0, base(map[string]any{"tls": 2, "network": "tcp", "tls_settings": map[string]any{"private_key": ts["private_key"], "short_id": "ab"}}))
	ts2 := settings(t, e.last(t, model.ServerTypeVless).(*model.ServerVless).TLSSettings)
	if ts2["public_key"] != pub || ts2["short_id"] != "ab" {
		t.Errorf("应由私钥推导公钥: %v", ts2)
	}
}

func testValidate(t *testing.T, e *env) {
	ctx := context.Background()
	try := func(typ string, body map[string]any, msg string) {
		t.Helper()
		b, _ := json.Marshal(body)
		wantMsg(t, e.Save(ctx, typ, 0, b), msg)
	}
	ss := map[string]any{"cipher": "aes-128-gcm"}
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "name": " "}), "节点名称不能为空")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "group_id": []any{}}), "权限组不能为空")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "group_id": "1"}), "权限组格式不正确")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "route_id": []any{"x"}}), "路由组格式不正确")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "parent_id": 1.5}), "父节点格式不正确")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "host": nil}), "节点地址不能为空")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "port": ""}), "连接端口不能为空")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "server_port": nil}), "后端服务端口不能为空")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "rate": "x"}), "倍率格式不正确")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "tags": "x"}), "标签格式不正确")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "show": 2}), "显示状态格式不正确")
	try(model.ServerTypeShadowsocks, base(nil), "加密方式不能为空")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "rc4"}), "加密方式格式不正确")
	try(model.ServerTypeShadowsocks, base(map[string]any{"cipher": "aes-128-gcm", "obfs": "tls"}), "混淆格式不正确")
	try(model.ServerTypeVmess, base(map[string]any{"tls": 0, "network": "foo"}), "传输协议格式不正确")
	try(model.ServerTypeVmess, base(map[string]any{"tls": 0, "network": "ws", "networkSettings": map[string]any{"security": "rc4"}}),
		"vmess加密类型只能是: auto, aes-128-gcm, chacha20-poly1305, none")
	try(model.ServerTypeTrojan, base(map[string]any{"network": "tcp", "allow_insecure": 2}), "允许不安全格式不正确")
	try(model.ServerTypeTuic, base(map[string]any{"disable_sni": 0, "zero_rtt_handshake": 0}), "insecure 格式不正确")
	try(model.ServerTypeHysteria, base(map[string]any{"version": 3, "insecure": 0}), "版本格式不正确")
	try(model.ServerTypeVless, base(map[string]any{"tls": 3, "network": "tcp"}), "TLS格式不正确")
	try(model.ServerTypeVless, base(map[string]any{"tls": 0, "network": "tcp", "flow": "xtls-rprx-direct"}), "流控格式不正确")
	try(model.ServerTypeV2node, base(map[string]any{"protocol": "socks", "tls": 0, "network": "tcp", "disable_sni": 0, "zero_rtt_handshake": 0}), "协议格式不正确")
	try(model.ServerTypeV2node, base(map[string]any{"protocol": "vless", "tls": 0, "network": "kcp", "disable_sni": 0, "zero_rtt_handshake": 0}), "传输协议格式不正确")
	wantErr(t, e.Save(ctx, "socks", 0, []byte(`{}`)), errTypeInvalid)
	wantErr(t, e.Save(ctx, model.ServerTypeShadowsocks, 0, []byte(`not json`)), errInvalid)
	b, _ := json.Marshal(base(ss))
	wantErr(t, e.Save(ctx, model.ServerTypeShadowsocks, 999999, b), errServerNotExist)
}

func testPartialUpdate(t *testing.T, e *env) {
	e.save(t, model.ServerTypeTrojan, 0, base(map[string]any{"network": "tcp", "server_name": "keep", "show": 1, "sort": 7}))
	id := e.last(t, model.ServerTypeTrojan).Common().ID
	e.now = e.now.Add(time.Hour)
	e.save(t, model.ServerTypeTrojan, id, base(map[string]any{"network": "ws", "name": "新名称"}))
	tj := e.last(t, model.ServerTypeTrojan).(*model.ServerTrojan)
	if tj.Name != "新名称" || *tj.Network != "ws" || *tj.ServerName != "keep" || tj.Show != 1 || *tj.Sort != 7 ||
		tj.UpdatedAt != e.now.Unix() || tj.CreatedAt == tj.UpdatedAt {
		t.Fatalf("未提交的列应保持不变: %+v", tj)
	}
	// 提交 null 时清空
	e.save(t, model.ServerTypeTrojan, id, base(map[string]any{"network": "ws", "server_name": nil}))
	if tj = e.last(t, model.ServerTypeTrojan).(*model.ServerTrojan); tj.ServerName != nil {
		t.Fatal("提交 null 应清空")
	}
	// Hysteria 修改时未提交混淆：混淆密码被清空、带宽补 0（与原项目一致）
	e.save(t, model.ServerTypeHysteria, 0, base(map[string]any{"version": 2, "insecure": 0, "obfs": "salamander", "up_mbps": 50}))
	hid := e.last(t, model.ServerTypeHysteria).Common().ID
	e.save(t, model.ServerTypeHysteria, hid, base(map[string]any{"version": 2, "insecure": 0}))
	hy := e.last(t, model.ServerTypeHysteria).(*model.ServerHysteria)
	if hy.ObfsPassword != nil || hy.UpMbps != 0 || hy.Obfs == nil {
		t.Fatalf("Hysteria 修改结果错误: %+v", hy)
	}
}

func testV2node(t *testing.T, e *env) {
	v2 := func() *model.ServerV2node { return e.last(t, model.ServerTypeV2node).(*model.ServerV2node) }
	flags := map[string]any{"disable_sni": 0, "zero_rtt_handshake": 0}
	with := func(m map[string]any) map[string]any {
		out := base(flags)
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	// trojan / hysteria2 / tuic 强制 TLS；anytls 的 TLS 0 改为 1
	for _, proto := range []string{"trojan", "hysteria2", "tuic", "anytls"} {
		e.save(t, model.ServerTypeV2node, 0, with(map[string]any{"protocol": proto, "tls": 0, "network": "tcp"}))
		if v2().TLS != 1 {
			t.Errorf("%s 应强制 TLS", proto)
		}
	}
	// shadowsocks 默认加密方式；混淆随机密码；带宽补 0；listen_ip
	e.save(t, model.ServerTypeV2node, 0, with(map[string]any{"protocol": "shadowsocks", "tls": 0, "network": "tcp", "obfs": "salamander", "listen_ip": "::"}))
	n := v2()
	if *n.Cipher != "aes-128-gcm" || n.ObfsPassword == nil || len(*n.ObfsPassword) != 16 || n.UpMbps != 0 || n.ListenIP != "::" {
		t.Errorf("shadowsocks 补全错误: %+v", n)
	}
	// 流控：非 tcp 且设置了非 mlkem 加密时清除；未设置加密时保留（与原项目一致）
	e.save(t, model.ServerTypeV2node, 0, with(map[string]any{"protocol": "vless", "tls": 0, "network": "ws", "flow": "xtls-rprx-vision", "encryption": "none"}))
	if v2().Flow != nil {
		t.Error("应清除流控")
	}
	e.save(t, model.ServerTypeV2node, 0, with(map[string]any{"protocol": "vless", "tls": 0, "network": "ws", "flow": "xtls-rprx-vision"}))
	if v2().Flow == nil {
		t.Error("未设置加密时应保留流控")
	}
	// mlkem 默认值、acceptProxyProtocol、padding_scheme
	e.save(t, model.ServerTypeV2node, 0, with(map[string]any{"protocol": "vless", "tls": 0, "network": "tcp", "encryption": "mlkem768x25519plus",
		"network_settings": map[string]any{"acceptProxyProtocol": "1"}, "padding_scheme": `{"a":1}`}))
	n = v2()
	es := settings(t, n.EncryptionSettings)
	if es["mode"] != "native" || es["rtt"] != "0rtt" || es["ticket"] != "600s" || es["private_key"] == nil ||
		settings(t, n.NetworkSettings)["acceptProxyProtocol"] != true || string(n.PaddingScheme) != `{"a":1}` {
		t.Errorf("mlkem / 其他补全错误: %v", es)
	}
	// ECH：未填外层域名时关闭；填写后生成密钥
	e.save(t, model.ServerTypeV2node, 0, with(map[string]any{"protocol": "vless", "tls": 1, "network": "tcp", "tls_settings": map[string]any{"ech": "custom"}}))
	if settings(t, v2().TLSSettings)["ech"] != "" {
		t.Error("未填写外层域名应关闭 ECH")
	}
	e.save(t, model.ServerTypeV2node, 0, with(map[string]any{"protocol": "vless", "tls": 1, "network": "tcp",
		"tls_settings": map[string]any{"ech": "custom", "ech_server_name": "cover.com", "ech_config": "keep"}}))
	ts := settings(t, v2().TLSSettings)
	if ts["ech_config"] != "keep" || ts["ech_key"] == nil || ts["ech_key"] == "" {
		t.Errorf("ECH 补全错误: %v", ts)
	}
	// 证书 remote 模式：生成自签名证书；修改时未提交证书则沿用已保存的
	e.save(t, model.ServerTypeV2node, 0, with(map[string]any{"protocol": "trojan", "tls": 1, "network": "tcp",
		"tls_settings": map[string]any{"cert_mode": "remote", "server_name": "n.example.com"}}))
	n = v2()
	ts = settings(t, n.TLSSettings)
	blk, _ := pem.Decode([]byte(ts["tls_cert"].(string)))
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil || cert.Subject.CommonName != "n.example.com" || len(ts["pinned_peer_cert_sha256"].(string)) != 64 {
		t.Fatalf("自签名证书错误: %v", err)
	}
	e.save(t, model.ServerTypeV2node, n.ID, with(map[string]any{"protocol": "trojan", "tls": 1, "network": "tcp",
		"tls_settings": map[string]any{"cert_mode": "remote", "server_name": "other.com"}}))
	ts2 := settings(t, v2().TLSSettings)
	if ts2["tls_cert"] != ts["tls_cert"] || ts2["pinned_peer_cert_sha256"] != ts["pinned_peer_cert_sha256"] || ts2["server_name"] != "other.com" {
		t.Error("修改时应沿用已保存的证书")
	}
	// REALITY
	e.save(t, model.ServerTypeV2node, 0, with(map[string]any{"protocol": "vless", "tls": 2, "network": "tcp"}))
	ts = settings(t, v2().TLSSettings)
	if pub, ok := x25519Public(ts["private_key"].(string)); !ok || pub != ts["public_key"] {
		t.Error("REALITY 密钥错误")
	}
}

func testOps(t *testing.T, e *env) {
	ctx := context.Background()
	e.save(t, model.ServerTypeVmess, 0, base(map[string]any{"tls": 0, "network": "tcp", "show": 1}))
	src := e.last(t, model.ServerTypeVmess).(*model.ServerVmess)
	e.now = e.now.Add(time.Hour)

	wantErr(t, e.SetShow(ctx, model.ServerTypeVmess, src.ID, 2), errShowInvalid)
	wantErr(t, e.SetShow(ctx, model.ServerTypeVmess, 999999, 1), errShowNotExist)
	wantErr(t, e.SetShow(ctx, "socks", src.ID, 1), errTypeInvalid)
	if err := e.SetShow(ctx, model.ServerTypeVmess, src.ID, 0); err != nil {
		t.Fatal(err)
	}
	e.SetShow(ctx, model.ServerTypeVmess, src.ID, 1)

	if err := e.Copy(ctx, model.ServerTypeVmess, src.ID); err != nil {
		t.Fatal(err)
	}
	cp := e.last(t, model.ServerTypeVmess).(*model.ServerVmess)
	if cp.ID == src.ID || cp.Show != 0 || cp.Name != src.Name || cp.CreatedAt != src.CreatedAt || cp.Network != "tcp" {
		t.Fatalf("复制错误: %+v", cp)
	}
	wantErr(t, e.Copy(ctx, model.ServerTypeVmess, 999999), errServerNotExist)

	if err := e.Sort(ctx, map[string]map[string]int64{model.ServerTypeVmess: {fmt.Sprint(src.ID): 5, fmt.Sprint(cp.ID): 3}}); err != nil {
		t.Fatal(err)
	}
	// 任一节点不存在时全部不生效
	wantErr(t, e.Sort(ctx, map[string]map[string]int64{model.ServerTypeVmess: {fmt.Sprint(src.ID): 9, "999999": 1}}), errSaveFailed)
	wantErr(t, e.Sort(ctx, map[string]map[string]int64{"socks": {"1": 1}}), errTypeInvalid)
	var got model.ServerVmess
	e.DB.Take(&got, src.ID)
	if *got.Sort != 5 {
		t.Fatalf("排序应回滚: %d", *got.Sort)
	}

	if err := e.Delete(ctx, model.ServerTypeVmess, cp.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.Delete(ctx, model.ServerTypeVmess, cp.ID), errNodeNotExist)
}

func testOverview(t *testing.T, e *env) {
	ctx := context.Background()
	e.set(t, map[string]any{"server_api_url": "", "app_url": "https://p.example.com", "server_token": "tok'en"})
	e.save(t, model.ServerTypeVless, 0, base(map[string]any{"tls": 0, "network": "tcp", "sort": 2}))
	parent := e.last(t, model.ServerTypeVless).Common().ID
	e.save(t, model.ServerTypeVless, 0, base(map[string]any{"tls": 0, "network": "tcp", "sort": 1, "parent_id": parent}))
	e.save(t, model.ServerTypeAnytls, 0, base(map[string]any{"insecure": 0, "padding_scheme": `["a"]`}))
	e.save(t, model.ServerTypeV2node, 0, base(map[string]any{"protocol": "vless", "tls": 0, "network": "tcp", "disable_sni": 0, "zero_rtt_handshake": 0, "sort": 3}))
	v2id := e.last(t, model.ServerTypeV2node).Common().ID

	now := e.now.Unix()
	setKey := func(name cache.KeyName, typ string, id, v int64) {
		e.Cache.SetJSON(ctx, e.Cache.KeyOf(name, cache.ServerKey(typ, id)), v, time.Hour)
	}
	setKey(cache.KeyServerOnlineUser, model.ServerTypeVless, parent, 12)
	setKey(cache.KeyServerLastCheckAt, model.ServerTypeVless, parent, now-10)
	setKey(cache.KeyServerLastPushAt, model.ServerTypeVless, parent, now-10)
	setKey(cache.KeyServerLastCheckAt, model.ServerTypeV2node, v2id, now-10)
	setKey(cache.KeyServerLastPushAt, model.ServerTypeV2node, v2id, now-301)

	list, err := e.Overview(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("总览错误: %v", err)
	}
	// 未设置排序的在前，其余按 sort 升序
	if list[0]["type"] != model.ServerTypeAnytls || list[1]["sort"] != float64(1) || list[2]["sort"] != float64(2) || list[3]["type"] != model.ServerTypeV2node {
		t.Fatalf("排序错误: %v %v %v %v", list[0]["type"], list[1]["sort"], list[2]["sort"], list[3]["type"])
	}
	if list[0]["padding_scheme"] != `["a"]` || list[0]["available_status"] != 0 || list[0]["online"] != (*int64)(nil) {
		t.Errorf("AnyTLS 字段错误: %v", list[0])
	}
	// 子节点使用父节点状态
	for _, i := range []int{1, 2} {
		if *list[i]["online"].(*int64) != 12 || list[i]["available_status"] != 2 {
			t.Errorf("VLESS 状态错误: %v", list[i])
		}
	}
	if list[3]["available_status"] != 1 {
		t.Errorf("超过 300 秒无推送应为 1: %v", list[3]["available_status"])
	}
	want := fmt.Sprintf("bash install.sh --api-host 'https://p.example.com' --node-id %d --api-key 'tok'\\''en'", v2id)
	if !strings.HasSuffix(list[3]["install_command"].(string), want) {
		t.Errorf("安装命令错误: %v", list[3]["install_command"])
	}
}

func sha1Sum(s string) [20]byte { return sha1.Sum([]byte(s)) }

func (e *env) set(t *testing.T, kv map[string]any) {
	t.Helper()
	if err := e.Settings.Set(context.Background(), kv); err != nil {
		t.Fatal(err)
	}
}

func testHTTP(t *testing.T, e *env) {
	ctx := context.Background()
	const secure = "nodeadminpath12345"
	e.set(t, map[string]any{"secure_path": secure})
	var admin, normal *model.User
	e.DB.Transaction(func(tx *gorm.DB) error {
		admin, _, _ = account.Create(tx, account.NewUser{Email: "admin@x.com", Password: "password123"})
		normal, _, _ = account.Create(tx, account.NewUser{Email: "u@x.com", Password: "password123"})
		return nil
	})
	e.DB.Model(admin).Update("is_admin", 1)
	adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
	userTok, _ := e.auth.Issue(ctx, normal.ID, "", "")

	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{Svc: e.Service, Auth: e.auth})
	do := func(method, path, body, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.Header.Set("Authorization", tok)
		}
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, req)
		return w
	}
	notFound := do("GET", "/api/t1/definitely-not-exist", "", "")
	p := "/api/t1/" + secure
	for _, c := range []struct{ method, path, tok string }{
		{"GET", p + "/servers", userTok}, {"GET", p + "/servers", ""}, {"POST", p + "/servers/vmess", userTok},
		{"GET", "/api/t1/wrongpath/servers", adminTok},
	} {
		if w := do(c.method, c.path, "{}", c.tok); w.Code != http.StatusNotFound || w.Body.String() != notFound.Body.String() {
			t.Errorf("%s %s 应返回统一 404，实际 %d", c.method, c.path, w.Code)
		}
	}
	if w := do("POST", p+"/servers/groups", `{"name":"G"}`, adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"G"`) {
		t.Fatalf("新建权限组失败: %d %s", w.Code, w.Body)
	}
	body := `{"name":"n","group_id":[1],"host":"h","port":"443","server_port":443,"rate":"1","tls":0,"network":"tcp"}`
	if w := do("POST", p+"/servers/vmess", body, adminTok); w.Code != 200 {
		t.Fatalf("新建节点失败: %d %s", w.Code, w.Body)
	}
	id := e.last(t, model.ServerTypeVmess).Common().ID
	if w := do("PUT", fmt.Sprintf("%s/servers/vmess/%d", p, id), strings.Replace(body, `"n"`, `"n2"`, 1), adminTok); w.Code != 200 {
		t.Fatalf("修改节点失败: %d %s", w.Code, w.Body)
	}
	if w := do("POST", fmt.Sprintf("%s/servers/vmess/%d/show", p, id), `{"show":1}`, adminTok); w.Code != 200 {
		t.Fatalf("显示节点失败: %d", w.Code)
	}
	if w := do("POST", fmt.Sprintf("%s/servers/vmess/%d/show", p, id), `{}`, adminTok); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("缺少 show 应返回 422: %d", w.Code)
	}
	if w := do("POST", fmt.Sprintf("%s/servers/vmess/%d/copy", p, id), "", adminTok); w.Code != 200 {
		t.Fatalf("复制失败: %d", w.Code)
	}
	if w := do("GET", p+"/servers", "", adminTok); w.Code != 200 || strings.Count(w.Body.String(), `"type":"vmess"`) != 2 {
		t.Fatalf("总览错误: %d %s", w.Code, w.Body)
	}
	if w := do("POST", p+"/servers/sort", fmt.Sprintf(`{"vmess":{"%d":3}}`, id), adminTok); w.Code != 200 {
		t.Fatalf("排序失败: %d %s", w.Code, w.Body)
	}
	if w := do("POST", p+"/servers/routes", `{"remarks":"r","action":"block","match":["a"]}`, adminTok); w.Code != 200 {
		t.Fatalf("新建路由失败: %d %s", w.Code, w.Body)
	}
	if w := do("GET", p+"/servers/routes", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"match":["a"]`) {
		t.Fatalf("路由列表错误: %s", w.Body)
	}
	if w := do("DELETE", fmt.Sprintf("%s/servers/vmess/%d", p, id), "", adminTok); w.Code != 200 {
		t.Fatalf("删除失败: %d", w.Code)
	}
	if w := do("DELETE", p+"/servers/vmess/abc", "", adminTok); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("非法 ID 应返回 422: %d", w.Code)
	}
	if w := do("POST", p+"/servers/socks", body, adminTok); w.Code != http.StatusNotFound {
		t.Fatalf("未知类型应返回 404: %d", w.Code)
	}
}
