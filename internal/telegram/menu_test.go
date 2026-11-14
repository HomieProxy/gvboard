package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"gvboard/internal/model"
	"gvboard/internal/order"
)

func callbackUpdate(from int64, data string) Update {
	id := updateSeq.Add(1)
	return Update{UpdateID: id, CallbackQuery: &CallbackQuery{
		ID: "cb" + strconv.FormatInt(id, 10), From: User{ID: from, LanguageCode: "zh"}, Data: data,
		Message: &Message{MessageID: 77, Chat: Chat{ID: from, Type: "private"}},
	}}
}

// click 点击按钮，返回机器人因此调用的 Bot API（不含 answerCallbackQuery）与回调应答。
func (e *env) click(from int64, data string) (calls []apiCall, answer apiCall) {
	e.api.mu.Lock()
	n := len(e.api.calls)
	e.api.mu.Unlock()
	e.HandleUpdate(context.Background(), callbackUpdate(from, data))
	e.api.mu.Lock()
	defer e.api.mu.Unlock()
	for _, c := range e.api.calls[n:] {
		if c.Method == "answerCallbackQuery" {
			answer = c
		} else {
			calls = append(calls, c)
		}
	}
	return calls, answer
}

// shown 点击按钮后显示的文本（修改或新发的最后一条）与按钮。
func (e *env) shown(t *testing.T, from int64, data string) (string, [][]InlineButton) {
	t.Helper()
	calls, ans := e.click(from, data)
	if len(calls) == 0 {
		t.Fatalf("点击 %s 没有显示任何内容，应答：%v", data, ans.Params)
	}
	last := calls[len(calls)-1]
	text, _ := last.Params["text"].(string)
	if text == "" {
		text, _ = last.Params["caption"].(string)
	}
	return text, buttons(last)
}

func buttons(c apiCall) [][]InlineButton {
	var kb InlineKeyboard
	switch v := c.Params["reply_markup"].(type) {
	case string:
		_ = json.Unmarshal([]byte(v), &kb)
	case map[string]any:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &kb)
	}
	return kb.Rows
}

func findButton(rows [][]InlineButton, text string) *InlineButton {
	for _, r := range rows {
		for i := range r {
			if strings.Contains(r[i].Text, text) {
				return &r[i]
			}
		}
	}
	return nil
}

func alertOf(a apiCall) string {
	s, _ := a.Params["text"].(string)
	return s
}

func (e *env) plan(t *testing.T, month int64) *model.Plan {
	t.Helper()
	content := "<p>高速 &amp; 稳定</p><script>x</script>"
	p := &model.Plan{Name: "标准<套餐>", GroupID: 1, TransferEnable: 100, Show: 1, Renew: 1, MonthPrice: &month, Content: &content}
	reset := int64(500)
	p.ResetPrice = &reset
	if err := e.DB.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMenu(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		admin, _ := e.user(t)
		u, _ := e.user(t)
		e.DB.Model(admin).Update("is_admin", 1)
		e.bindDirect(t, admin, 9001)
		e.bindDirect(t, u, 9002)

		// 未绑定：只有网站按钮
		e.HandleUpdate(ctx, message(9003, "zh", "/start"))
		calls := e.api.called("sendMessage")
		rows := buttons(calls[len(calls)-1])
		if len(rows) != 1 || rows[0][0].URL != "https://gvb.example" {
			t.Fatalf("未绑定时只应有网站按钮：%+v", rows)
		}
		// 已绑定：主菜单；管理员才有概况
		e.HandleUpdate(ctx, message(9002, "zh", "/menu"))
		calls = e.api.called("sendMessage")
		rows = buttons(calls[len(calls)-1])
		for _, want := range []string{"我的订阅", "购买套餐", "我的订单", "邀请", "通知设置", "访问网站"} {
			if findButton(rows, want) == nil {
				t.Errorf("主菜单缺少 %s", want)
			}
		}
		if findButton(rows, "管理概况") != nil {
			t.Error("普通用户不应看到管理概况")
		}
		text, rows := e.shown(t, 9001, cbMenu)
		if findButton(rows, "管理概况") == nil || !strings.Contains(text, "tg1@x.com") {
			t.Fatalf("管理员主菜单错误：%s", text)
		}
		if c, _ := e.click(9001, cbMenu); c[0].Method != "editMessageText" || c[0].Params["message_id"].(float64) != 77 {
			t.Fatalf("按钮应原地修改消息：%+v", c)
		}

		// 自定义欢迎消息：HTML 与变量；格式错误时转义重发
		e.set(t, map[string]any{"telegram_welcome": "<b>{app_name}</b> 欢迎"})
		wantContains(t, e.send(9002, "zh", "/start"), "<b>Gvb&lt;Test&gt;</b> 欢迎", "已绑定账号")
		e.set(t, map[string]any{"telegram_welcome": "<bad> {app_name}"})
		wantContains(t, e.send(9002, "zh", "/start"), "&lt;bad&gt; Gvb&lt;Test&gt;")
		e.set(t, map[string]any{"telegram_welcome": ""})

		// 通知设置
		text, rows = e.shown(t, 9002, cbPrefs)
		if findButton(rows, "✅ 订单支付通知") == nil {
			t.Fatalf("默认应开启：%v", rows)
		}
		_, rows = e.shown(t, 9002, cbPrefs+":o")
		if findButton(rows, "⬜ 订单支付通知") == nil || findButton(rows, "✅ 工单回复通知") == nil {
			t.Fatalf("切换后按钮错误：%v", rows)
		}
		p, _ := e.prefs(ctx, u.ID)
		if p.NotifyOrder != 0 || p.NotifyTicket != 1 {
			t.Fatalf("偏好未保存：%+v", p)
		}
		wantContains(t, text, "通知设置")

		// 未绑定点击需要绑定的按钮：弹窗提示
		calls, ans := e.click(9003, cbSubs)
		if len(calls) != 0 || !strings.Contains(alertOf(ans), "请先绑定账号") || ans.Params["show_alert"] != true {
			t.Fatalf("未绑定应弹窗提示：%v %v", calls, ans.Params)
		}
		// 群聊中的按钮只应答
		up := callbackUpdate(9002, cbSubs)
		up.CallbackQuery.Message.Chat = Chat{ID: -5, Type: "group"}
		e.api.reset()
		e.HandleUpdate(ctx, up)
		if len(e.api.calls) != 1 || e.api.calls[0].Method != "answerCallbackQuery" {
			t.Fatalf("群聊按钮应只应答：%+v", e.api.calls)
		}
		// 未知按钮只应答
		if calls, _ := e.click(9002, "zzz:1"); len(calls) != 0 {
			t.Fatal("未知按钮不应有动作")
		}

		// 邀请：自动生成邀请码
		text, _ = e.shown(t, 9002, cbInvite)
		wantContains(t, text, "https://gvb.example/#/register?code=", "已邀请：0 人")
		var codes int64
		e.DB.Model(&model.InviteCode{}).Where("user_id = ?", u.ID).Count(&codes)
		if codes != 1 {
			t.Fatalf("应自动生成 1 个邀请码，实际 %d", codes)
		}
		e.shown(t, 9002, cbInvite)
		e.DB.Model(&model.InviteCode{}).Where("user_id = ?", u.ID).Count(&codes)
		if codes != 1 {
			t.Fatal("已有邀请码时不应重复生成")
		}

		// 管理员概况
		// 统计区间与原项目一致为 [起点, 当前秒)，数据需早于当前秒
		past := time.Now().Unix() - 5
		e.DB.Model(&model.User{}).Where("1 = 1").Update("created_at", past)
		e.DB.Create(&model.Order{UserID: u.ID, TradeNo: "ADM1", PlanID: 1, Period: "month_price", TotalAmount: 1234, Status: 3, CreatedAt: past})
		text, _ = e.shown(t, 9001, cbAdmin)
		wantContains(t, text, "今日收入：¥12.34", "本月收入：¥12.34", "今日注册：2")
		if calls, _ := e.click(9002, cbAdmin); len(calls) != 0 {
			t.Fatal("普通用户不能查看管理概况")
		}
	})
}

func TestSubscriptionMenu(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		u, sub := e.user(t)
		other, otherSub := e.user(t)
		_ = other
		e.bindDirect(t, u, 9101)
		e.DB.Model(sub).Updates(map[string]any{"transfer_enable": int64(10) << 30, "u": int64(1) << 30, "expired_at": nil, "name": "主<订阅>"})

		_, rows := e.shown(t, 9101, cbSubs)
		b := findButton(rows, "主<订阅>")
		if b == nil || !strings.HasPrefix(b.Text, "🟢") || b.CallbackData != fmt.Sprintf("sub:%d", sub.ID) {
			t.Fatalf("订阅列表错误：%+v", rows)
		}
		text, rows := e.shown(t, 9101, b.CallbackData)
		wantContains(t, text, "主&lt;订阅&gt;", "总流量：<code>10 GB</code>", "剩余：<code>9 GB</code>", "长期有效", "可用")
		if findButton(rows, "订阅链接") == nil || findButton(rows, "续费") != nil {
			t.Fatalf("无套餐的订阅不应有续费按钮：%v", rows)
		}
		// 他人订阅
		if _, ans := e.click(9101, fmt.Sprintf("sub:%d", otherSub.ID)); !strings.Contains(alertOf(ans), "订阅不存在") {
			t.Fatalf("访问他人订阅应提示不存在：%v", ans.Params)
		}
		if _, ans := e.click(9101, "sub:abc"); !strings.Contains(alertOf(ans), "参数错误") {
			t.Fatalf("非法参数应提示：%v", ans.Params)
		}
		// 订阅链接：新消息
		calls, _ := e.click(9101, fmt.Sprintf("sl:%d", sub.ID))
		if len(calls) != 1 || calls[0].Method != "sendMessage" || !strings.Contains(calls[0].Params["text"].(string), sub.Token) {
			t.Fatalf("订阅链接错误：%+v", calls)
		}
		// 重置：确认后 token 变化
		text, _ = e.shown(t, 9101, fmt.Sprintf("sr:%d", sub.ID))
		wantContains(t, text, "确定重置")
		var still model.Subscription
		e.DB.Take(&still, sub.ID)
		if still.Token != sub.Token {
			t.Fatal("确认前不应重置")
		}
		calls, ans := e.click(9101, fmt.Sprintf("sr!:%d", sub.ID))
		e.DB.Take(&still, sub.ID)
		if still.Token == sub.Token || !strings.Contains(calls[0].Params["text"].(string), still.Token) || alertOf(ans) != "重置成功" {
			t.Fatalf("重置失败：%+v %v", calls, ans.Params)
		}
		if _, ans := e.click(9101, fmt.Sprintf("sr!:%d", otherSub.ID)); !strings.Contains(alertOf(ans), "订阅不存在") {
			t.Fatal("不能重置他人订阅")
		}
		var os model.Subscription
		e.DB.Take(&os, otherSub.ID)
		if os.Token != otherSub.Token {
			t.Fatal("他人订阅被重置")
		}
	})
}

func TestBuyFlow(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, sub := e.user(t)
		other, _ := e.user(t)
		e.bindDirect(t, u, 9201)
		e.bindDirect(t, other, 9202)
		p := e.plan(t, 1000)
		pid := strconv.FormatInt(p.ID, 10)
		sid := strconv.FormatInt(sub.ID, 10)
		if err := e.DB.Create(&model.Coupon{Code: "TG3", Name: "立减", Type: order.CouponAmount, Value: 300, Show: 1, StartedAt: 1, EndedAt: time.Now().Unix() + 86400}).Error; err != nil {
			t.Fatal(err)
		}
		e.DB.Model(&model.User{}).Where("id = ?", u.ID).Update("balance", 200)

		_, rows := e.shown(t, 9201, cbBuy)
		b := findButton(rows, "标准<套餐>")
		if b == nil || b.CallbackData != "plan:"+pid+":0" {
			t.Fatalf("套餐列表错误：%v", rows)
		}
		text, rows := e.shown(t, 9201, b.CallbackData)
		wantContains(t, text, "标准&lt;套餐&gt;", "流量：100 GB", "高速 &amp; 稳定")
		if strings.Contains(text, "<p>") || findButton(rows, "月付 · ¥10") == nil || findButton(rows, "重置流量包") != nil {
			t.Fatalf("套餐详情错误：%s %v", text, rows)
		}
		_, rows = e.shown(t, 9201, "per:"+pid+":0:month_price")
		if findButton(rows, "新开订阅") == nil || findButton(rows, subName(sub)) == nil {
			t.Fatalf("应可选择新开或已有订阅：%v", rows)
		}
		text, rows = e.shown(t, 9201, "dr:"+pid+":"+sid+":month_price")
		wantContains(t, text, "价格：¥10", "将优先使用账户余额 ¥2 抵扣")

		// 优惠券：无效码提示，有效码显示优惠
		e.shown(t, 9201, cbCoupon)
		wantContains(t, e.send(9201, "zh", "NOPE"), "优惠券无效")
		got := e.send(9201, "zh", " TG3 ")
		wantContains(t, got, "优惠券：立减（-¥3）")
		// 其他用户不能使用这个草稿
		if _, ans := e.click(9202, cbConfirm); !strings.Contains(alertOf(ans), "已过期") {
			t.Fatalf("他人不能使用草稿：%v", ans.Params)
		}
		// 没有等待优惠券时，普通文本提示菜单
		wantContains(t, e.send(9201, "zh", "hello"), "/menu")

		// 下单：优惠券 + 余额抵扣
		text, rows = e.shown(t, 9201, cbConfirm)
		var o model.Order
		if err := e.DB.Where("user_id = ?", u.ID).Order("id DESC").Take(&o).Error; err != nil {
			t.Fatal(err)
		}
		if o.TotalAmount != 500 || o.CouponID == nil || o.BalanceAmount == nil || *o.BalanceAmount != 200 || o.SubscriptionID == nil || *o.SubscriptionID != sub.ID {
			t.Fatalf("订单金额错误：%+v", o)
		}
		wantContains(t, text, o.TradeNo, "金额：¥5", "待支付", "优惠：¥3", "余额抵扣：¥2")
		if findButton(rows, "支付宝") == nil || findButton(rows, "信用卡") != nil || findButton(rows, "取消订单") == nil {
			t.Fatalf("支付方式错误（信用卡需在网站支付）：%v", rows)
		}
		if _, ans := e.click(9201, cbConfirm); !strings.Contains(alertOf(ans), "已过期") {
			t.Fatal("下单后草稿应删除")
		}

		// 二维码支付：本机生成图片
		e.payer.typ, e.payer.data = 0, "https://qr.example/pay?x=1"
		calls, _ := e.click(9201, "pay:"+o.TradeNo+":1")
		if len(calls) != 1 || calls[0].Method != "sendPhoto" || calls[0].Params["photo_size"].(float64) < 200 {
			t.Fatalf("应发送二维码图片：%+v", calls)
		}
		wantContains(t, calls[0].Params["caption"].(string), "¥5", o.TradeNo)
		if findButton(buttons(calls[0]), "我已支付") == nil {
			t.Fatal("二维码应附带刷新按钮")
		}
		// 跳转支付
		e.payer.typ, e.payer.data = 1, "https://pay.example/go"
		_, rows = e.shown(t, 9201, "pay:"+o.TradeNo+":1")
		if b := findButton(rows, "前往支付"); b == nil || b.URL != "https://pay.example/go" {
			t.Fatalf("应有支付链接按钮：%v", rows)
		}
		// 不支持的结果与他人订单
		e.payer.typ, e.payer.data = 0, map[string]any{"x": 1}
		if _, ans := e.click(9201, "pay:"+o.TradeNo+":1"); !strings.Contains(alertOf(ans), "请在网站完成支付") {
			t.Fatalf("不支持的支付结果应提示：%v", ans.Params)
		}
		if _, ans := e.click(9202, cbOrder+":"+o.TradeNo); !strings.Contains(alertOf(ans), "订单不存在") {
			t.Fatal("不能查看他人订单")
		}
		if _, ans := e.click(9202, cbCancel+":"+o.TradeNo); !strings.Contains(alertOf(ans), "订单不存在") {
			t.Fatal("不能取消他人订单")
		}

		// 订单列表与取消（退回余额）
		_, rows = e.shown(t, 9201, cbOrders)
		if findButton(rows, "标准<套餐> · ¥5") == nil {
			t.Fatalf("订单列表错误：%v", rows)
		}
		_, ans := e.click(9201, cbCancel+":"+o.TradeNo)
		e.DB.Take(&o, o.ID)
		var bal int64
		e.DB.Model(&model.User{}).Select("balance").Where("id = ?", u.ID).Scan(&bal)
		if o.Status != model.OrderStatusCancelled || bal != 200 || alertOf(ans) != "订单已取消" {
			t.Fatalf("取消失败：状态 %d 余额 %d", o.Status, bal)
		}

		// 余额足够：订单金额为 0，确认开通
		e.DB.Model(&model.User{}).Where("id = ?", u.ID).Update("balance", 100000)
		e.shown(t, 9201, "dr:"+pid+":"+sid+":month_price")
		_, rows = e.shown(t, 9201, cbConfirm)
		act := findButton(rows, "确认开通")
		if act == nil || findButton(rows, "支付宝") != nil {
			t.Fatalf("0 元订单应只有确认开通：%v", rows)
		}
		_, ans = e.click(9201, act.CallbackData)
		var paid model.Order
		e.DB.Where("user_id = ?", u.ID).Order("id DESC").Take(&paid)
		if paid.Status != model.OrderStatusProcessing || !strings.Contains(alertOf(ans), "支付成功") {
			t.Fatalf("确认开通失败：%d %v", paid.Status, ans.Params)
		}

		// 已有订阅续费：从订阅详情进入时可选重置流量包
		e.DB.Model(sub).Update("plan_id", p.ID)
		_, rows = e.shown(t, 9201, "sub:"+sid)
		renew := findButton(rows, "续费")
		if renew == nil || renew.CallbackData != "plan:"+pid+":"+sid {
			t.Fatalf("续费按钮错误：%v", rows)
		}
		_, rows = e.shown(t, 9201, renew.CallbackData)
		if b := findButton(rows, "重置流量包"); b == nil || b.CallbackData != "per:"+pid+":"+sid+":reset_price" {
			t.Fatalf("已有订阅应可选重置流量包：%v", rows)
		}
		_ = ctx
	})
}
