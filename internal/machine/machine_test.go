package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"gvboard/internal/auth"
	"gvboard/internal/config"
	"gvboard/internal/model"
	"gvboard/internal/nodeapi"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/sysconfig"
	"gvboard/internal/testutil"
)

const serverToken = "machine-test-server-token-0123456789"

func TestMachine(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) { testMachine(t, driver) })
	}
}

func testMachine(t *testing.T, driver string) {
	ctx := context.Background()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ctx, map[string]any{"server_token": serverToken, "server_pull_interval": 60}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2027, 3, 15, 12, 0, 0, 0, time.Local)
	svc := &Service{DB: db, Settings: st, Now: func() time.Time { return now }}
	nodeSvc := &nodeapi.Service{DB: db, Cache: c, Settings: st, Now: func() time.Time { return now }}
	au := auth.New(db, c, "base64:bm9kZS10ZXN0LWtleS0wMTIzNDU2Nzg5YWJjZGVm")
	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&nodeapi.Module{Svc: nodeSvc, Auth: au, Machines: svc}, &Module{Svc: svc, Auth: au})
	do := func(method, path, body string, h map[string]string) *httptest.ResponseRecorder {
		hr := httptest.NewRequest(method, path, strings.NewReader(body))
		hr.Header.Set("Content-Type", "application/json")
		for k, v := range h {
			hr.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, hr)
		return w
	}
	enroll := func(code string) *httptest.ResponseRecorder {
		return do("POST", "/api/v2/server/machine/enroll", fmt.Sprintf(`{"code":%q,"hostname":"vps-1","version":"v0.4.9-gvboard.2"}`, code), nil)
	}
	getConfig := func(tok string, h map[string]string) *httptest.ResponseRecorder {
		if h == nil {
			h = map[string]string{}
		}
		h["Authorization"] = "Bearer " + tok
		return do("GET", "/api/v2/server/machine/config?version=v0.4.9-gvboard.2", "", h)
	}

	newNode := func(name string) int64 {
		n := &model.ServerV2node{ServerBase: model.ServerBase{Name: name, GroupID: model.IntList{1}, Host: "h", Port: "443", ServerPort: 443, Rate: "1", Show: 1}, ListenIP: "0.0.0.0", Protocol: "vless"}
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	n1, n2, n3 := newNode("n1"), newNode("n2"), newNode("n3")
	vm := &model.ServerVmess{ServerBase: model.ServerBase{Name: "vm", GroupID: model.IntList{1}, Host: "h", Port: "443", ServerPort: 443, Rate: "1", Show: 1}, Network: "tcp"}
	if err := db.Create(vm).Error; err != nil {
		t.Fatal(err)
	}

	// 未开启：不能新建，接入接口 404。
	if _, err := svc.Create(ctx, Input{Name: "a", ServerIDs: []int64{n1}}); err != errDisabled {
		t.Fatalf("未开启时新建应失败: %v", err)
	}
	if w := enroll("x"); w.Code != http.StatusNotFound {
		t.Fatalf("未开启时接入应 404: %d", w.Code)
	}
	if err := st.Set(ctx, map[string]any{"node_machine_enable": true}); err != nil {
		t.Fatal(err)
	}

	// 校验：日志级别、节点类型、节点归属。
	for name, in := range map[string]Input{
		"空名称":   {Name: " "},
		"日志级别":  {Name: "a", LogLevel: "trace"},
		"节点不存在": {Name: "a", ServerIDs: []int64{99999}},
	} {
		if _, err := svc.Create(ctx, in); err == nil {
			t.Errorf("%s 应校验失败", name)
		}
	}
	a, err := svc.Create(ctx, Input{Name: " A ", ServerIDs: []int64{n1, n2, n1}, AuditIDs: []int64{n2, n3}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Machine.Name != "A" || len(a.Machine.ServerIDs) != 2 || len(a.Machine.AuditIDs) != 1 || a.Machine.AuditIDs[0] != n2 ||
		a.Machine.LogLevel != "info" || len(a.Code) != 32 || a.ExpiresAt != now.Add(EnrollTTL).Unix() {
		t.Fatalf("新建结果不符: %+v", a)
	}
	if _, err := svc.Create(ctx, Input{Name: "B", ServerIDs: []int64{n1}}); err == nil {
		t.Fatal("节点已属于其他节点机时应失败")
	}
	b, err := svc.Create(ctx, Input{Name: "B", ServerIDs: []int64{n3}})
	if err != nil {
		t.Fatal(err)
	}

	// 接入：错误接入码 404，正确接入码换取凭据，重复使用 404，过期 404。
	if w := enroll("wrong"); w.Code != http.StatusNotFound {
		t.Fatalf("错误接入码应 404: %d", w.Code)
	}
	w := enroll(a.Code)
	if w.Code != http.StatusOK {
		t.Fatalf("接入失败: %d %s", w.Code, w.Body)
	}
	var got Enrolled
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.MachineID != a.Machine.ID || !strings.HasPrefix(got.Token, tokenPrefix) {
		t.Fatalf("接入结果不符: %s", w.Body)
	}
	tok := got.Token
	if w := enroll(a.Code); w.Code != http.StatusNotFound {
		t.Fatalf("接入码重复使用应 404: %d", w.Code)
	}
	now = now.Add(EnrollTTL + time.Second)
	if w := enroll(b.Code); w.Code != http.StatusNotFound {
		t.Fatalf("过期接入码应 404: %d", w.Code)
	}
	list, err := svc.List(ctx)
	if err != nil || len(list) != 2 || !list[0].Enrolled || list[0].Hostname != "vps-1" || list[0].Version != "v0.4.9-gvboard.2" || list[1].Enrolled {
		t.Fatalf("列表不符: %+v %v", list, err)
	}

	// 下发配置与 ETag。
	w = getConfig(tok, nil)
	var cf Config
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &cf) != nil {
		t.Fatalf("获取配置失败: %d %s", w.Code, w.Body)
	}
	if cf.LogLevel != "info" || cf.PullInterval != 60 || len(cf.Nodes) != 2 ||
		cf.Nodes[0] != (NodeConfig{NodeID: n1}) || cf.Nodes[1] != (NodeConfig{NodeID: n2, AuditEnable: true}) {
		t.Fatalf("配置不符: %+v", cf)
	}
	etag := w.Header().Get("ETag")
	if w := getConfig(tok, map[string]string{"If-None-Match": etag}); w.Code != http.StatusNotModified {
		t.Fatalf("ETag 未生效: %d", w.Code)
	}
	if w := getConfig(serverToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("通讯密钥不能获取节点机配置: %d", w.Code)
	}
	if w := getConfig(tokenPrefix+"0000", nil); w.Code != http.StatusNotFound {
		t.Fatalf("伪造凭据应 404: %d", w.Code)
	}

	// 节点机凭据用于原有节点接口：只能访问本机的 v2node 节点。
	uni := func(tk, typ string, id int64) int {
		return do("GET", fmt.Sprintf("/api/v1/server/UniProxy/user?token=%s&node_type=%s&node_id=%d", tk, typ, id), "", nil).Code
	}
	v2cfg := func(tk string, id int64) int {
		return do("GET", fmt.Sprintf("/api/v2/server/config?token=%s&node_id=%d", tk, id), "", nil).Code
	}
	if uni(tok, "v2node", n1) != http.StatusOK || v2cfg(tok, n2) != http.StatusOK {
		t.Fatal("节点机凭据应能访问本机节点")
	}
	if uni(tok, "v2node", n3) != http.StatusNotFound || v2cfg(tok, n3) != http.StatusNotFound ||
		uni(tok, "vmess", vm.ID) != http.StatusNotFound || uni(tok, "vless", n1) != http.StatusNotFound {
		t.Fatal("节点机凭据不能访问其他节点或其他类型")
	}
	if do("GET", fmt.Sprintf("/api/v1/server/Deepbwork/user?token=%s&node_id=%d", tok, n1), "", nil).Code != http.StatusNotFound {
		t.Fatal("节点机凭据不能访问旧版接口")
	}
	if uni(serverToken, "v2node", n3) != http.StatusOK {
		t.Fatal("通讯密钥仍应可用")
	}

	// 被跳过节点的上报：只保留本机节点、每个节点一条、原因截断；空列表清除。
	report := func(tk, body string) int {
		return do("POST", "/api/v2/server/machine/node-errors", body, map[string]string{"Authorization": "Bearer " + tk}).Code
	}
	errsOf := func(id int64) []NodeError {
		t.Helper()
		list, err := svc.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range list {
			if v.ID == id {
				return v.NodeErrors
			}
		}
		t.Fatalf("找不到节点机 %d", id)
		return nil
	}
	if report(tokenPrefix+"0000", `{"errors":[]}`) != http.StatusNotFound || report(serverToken, `{"errors":[]}`) != http.StatusNotFound {
		t.Fatal("伪造凭据、通讯密钥上报应 404")
	}
	if c := report(tok, `{"errors":"x"}`); c < 400 || c == http.StatusNotFound {
		t.Fatalf("格式错误的上报应失败: %d", c)
	}
	reportBody := fmt.Sprintf(`{"errors":[{"node_id":%d,"error":"端口 443/TCP 与本面板节点 #%d 冲突，已跳过"},{"node_id":%d,"error":"重复"},{"node_id":%d,"error":"其他节点机"},{"node_id":%d,"error":%q}]}`,
		n2, n1, n2, n3, n1, strings.Repeat("长", 300))
	if c := report(tok, reportBody); c != http.StatusNoContent {
		t.Fatalf("上报失败: %d", c)
	}
	if es := errsOf(a.Machine.ID); len(es) != 2 || es[0].NodeID != n2 || !strings.Contains(es[0].Error, "冲突") ||
		es[1].NodeID != n1 || utf8.RuneCountInString(es[1].Error) != maxNodeErrorLen {
		t.Fatalf("上报结果不符: %+v", es)
	}
	if report(tok, `{"errors":[]}`) != http.StatusNoContent || len(errsOf(a.Machine.ID)) != 0 {
		t.Fatal("空列表应清除上报")
	}
	if report(tok, reportBody) != http.StatusNoContent {
		t.Fatal("上报失败")
	}

	// 修改后立即下发；删除节点后不再下发。
	if _, err := svc.Update(ctx, a.Machine.ID, Input{Name: "A", ServerIDs: []int64{n1, n2}, AuditIDs: []int64{n1}, LogLevel: "debug"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&model.ServerV2node{}, n2).Error; err != nil {
		t.Fatal(err)
	}
	w = getConfig(tok, map[string]string{"If-None-Match": etag})
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &cf) != nil || cf.LogLevel != "debug" ||
		len(cf.Nodes) != 1 || cf.Nodes[0] != (NodeConfig{NodeID: n1, AuditEnable: true}) {
		t.Fatalf("修改后配置不符: %d %s", w.Code, w.Body)
	}
	if _, err := svc.Update(ctx, a.Machine.ID, Input{Name: "A", ServerIDs: []int64{n1}, AuditIDs: []int64{n1}, LogLevel: "debug"}); err != nil {
		t.Fatal(err)
	}
	if es := errsOf(a.Machine.ID); len(es) != 1 || es[0].NodeID != n1 {
		t.Fatalf("已不属于本机的节点不应显示: %+v", es)
	}

	// 存在已接入的节点机时不能关闭开关。
	sc := &sysconfig.Service{DB: db, Settings: st}
	if err := sc.Save(ctx, map[string]any{"node_machine_enable": false}); err == nil {
		t.Fatal("存在已接入的节点机时关闭开关应失败")
	}

	// 重新生成接入码会吊销旧凭据；吊销后立即 404。
	re, err := svc.Reenroll(ctx, a.Machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	if getConfig(tok, nil).Code != http.StatusNotFound || uni(tok, "v2node", n1) != http.StatusNotFound {
		t.Fatal("重新生成接入码后旧凭据应失效")
	}
	if len(errsOf(a.Machine.ID)) != 0 {
		t.Fatal("重新生成接入码应清除上报")
	}
	w = enroll(re.Code)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("重新接入失败: %d", w.Code)
	}
	if getConfig(got.Token, nil).Code != http.StatusOK {
		t.Fatal("重新接入后的凭据应可用")
	}

	// 远程更新：校验版本号与接入状态；下发后配置带更新指令；失败原因只记录当前指令的；运行目标版本后视为完成。
	const target = "v0.4.9-gvboard.3"
	if _, err := svc.RequestUpdate(ctx, []int64{a.Machine.ID}, "latest"); err != errVersion {
		t.Fatalf("非法版本号应失败: %v", err)
	}
	if _, err := svc.RequestUpdate(ctx, []int64{b.Machine.ID}, target); err == nil {
		t.Fatal("未接入的节点机不能更新")
	}
	if _, err := svc.RequestUpdate(ctx, []int64{a.Machine.ID}, "v0.4.9-gvboard.2"); err == nil {
		t.Fatal("已是该版本时不应下发")
	}
	fetch := func(version string, rep url.Values) Config {
		t.Helper()
		q := url.Values{"version": {version}}
		for k, v := range rep {
			q[k] = v
		}
		w := do("GET", "/api/v2/server/machine/config?"+q.Encode(), "", map[string]string{"Authorization": "Bearer " + got.Token})
		var cf Config
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &cf) != nil {
			t.Fatalf("获取配置失败: %d %s", w.Code, w.Body)
		}
		return cf
	}
	machineA := func() View {
		t.Helper()
		list, err := svc.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range list {
			if v.ID == a.Machine.ID {
				return v
			}
		}
		t.Fatal("找不到节点机 A")
		return View{}
	}
	if cf := fetch("v0.4.9-gvboard.2", nil); cf.Update != nil {
		t.Fatalf("没有更新时不应下发: %+v", cf.Update)
	}
	if n, err := svc.RequestUpdate(ctx, []int64{a.Machine.ID, b.Machine.ID, a.Machine.ID}, " "+target+" "); err != nil || n != 1 {
		t.Fatalf("下发更新失败: %d %v", n, err)
	}
	cf = fetch("v0.4.9-gvboard.2", nil)
	if cf.Update == nil || *cf.Update != (Update{Version: target, ID: 1}) {
		t.Fatalf("更新指令不符: %+v", cf.Update)
	}
	fetch("v0.4.9-gvboard.2", url.Values{"update_id": {"0"}, "update_error": {"旧指令"}})
	if v := machineA(); v.UpdateError != "" {
		t.Fatalf("其他指令的失败原因不应记录: %q", v.UpdateError)
	}
	fetch("v0.4.9-gvboard.2", url.Values{"update_id": {"1"}, "update_error": {"下载失败"}})
	if v := machineA(); v.UpdateVersion != target || v.UpdateError != "下载失败" {
		t.Fatalf("失败原因未记录: %+v", v)
	}
	if _, err := svc.RequestUpdate(ctx, []int64{a.Machine.ID}, target); err != nil {
		t.Fatal(err)
	}
	if v := machineA(); v.UpdateID != 2 || v.UpdateError != "" {
		t.Fatalf("重新下发应生成新编号并清除失败原因: %+v", v)
	}
	if err := svc.CancelUpdate(ctx, a.Machine.ID); err != nil {
		t.Fatal(err)
	}
	if cf := fetch("v0.4.9-gvboard.2", nil); cf.Update != nil {
		t.Fatal("取消后不应再下发")
	}
	if _, err := svc.RequestUpdate(ctx, []int64{a.Machine.ID}, target); err != nil {
		t.Fatal(err)
	}
	if cf := fetch(target, nil); cf.Update != nil {
		t.Fatal("运行目标版本后不应再下发")
	}
	if v := machineA(); v.UpdateVersion != "" || v.Version != target || v.UpdateID != 3 {
		t.Fatalf("更新完成后状态不符: %+v", v)
	}
	if err := svc.Revoke(ctx, a.Machine.ID); err != nil {
		t.Fatal(err)
	}
	if getConfig(got.Token, nil).Code != http.StatusNotFound || uni(got.Token, "v2node", n1) != http.StatusNotFound {
		t.Fatal("吊销后凭据应失效")
	}
	if err := sc.Save(ctx, map[string]any{"node_machine_enable": false}); err != nil {
		t.Fatalf("没有已接入的节点机时应能关闭: %v", err)
	}
	if err := svc.Delete(ctx, b.Machine.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, b.Machine.ID, Input{Name: "B"}); err != errNotFound {
		t.Fatalf("删除后修改应返回不存在: %v", err)
	}
}
