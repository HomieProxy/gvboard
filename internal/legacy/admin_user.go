package legacy

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gvboard/internal/adminuser"
	"gvboard/internal/database"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/subscription"
	"gvboard/internal/util"
)

const gb = 1073741824

var userFilterKeys = []string{"id", "email", "transfer_enable", "device_limit", "d", "expired_at", "uuid", "token",
	"invite_by_email", "invite_user_id", "plan_id", "banned", "remarks", "is_admin"}

// legacyUser 原项目 v2_user 一行：账号字段取自用户，套餐、流量、到期、UUID、订阅 token 等取自主订阅（决策 D58）。
// 不返回密码相关字段。
func legacyUser(u *model.User, sub *model.Subscription) gin.H {
	h := gin.H{
		"id": u.ID, "invite_user_id": u.InviteUserID, "telegram_id": u.TelegramID, "email": u.Email,
		"balance": u.Balance, "discount": u.Discount, "commission_type": u.CommissionType, "commission_rate": u.CommissionRate,
		"commission_balance": u.CommissionBalance, "banned": u.Banned, "is_admin": u.IsAdmin, "is_staff": u.IsStaff,
		"last_login_at": u.LastLoginAt, "last_login_ip": u.LastLoginIP, "remarks": u.Remarks,
		"created_at": u.CreatedAt, "updated_at": u.UpdatedAt,
		"t": 0, "u": 0, "d": 0, "transfer_enable": 0, "device_limit": nil, "uuid": nil, "token": nil,
		"group_id": nil, "plan_id": nil, "speed_limit": nil, "expired_at": nil,
		"auto_renewal": 0, "remind_expire": 0, "remind_traffic": 0,
	}
	if sub != nil {
		h["t"], h["u"], h["d"], h["transfer_enable"] = sub.T, sub.U, sub.D, sub.TransferEnable
		h["device_limit"], h["uuid"], h["token"], h["group_id"] = sub.DeviceLimit, sub.UUID, sub.Token, sub.GroupID
		h["plan_id"], h["speed_limit"], h["expired_at"] = sub.PlanID, sub.SpeedLimit, sub.ExpiredAt
		h["auto_renewal"], h["remind_expire"], h["remind_traffic"] = sub.AutoRenewal, sub.RemindExpire, sub.RemindTraffic
	}
	return h
}

// primaryOf 用户的主订阅，没有时返回 nil。
func (m *Module) primaryOf(ctx context.Context, userID int64) (*model.Subscription, error) {
	sub, err := m.Subs.Primary(ctx, m.DB, userID)
	if errors.Is(err, subscription.ErrNotFound) {
		return nil, nil
	}
	return sub, err
}

// userFilters 原项目用户筛选参数 filter[i][key|condition|value]。validate 为 true 时按原项目 UserFetch 规则校验。
func userFilters(in input, validate bool) ([]adminuser.Filter, error) {
	v := newValidator(in)
	var out []adminuser.Filter
	list, _ := in.values("filter")
	for i, raw := range list {
		f, _ := raw.(map[string]any)
		fin := input(f)
		if validate {
			prefix := "filter." + strconv.Itoa(i) + "."
			if v.check(prefix+"key", fin.raw("key") != nil, "过滤键不能为空") {
				v.check(prefix+"key", slices.Contains(userFilterKeys, fin.str("key")), "过滤键参数有误")
			}
			if v.check(prefix+"condition", fin.raw("condition") != nil, "过滤条件不能为空") {
				v.check(prefix+"condition", slices.Contains(orderFilterConditions, fin.str("condition")), "过滤条件参数有误")
			}
			v.check(prefix+"value", fin.raw("value") != nil, "过滤值不能为空")
		}
		out = append(out, adminuser.Filter{Key: fin.str("key"), Condition: fin.str("condition"), Value: fin.str("value")})
	}
	return out, v.err()
}

// adminUserFetch 用户列表：每行为账号字段 + 主订阅字段，另附 total_used、plan_name、alive_ip、ips、subscribe_url。
func (m *Module) adminUserFetch(c *gin.Context) {
	in := params(c)
	filters, err := userFilters(in, true)
	if err != nil {
		m.fail(c, err)
		return
	}
	ctx := c.Request.Context()
	current, size := in.page()
	sort, sortType := in.sortParams()
	if !in.truthy("sort") {
		sort = "created_at"
	}
	res, err := m.AdminUsers.List(ctx, adminuser.Query{
		Page: adminuser.Page{Current: current, PageSize: size}, Sort: sort, SortType: sortType, Filter: filters,
	}, baseURL(c))
	if err != nil {
		m.fail(c, err)
		return
	}
	ids := make([]int64, len(res.Data))
	for i, row := range res.Data {
		ids[i] = row.ID
	}
	users := map[int64]*model.User{}
	if len(ids) > 0 {
		var list []model.User
		if err := m.DB.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error; err != nil {
			m.fail(c, err)
			return
		}
		for i := range list {
			users[list[i].ID] = &list[i]
		}
	}
	out := make([]gin.H, 0, len(res.Data))
	for _, row := range res.Data {
		u := users[row.ID]
		if u == nil {
			continue
		}
		var sub *model.Subscription
		if row.Primary != nil {
			sub = &row.Primary.Subscription
		}
		h := legacyUser(u, sub)
		h["total_used"], h["alive_ip"], h["ips"], h["subscribe_url"] = 0, 0, "", nil
		if p := row.Primary; p != nil {
			h["total_used"], h["alive_ip"], h["subscribe_url"] = p.U+p.D, p.AliveIP, p.SubscribeURL
			if p.PlanName != "" {
				h["plan_name"] = p.PlanName
			}
			devices, err := m.Subs.AliveDevices(ctx, p.ID)
			if err != nil {
				m.fail(c, err)
				return
			}
			ips := make([]string, len(devices))
			for i, d := range devices {
				ips[i] = d.IP + "_" + d.Node
			}
			h["ips"] = strings.Join(ips, ", ")
		}
		out = append(out, h)
	}
	paged(c, out, res.Total)
}

func (m *Module) adminUserInfo(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortRaw("参数错误"))
		return
	}
	ctx := c.Request.Context()
	id, _ := in.id()
	row, err := m.legacyUserByID(ctx, id)
	if err != nil {
		m.fail(c, err)
		return
	}
	if inviter, _ := row["invite_user_id"].(*int64); inviter != nil {
		inv, err := m.legacyUserByID(ctx, *inviter)
		var ae *abortError
		switch {
		case err == nil:
			row["invite_user"] = inv
		case !errors.As(err, &ae):
			m.fail(c, err)
			return
		}
	}
	ok(c, row)
}

func (m *Module) legacyUserByID(ctx context.Context, id int64) (gin.H, error) {
	var u model.User
	err := m.DB.WithContext(ctx).Take(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || id == 0 {
		return nil, abortRaw("用户不存在")
	} else if err != nil {
		return nil, err
	}
	sub, err := m.primaryOf(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return legacyUser(&u, sub), nil
}

// userUpdateRules 原项目 UserUpdate 中整数字段的校验文案。
var userUpdateRules = []struct{ field, msg string }{
	{"device_limit", "设备数限制格式不正确"}, {"expired_at", "到期时间格式不正确"}, {"plan_id", "订阅计划格式不正确"},
	{"u", "上行流量格式不正确"}, {"d", "下行流量格式不正确"}, {"balance", "余额格式不正确"},
	{"commission_type", "validation.integer"}, {"commission_balance", "佣金格式不正确"}, {"speed_limit", "限速格式不正确"},
}

func (m *Module) validateUserUpdate(in input) error {
	v := newValidator(in)
	if v.required("email", "邮箱不能为空") {
		v.check("email", util.EmailValid(in.str("email"), false), "邮箱格式不正确")
	}
	if v.present("password") {
		v.check("password", len([]rune(in.str("password"))) >= 8, "密码长度最小8位")
	}
	if v.present("transfer_enable") {
		_, isNum := in.float("transfer_enable")
		v.check("transfer_enable", isNum, "流量格式不正确")
	}
	for _, f := range []struct{ field, req, msg string }{
		{"banned", "是否封禁不能为空", "是否封禁格式不正确"},
		{"is_admin", "是否管理员不能为空", "是否管理员格式不正确"},
		{"is_staff", "是否员工不能为空", "是否员工格式不正确"},
	} {
		if v.required(f.field, f.req) {
			s := in.str(f.field)
			v.check(f.field, s == "0" || s == "1", f.msg)
		}
	}
	for _, r := range userUpdateRules {
		v.integer(r.field, r.msg)
	}
	for _, f := range []struct{ field, name string }{{"commission_rate", "推荐返利比例"}, {"discount", "专属折扣比例"}} {
		if !v.present(f.field) {
			continue
		}
		n, isInt := in.int(f.field)
		if v.check(f.field, isInt, f.name+"格式不正确") && v.check(f.field, n >= 0, f.name+"最小为0") {
			v.check(f.field, n <= 100, f.name+"最大为100")
		}
	}
	return v.err()
}

// optional 可为空的整数参数：存在时 Set 为 true，null 为清空。
func (in input) optional(k string) httpx.Optional[int64] {
	if !in.has(k) {
		return httpx.Optional[int64]{}
	}
	return httpx.Optional[int64]{Set: true, Value: in.optInt(k, nil)}
}

// adminUserUpdate 修改用户：账号字段改用户，订阅字段改主订阅（决策 D58）。
// 用户没有主订阅时，订阅字段与空订阅的显示值一致则忽略，否则报错。
func (m *Module) adminUserUpdate(c *gin.Context) {
	in := params(c)
	if err := m.validateUserUpdate(in); err != nil {
		m.fail(c, err)
		return
	}
	ctx := c.Request.Context()
	id, _ := in.id()
	var u model.User
	if err := m.DB.WithContext(ctx).Take(&u, id).Error; errors.Is(err, gorm.ErrRecordNotFound) || id == 0 {
		m.fail(c, abortRaw("用户不存在"))
		return
	} else if err != nil {
		m.fail(c, err)
		return
	}
	email := in.str("email")
	if !strings.EqualFold(email, u.Email) {
		var n int64
		if err := database.WhereEmail(m.DB.WithContext(ctx).Model(&model.User{}), email).Count(&n).Error; err != nil {
			m.fail(c, err)
			return
		} else if n > 0 {
			m.fail(c, abortRaw("邮箱已被使用"))
			return
		}
	}

	sub := adminuser.SubInput{
		PlanID: in.optional("plan_id"), DeviceLimit: in.optional("device_limit"),
		SpeedLimit: in.optional("speed_limit"), ExpiredAt: in.optional("expired_at"),
		U: in.optInt("u", nil), D: in.optInt("d", nil),
	}
	if in.raw("transfer_enable") != nil {
		f, _ := in.float("transfer_enable")
		n := int64(math.Round(f))
		sub.TransferEnable = &n
	}
	if sub.PlanID.Value != nil {
		var p model.Plan
		if err := m.DB.WithContext(ctx).Select("id").Take(&p, *sub.PlanID.Value).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			m.fail(c, abortRaw("订阅计划不存在"))
			return
		} else if err != nil {
			m.fail(c, err)
			return
		}
	}
	primary, err := m.primaryOf(ctx, u.ID)
	if err != nil {
		m.fail(c, err)
		return
	}
	if primary == nil && !emptySubInput(sub) {
		m.fail(c, abortRaw("该用户没有主订阅，无法修改订阅信息"))
		return
	}

	acc := adminuser.UpdateInput{Email: &email, CommissionRate: in.optional("commission_rate"), Discount: in.optional("discount")}
	if in.raw("password") != nil {
		pw := in.str("password")
		acc.Password = &pw
	}
	acc.Balance, acc.CommissionBalance = in.optInt("balance", nil), in.optInt("commission_balance", nil)
	for _, f := range []struct {
		key string
		dst **int
	}{{"banned", &acc.Banned}, {"is_admin", &acc.IsAdmin}, {"is_staff", &acc.IsStaff}, {"commission_type", &acc.CommissionType}} {
		if n, isInt := in.int(f.key); isInt {
			v := int(n)
			*f.dst = &v
		}
	}
	if in.has("remarks") {
		acc.Remarks = httpx.Optional[string]{Set: true, Value: in.optStr("remarks", nil)}
	}
	if err := m.AdminUsers.Update(ctx, uid(c), u.ID, acc); err != nil {
		m.fail(c, err)
		return
	}
	if primary != nil {
		if err := m.AdminUsers.UpdateSub(ctx, primary.ID, sub); err != nil {
			m.fail(c, err)
			return
		}
	}
	if in.has("invite_user_email") {
		if err := m.setInviter(ctx, u.ID, in.str("invite_user_email")); err != nil {
			m.fail(c, err)
			return
		}
	}
	ok(c, true)
}

// emptySubInput 订阅字段是否与没有主订阅时的显示值一致（流量 0、其余为空）。
func emptySubInput(in adminuser.SubInput) bool {
	for _, v := range []*int64{in.TransferEnable, in.U, in.D} {
		if v != nil && *v != 0 {
			return false
		}
	}
	for _, o := range []httpx.Optional[int64]{in.PlanID, in.DeviceLimit, in.SpeedLimit, in.ExpiredAt} {
		if o.Value != nil {
			return false
		}
	}
	return true
}

// setInviter 原项目：邀请人邮箱为空时清除邀请人；邮箱不存在时不修改。
func (m *Module) setInviter(ctx context.Context, userID int64, email string) error {
	if email != "" {
		var n int64
		if err := database.WhereEmail(m.DB.WithContext(ctx).Model(&model.User{}), email).Count(&n).Error; err != nil {
			return err
		} else if n == 0 {
			return nil
		}
	}
	return m.AdminUsers.SetInviter(ctx, userID, email)
}

func (m *Module) adminUserGenerate(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.generateCount()
	v.integer("expired_at", "validation.integer")
	v.integer("plan_id", "validation.integer")
	v.required("email_suffix", "validation.required")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	g := adminuser.GenerateInput{
		EmailPrefix: in.str("email_prefix"), EmailSuffix: in.str("email_suffix"), Password: in.str("password"),
		PlanID: in.intOr("plan_id", 0), ExpiredAt: in.optInt("expired_at", nil),
	}
	if !in.truthy("email_prefix") {
		if !in.truthy("generate_count") {
			c.Status(http.StatusOK)
			return
		}
		g.GenerateCount = int(in.intOr("generate_count", 0))
	}
	data, err := m.AdminUsers.Generate(c.Request.Context(), g, baseURL(c))
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) {
			switch he.Msg {
			case "Subscription plan does not exist":
				err = abortRaw("订阅计划不存在")
			case "Email is already in use":
				err = abortRaw("邮箱已存在于系统中")
			}
		}
		m.fail(c, err)
		return
	}
	if g.GenerateCount == 0 {
		ok(c, true)
		return
	}
	csvOut(c, data)
}

// adminUserDumpCSV 导出用户（原项目列），订阅字段取自主订阅。
func (m *Module) adminUserDumpCSV(c *gin.Context) {
	filters, _ := userFilters(params(c), false)
	ctx := c.Request.Context()
	q, err := m.AdminUsers.Filtered(ctx, filters)
	if err != nil {
		m.fail(c, err)
		return
	}
	names := map[int64]string{}
	var plans []model.Plan
	if err := m.DB.WithContext(ctx).Select("id", "name").Find(&plans).Error; err != nil {
		m.fail(c, err)
		return
	}
	for _, p := range plans {
		names[p.ID] = p.Name
	}
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF邮箱,余额,推广佣金,总流量,设备数限制,剩余流量,套餐到期时间,订阅计划,订阅地址\r\n")
	lastID := int64(0)
	for {
		var users []model.User
		err := q.Session(&gorm.Session{}).Select("id", "email", "balance", "commission_balance").
			Where("id > ?", lastID).Order("id").Limit(500).Find(&users).Error
		if err != nil {
			m.fail(c, err)
			return
		}
		ids := make([]int64, len(users))
		for i := range users {
			ids[i] = users[i].ID
		}
		subs := map[int64]*model.Subscription{}
		if len(ids) > 0 {
			var list []model.Subscription
			if err := m.DB.WithContext(ctx).Where("user_id IN ? AND is_primary = 1", ids).Find(&list).Error; err != nil {
				m.fail(c, err)
				return
			}
			for i := range list {
				subs[list[i].UserID] = &list[i]
			}
		}
		for _, u := range users {
			cells := []string{u.Email, yuan(u.Balance), yuan(u.CommissionBalance), "0", "", "0", "长期有效", "无订阅", ""}
			if sub := subs[u.ID]; sub != nil {
				cells[3], cells[5] = gbText(sub.TransferEnable), gbText(sub.TransferEnable-sub.U-sub.D)
				if sub.DeviceLimit != nil {
					cells[4] = strconv.FormatInt(*sub.DeviceLimit, 10)
				}
				if sub.ExpiredAt != nil {
					cells[6] = time.Unix(*sub.ExpiredAt, 0).Format("2006-01-02 15:04:05")
				}
				if sub.PlanID != nil {
					if name, found := names[*sub.PlanID]; found {
						cells[7] = name
					}
				}
				link, err := m.Subs.Linker.URL(ctx, sub, baseURL(c))
				if err != nil {
					m.fail(c, err)
					return
				}
				cells[8] = link
			}
			for i := range cells {
				if s := cells[i]; s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
					cells[i] = "'" + s
				}
			}
			buf.WriteString(strings.Join(cells, ",") + "\r\n")
		}
		if len(users) < 500 {
			break
		}
		lastID = users[len(users)-1].ID
	}
	csvOut(c, buf.Bytes())
}

func yuan(cents int64) string { return strconv.FormatFloat(float64(cents)/100, 'f', -1, 64) }

func gbText(b int64) string { return strconv.FormatFloat(float64(b)/gb, 'f', -1, 64) }

func (m *Module) adminUserSendMail(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("subject", "主题不能为空")
	v.required("content", "发送内容不能为空")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	filters, _ := userFilters(in, false)
	_, err := m.AdminUsers.SendMail(c.Request.Context(), adminuser.MailInput{
		Filter: filters, Subject: in.str("subject"), Content: in.str("content"),
	})
	m.respond(c, true, err)
}

func (m *Module) adminUserBan(c *gin.Context) {
	filters, _ := userFilters(params(c), false)
	_, err := m.AdminUsers.Ban(c.Request.Context(), uid(c), filters)
	m.respond(c, true, err)
}

func (m *Module) adminUserAllDel(c *gin.Context) {
	filters, _ := userFilters(params(c), false)
	_, err := m.AdminUsers.BatchDelete(c.Request.Context(), uid(c), filters)
	m.respond(c, true, err)
}

func (m *Module) adminUserDel(c *gin.Context) {
	id, _ := params(c).id()
	err := m.AdminUsers.Delete(c.Request.Context(), uid(c), id)
	if isNotFound(err) {
		err = abortRaw("用户不存在")
	}
	m.respond(c, true, err)
}

// adminUserResetSecret 重置主订阅的 UUID 与订阅 token。
func (m *Module) adminUserResetSecret(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := params(c).id()
	if found, err := m.exists(c, &model.User{}, id); err != nil || !found {
		if err == nil {
			err = abortRaw("用户不存在")
		}
		m.fail(c, err)
		return
	}
	sub, err := m.primaryOf(ctx, id)
	if err != nil {
		m.fail(c, err)
		return
	}
	if sub == nil {
		m.fail(c, abortRaw("该用户没有主订阅"))
		return
	}
	m.respond(c, true, m.AdminUsers.ResetSub(ctx, sub.ID))
}
