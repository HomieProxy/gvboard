package telegram

import (
	"context"
	"strconv"
	"time"

	"gvboard/internal/i18n"
	"gvboard/internal/model"
	"gvboard/internal/subscription"
	"gvboard/internal/util"
)

func argID(c *Context, i int) (int64, error) {
	if len(c.Args) <= i {
		return 0, errInvalid
	}
	id, err := strconv.ParseInt(c.Args[i], 10, 64)
	if err != nil || id < 0 {
		return 0, errInvalid
	}
	return id, nil
}

// cbSubs 订阅列表。只读数据库，不生成订阅链接（一次性链接方式下生成链接会创建新 token）。
func (s *Service) cbSubs(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	var subs []model.Subscription
	if err := s.DB.WithContext(ctx).Where("user_id = ?", u.ID).Order("is_primary DESC").Order("id").Find(&subs).Error; err != nil {
		return err
	}
	kb := &InlineKeyboard{}
	now := s.nowTime().Unix()
	for i := range subs {
		sub := &subs[i]
		icon := "🟢 "
		if !subscription.Available(sub, now) {
			icon = "⚪ "
		}
		kb.Rows = append(kb.Rows, row(btn(icon+subName(sub)+" · "+util.TrafficConvert(sub.TransferEnable-sub.U-sub.D),
			cbSub+":"+strconv.FormatInt(sub.ID, 10))))
	}
	kb.Rows = append(kb.Rows, c.back(cbMenu))
	return c.Show(ctx, "📦 <b>"+esc(c.T("My subscriptions", nil))+"</b>\n"+separator+"\n"+
		esc(c.T("Select a subscription to view details.", nil)), kb)
}

func (s *Service) expireText(c *Context, ts *int64) string {
	if ts == nil {
		return c.T("Long-term", nil)
	}
	return time.Unix(*ts, 0).Format(time.DateTime)
}

// cbSub 订阅详情。
func (s *Service) cbSub(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	id, err := argID(c, 0)
	if err != nil {
		return err
	}
	sub, err := s.Subscriptions.Find(ctx, s.DB, u.ID, id)
	if err != nil {
		return err
	}
	plan := c.T("No plan", nil)
	if sub.PlanID != nil {
		var p model.Plan
		if err := s.DB.WithContext(ctx).Select("name").Where("id = ?", *sub.PlanID).Take(&p).Error; err == nil {
			plan = p.Name
		}
	}
	status := c.T("Available", nil)
	if !subscription.Available(sub, s.nowTime().Unix()) {
		status = c.T("Unavailable", nil)
	}
	device := c.T("Unlimited", nil)
	if sub.DeviceLimit != nil && *sub.DeviceLimit > 0 {
		device = strconv.FormatInt(*sub.DeviceLimit, 10)
	}
	text := "📦 <b>" + esc(subName(sub)) + "</b>\n" + separator + "\n" + c.T("telegram.subscription", i18n.Params{
		"plan":    "<code>" + esc(plan) + "</code>",
		"status":  esc(status),
		"total":   "<code>" + util.TrafficConvert(sub.TransferEnable) + "</code>",
		"used":    "<code>" + util.TrafficConvert(sub.U+sub.D) + "</code>",
		"remain":  "<code>" + util.TrafficConvert(sub.TransferEnable-sub.U-sub.D) + "</code>",
		"expired": "<code>" + esc(s.expireText(c, sub.ExpiredAt)) + "</code>",
		"device":  esc(device),
	})
	ids := strconv.FormatInt(sub.ID, 10)
	kb := &InlineKeyboard{Rows: [][]InlineButton{
		row(btn("🔗 "+c.T("Subscription link", nil), cbSubLink+":"+ids), btn("♻️ "+c.T("Reset link", nil), cbSubReset+":"+ids)),
	}}
	if s.Orders != nil && sub.PlanID != nil {
		kb.Rows = append(kb.Rows, row(btn("🔄 "+c.T("Renew", nil), cbPlan+":"+strconv.FormatInt(*sub.PlanID, 10)+":"+ids)))
	}
	kb.Rows = append(kb.Rows, c.back(cbSubs))
	return c.Show(ctx, text, kb)
}

// cbSubLink 发送订阅链接（新消息，便于复制）。
func (s *Service) cbSubLink(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	id, err := argID(c, 0)
	if err != nil {
		return err
	}
	v, err := s.Subscriptions.Get(ctx, u.ID, id, s.siteURL())
	if err != nil {
		return err
	}
	return c.Reply(ctx, "🔗 <b>"+esc(c.T("Subscription link", nil))+"</b>\n<code>"+esc(v.SubscribeURL)+"</code>\n\n"+
		esc(c.T("Do not share the subscription link with others.", nil)), nil)
}

// cbSubReset 重置订阅链接前确认。
func (s *Service) cbSubReset(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	id, err := argID(c, 0)
	if err != nil {
		return err
	}
	sub, err := s.Subscriptions.Find(ctx, s.DB, u.ID, id)
	if err != nil {
		return err
	}
	ids := strconv.FormatInt(sub.ID, 10)
	return c.Show(ctx, "♻️ "+esc(c.T("Reset the subscription link of :name? The old link and node connections will stop working immediately.", i18n.Params{"name": subName(sub)})),
		&InlineKeyboard{Rows: [][]InlineButton{
			row(btn("✅ "+c.T("Confirm", nil), cbSubReset2+":"+ids), btn(c.T("Cancel", nil), cbSub+":"+ids)),
		}})
}

func (s *Service) cbSubResetConfirm(ctx context.Context, c *Context) error {
	u, _ := c.User(ctx)
	id, err := argID(c, 0)
	if err != nil {
		return err
	}
	link, err := s.Subscriptions.ResetSecurity(ctx, u.ID, id, s.siteURL())
	if err != nil {
		return err
	}
	c.Answer(ctx, c.T("Reset successfully", nil), false)
	if err := c.Reply(ctx, "🔗 <b>"+esc(c.T("Subscription link", nil))+"</b>\n<code>"+esc(link)+"</code>", nil); err != nil {
		return err
	}
	return s.cbSub(ctx, c)
}
