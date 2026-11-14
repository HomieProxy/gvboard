package legacy

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"gvboard/internal/model"
	"gvboard/internal/queue"
)

type fakeQueue struct{ workers int }

func (f *fakeQueue) Stats() ([]queue.Stats, error) {
	return []queue.Stats{{Queue: "send_email", Pending: 1, Processed: 3}, {Queue: "order_handle", Failed: 2}}, nil
}

func (f *fakeQueue) Workers() (int, int, error) { return f.workers, f.workers * 4, nil }

const adminPath = "gvbtestadminpath"

// admin 创建管理员并返回其令牌，同时设置安全路径。
func (e *env) admin() string {
	e.t.Helper()
	e.set(map[string]any{"secure_path": adminPath})
	u, _ := e.createUser("admin@x.com")
	if err := e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("is_admin", 1).Error; err != nil {
		e.t.Fatal(err)
	}
	return e.login(u.ID)
}

func ap(p string) string { return "/api/v1/" + adminPath + "/" + p }

func wantOK(t *testing.T, r resp) {
	t.Helper()
	if r.code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d %s", r.code, r.raw)
	}
}

func testAdminAuth(t *testing.T, e *env) {
	tok := e.admin()
	u, _ := e.createUser("normal@x.com")
	normal := e.login(u.ID)
	for name, r := range map[string]resp{
		"安全路径错误": e.get("/api/v1/wrongpath1234567/plan/fetch", tok),
		"未登录":    e.get(ap("plan/fetch"), ""),
		"非管理员":   e.get(ap("plan/fetch"), normal),
		"无效令牌":   e.get(ap("plan/fetch"), "bad"),
	} {
		if r.code != http.StatusNotFound {
			t.Errorf("%s 应返回 404，实际 %d %s", name, r.code, r.raw)
		}
	}
	wantOK(t, e.get(ap("plan/fetch"), tok))
	wantOK(t, e.get(ap("plan/fetch")+"?auth_data="+url.QueryEscape(tok), ""))
	for _, p := range []string{"notice/update", "user/setInviteUser"} {
		wantMsg(t, e.post(ap(p), tok, url.Values{}), http.StatusInternalServerError, e.zh(msgInternal))
	}
	wantMsg(t, e.get(ap("stat/getStat"), tok), http.StatusInternalServerError, e.zh(msgInternal))
}

func testAdminPlan(t *testing.T, e *env) {
	tok := e.admin()
	wantOK(t, e.post(ap("server/group/save"), tok, url.Values{"name": {"组A"}}))
	groups := e.get(ap("server/group/fetch"), tok).list(t)
	gid := itoa(int64(groups[0].(map[string]any)["id"].(float64)))
	if g := e.get(ap("server/group/fetch")+"?group_id="+gid, tok).list(t); len(g) != 1 || g[0].(map[string]any)["name"] != "组A" {
		t.Fatalf("按 ID 查询权限组错误: %v", g)
	}
	wantMsg(t, e.post(ap("server/group/save"), tok, url.Values{}), http.StatusInternalServerError, "组名不能为空")

	wantInvalid(t, e.post(ap("plan/save"), tok, url.Values{"name": {"p"}}), "group_id")
	wantOK(t, e.post(ap("plan/save"), tok, url.Values{
		"name": {"基础"}, "group_id": {gid}, "transfer_enable": {"100"}, "month_price": {"1000"},
	}))
	plans := e.get(ap("plan/fetch"), tok).list(t)
	if len(plans) != 1 {
		t.Fatalf("套餐数量错误: %v", plans)
	}
	plan := plans[0].(map[string]any)
	pid := itoa(int64(plan["id"].(float64)))
	wantOK(t, e.post(ap("plan/update"), tok, url.Values{"id": {pid}, "show": {"1"}}))
	wantOK(t, e.post(ap("plan/sort"), tok, url.Values{"plan_ids[0]": {pid}}))
	// 只修改部分字段时其余字段保持不变
	wantOK(t, e.post(ap("plan/save"), tok, url.Values{"id": {pid}, "name": {"基础2"}, "group_id": {gid}, "transfer_enable": {"200"}}))
	var p model.Plan
	e.db.Take(&p, plan["id"])
	if p.Name != "基础2" || p.MonthPrice == nil || *p.MonthPrice != 1000 || p.Show != 1 {
		t.Fatalf("套餐修改结果错误: %+v", p)
	}

	// 优惠券：单个与批量（CSV）
	coupon := url.Values{"name": {"券"}, "type": {"1"}, "value": {"100"}, "started_at": {"1"}, "ended_at": {"2000000000"}, "limit_plan_ids[0]": {pid}}
	wantOK(t, e.post(ap("coupon/generate"), tok, coupon))
	coupon.Set("generate_count", "3")
	r := e.post(ap("coupon/generate"), tok, coupon)
	if r.code != http.StatusOK || !strings.HasPrefix(r.hdr.Get("Content-Type"), "text/csv") || strings.Count(r.raw, "\n") < 4 {
		t.Fatalf("批量生成优惠券应输出 CSV: %d %s", r.code, r.raw)
	}
	coupon.Set("generate_count", "501")
	wantInvalid(t, e.post(ap("coupon/generate"), tok, coupon), "generate_count")
	cr := e.get(ap("coupon/fetch"), tok)
	if cr.body["total"].(float64) != 4 {
		t.Fatalf("优惠券数量错误: %s", cr.raw)
	}
	cid := itoa(int64(cr.list(t)[0].(map[string]any)["id"].(float64)))
	wantOK(t, e.post(ap("coupon/show"), tok, url.Values{"id": {cid}}))
	wantOK(t, e.post(ap("coupon/drop"), tok, url.Values{"id": {cid}}))

	// 礼品卡：类型 5 必须指定订阅
	wantInvalid(t, e.post(ap("giftcard/generate"), tok, url.Values{"name": {"卡"}, "type": {"5"}, "value": {"1"}, "started_at": {"1"}, "ended_at": {"2"}}), "plan_id")
	wantOK(t, e.post(ap("giftcard/generate"), tok, url.Values{"name": {"卡"}, "type": {"1"}, "value": {"500"}, "started_at": {"1"}, "ended_at": {"2000000000"}}))
	gr := e.get(ap("giftcard/fetch"), tok)
	gcid := itoa(int64(gr.list(t)[0].(map[string]any)["id"].(float64)))
	wantMsg(t, e.post(ap("giftcard/drop"), tok, url.Values{}), http.StatusBadRequest, "未找到礼品卡")
	wantOK(t, e.post(ap("giftcard/drop"), tok, url.Values{"id": {gcid}}))

	wantMsg(t, e.post(ap("plan/drop"), tok, url.Values{"id": {"99999"}}), http.StatusInternalServerError, "该订阅ID不存在")
}

func testAdminContent(t *testing.T, e *env) {
	tok := e.admin()
	wantInvalid(t, e.post(ap("notice/save"), tok, url.Values{"title": {"t"}}), "content")
	wantOK(t, e.post(ap("notice/save"), tok, url.Values{"title": {"公告"}, "content": {"内容"}, "tags[0]": {"a"}}))
	nr := e.get(ap("notice/fetch"), tok)
	nid := itoa(int64(nr.list(t)[0].(map[string]any)["id"].(float64)))
	wantOK(t, e.post(ap("notice/show"), tok, url.Values{"id": {nid}}))
	wantMsg(t, e.post(ap("notice/drop"), tok, url.Values{"id": {"99999"}}), http.StatusInternalServerError, "公告不存在")
	wantOK(t, e.post(ap("notice/drop"), tok, url.Values{"id": {nid}}))

	k := url.Values{"title": {"文档"}, "category": {"分类"}, "language": {"zh-CN"}, "body": {"正文"}}
	wantOK(t, e.post(ap("knowledge/save"), tok, k))
	kl := e.get(ap("knowledge/fetch"), tok).list(t)
	kid := itoa(int64(kl[0].(map[string]any)["id"].(float64)))
	if d := e.get(ap("knowledge/fetch")+"?id="+kid, tok).obj(t); d["body"] != "正文" {
		t.Fatalf("知识详情错误: %v", d)
	}
	if c := e.get(ap("knowledge/getCategory"), tok).list(t); len(c) != 1 || c[0] != "分类" {
		t.Fatalf("知识分类错误: %v", c)
	}
	wantOK(t, e.post(ap("knowledge/show"), tok, url.Values{"id": {kid}}))
	wantOK(t, e.post(ap("knowledge/sort"), tok, url.Values{"knowledge_ids[0]": {kid}}))
	wantOK(t, e.post(ap("knowledge/drop"), tok, url.Values{"id": {kid}}))

	// 工单：用户提交，管理员回复与关闭
	u, _ := e.createUser("tk@x.com")
	wantOK(t, e.post("/api/v1/user/ticket/save", e.login(u.ID), url.Values{"subject": {"问题"}, "level": {"1"}, "message": {"求助"}}))
	tr := e.get(ap("ticket/fetch")+"?reply_status[0]=0", tok)
	if tr.body["total"].(float64) != 1 {
		t.Fatalf("工单列表错误: %s", tr.raw)
	}
	tid := itoa(int64(tr.list(t)[0].(map[string]any)["id"].(float64)))
	if d := e.get(ap("ticket/fetch")+"?id="+tid, tok).obj(t); len(d["message"].([]any)) != 1 {
		t.Fatalf("工单详情错误: %v", d)
	}
	wantMsg(t, e.post(ap("ticket/reply"), tok, url.Values{"id": {tid}}), http.StatusInternalServerError, "消息不能为空")
	wantOK(t, e.post(ap("ticket/reply"), tok, url.Values{"id": {tid}, "message": {"已处理"}}))
	wantOK(t, e.post(ap("ticket/close"), tok, url.Values{"id": {tid}}))
	var tk model.Ticket
	e.db.Take(&tk, tr.list(t)[0].(map[string]any)["id"])
	if tk.Status != 1 {
		t.Fatalf("工单未关闭: %+v", tk)
	}
}

func testAdminPayment(t *testing.T, e *env) {
	tok := e.admin()
	if l := e.get(ap("payment/getPaymentMethods"), tok).list(t); len(l) == 0 {
		t.Fatal("支付方式列表为空")
	}
	if f := e.post(ap("payment/getPaymentForm"), tok, url.Values{"payment": {"EPay"}}).obj(t); f["pid"] == nil {
		t.Fatalf("支付表单错误: %v", f)
	}
	pay := url.Values{"name": {"易支付"}, "payment": {"EPay"}, "config[url]": {"https://pay.example"}, "config[pid]": {"1"}, "config[key]": {"k"}}
	wantMsg(t, e.post(ap("payment/save"), tok, pay), http.StatusInternalServerError, "请在站点配置中配置站点地址")
	e.set(map[string]any{"app_url": "https://site.example"})
	wantInvalid(t, e.post(ap("payment/save"), tok, url.Values{"name": {"x"}}), "payment")
	wantOK(t, e.post(ap("payment/save"), tok, pay))
	list := e.get(ap("payment/fetch"), tok).list(t)
	item := list[0].(map[string]any)
	id := itoa(int64(item["id"].(float64)))
	// 与原项目 url() 一致，回调地址使用当前请求的域名
	if !strings.Contains(item["notify_url"].(string), "/payment/notify/EPay/") {
		t.Fatalf("回调地址错误: %v", item["notify_url"])
	}
	wantOK(t, e.post(ap("payment/show"), tok, url.Values{"id": {id}}))
	wantOK(t, e.post(ap("payment/sort"), tok, url.Values{"ids[0]": {id}}))
	wantOK(t, e.post(ap("payment/drop"), tok, url.Values{"id": {id}}))
}

func testAdminUser(t *testing.T, e *env) {
	tok := e.admin()
	u, sub := e.createUser("member@x.com")
	uid := itoa(u.ID)

	r := e.get(ap("user/fetch")+"?filter[0][key]=email&filter[0][condition]=模糊&filter[0][value]=member", tok)
	if r.body["total"].(float64) != 1 {
		t.Fatalf("用户筛选错误: %s", r.raw)
	}
	row := r.list(t)[0].(map[string]any)
	if row["uuid"] != sub.UUID || row["password"] != nil || row["subscribe_url"] == nil {
		t.Fatalf("用户列表字段错误: %v", row)
	}
	wantInvalid(t, e.get(ap("user/fetch")+"?filter[0][key]=password&filter[0][condition]=%3D&filter[0][value]=1", tok), "filter.0.key")

	info := e.get(ap("user/getUserInfoById")+"?id="+uid, tok).obj(t)
	if info["email"] != "member@x.com" || info["token"] != sub.Token {
		t.Fatalf("用户详情错误: %v", info)
	}

	upd := func(kv ...string) url.Values {
		v := url.Values{"id": {uid}, "email": {"member@x.com"}, "banned": {"0"}, "is_admin": {"0"}, "is_staff": {"0"}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	wantInvalid(t, e.post(ap("user/update"), tok, url.Values{"id": {uid}, "email": {"member@x.com"}}), "banned")
	// 与原项目一致，流量以字节提交（由前端换算）
	wantOK(t, e.post(ap("user/update"), tok, upd("transfer_enable", itoa(10*gb), "remarks", "备注")))
	if s := e.sub(sub.ID); s.TransferEnable != 10*gb {
		t.Fatalf("流量应写入主订阅: %d", s.TransferEnable)
	}
	other, _ := e.createUser("other@x.com")
	wantMsg(t, e.post(ap("user/update"), tok, upd("email", "other@x.com")), http.StatusInternalServerError, "邮箱已被使用")
	wantOK(t, e.post(ap("user/update"), tok, upd("invite_user_email", "other@x.com")))
	var got model.User
	e.db.Take(&got, u.ID)
	if got.InviteUserID == nil || *got.InviteUserID != other.ID {
		t.Fatalf("邀请人未设置: %+v", got.InviteUserID)
	}

	wantOK(t, e.post(ap("user/resetSecret"), tok, url.Values{"id": {uid}}))
	if s := e.sub(sub.ID); s.UUID == sub.UUID || s.Token == sub.Token {
		t.Fatal("重置密钥应作用于主订阅")
	}

	// 没有主订阅时：订阅字段读取为空值，修改订阅字段报错
	e.db.Model(&model.Subscription{}).Where("id = ?", sub.ID).Update("is_primary", 0)
	if info := e.get(ap("user/getUserInfoById")+"?id="+uid, tok).obj(t); info["uuid"] != nil {
		t.Fatalf("无主订阅时 uuid 应为空: %v", info["uuid"])
	}
	wantMsg(t, e.post(ap("user/update"), tok, upd("transfer_enable", "5")),
		http.StatusInternalServerError, "该用户没有主订阅，无法修改订阅信息")
	wantMsg(t, e.post(ap("user/resetSecret"), tok, url.Values{"id": {uid}}), http.StatusInternalServerError, "该用户没有主订阅")
	e.db.Model(&model.Subscription{}).Where("id = ?", sub.ID).Update("is_primary", 1)

	wantOK(t, e.post(ap("user/generate"), tok, url.Values{"email_prefix": {"gen"}, "email_suffix": {"x.com"}, "password": {"pass12345"}}))
	wantMsg(t, e.post(ap("user/generate"), tok, url.Values{"email_prefix": {"gen"}, "email_suffix": {"x.com"}}),
		http.StatusInternalServerError, "邮箱已存在于系统中")
	r = e.post(ap("user/generate"), tok, url.Values{"generate_count": {"2"}, "email_suffix": {"y.com"}})
	if r.code != http.StatusOK || !strings.HasPrefix(r.hdr.Get("Content-Type"), "text/csv") {
		t.Fatalf("批量生成用户应输出 CSV: %d %s", r.code, r.raw)
	}
	r = e.post(ap("user/dumpCSV"), tok, url.Values{"filter[0][key]": {"email"}, "filter[0][condition]": {"="}, "filter[0][value]": {"member@x.com"}})
	if r.code != http.StatusOK || !strings.Contains(r.raw, "member@x.com") {
		t.Fatalf("导出 CSV 错误: %d %s", r.code, r.raw)
	}

	wantOK(t, e.post(ap("user/ban"), tok, url.Values{"filter[0][key]": {"email"}, "filter[0][condition]": {"="}, "filter[0][value]": {"other@x.com"}}))
	var banned model.User
	e.db.Take(&banned, other.ID)
	if banned.Banned != 1 {
		t.Fatal("批量封禁未生效")
	}
	wantOK(t, e.post(ap("user/delUser"), tok, url.Values{"id": {itoa(other.ID)}}))
	var n int64
	e.db.Model(&model.User{}).Where("id = ?", other.ID).Count(&n)
	if n != 0 {
		t.Fatal("删除用户未生效")
	}
}

func testAdminOrder(t *testing.T, e *env) {
	tok := e.admin()
	u, sub := e.createUser("buyer@x.com")
	wantOK(t, e.post(ap("server/group/save"), tok, url.Values{"name": {"g"}}))
	var g model.ServerGroup
	e.db.Take(&g)
	plan := model.Plan{Name: "p", GroupID: g.ID, TransferEnable: 1, MonthPrice: ptr[int64](1000), Show: 1, Renew: 1}
	if err := e.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	wantInvalid(t, e.post(ap("order/assign"), tok, url.Values{"email": {"buyer@x.com"}}), "plan_id")
	wantMsg(t, e.post(ap("order/assign"), tok, url.Values{"email": {"none@x.com"}, "plan_id": {itoa(plan.ID)}, "period": {"month_price"}, "total_amount": {"0"}}),
		http.StatusInternalServerError, "该用户不存在")
	r := e.post(ap("order/assign"), tok, url.Values{"email": {"buyer@x.com"}, "plan_id": {itoa(plan.ID)}, "period": {"month_price"}, "total_amount": {"0"}})
	tradeNo, _ := r.data().(string)
	if r.code != http.StatusOK || tradeNo == "" {
		t.Fatalf("分配订单失败: %d %s", r.code, r.raw)
	}
	var o model.Order
	e.db.Where("trade_no = ?", tradeNo).Take(&o)
	if o.SubscriptionID == nil || *o.SubscriptionID != sub.ID {
		t.Fatalf("分配订单应作用于主订阅: %+v", o.SubscriptionID)
	}
	l := e.get(ap("order/fetch")+"?filter[0][key]=trade_no&filter[0][condition]=%3D&filter[0][value]="+tradeNo, tok)
	if l.body["total"].(float64) != 1 {
		t.Fatalf("订单筛选错误: %s", l.raw)
	}
	wantInvalid(t, e.get(ap("order/fetch")+"?filter[0][key]=bad&filter[0][condition]=%3D&filter[0][value]=1", tok), "filter.0.key")
	if d := e.post(ap("order/detail"), tok, url.Values{"id": {itoa(o.ID)}}).obj(t); d["trade_no"] != tradeNo {
		t.Fatalf("订单详情错误: %v", d)
	}
	wantOK(t, e.post(ap("order/paid"), tok, url.Values{"trade_no": {tradeNo}}))
	e.db.Take(&o, o.ID)
	if o.Status == model.OrderStatusPending || o.UserID != u.ID {
		t.Fatalf("订单应已支付: %+v", o)
	}
	wantInvalid(t, e.post(ap("order/update"), tok, url.Values{"trade_no": {tradeNo}, "commission_status": {"9"}}), "commission_status")
	wantMsg(t, e.post(ap("order/cancel"), tok, url.Values{"trade_no": {"none"}}), http.StatusInternalServerError, "订单不存在")
}

func testAdminServer(t *testing.T, e *env) {
	tok := e.admin()
	wantOK(t, e.post(ap("server/group/save"), tok, url.Values{"name": {"g"}}))
	var g model.ServerGroup
	e.db.Take(&g)
	gid := itoa(g.ID)

	wantInvalid(t, e.post(ap("server/route/save"), tok, url.Values{"remarks": {"r"}, "action": {"block"}}), "match")
	wantOK(t, e.post(ap("server/route/save"), tok, url.Values{"remarks": {"r"}, "action": {"block"}, "match[0]": {"example.com"}}))
	wantOK(t, e.post(ap("server/route/save"), tok, url.Values{"remarks": {"d"}, "action": {"default_out"}}))
	routes := e.get(ap("server/route/fetch"), tok).list(t)
	if len(routes) != 2 || routes[0].(map[string]any)["match"].([]any)[0] != "example.com" {
		t.Fatalf("路由列表错误: %v", routes)
	}
	wantOK(t, e.post(ap("server/route/drop"), tok, url.Values{"id": {itoa(int64(routes[1].(map[string]any)["id"].(float64)))}}))

	vmess := url.Values{
		"name": {"节点"}, "group_id[0]": {gid}, "host": {"a.example"}, "port": {"443"}, "server_port": {"443"},
		"rate": {"1"}, "tls": {"1"}, "network": {"ws"}, "networkSettings[path]": {"/ws"}, "networkSettings[headers][Host]": {"a.example"},
	}
	// 节点参数由原生服务校验，返回单条消息
	wantMsg(t, e.post(ap("server/vmess/save"), tok, url.Values{"name": {"x"}}), http.StatusInternalServerError, "权限组不能为空")
	wantOK(t, e.post(ap("server/vmess/save"), tok, vmess))
	var srv model.ServerVmess
	e.db.Take(&srv)
	var ns map[string]any
	_ = srv.NetworkSettings.Decode(&ns)
	if ns["path"] != "/ws" || ns["headers"].(map[string]any)["Host"] != "a.example" {
		t.Fatalf("嵌套表单参数应保存为对象: %v", ns)
	}
	id := itoa(srv.ID)
	vmess.Set("id", id)
	vmess.Set("name", "节点2")
	wantOK(t, e.post(ap("server/vmess/save"), tok, vmess))
	wantInvalid(t, e.post(ap("server/vmess/update"), tok, url.Values{"id": {id}, "show": {"5"}}), "show")
	wantOK(t, e.post(ap("server/vmess/update"), tok, url.Values{"id": {id}, "show": {"1"}}))
	wantOK(t, e.post(ap("server/vmess/copy"), tok, url.Values{"id": {id}}))
	nodes := e.get(ap("server/manage/getNodes"), tok).list(t)
	if len(nodes) != 2 || nodes[0].(map[string]any)["type"] != "vmess" {
		t.Fatalf("节点总览错误: %v", nodes)
	}
	wantOK(t, e.post(ap("server/manage/sort"), tok, url.Values{"vmess[" + id + "]": {"5"}}))
	e.db.Take(&srv, srv.ID)
	if srv.Name != "节点2" || srv.Show != 1 || srv.Sort == nil || *srv.Sort != 5 {
		t.Fatalf("节点修改结果错误: %+v", srv)
	}
	wantMsg(t, e.post(ap("server/vmess/drop"), tok, url.Values{"id": {"99999"}}), http.StatusInternalServerError, "节点ID不存在")
	wantOK(t, e.post(ap("server/vmess/drop"), tok, url.Values{"id": {id}}))
}

func testAdminSystem(t *testing.T, e *env) {
	tok := e.admin()
	all := e.get(ap("config/fetch"), tok).obj(t)
	if all["site"] == nil || all["safe"] == nil {
		t.Fatalf("配置分组缺失: %v", all)
	}
	if one := e.get(ap("config/fetch")+"?key=site", tok).obj(t); len(one) != 1 || one["site"] == nil {
		t.Fatalf("按分组读取配置错误: %v", one)
	}
	wantOK(t, e.post(ap("config/save"), tok, url.Values{"app_name": {"站点"}, "try_out_enable": {"1"}, "unknown_key": {"x"}}))
	if e.st.String("app_name") != "站点" {
		t.Fatal("配置未保存")
	}
	if l := e.get(ap("config/getEmailTemplate"), tok).list(t); len(l) == 0 {
		t.Fatal("邮件模板为空")
	}

	o := e.get(ap("stat/getOverride"), tok).obj(t)
	if _, found := o["online_user"]; !found {
		t.Fatalf("概况字段缺失: %v", o)
	}
	e.db.Create(&model.Stat{RecordAt: 1700000000, RecordType: "d", RegisterCount: 2, PaidTotal: 1050})
	if l := e.get(ap("stat/getOrder"), tok).list(t); len(l) != 5 || l[3].(map[string]any)["value"] != 10.5 {
		t.Fatalf("每日统计错误: %v", l)
	}
	for _, p := range []string{"getServerLastRank", "getServerTodayRank", "getUserLastRank", "getUserTodayRank"} {
		e.get(ap("stat/"+p), tok).list(t)
	}
	wantInvalid(t, e.get(ap("stat/getStatUser"), tok), "user_id")
	if r := e.get(ap("stat/getStatUser")+"?user_id=1", tok); r.code != http.StatusOK || r.body["total"] == nil {
		t.Fatalf("用户流量记录错误: %d %s", r.code, r.raw)
	}

	if s := e.get(ap("system/getSystemStatus"), tok).obj(t); s["horizon"] != true || s["schedule"] != false {
		t.Fatalf("系统状态错误: %v", s)
	}
	if q := e.get(ap("system/getQueueStats"), tok).obj(t); q["failedJobs"].(float64) != 2 || q["recentJobs"].(float64) != 3 {
		t.Fatalf("队列统计错误: %v", q)
	}
	if w := e.get(ap("system/getQueueWorkload"), tok).list(t); len(w) != 2 || w[0].(map[string]any)["name"] != "order_handle" {
		t.Fatalf("队列负载错误: %v", w)
	}
	if r := e.get(ap("system/getQueueMasters"), tok); r.code != http.StatusOK || r.body["gvboard"] == nil {
		t.Fatalf("队列主进程错误: %d %s", r.code, r.raw)
	}
	e.db.Create(&model.Log{Title: "错误", Level: ptr("ERROR"), URI: "/", Method: "GET"})
	e.db.Create(&model.Log{Title: "信息", Level: ptr("INFO"), URI: "/", Method: "GET"})
	if r := e.get(ap("system/getSystemLog")+"?filter[0][key]=level&filter[0][condition]=is&filter[0][value]=ERROR", tok); r.body["total"].(float64) != 1 {
		t.Fatalf("系统日志筛选错误: %s", r.raw)
	}
	wantInvalid(t, e.get(ap("system/getSystemLog")+"?filter[0][key]=title&filter[0][condition]=is&filter[0][value]=x", tok), "filter.0.key")
}
