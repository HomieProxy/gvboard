package subscribe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"gvboard/internal/model"
	"gvboard/internal/phpx"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
)

// testdata/vectors/expected.json 由原项目 PHP 协议类（php 8.2 + symfony/yaml 4.4）对 input.json 生成，
// 仅打上决策 D40 列出的修复。这里用同样的输入运行 Go 实现，要求输出逐字节一致。

type vectorCase struct {
	Name    string      `json:"name"`
	Headers [][2]string `json:"headers"`
	Body    string      `json:"body"`
}

var vectorHandlers = map[string]struct {
	handle     func(*Ctx) (*Response, error)
	remoteCert bool
}{
	"v2RayTun":           {v2RayTun, false},
	"V2rayNG":            {general, false},
	"V2rayN":             {general, false},
	"Surge":              {surge, false},
	"Surfboard":          {surfboard, false},
	"Stash":              {stash, false},
	"Shadowsocks":        {sip008, false},
	"Shadowrocket":       {shadowrocket, false},
	"SagerNet":           {sagerNet, false},
	"SSRPlus":            {general, false},
	"QuantumultX":        {quantumultX, false},
	"Passwall":           {general, false},
	"Loon":               {loon, false},
	"General":            {general, false},
	"ClashVerge":         {clashMeta(metaVerge), true},
	"ClashNyanpasu":      {clashMeta(metaNyanpasu), true},
	"ClashMeta":          {clashMeta(metaMeta), true},
	"Clash":              {clash, true},
	`Singbox\Singbox`:    {singbox(false), true},
	`Singbox\SingboxOld`: {singbox(true), true},
	"App":                {appConfig, false},
}

func readJSON(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func vectorSub(u *phpx.Arr) *model.Subscription {
	sub := &model.Subscription{
		ID: phpx.Int(u.Get("id")), UUID: u.Str("uuid"), Token: u.Str("token"),
		U: phpx.Int(u.Get("u")), D: phpx.Int(u.Get("d")), TransferEnable: phpx.Int(u.Get("transfer_enable")),
	}
	if v := u.Get("expired_at"); v != nil {
		n := phpx.Int(v)
		sub.ExpiredAt = &n
	}
	if v := u.Get("speed_limit"); v != nil {
		n := phpx.Int(v)
		sub.SpeedLimit = &n
	}
	return sub
}

func normHeaders(h [][2]string) []string {
	out := make([]string, 0, len(h))
	for _, x := range h {
		out = append(out, strings.ToLower(x[0])+": "+x[1])
	}
	sort.Strings(out)
	return out
}

// 设置 UPDATE_VECTORS=1 时，用当前 Go 输出覆盖 expected.json 中指定格式的用例（用于原项目尚未支持的增强）。
func TestUpdateVectors(t *testing.T) {
	names := strings.Split(os.Getenv("UPDATE_VECTORS"), ",")
	if len(names) == 0 || names[0] == "" {
		t.Skip("设置 UPDATE_VECTORS=Surge,QuantumultX 可更新期望")
	}
	want := map[string]bool{}
	for _, n := range names {
		want[strings.TrimSpace(n)] = true
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	defer func() { time.Local = old }()

	inV, err := phpx.JSONDecode(readJSON(t, "testdata/vectors/input.json"))
	if err != nil {
		t.Fatal(err)
	}
	in := inV.(*phpx.Arr)
	var expected struct {
		Cases []vectorCase `json:"cases"`
	}
	if err := json.Unmarshal(readJSON(t, "testdata/vectors/expected.json"), &expected); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{}
	in.Arr("config").Each(func(k, v any) { cfg[phpx.Str(k)] = v })
	newSvc := func(extra map[string]any) *Service {
		vals := map[string]any{}
		for k, v := range cfg {
			vals[k] = v
		}
		for k, v := range extra {
			vals[k] = v
		}
		st := setting.NewStatic(vals)
		return &Service{Settings: st, Linker: &subscription.Linker{Settings: st}}
	}
	svcDefault := newSvc(nil)
	svcCustom := newSvc(map[string]any{SettingKey(tplClash): in.Str("custom_clash")})
	users := in.Arr("users").Values()
	host := in.Str("host")
	updated := 0
	for i, tc := range expected.Cases {
		parts := strings.Split(tc.Name, "|")
		if !want[parts[0]] {
			continue
		}
		h, ok := vectorHandlers[parts[0]]
		if !ok {
			t.Fatalf("未知格式 %s", parts[0])
		}
		ui := phpx.Int(parts[1])
		sub := vectorSub(users[ui].(*phpx.Arr))
		var servers []*phpx.Arr
		for _, s := range in.Arr("servers").Values() {
			servers = append(servers, s.(*phpx.Arr).Clone())
		}
		svc := svcDefault
		switch parts[2] {
		case "info":
			day := 12
			servers = addInfoServers(servers, sub, &day)
		case "custom":
			svc = svcCustom
		}
		if h.remoteCert {
			markRemoteCert(servers)
		}
		resp, err := h.handle(&Ctx{
			Context: context.Background(), svc: svc, Sub: sub, Servers: servers,
			AppName: cfg["app_name"].(string), AppURL: cfg["app_url"].(string),
			Host: host, BaseURL: "https://" + host,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.Name, err)
		}
		expected.Cases[i].Body = base64.StdEncoding.EncodeToString(resp.Body)
		gotH := resp.Headers
		if resp.ContentType != htmlType {
			gotH = append(append([][2]string(nil), gotH...), [2]string{"content-type", resp.ContentType})
		}
		expected.Cases[i].Headers = gotH
		updated++
	}
	out, err := json.MarshalIndent(expected, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	if err := os.WriteFile("testdata/vectors/expected.json", out, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("已更新 %d 条期望", updated)
}

func TestProtocolsMatchPHP(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	defer func() { time.Local = old }()

	inV, err := phpx.JSONDecode(readJSON(t, "testdata/vectors/input.json"))
	if err != nil {
		t.Fatal(err)
	}
	in := inV.(*phpx.Arr)
	var expected struct {
		Cases []vectorCase `json:"cases"`
	}
	if err := json.Unmarshal(readJSON(t, "testdata/vectors/expected.json"), &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected.Cases) == 0 {
		t.Fatal("没有对照用例")
	}

	cfg := map[string]any{}
	in.Arr("config").Each(func(k, v any) { cfg[phpx.Str(k)] = v })
	newSvc := func(extra map[string]any) *Service {
		vals := map[string]any{}
		for k, v := range cfg {
			vals[k] = v
		}
		for k, v := range extra {
			vals[k] = v
		}
		st := setting.NewStatic(vals)
		return &Service{Settings: st, Linker: &subscription.Linker{Settings: st}}
	}
	svcDefault := newSvc(nil)
	svcCustom := newSvc(map[string]any{SettingKey(tplClash): in.Str("custom_clash")})
	users := in.Arr("users").Values()
	host := in.Str("host")

	for _, tc := range expected.Cases {
		parts := strings.Split(tc.Name, "|")
		h, ok := vectorHandlers[parts[0]]
		if !ok {
			t.Fatalf("未知格式 %s", parts[0])
		}
		ui := phpx.Int(parts[1])
		sub := vectorSub(users[ui].(*phpx.Arr))
		var servers []*phpx.Arr
		for _, s := range in.Arr("servers").Values() {
			servers = append(servers, s.(*phpx.Arr).Clone())
		}
		svc := svcDefault
		switch parts[2] {
		case "info":
			day := 12
			servers = addInfoServers(servers, sub, &day)
		case "custom":
			svc = svcCustom
		}
		if h.remoteCert {
			markRemoteCert(servers)
		}
		resp, err := h.handle(&Ctx{
			Context: context.Background(), svc: svc, Sub: sub, Servers: servers,
			AppName: cfg["app_name"].(string), AppURL: cfg["app_url"].(string),
			Host: host, BaseURL: "https://" + host,
		})
		if err != nil {
			t.Errorf("%s: %v", tc.Name, err)
			continue
		}
		want, _ := base64.StdEncoding.DecodeString(tc.Body)
		if string(resp.Body) != string(want) {
			t.Errorf("%s: 内容不一致\n%s", tc.Name, firstDiff(string(want), string(resp.Body)))
		}
		gotH := resp.Headers
		if resp.ContentType != htmlType {
			gotH = append(append([][2]string(nil), gotH...), [2]string{"content-type", resp.ContentType})
		}
		if g, w := normHeaders(gotH), normHeaders(tc.Headers); strings.Join(g, "\n") != strings.Join(w, "\n") {
			t.Errorf("%s: 响应头不一致\n期望 %q\n实际 %q", tc.Name, w, g)
		}
	}
}

// firstDiff 显示第一处不同附近的内容（base64 内容先解码）。
func firstDiff(want, got string) string {
	if w, err := base64.StdEncoding.DecodeString(want); err == nil && len(want) > 0 {
		if g, err := base64.StdEncoding.DecodeString(got); err == nil {
			want, got = string(w), string(g)
		}
	}
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	lo := i - 120
	if lo < 0 {
		lo = 0
	}
	clip := func(s string) string {
		hi := i + 200
		if hi > len(s) {
			hi = len(s)
		}
		if lo > len(s) {
			return ""
		}
		return s[lo:hi]
	}
	return "期望: " + clip(want) + "\n实际: " + clip(got)
}
