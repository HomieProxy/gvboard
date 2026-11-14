package legacy

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gvboard/internal/auth"
	"gvboard/internal/model"
	"gvboard/internal/subscription"
	"gvboard/internal/usercenter"
)

func (m *Module) user(c *gin.Context) (*model.User, error) {
	var u model.User
	err := m.DB.WithContext(c.Request.Context()).Take(&u, uid(c)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, abort("The user does not exist")
	}
	return &u, err
}

func (m *Module) unbindTelegram(c *gin.Context) {
	m.respond(c, true, m.Users.UnbindTelegram(c.Request.Context(), uid(c)))
}

func (m *Module) resetSecurity(c *gin.Context) {
	id, err := m.primaryID(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	link, err := m.Subs.ResetSecurity(c.Request.Context(), uid(c), id, baseURL(c))
	m.respond(c, link, err)
}

// info 原项目 user/info：账户字段取自用户，套餐、流量、到期、提醒等订阅字段取自主订阅。
func (m *Module) info(c *gin.Context) {
	u, err := m.user(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	sub, err := m.primary(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	sum := md5.Sum([]byte(u.Email))
	data := gin.H{
		"email":              u.Email,
		"transfer_enable":    nil,
		"device_limit":       nil,
		"last_login_at":      u.LastLoginAt,
		"created_at":         u.CreatedAt,
		"banned":             u.Banned,
		"auto_renewal":       nil,
		"remind_expire":      nil,
		"remind_traffic":     nil,
		"expired_at":         nil,
		"balance":            u.Balance,
		"commission_balance": u.CommissionBalance,
		"plan_id":            nil,
		"discount":           u.Discount,
		"commission_rate":    u.CommissionRate,
		"telegram_id":        u.TelegramID,
		"uuid":               nil,
		"avatar_url":         "https://cravatar.cn/avatar/" + hex.EncodeToString(sum[:]) + "?s=64&d=identicon",
	}
	if sub != nil {
		data["transfer_enable"], data["device_limit"] = sub.TransferEnable, sub.DeviceLimit
		data["auto_renewal"], data["remind_expire"], data["remind_traffic"] = sub.AutoRenewal, sub.RemindExpire, sub.RemindTraffic
		data["expired_at"], data["plan_id"], data["uuid"] = sub.ExpiredAt, sub.PlanID, sub.UUID
	}
	ok(c, data)
}

func (m *Module) newPeriod(c *gin.Context) {
	id, err := m.primaryID(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	m.respond(c, true, m.Subs.NewPeriod(c.Request.Context(), uid(c), id))
}

// redeemGiftcard 时长、流量、重置流量、套餐类礼品卡作用于主订阅；没有主订阅时套餐类新开订阅。
func (m *Module) redeemGiftcard(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("giftcard", "Giftcard cannot be empty")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	id, err := m.primaryID(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	res, err := m.Users.RedeemGiftcard(c.Request.Context(), uid(c), usercenter.RedeemInput{Code: in.str("giftcard"), SubscriptionID: id})
	if err != nil {
		m.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": true, "type": res.Type, "value": res.Value})
}

func (m *Module) changePassword(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("old_password", "Old password cannot be empty")
	if v.required("new_password", "New password cannot be empty") {
		v.check("new_password", len([]rune(in.str("new_password"))) >= 8, "Password must be greater than 8 digits")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	m.respond(c, true, m.Users.ChangePassword(c.Request.Context(), uid(c), in.str("old_password"), in.str("new_password")))
}

// update 修改主订阅的自动续费与提醒设置。
func (m *Module) update(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	fields := []struct {
		name, msg string
	}{
		{"auto_renewal", "The selected auto renewal is invalid."},
		{"remind_expire", "The selected remind expire is invalid."},
		{"remind_traffic", "The selected remind traffic is invalid."},
	}
	var upd subscription.UpdateInput
	targets := []**int{&upd.AutoRenewal, &upd.RemindExpire, &upd.RemindTraffic}
	changed := false
	for i, f := range fields {
		if !v.present(f.name) {
			continue
		}
		s := in.str(f.name)
		if v.check(f.name, s == "0" || s == "1", f.msg) {
			n := int(s[0] - '0')
			*targets[i] = &n
			changed = true
		}
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	if !changed {
		ok(c, true)
		return
	}
	id, err := m.primaryID(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	m.respond(c, true, m.Subs.Update(c.Request.Context(), uid(c), id, upd))
}

// getSubscribe 原项目 user/getSubscribe，订阅信息取自主订阅。
func (m *Module) getSubscribe(c *gin.Context) {
	u, err := m.user(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	sub, err := m.primary(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	data := gin.H{
		"plan_id": nil, "token": nil, "expired_at": nil, "u": nil, "d": nil,
		"transfer_enable": nil, "device_limit": nil, "email": u.Email, "uuid": nil,
		"alive_ip": 0, "subscribe_url": nil, "reset_day": nil,
		"allow_new_period": m.Settings.Int("allow_new_period"),
	}
	if sub == nil {
		ok(c, data)
		return
	}
	ctx := c.Request.Context()
	data["plan_id"], data["token"], data["expired_at"] = sub.PlanID, sub.Token, sub.ExpiredAt
	data["u"], data["d"], data["transfer_enable"] = sub.U, sub.D, sub.TransferEnable
	data["device_limit"], data["uuid"] = sub.DeviceLimit, sub.UUID
	var plan *model.Plan
	if sub.PlanID != nil {
		var p model.Plan
		if err := m.DB.WithContext(ctx).Take(&p, *sub.PlanID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			m.fail(c, abort("Subscription plan does not exist"))
			return
		} else if err != nil {
			m.fail(c, err)
			return
		}
		plan = &p
		data["plan"] = plan
	}
	alive, err := m.Subs.AliveIP(ctx, sub.ID)
	if err != nil {
		m.fail(c, err)
		return
	}
	link, err := m.Subs.Linker.URL(ctx, sub, baseURL(c))
	if err != nil {
		m.fail(c, err)
		return
	}
	data["alive_ip"], data["subscribe_url"] = alive, link
	data["reset_day"] = subscription.ResetDay(sub, plan, m.Settings.Int("reset_traffic_method"), time.Now())
	ok(c, data)
}

// getStat [待支付订单数, 未关闭工单数, 邀请人数]。
func (m *Module) getStat(c *gin.Context) {
	st, err := m.Users.Stat(c.Request.Context(), uid(c))
	if err != nil {
		m.fail(c, err)
		return
	}
	ok(c, []int64{st.PendingOrders, st.OpenTickets, st.Invitees})
}

func (m *Module) checkLogin(c *gin.Context) {
	data := gin.H{"is_login": true}
	if auth.Current(c).IsAdmin {
		data["is_admin"] = true
	}
	ok(c, data)
}

func (m *Module) transfer(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	if v.required("transfer_amount", "The transfer amount cannot be empty") {
		n, isInt := in.int("transfer_amount")
		if v.check("transfer_amount", isInt, "The transfer amount parameter is wrong") {
			v.check("transfer_amount", n >= 1, "The transfer amount parameter is wrong")
		}
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	amount, _ := in.int("transfer_amount")
	m.respond(c, true, m.Users.TransferCommission(c.Request.Context(), uid(c), amount))
}

// activeSessions 会话 ID 到会话信息（ip、login_at、ua）的对象，没有会话时为空数组（与 PHP 空数组一致）。
func (m *Module) activeSessions(c *gin.Context) {
	sessions, err := m.Auth.Sessions(c.Request.Context(), uid(c))
	if err != nil {
		m.fail(c, err)
		return
	}
	if len(sessions) == 0 {
		ok(c, []any{})
		return
	}
	ok(c, sessions)
}

func (m *Module) removeSession(c *gin.Context) {
	m.respond(c, true, m.Auth.RemoveSession(c.Request.Context(), uid(c), params(c).str("session_id")))
}
