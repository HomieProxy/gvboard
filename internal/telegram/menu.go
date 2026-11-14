package telegram

import (
	"context"
	"strings"

	"gvboard/internal/adminstat"
	"gvboard/internal/i18n"
	"gvboard/internal/model"
)

// 按钮动作名称（callback_data 最长 64 字节，名称尽量短）。
const (
	cbMenu      = "m"   // 主菜单
	cbPrefs     = "pf"  // 通知设置，参数 o / t / r 为切换
	cbInvite    = "inv" // 邀请
	cbAdmin     = "adm" // 管理员概况
	cbSubs      = "subs"
	cbSub       = "sub"  // 订阅详情 :id
	cbSubLink   = "sl"   // 订阅链接 :id
	cbSubReset  = "sr"   // 重置订阅链接确认 :id
	cbSubReset2 = "sr!"  // 确认重置 :id
	cbBuy       = "buy"  // 套餐列表
	cbPlan      = "plan" // 套餐周期 :planID:subID
	cbPeriod    = "per"  // 选择订阅 :planID:subID:period
	cbDraft     = "dr"   // 下单确认 :planID:subID:period（无参数时显示当前草稿）
	cbCoupon    = "cp"   // 输入优惠券
	cbConfirm   = "ok"   // 确认下单
	cbOrders    = "ord"  // 订单列表
	cbOrder     = "od"   // 订单详情 :tradeNo
	cbPay       = "pay"  // 支付 :tradeNo:methodID
	cbCancel    = "oc"   // 取消订单 :tradeNo
	cbNoop      = "noop" // 不做任何事（分隔用）
	backArrow   = "« "
	maxListRows = 10
)

func btn(text, data string) InlineButton { return InlineButton{Text: text, CallbackData: data} }

func row(b ...InlineButton) []InlineButton { return b }

func (c *Context) back(data string) []InlineButton {
	return row(btn(backArrow+c.T("Back", nil), data))
}

func (s *Service) builtinCallbacks() []Callback {
	return []Callback{
		{Name: cbMenu, Handle: s.showMenu},
		{Name: cbNoop, Handle: func(context.Context, *Context) error { return nil }},
		{Name: cbPrefs, Bound: true, Handle: s.cbPrefs},
		{Name: cbInvite, Bound: true, Handle: s.cbInvite},
		{Name: cbAdmin, Bound: true, Handle: s.cbAdmin},
		{Name: cbSubs, Bound: true, Handle: s.cbSubs},
		{Name: cbSub, Bound: true, Handle: s.cbSub},
		{Name: cbSubLink, Bound: true, Handle: s.cbSubLink},
		{Name: cbSubReset, Bound: true, Handle: s.cbSubReset},
		{Name: cbSubReset2, Bound: true, Handle: s.cbSubResetConfirm},
		{Name: cbBuy, Bound: true, Handle: s.cbBuy},
		{Name: cbPlan, Bound: true, Handle: s.cbPlan},
		{Name: cbPeriod, Bound: true, Handle: s.cbPeriod},
		{Name: cbDraft, Bound: true, Handle: s.cbDraft},
		{Name: cbCoupon, Bound: true, Handle: s.cbCoupon},
		{Name: cbConfirm, Bound: true, Handle: s.cbConfirm},
		{Name: cbOrders, Bound: true, Handle: s.cbOrders},
		{Name: cbOrder, Bound: true, Handle: s.cbOrder},
		{Name: cbPay, Bound: true, Handle: s.cbPay},
		{Name: cbCancel, Bound: true, Handle: s.cbCancel},
	}
}

// menuKeyboard 主菜单按钮。未绑定时只有网站链接。
func (s *Service) menuKeyboard(c *Context, u *model.User) *InlineKeyboard {
	kb := &InlineKeyboard{}
	if u != nil {
		var r []InlineButton
		if s.Subscriptions != nil {
			r = append(r, btn("📦 "+c.T("My subscriptions", nil), cbSubs))
		}
		if s.Orders != nil {
			r = append(r, btn("🛒 "+c.T("Buy a plan", nil), cbBuy))
		}
		if len(r) > 0 {
			kb.Rows = append(kb.Rows, r)
		}
		r = nil
		if s.Orders != nil {
			r = append(r, btn("🧾 "+c.T("My orders", nil), cbOrders))
		}
		if s.Invites != nil {
			r = append(r, btn("🎁 "+c.T("Invite", nil), cbInvite))
		}
		if len(r) > 0 {
			kb.Rows = append(kb.Rows, r)
		}
		r = row(btn("🔔 "+c.T("Notifications", nil), cbPrefs))
		if u.IsAdmin == 1 {
			r = append(r, btn("📊 "+c.T("Overview", nil), cbAdmin))
		}
		kb.Rows = append(kb.Rows, r)
	}
	if site := s.siteURL(); strings.HasPrefix(site, "https://") || strings.HasPrefix(site, "http://") {
		kb.Rows = append(kb.Rows, row(InlineButton{Text: "🌐 " + c.T("Website", nil), URL: site}))
	}
	if len(kb.Rows) == 0 {
		return nil
	}
	return kb
}

func (s *Service) siteURL() string { return strings.TrimRight(s.Settings.String("app_url"), "/") }

// showMenu 欢迎信息与主菜单（/start、/menu、返回主菜单）。
// 自定义欢迎消息按 Telegram HTML 发送，格式错误导致发送失败时转义为纯文本重发（决策 D52）。
func (s *Service) showMenu(ctx context.Context, c *Context) error {
	u, err := s.userByTelegram(ctx, c.From.ID)
	if err != nil {
		return err
	}
	var status string
	if u != nil {
		s.rememberLocale(ctx, u.ID, c.Locale)
		status = c.T("Bound account: :email", i18n.Params{"email": "<code>" + esc(u.Email) + "</code>"})
	} else {
		status = esc(c.T("You have not bound an account yet. Click Bind Telegram in the user center of the website, or send /bind followed by your subscription link.", nil))
	}
	kb := s.menuKeyboard(c, u)
	name := s.Settings.String("app_name")
	custom := strings.TrimSpace(s.Settings.String("telegram_welcome"))
	if custom == "" {
		var b strings.Builder
		b.WriteString("👋 " + esc(c.T("Welcome to :app_name!", i18n.Params{"app_name": name})) + "\n\n" + status)
		b.WriteString("\n\n" + esc(c.T("Available commands:", nil)))
		for _, cmd := range s.MenuCommands(c.Locale) {
			b.WriteString("\n/" + cmd.Command + " — " + esc(cmd.Description))
		}
		return c.Show(ctx, b.String(), kb)
	}
	err = c.Show(ctx, strings.ReplaceAll(custom, "{app_name}", esc(name))+"\n\n"+status, kb)
	if IsAPIError(err, 400) {
		err = c.Show(ctx, esc(strings.ReplaceAll(custom, "{app_name}", name))+"\n\n"+status, kb)
	}
	return err
}

// cbPrefs 通知设置，参数 o / t / r 切换对应开关。
func (s *Service) cbPrefs(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	p, err := s.prefs(ctx, u.ID)
	if err != nil {
		return err
	}
	if len(c.Args) == 1 {
		col := map[string]string{"o": "notify_order", "t": "notify_ticket", "r": "notify_remind"}[c.Args[0]]
		cur := map[string]*int{"o": &p.NotifyOrder, "t": &p.NotifyTicket, "r": &p.NotifyRemind}[c.Args[0]]
		if col != "" {
			*cur = 1 - boolInt(*cur != 0)
			if err := s.savePrefs(ctx, u.ID, map[string]any{col: *cur}); err != nil {
				return err
			}
		}
	}
	mark := func(on int, key string) string {
		if on != 0 {
			return "✅ " + c.T(key, nil)
		}
		return "⬜ " + c.T(key, nil)
	}
	kb := &InlineKeyboard{Rows: [][]InlineButton{
		row(btn(mark(p.NotifyOrder, "Order payment notifications"), cbPrefs+":o")),
		row(btn(mark(p.NotifyTicket, "Ticket reply notifications"), cbPrefs+":t")),
		row(btn(mark(p.NotifyRemind, "Expiration and traffic reminders"), cbPrefs+":r")),
		c.back(cbMenu),
	}}
	return c.Show(ctx, "🔔 <b>"+esc(c.T("Notifications", nil))+"</b>\n"+separator+"\n"+
		esc(c.T("Tap to turn notifications on or off. The settings are shared with the website.", nil)), kb)
}

// cbInvite 邀请链接与佣金。没有可用邀请码时自动生成一个（受邀请码数量上限限制）。
func (s *Service) cbInvite(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	ov, err := s.Invites.Fetch(ctx, u.ID)
	if err != nil {
		return err
	}
	if len(ov.Codes) == 0 {
		if code, err := s.Invites.Generate(ctx, u.ID); err == nil {
			ov.Codes = append(ov.Codes, *code)
		}
	}
	link := "-"
	if len(ov.Codes) > 0 {
		link = s.siteURL() + "/#/register?code=" + ov.Codes[0].Code
	}
	st := ov.Stat
	text := "🎁 <b>" + esc(c.T("Invite", nil)) + "</b>\n" + separator + "\n" + c.T("telegram.invite", i18n.Params{
		"link":     "<code>" + esc(link) + "</code>",
		"count":    st.Invitees,
		"rate":     st.CommissionRate,
		"balance":  s.price(st.CommissionBalance),
		"pending":  s.price(int64(st.PendingCommission)),
		"total":    s.price(st.TotalCommission),
		"currency": esc(s.Settings.String("currency")),
	})
	return c.Show(ctx, text, &InlineKeyboard{Rows: [][]InlineButton{c.back(cbMenu)}})
}

// price 金额（分）加货币符号。
func (s *Service) price(cents int64) string {
	return esc(s.Settings.String("currency_symbol")) + money(cents)
}

// cbAdmin 管理员概况（与原项目管理端首页统计口径一致），只有管理员可见。
func (s *Service) cbAdmin(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	if u.IsAdmin != 1 {
		return nil
	}
	st, err := adminstat.GetOverview(ctx, s.DB, s.nowTime())
	if err != nil {
		return err
	}
	text := "📊 <b>" + esc(c.T("Overview", nil)) + "</b>\n" + separator + "\n" + c.T("telegram.overview", i18n.Params{
		"day_income": s.price(st.DayIncome), "month_income": s.price(st.MonthIncome), "last_income": s.price(st.LastMonthIncome),
		"day_register": st.DayRegisterTotal, "month_register": st.MonthRegisterTotal, "online": st.OnlineSubscription,
		"tickets": st.TicketPendingTotal, "commission": st.CommissionPendingTotal,
	})
	return c.Show(ctx, text, &InlineKeyboard{Rows: [][]InlineButton{
		row(btn("🔄 "+c.T("Refresh", nil), cbAdmin)), c.back(cbMenu),
	}})
}
