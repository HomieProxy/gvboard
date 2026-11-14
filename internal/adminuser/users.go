package adminuser

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/database"
	"gvboard/internal/httpx"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/subscription"
	"gvboard/internal/util"
)

// User 管理端看到的账号字段（订阅数据见 Sub）。
type User struct {
	ID                int64   `json:"id"`
	Email             string  `json:"email"`
	Balance           int64   `json:"balance"`
	CommissionBalance int64   `json:"commission_balance"`
	CommissionType    int     `json:"commission_type"`
	CommissionRate    *int64  `json:"commission_rate"`
	Discount          *int64  `json:"discount"`
	Banned            int     `json:"banned"`
	IsAdmin           int     `json:"is_admin"`
	IsStaff           int     `json:"is_staff"`
	InviteUserID      *int64  `json:"invite_user_id"`
	TelegramID        *int64  `json:"telegram_id"`
	Remarks           *string `json:"remarks"`
	SubscriptionLimit *int64  `json:"subscription_limit"`
	LastLoginAt       *int64  `json:"last_login_at"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
}

func userOf(u *model.User) User {
	return User{
		ID: u.ID, Email: u.Email, Balance: u.Balance, CommissionBalance: u.CommissionBalance,
		CommissionType: u.CommissionType, CommissionRate: u.CommissionRate, Discount: u.Discount,
		Banned: u.Banned, IsAdmin: u.IsAdmin, IsStaff: u.IsStaff, InviteUserID: u.InviteUserID,
		TelegramID: u.TelegramID, Remarks: u.Remarks, SubscriptionLimit: u.SubscriptionLimit,
		LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
	}
}

// Sub 管理端看到的订阅。
type Sub struct {
	model.Subscription
	PlanName     string `json:"plan_name,omitempty"`
	AliveIP      int64  `json:"alive_ip"`
	SubscribeURL string `json:"subscribe_url"`
}

// ListRow 用户列表的一行：账号字段、订阅数、主订阅摘要、全部订阅的在线设备数之和。
type ListRow struct {
	User
	SubscriptionCount int   `json:"subscription_count"`
	AliveIP           int64 `json:"alive_ip"`
	Primary           *Sub  `json:"primary"`
}

// Query 用户列表查询参数（对应原项目 admin/user/fetch）。
type Query struct {
	Page
	Sort     string   `json:"sort"`
	SortType string   `json:"sort_type"`
	Filter   []Filter `json:"filter"`
}

func (s *Service) filtered(ctx context.Context, filters []Filter) (*gorm.DB, error) {
	db := s.DB.WithContext(ctx)
	return applyFilters(db, db.Model(&model.User{}), filters)
}

// Filtered 符合筛选条件的用户查询（字段与条件只允许白名单内的值）。
func (s *Service) Filtered(ctx context.Context, filters []Filter) (*gorm.DB, error) {
	return s.filtered(ctx, filters)
}

// List 用户列表。
func (s *Service) List(ctx context.Context, in Query, baseURL string) (*PageResult[ListRow], error) {
	q, err := s.filtered(ctx, in.Filter)
	if err != nil {
		return nil, err
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	offset, limit := in.Page.normalize()
	var users []model.User
	desc := !strings.EqualFold(in.SortType, "ASC")
	err = q.Order(clause.OrderByColumn{Column: clause.Column{Name: sortColumn(in.Sort)}, Desc: desc}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: desc}).
		Offset(offset).Limit(limit).Find(&users).Error
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(users))
	for i := range users {
		ids[i] = users[i].ID
	}
	subs, err := s.subsOf(ctx, ids, baseURL)
	if err != nil {
		return nil, err
	}
	out := make([]ListRow, 0, len(users))
	for i := range users {
		row := ListRow{User: userOf(&users[i])}
		for j := range subs[users[i].ID] {
			sub := &subs[users[i].ID][j]
			row.SubscriptionCount++
			row.AliveIP += sub.AliveIP
			if sub.IsPrimary == 1 {
				row.Primary = sub
			}
		}
		out = append(out, row)
	}
	return &PageResult[ListRow]{Data: out, Total: total}, nil
}

// subsOf 一批用户的订阅（主订阅在前），附套餐名、在线设备数与订阅链接。
func (s *Service) subsOf(ctx context.Context, userIDs []int64, baseURL string) (map[int64][]Sub, error) {
	out := map[int64][]Sub{}
	if len(userIDs) == 0 {
		return out, nil
	}
	db := s.DB.WithContext(ctx)
	var subs []model.Subscription
	if err := db.Where("user_id IN ?", userIDs).Order("is_primary DESC").Order("id").Find(&subs).Error; err != nil {
		return nil, err
	}
	names, err := planNames(db)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		v, err := s.sub(ctx, &subs[i], names, baseURL)
		if err != nil {
			return nil, err
		}
		out[subs[i].UserID] = append(out[subs[i].UserID], *v)
	}
	return out, nil
}

func (s *Service) sub(ctx context.Context, sub *model.Subscription, names map[int64]string, baseURL string) (*Sub, error) {
	v := &Sub{Subscription: *sub}
	if sub.PlanID != nil {
		v.PlanName = names[*sub.PlanID]
	}
	var err error
	if v.AliveIP, err = s.Subscriptions.AliveIP(ctx, sub.ID); err != nil {
		return nil, err
	}
	if v.SubscribeURL, err = s.Subscriptions.Linker.URL(ctx, sub, baseURL); err != nil {
		return nil, err
	}
	return v, nil
}

func planNames(db *gorm.DB) (map[int64]string, error) {
	var plans []model.Plan
	if err := db.Select("id", "name").Find(&plans).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(plans))
	for _, p := range plans {
		out[p.ID] = p.Name
	}
	return out, nil
}

// Inviter 邀请人简要信息。
type Inviter struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

// DetailSub 用户详情中的订阅，另附各节点在线 IP 与防分享窗口内拉取订阅的不同 IP 数（决策 D46）。
type DetailSub struct {
	Sub
	Devices  []subscription.AliveDevice `json:"devices"`
	ShareIPs int64                      `json:"share_ips"`
}

// Detail 用户详情（对应原项目 admin/user/getUserInfoById），附邀请人与全部订阅。
type Detail struct {
	User
	InviteUser    *Inviter    `json:"invite_user"`
	Subscriptions []DetailSub `json:"subscriptions"`
}

func (s *Service) user(ctx context.Context, db *gorm.DB, id int64) (*model.User, error) {
	var u model.User
	err := db.WithContext(ctx).Take(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errUserNotExist
	}
	return &u, err
}

// Get 用户详情。
func (s *Service) Get(ctx context.Context, id int64, baseURL string) (*Detail, error) {
	u, err := s.user(ctx, s.DB, id)
	if err != nil {
		return nil, err
	}
	d := &Detail{User: userOf(u), Subscriptions: []DetailSub{}}
	if u.InviteUserID != nil {
		var inv model.User
		err := s.DB.WithContext(ctx).Select("id", "email").Take(&inv, *u.InviteUserID).Error
		if err == nil {
			d.InviteUser = &Inviter{ID: inv.ID, Email: inv.Email}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	subs, err := s.subsOf(ctx, []int64{id}, baseURL)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs[id] {
		devices, err := s.Subscriptions.AliveDevices(ctx, sub.ID)
		if err != nil {
			return nil, err
		}
		ds := DetailSub{Sub: sub, Devices: devices}
		if s.Guard != nil {
			if ds.ShareIPs, err = s.Guard.ShareIPs(ctx, sub.ID); err != nil {
				return nil, err
			}
		}
		d.Subscriptions = append(d.Subscriptions, ds)
	}
	return d, nil
}

// UpdateInput 修改账号字段（对应原项目 admin/user/update 中的账号部分），没有传的字段不修改。
// 可为空的字段传 null 表示清空；telegram_id 只能清空（解除绑定）。
type UpdateInput struct {
	Email             *string                `json:"email"`
	Password          *string                `json:"password"`
	Balance           *int64                 `json:"balance"`
	CommissionBalance *int64                 `json:"commission_balance"`
	CommissionType    *int                   `json:"commission_type"`
	CommissionRate    httpx.Optional[int64]  `json:"commission_rate"`
	Discount          httpx.Optional[int64]  `json:"discount"`
	Banned            *int                   `json:"banned"`
	IsAdmin           *int                   `json:"is_admin"`
	IsStaff           *int                   `json:"is_staff"`
	Remarks           httpx.Optional[string] `json:"remarks"`
	SubscriptionLimit httpx.Optional[int64]  `json:"subscription_limit"`
	TelegramID        httpx.Optional[int64]  `json:"telegram_id"`
}

func flag(v *int) bool { return v == nil || *v == 0 || *v == 1 }

func percent(o httpx.Optional[int64]) bool {
	return o.Value == nil || (*o.Value >= 0 && *o.Value <= 100)
}

// Update 修改用户。actorID 为当前管理员，不能封禁自己或取消自己的管理员权限。
// 封禁时立即删除全部登录会话（与原项目一致）。
func (s *Service) Update(ctx context.Context, actorID, id int64, in UpdateInput) error {
	if !flag(in.Banned) || !flag(in.IsAdmin) || !flag(in.IsStaff) || !percent(in.CommissionRate) || !percent(in.Discount) {
		return errInvalid
	}
	if in.CommissionType != nil && (*in.CommissionType < 0 || *in.CommissionType > 2) {
		return errInvalid
	}
	if in.SubscriptionLimit.Value != nil && (*in.SubscriptionLimit.Value < 0 || *in.SubscriptionLimit.Value > 1000) {
		return errInvalid
	}
	if in.TelegramID.Value != nil {
		return errInvalid
	}
	if in.Remarks.Value != nil && utf8.RuneCountInString(*in.Remarks.Value) > 65535/4 {
		return errInvalid
	}
	if id == actorID && ((in.Banned != nil && *in.Banned == 1) || (in.IsAdmin != nil && *in.IsAdmin == 0)) {
		return errSelf
	}
	values := map[string]any{}
	if in.Email != nil {
		email := strings.TrimSpace(*in.Email)
		if !util.EmailValid(email, false) {
			return errEmailFormat
		}
		values["email"] = email
	}
	if in.Password != nil && *in.Password != "" {
		if utf8.RuneCountInString(*in.Password) < 8 {
			return errPasswordMin
		}
		hash, err := util.HashPassword(*in.Password)
		if err != nil {
			return err
		}
		values["password"], values["password_algo"], values["password_salt"] = hash, nil, nil
	}
	for col, v := range map[string]*int64{"balance": in.Balance, "commission_balance": in.CommissionBalance} {
		if v != nil {
			values[col] = *v
		}
	}
	for col, v := range map[string]*int{"commission_type": in.CommissionType, "banned": in.Banned, "is_admin": in.IsAdmin, "is_staff": in.IsStaff} {
		if v != nil {
			values[col] = *v
		}
	}
	for col, o := range map[string]httpx.Optional[int64]{"commission_rate": in.CommissionRate, "discount": in.Discount,
		"subscription_limit": in.SubscriptionLimit, "telegram_id": in.TelegramID} {
		if o.Set {
			values[col] = o.Value
		}
	}
	if in.Remarks.Set {
		values["remarks"] = in.Remarks.Value
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		u, err := s.user(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), id)
		if err != nil {
			return err
		}
		if email, ok := values["email"].(string); ok && !strings.EqualFold(email, u.Email) {
			var n int64
			if err := database.WhereEmail(tx.Model(&model.User{}), email).Where("id <> ?", id).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return errEmailUsed
			}
		}
		if len(values) == 0 {
			return nil
		}
		return tx.Model(u).Updates(values).Error
	})
	if err != nil {
		return err
	}
	if in.Banned != nil && *in.Banned == 1 {
		return s.Auth.RemoveAll(ctx, id)
	}
	return nil
}

// SetInviter 设置邀请人（对应原项目 admin/user/setInviteUser，原项目只有路由没有实现）。
// email 为空时清除邀请人。邀请人不能是用户自己，也不能是用户邀请链下游的用户（避免循环，三级分销会重复发放）。
func (s *Service) SetInviter(ctx context.Context, id int64, email string) error {
	email = strings.TrimSpace(email)
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		u, err := s.user(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), id)
		if err != nil {
			return err
		}
		if email == "" {
			return tx.Model(u).Update("invite_user_id", nil).Error
		}
		var inv model.User
		err = database.WhereEmail(tx, email).Select("id", "invite_user_id").Take(&inv).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errInviterNotExist
		}
		if err != nil {
			return err
		}
		// 沿邀请人向上查找，出现被设置的用户即为循环。
		cur, seen := &inv, map[int64]bool{}
		for {
			if cur.ID == id {
				return errInviterSelf
			}
			if cur.InviteUserID == nil || seen[cur.ID] {
				break
			}
			seen[cur.ID] = true
			var next model.User
			err := tx.Select("id", "invite_user_id").Take(&next, *cur.InviteUserID).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				break
			}
			if err != nil {
				return err
			}
			cur = &next
		}
		return tx.Model(u).Update("invite_user_id", inv.ID).Error
	})
}

// deleteUsers 在事务中删除用户及其订阅、订单、邀请码、工单、Telegram 偏好，并清空被邀请人的邀请人
// （与原项目 delUser 一致，另删除订阅与 Telegram 偏好）。佣金记录、流量统计保留。
func deleteUsers(tx *gorm.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tickets := tx.Model(&model.Ticket{}).Select("id").Where("user_id IN ?", ids)
	steps := []func() error{
		func() error { return tx.Where("ticket_id IN (?)", tickets).Delete(&model.TicketMessage{}).Error },
		func() error { return tx.Where("user_id IN ?", ids).Delete(&model.Ticket{}).Error },
		func() error { return tx.Where("user_id IN ?", ids).Delete(&model.Order{}).Error },
		func() error { return tx.Where("user_id IN ?", ids).Delete(&model.InviteCode{}).Error },
		func() error {
			return tx.Model(&model.User{}).Where("invite_user_id IN ?", ids).Update("invite_user_id", nil).Error
		},
		func() error { return tx.Where("user_id IN ?", ids).Delete(&model.Subscription{}).Error },
		func() error { return tx.Where("user_id IN ?", ids).Delete(&model.UserTelegram{}).Error },
		func() error { return tx.Where("id IN ?", ids).Delete(&model.User{}).Error },
	}
	for _, f := range steps {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

// Delete 删除单个用户（对应原项目 admin/user/delUser），不能删除自己。
func (s *Service) Delete(ctx context.Context, actorID, id int64) error {
	if id == actorID {
		return errSelf
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := s.user(ctx, tx, id); err != nil {
			return err
		}
		return deleteUsers(tx, []int64{id})
	})
	if err != nil {
		return err
	}
	return s.Auth.RemoveAll(ctx, id)
}

const batchSize = 500

// matchedIDs 符合筛选条件的用户 ID（不含当前管理员）。批量封禁、删除必须至少有一个筛选条件。
func (s *Service) matchedIDs(ctx context.Context, actorID int64, filters []Filter) ([]int64, error) {
	if len(filters) == 0 {
		return nil, errFilterRequired
	}
	q, err := s.filtered(ctx, filters)
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := q.Where("id <> ?", actorID).Order("id").Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// Ban 批量封禁（对应原项目 admin/user/ban），返回封禁人数。
func (s *Service) Ban(ctx context.Context, actorID int64, filters []Filter) (int, error) {
	ids, err := s.matchedIDs(ctx, actorID, filters)
	if err != nil {
		return 0, err
	}
	for chunk := range slices.Chunk(ids, batchSize) {
		if err := s.DB.WithContext(ctx).Model(&model.User{}).Where("id IN ?", chunk).Update("banned", 1).Error; err != nil {
			return 0, err
		}
	}
	for _, id := range ids {
		if err := s.Auth.RemoveAll(ctx, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// BatchDelete 批量删除（对应原项目 admin/user/allDel），在一个事务中完成，返回删除人数。
func (s *Service) BatchDelete(ctx context.Context, actorID int64, filters []Filter) (int, error) {
	ids, err := s.matchedIDs(ctx, actorID, filters)
	if err != nil {
		return 0, err
	}
	return s.DeleteByIDs(ctx, ids)
}

// EmptyUserIDs 没有任何数据的用户（对应原项目 clear:user）：从未登录，所有订阅都没有套餐、流量为 0、到期时间为 0。
// 另外排除管理员、客服、有余额或佣金、有订单的用户，避免误删。
func (s *Service) EmptyUserIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := s.DB.WithContext(ctx).Model(&model.User{}).
		Where("last_login_at IS NULL AND is_admin = 0 AND is_staff = 0 AND balance = 0 AND commission_balance = 0").
		Where("NOT EXISTS (SELECT 1 FROM v2_subscription s WHERE s.user_id = v2_user.id"+
			" AND (s.plan_id IS NOT NULL OR s.transfer_enable <> 0 OR s.expired_at IS NULL OR s.expired_at <> 0))").
		Where("NOT EXISTS (SELECT 1 FROM v2_order o WHERE o.user_id = v2_user.id)").
		Order("id").Pluck("id", &ids).Error
	return ids, err
}

// DeleteByIDs 删除指定用户及其订阅等数据并注销会话，返回删除人数。
func (s *Service) DeleteByIDs(ctx context.Context, ids []int64) (int, error) {
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for chunk := range slices.Chunk(ids, batchSize) {
			if err := deleteUsers(tx, chunk); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := s.Auth.RemoveAll(ctx, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// MailInput 群发邮件参数（对应原项目 admin/user/sendMail）。没有筛选条件时发给全部用户。
type MailInput struct {
	Filter  []Filter `json:"filter"`
	Subject string   `json:"subject"`
	Content string   `json:"content"`
}

// SendMail 按筛选条件投递群发邮件（notify 模板，内容按管理员填写的 HTML 输出），返回投递数。
func (s *Service) SendMail(ctx context.Context, in MailInput) (int, error) {
	if strings.TrimSpace(in.Subject) == "" || strings.TrimSpace(in.Content) == "" {
		return 0, errMailRequired
	}
	q, err := s.filtered(ctx, in.Filter)
	if err != nil {
		return 0, err
	}
	n, lastID := 0, int64(0)
	for {
		var users []model.User
		if err := q.Session(&gorm.Session{}).Select("id", "email").Where("id > ?", lastID).Order("id").Limit(batchSize).Find(&users).Error; err != nil {
			return n, err
		}
		for _, u := range users {
			err := s.Mail.DispatchMass(ctx, mail.Payload{
				Email: u.Email, Subject: in.Subject, TemplateName: "notify", TrustedHTML: true,
				TemplateValue: map[string]any{
					"name": s.Settings.String("app_name"), "url": s.Settings.String("app_url"), "content": in.Content,
				},
			})
			if err != nil {
				return n, err
			}
			n++
		}
		if len(users) < batchSize {
			return n, nil
		}
		lastID = users[len(users)-1].ID
	}
}
