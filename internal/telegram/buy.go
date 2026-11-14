package telegram

import (
	"context"
	"errors"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/skip2/go-qrcode"

	"gvboard/internal/cache"
	"gvboard/internal/httpx"
	"gvboard/internal/i18n"
	"gvboard/internal/model"
	"gvboard/internal/order"
)

const (
	draftTTL       = 30 * time.Minute
	maxPlanContent = 800
	qrSize         = 512
)

var (
	errInvalid      = httpx.Validation("Invalid parameter")
	errDraftExpired = httpx.BadRequest("The order information has expired, please choose again")
	errPayOnSite    = httpx.BadRequest("Please complete the payment with this method on the website")
	tagRe           = regexp.MustCompile(`<[^>]*>`)
	blankLinesRe    = regexp.MustCompile(`\n{3,}`)
)

// periods 下单周期的显示顺序与文案（重置流量包只能用于已有订阅）。
var periods = []struct{ key, label string }{
	{order.PeriodMonth, "Monthly"},
	{order.PeriodQuarter, "Quarterly"},
	{order.PeriodHalfYear, "Half-yearly"},
	{order.PeriodYear, "Yearly"},
	{order.PeriodTwoYear, "Two years"},
	{order.PeriodThreeYear, "Three years"},
	{order.PeriodOnetime, "One-time"},
	{order.PeriodReset, "Reset traffic"},
}

func periodLabel(c *Context, p string) string {
	for _, v := range periods {
		if v.key == p {
			return c.T(v.label, nil)
		}
	}
	if p == order.PeriodDeposit {
		return c.T("Deposit", nil)
	}
	return p
}

func statusLabel(c *Context, st int) string {
	switch st {
	case model.OrderStatusPending:
		return "⏳ " + c.T("Pending payment", nil)
	case model.OrderStatusProcessing:
		return "⚙️ " + c.T("Processing", nil)
	case model.OrderStatusCancelled:
		return "❌ " + c.T("Cancelled", nil)
	case model.OrderStatusCompleted:
		return "✅ " + c.T("Completed", nil)
	case model.OrderStatusDiscounted:
		return "↩️ " + c.T("Discounted", nil)
	}
	return strconv.Itoa(st)
}

// plainText 套餐说明（HTML / Markdown）去掉标签后作为纯文本显示。
func plainText(s string, n int) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = html.UnescapeString(tagRe.ReplaceAllString(s, ""))
	s = blankLinesRe.ReplaceAllString(strings.TrimSpace(s), "\n\n")
	return truncate(s, n)
}

// cbBuy 销售中的套餐。
func (s *Service) cbBuy(ctx context.Context, c *Context) error {
	plans, err := s.Orders.Plans(ctx)
	if err != nil {
		return err
	}
	kb := &InlineKeyboard{}
	for i := range plans {
		p := &plans[i]
		if p.CapacityLimit != nil && *p.CapacityLimit <= 0 {
			continue
		}
		kb.Rows = append(kb.Rows, row(btn(p.Name, cbPlan+":"+strconv.FormatInt(p.ID, 10)+":0")))
	}
	text := "🛒 <b>" + esc(c.T("Buy a plan", nil)) + "</b>\n" + separator + "\n"
	if len(kb.Rows) == 0 {
		text += esc(c.T("No plans available at the moment.", nil))
	} else {
		text += esc(c.T("Select a plan.", nil))
	}
	kb.Rows = append(kb.Rows, c.back(cbMenu))
	return c.Show(ctx, text, kb)
}

// cbPlan 套餐详情与可选周期，参数 :planID:subID（subID 为 0 表示尚未选择订阅）。
func (s *Service) cbPlan(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	planID, err := argID(c, 0)
	if err != nil {
		return err
	}
	subID, err := argID(c, 1)
	if err != nil {
		return err
	}
	if subID != 0 {
		if _, err := s.Subscriptions.Find(ctx, s.DB, u.ID, subID); err != nil {
			return err
		}
	}
	p, err := s.Orders.Plan(ctx, u.ID, planID)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("🛒 <b>" + esc(p.Name) + "</b>\n" + separator + "\n")
	b.WriteString(esc(c.T("Traffic: :traffic GB", i18n.Params{"traffic": p.TransferEnable})))
	if p.DeviceLimit != nil && *p.DeviceLimit > 0 {
		b.WriteString("\n" + esc(c.T("Devices: :count", i18n.Params{"count": *p.DeviceLimit})))
	}
	if p.Content != nil {
		if t := plainText(*p.Content, maxPlanContent); t != "" {
			b.WriteString("\n\n" + esc(t))
		}
	}
	kb := &InlineKeyboard{}
	base := cbPeriod + ":" + strconv.FormatInt(p.ID, 10) + ":" + strconv.FormatInt(subID, 10) + ":"
	for _, per := range periods {
		price := order.Price(p, per.key)
		if price == nil || (per.key == order.PeriodReset && subID == 0) {
			continue
		}
		kb.Rows = append(kb.Rows, row(btn(c.T(per.label, nil)+" · "+s.Settings.String("currency_symbol")+money(*price), base+per.key)))
	}
	if len(kb.Rows) == 0 {
		b.WriteString("\n\n" + esc(c.T("This plan cannot be purchased at the moment.", nil)))
	}
	backTo := cbBuy
	if subID != 0 {
		backTo = cbSub + ":" + strconv.FormatInt(subID, 10)
	}
	kb.Rows = append(kb.Rows, c.back(backTo))
	return c.Show(ctx, b.String(), kb)
}

// cbPeriod 选择新开订阅或已有订阅，参数 :planID:subID:period。已指定订阅时直接进入下单确认。
func (s *Service) cbPeriod(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	if len(c.Args) != 3 {
		return errInvalid
	}
	planID, err := argID(c, 0)
	if err != nil {
		return err
	}
	if c.Args[1] != "0" {
		return s.cbDraft(ctx, c)
	}
	var subs []model.Subscription
	if err := s.DB.WithContext(ctx).Where("user_id = ?", u.ID).Order("is_primary DESC").Order("id").Find(&subs).Error; err != nil {
		return err
	}
	pid, period := strconv.FormatInt(planID, 10), c.Args[2]
	kb := &InlineKeyboard{}
	if period != order.PeriodReset {
		kb.Rows = append(kb.Rows, row(btn("🆕 "+c.T("New subscription", nil), cbDraft+":"+pid+":0:"+period)))
	}
	for i := range subs {
		kb.Rows = append(kb.Rows, row(btn("🔄 "+subName(&subs[i]), cbDraft+":"+pid+":"+strconv.FormatInt(subs[i].ID, 10)+":"+period)))
	}
	kb.Rows = append(kb.Rows, c.back(cbPlan+":"+pid+":0"))
	return c.Show(ctx, "🛒 "+esc(c.T("Buy for a new subscription, or renew / change the plan of an existing subscription?", nil)), kb)
}

// draft 机器人中的下单草稿，保存在 Redis（按 Telegram 用户）。
type draft struct {
	UserID   int64  `json:"user_id"`
	PlanID   int64  `json:"plan_id"`
	SubID    int64  `json:"sub_id"`
	Period   string `json:"period"`
	Coupon   string `json:"coupon"`
	Awaiting bool   `json:"awaiting"` // 等待用户发送优惠券码
}

func (s *Service) draftKey(c *Context) string {
	return s.Cache.KeyOf(cache.KeyTelegramDraft, c.From.ID)
}

func (s *Service) loadDraft(ctx context.Context, c *Context, userID int64) (*draft, error) {
	var d draft
	ok, err := s.Cache.GetJSON(ctx, s.draftKey(c), &d)
	if err != nil {
		return nil, err
	}
	if !ok || d.UserID != userID {
		return nil, nil
	}
	return &d, nil
}

func (s *Service) saveDraft(ctx context.Context, c *Context, d *draft) error {
	return s.Cache.SetJSON(ctx, s.draftKey(c), d, draftTTL)
}

// cbDraft 下单确认。带参数 :planID:subID:period 时新建草稿，否则显示当前草稿（取消输入优惠券）。
func (s *Service) cbDraft(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	var d *draft
	if len(c.Args) == 3 {
		planID, err := argID(c, 0)
		if err != nil {
			return err
		}
		subID, err := argID(c, 1)
		if err != nil {
			return err
		}
		d = &draft{UserID: u.ID, PlanID: planID, SubID: subID, Period: c.Args[2]}
	} else {
		var err error
		if d, err = s.loadDraft(ctx, c, u.ID); err != nil {
			return err
		}
		if d == nil {
			return errDraftExpired
		}
		d.Awaiting = false
	}
	if err := s.saveDraft(ctx, c, d); err != nil {
		return err
	}
	text, kb, err := s.renderDraft(ctx, c, u, d)
	if err != nil {
		return err
	}
	return c.Show(ctx, text, kb)
}

// renderDraft 下单确认页。金额为预估值，实际金额（会员折扣、余额抵扣、补差价等）以订单为准。
func (s *Service) renderDraft(ctx context.Context, c *Context, u *model.User, d *draft) (string, *InlineKeyboard, error) {
	p, err := s.Orders.Plan(ctx, u.ID, d.PlanID)
	if err != nil {
		return "", nil, err
	}
	price := order.Price(p, d.Period)
	if price == nil {
		return "", nil, errInvalid
	}
	target := c.T("New subscription", nil)
	if d.SubID != 0 {
		sub, err := s.Subscriptions.Find(ctx, s.DB, u.ID, d.SubID)
		if err != nil {
			return "", nil, err
		}
		target = subName(sub)
	}
	var b strings.Builder
	b.WriteString("🧾 <b>" + esc(c.T("Confirm order", nil)) + "</b>\n" + separator + "\n")
	b.WriteString(c.T("telegram.draft", i18n.Params{
		"plan": "<code>" + esc(p.Name) + "</code>", "period": esc(periodLabel(c, d.Period)),
		"target": "<code>" + esc(target) + "</code>", "price": s.price(*price),
	}))
	if d.Coupon != "" {
		cp, err := s.Orders.CheckCoupon(ctx, u.ID, order.CouponCheckInput{Code: d.Coupon, PlanID: d.PlanID, Period: d.Period})
		if err != nil {
			return "", nil, err
		}
		off := cp.Value
		if cp.Type == order.CouponPercent {
			off = *price * cp.Value / 100
		}
		if off > *price {
			off = *price
		}
		b.WriteString("\n" + c.T("Coupon: :name (-:amount)", i18n.Params{"name": esc(cp.Name), "amount": s.price(off)}))
	}
	var bal int64
	if err := s.DB.WithContext(ctx).Model(&model.User{}).Select("balance").Where("id = ?", u.ID).Scan(&bal).Error; err != nil {
		return "", nil, err
	}
	if bal > 0 {
		b.WriteString("\n" + esc(c.T("Your balance of :amount will be used first.", i18n.Params{"amount": s.Settings.String("currency_symbol") + money(bal)})))
	}
	b.WriteString("\n\n" + esc(c.T("The final amount is shown on the order.", nil)))
	kb := &InlineKeyboard{Rows: [][]InlineButton{row(btn("✅ "+c.T("Place order", nil), cbConfirm))}}
	if d.Coupon == "" {
		kb.Rows = append(kb.Rows, row(btn("🎟 "+c.T("Use a coupon", nil), cbCoupon)))
	} else {
		kb.Rows = append(kb.Rows, row(btn("❌ "+c.T("Remove the coupon", nil), cbCoupon+":x")))
	}
	kb.Rows = append(kb.Rows, c.back(cbPlan+":"+strconv.FormatInt(d.PlanID, 10)+":"+strconv.FormatInt(d.SubID, 10)))
	return b.String(), kb, nil
}

// cbCoupon 开始输入优惠券；参数 x 为移除已使用的优惠券。
func (s *Service) cbCoupon(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	d, err := s.loadDraft(ctx, c, u.ID)
	if err != nil {
		return err
	}
	if d == nil {
		return errDraftExpired
	}
	if len(c.Args) == 1 && c.Args[0] == "x" {
		d.Coupon, d.Awaiting = "", false
		if err := s.saveDraft(ctx, c, d); err != nil {
			return err
		}
		text, kb, err := s.renderDraft(ctx, c, u, d)
		if err != nil {
			return err
		}
		return c.Show(ctx, text, kb)
	}
	d.Awaiting = true
	if err := s.saveDraft(ctx, c, d); err != nil {
		return err
	}
	return c.Show(ctx, "🎟 "+esc(c.T("Please send the coupon code.", nil)),
		&InlineKeyboard{Rows: [][]InlineButton{row(btn(c.T("Cancel", nil), cbDraft))}})
}

// handleText 私聊中的普通文本：等待优惠券时作为优惠券码，否则提示打开菜单。
func (s *Service) handleText(ctx context.Context, c *Context) error {
	u, err := s.userByTelegram(ctx, c.From.ID)
	if err != nil {
		return err
	}
	if u != nil && s.Orders != nil {
		d, err := s.loadDraft(ctx, c, u.ID)
		if err != nil {
			return err
		}
		if d != nil && d.Awaiting {
			code := strings.TrimSpace(c.Msg.Text)
			if _, err := s.Orders.CheckCoupon(ctx, u.ID, order.CouponCheckInput{Code: code, PlanID: d.PlanID, Period: d.Period}); err != nil {
				return err
			}
			d.Coupon, d.Awaiting = code, false
			if err := s.saveDraft(ctx, c, d); err != nil {
				return err
			}
			text, kb, err := s.renderDraft(ctx, c, u, d)
			if err != nil {
				return err
			}
			return c.Reply(ctx, text, kb)
		}
	}
	return c.Reply(ctx, esc(c.T("Send /menu to open the main menu.", nil)), nil)
}

// cbConfirm 按草稿下单（与网站下单相同的校验与余额抵扣）。
func (s *Service) cbConfirm(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	d, err := s.loadDraft(ctx, c, u.ID)
	if err != nil {
		return err
	}
	if d == nil {
		return errDraftExpired
	}
	tradeNo, err := s.Orders.Create(ctx, u.ID, order.CreateInput{
		PlanID: d.PlanID, Period: d.Period, CouponCode: d.Coupon, SubscriptionID: d.SubID,
	})
	if err != nil {
		return err
	}
	if err := s.Cache.Del(ctx, s.draftKey(c)).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	c.Args = []string{tradeNo}
	return s.cbOrder(ctx, c)
}

// cbOrders 最近的订单。
func (s *Service) cbOrders(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	list, err := s.Orders.List(ctx, u.ID, nil)
	if err != nil {
		return err
	}
	kb := &InlineKeyboard{}
	for i := range list {
		if i == maxListRows {
			break
		}
		o := &list[i]
		name := periodLabel(c, o.Period)
		if o.Plan != nil && o.PlanID != 0 {
			name = o.Plan.Name
		}
		label := strings.Fields(statusLabel(c, o.Status))[0] + " " + name + " · " + s.Settings.String("currency_symbol") + money(o.TotalAmount)
		kb.Rows = append(kb.Rows, row(btn(label, cbOrder+":"+o.TradeNo)))
	}
	text := "🧾 <b>" + esc(c.T("My orders", nil)) + "</b>\n" + separator + "\n"
	if len(list) == 0 {
		text += esc(c.T("No orders yet.", nil))
	} else {
		text += esc(c.T("The latest :count orders are shown.", i18n.Params{"count": len(kb.Rows)}))
	}
	kb.Rows = append(kb.Rows, c.back(cbMenu))
	return c.Show(ctx, text, kb)
}

// cbOrder 订单详情；待支付时列出支付方式与取消按钮。
func (s *Service) cbOrder(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	if len(c.Args) != 1 {
		return errInvalid
	}
	o, err := s.Orders.Detail(ctx, u.ID, c.Args[0])
	if err != nil {
		return err
	}
	plan := periodLabel(c, o.Period)
	if o.Plan != nil && o.PlanID != 0 {
		plan = o.Plan.Name
	}
	var b strings.Builder
	b.WriteString("🧾 <b>" + esc(c.T("Order details", nil)) + "</b>\n" + separator + "\n")
	b.WriteString(c.T("telegram.order", i18n.Params{
		"trade_no": "<code>" + esc(o.TradeNo) + "</code>", "plan": esc(plan), "period": esc(periodLabel(c, o.Period)),
		"amount": s.price(o.TotalAmount), "status": esc(statusLabel(c, o.Status)),
		"created": time.Unix(o.CreatedAt, 0).Format(time.DateTime),
	}))
	for _, v := range []struct {
		key string
		amt *int64
	}{{"Discount: :amount", o.DiscountAmount}, {"Balance used: :amount", o.BalanceAmount}, {"Plan conversion: :amount", o.SurplusAmount}} {
		if v.amt != nil && *v.amt != 0 {
			b.WriteString("\n" + c.T(v.key, i18n.Params{"amount": s.price(*v.amt)}))
		}
	}
	kb := &InlineKeyboard{}
	if o.Status == model.OrderStatusPending {
		if o.TotalAmount <= 0 {
			kb.Rows = append(kb.Rows, row(btn("✅ "+c.T("Activate", nil), cbPay+":"+o.TradeNo+":0")))
		} else if s.Payments != nil {
			methods, err := s.Payments.Methods(ctx)
			if err != nil {
				return err
			}
			for _, m := range methods {
				if m.Payment == "StripeCredit" {
					continue
				}
				kb.Rows = append(kb.Rows, row(btn("💳 "+m.Name, cbPay+":"+o.TradeNo+":"+strconv.FormatInt(m.ID, 10))))
			}
		}
		kb.Rows = append(kb.Rows, row(btn("❌ "+c.T("Cancel order", nil), cbCancel+":"+o.TradeNo)))
	}
	kb.Rows = append(kb.Rows, row(btn("🔄 "+c.T("Refresh", nil), cbOrder+":"+o.TradeNo)), c.back(cbOrders))
	return c.Show(ctx, b.String(), kb)
}

// cbPay 结算：二维码类网关在本机生成二维码图片发送，跳转类网关给出支付链接按钮（决策 D51）。
func (s *Service) cbPay(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	if len(c.Args) != 2 {
		return errInvalid
	}
	tradeNo := c.Args[0]
	method, err := strconv.ParseInt(c.Args[1], 10, 64)
	if err != nil {
		return errInvalid
	}
	res, err := s.Orders.Checkout(ctx, u.ID, tradeNo, order.PayInput{Method: method})
	if err != nil {
		return err
	}
	refresh := &InlineKeyboard{Rows: [][]InlineButton{row(btn("🔄 "+c.T("I have paid, refresh", nil), cbOrder+":"+tradeNo))}}
	data, _ := res.Data.(string)
	switch {
	case res.Type == -1 || res.Type == 2:
		c.Answer(ctx, c.T("Payment successful, the order is being processed", nil), false)
		c.Args = []string{tradeNo}
		return s.cbOrder(ctx, c)
	case res.Type == 0 && data != "":
		png, err := qrcode.Encode(data, qrcode.Medium, qrSize)
		if err != nil {
			return err
		}
		o, err := s.Orders.Detail(ctx, u.ID, tradeNo)
		if err != nil {
			return err
		}
		total := o.TotalAmount
		if o.HandlingAmount != nil {
			total += *o.HandlingAmount
		}
		cl, err := s.Client()
		if err != nil {
			return err
		}
		caption := c.T("telegram.pay_qrcode", i18n.Params{"amount": s.price(total), "trade_no": "<code>" + esc(tradeNo) + "</code>"})
		return cl.SendPhoto(ctx, c.ChatID, png, caption, refresh)
	case res.Type == 1 && (strings.HasPrefix(data, "https://") || strings.HasPrefix(data, "http://")):
		refresh.Rows = append([][]InlineButton{row(InlineButton{Text: "💳 " + c.T("Go to pay", nil), URL: data})}, refresh.Rows...)
		return c.Show(ctx, "💳 "+esc(c.T("Tap the button below to pay. Come back and refresh after paying.", nil)), refresh)
	}
	return errPayOnSite
}

// cbCancel 取消待支付订单（退回已抵扣的余额）。
func (s *Service) cbCancel(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	if len(c.Args) != 1 {
		return errInvalid
	}
	if err := s.Orders.Cancel(ctx, u.ID, c.Args[0]); err != nil {
		return err
	}
	c.Answer(ctx, c.T("Order cancelled", nil), false)
	return s.cbOrder(ctx, c)
}
