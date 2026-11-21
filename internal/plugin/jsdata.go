package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/balance"
	"gvboard/internal/hook"
	"gvboard/internal/model"
)

// JS 插件业务数据写操作的单次上限。
const (
	maxBalanceDelta = 100000000               // 分
	maxTrafficDelta = int64(100) << 40        // 字节（100 TB）
	maxExtendDelta  = int64(10 * 366 * 86400) // 秒（约 10 年）
	maxOrderList    = 100
)

var errPermission = errors.New("插件没有该权限")

// jsData JS 插件可调用的业务数据接口（决策 D57）：只返回非敏感字段。
type jsData struct {
	m    *Manager
	code string
	man  *manifest
}

func (d *jsData) need(perm string) error {
	if !d.man.can(perm) {
		return fmt.Errorf("%w：%s", errPermission, perm)
	}
	return nil
}

func (d *jsData) db(ctx context.Context) *gorm.DB { return d.m.DB.WithContext(ctx) }

func userView(u *model.User) map[string]any {
	return map[string]any{
		"id": u.ID, "email": u.Email, "balance": u.Balance, "commission_balance": u.CommissionBalance,
		"discount": u.Discount, "invite_user_id": u.InviteUserID, "telegram_id": u.TelegramID,
		"banned": u.Banned == 1, "is_admin": u.IsAdmin == 1, "is_staff": u.IsStaff == 1,
		"last_login_at": u.LastLoginAt, "created_at": u.CreatedAt,
	}
}

var userCols = []string{"id", "email", "balance", "commission_balance", "discount", "invite_user_id", "telegram_id",
	"banned", "is_admin", "is_staff", "last_login_at", "created_at"}

func (d *jsData) user(ctx context.Context, q func(*gorm.DB) *gorm.DB) (map[string]any, error) {
	if err := d.need(PermUserRead); err != nil {
		return nil, err
	}
	var u model.User
	err := q(d.db(ctx).Select(userCols)).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return userView(&u), nil
}

func (d *jsData) UserByID(ctx context.Context, id int64) (map[string]any, error) {
	return d.user(ctx, func(db *gorm.DB) *gorm.DB { return db.Where("id = ?", id) })
}

func (d *jsData) UserByEmail(ctx context.Context, email string) (map[string]any, error) {
	return d.user(ctx, func(db *gorm.DB) *gorm.DB {
		return db.Where("LOWER(email) = ?", strings.ToLower(strings.TrimSpace(email)))
	})
}

// AddBalance 调整余额（分，可为负数，余额不能小于 0），返回调整后的余额。remark 写入余额变动日志。
func (d *jsData) AddBalance(ctx context.Context, id, amount int64, remark string) (int64, error) {
	if err := d.need(PermUserBalance); err != nil {
		return 0, err
	}
	return d.m.addBalance(ctx, d.code, id, amount, remark)
}

// addBalance 插件调整用户余额并记录余额变动日志（决策 D86），JS 插件与编译期插件共用。
func (m *Manager) addBalance(ctx context.Context, code string, id, amount int64, remark string) (int64, error) {
	if amount == 0 || amount > maxBalanceDelta || amount < -maxBalanceDelta {
		return 0, fmt.Errorf("调整金额必须为非零整数，且绝对值不超过 %d 分", maxBalanceDelta)
	}
	var after int64
	err := m.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		after, err = balance.Apply(tx, balance.Change{UserID: id, Amount: amount, Type: balance.TypePlugin, Plugin: code, Remark: remark})
		if errors.Is(err, balance.ErrInsufficient) || errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("用户不存在或余额不足")
		}
		return err
	})
	if err != nil {
		return 0, err
	}
	m.log().Info("插件调整用户余额", zap.String("plugin", code), zap.Int64("user_id", id), zap.Int64("amount", amount))
	return after, nil
}

func subscriptionView(s *model.Subscription) map[string]any {
	return map[string]any{
		"id": s.ID, "user_id": s.UserID, "name": s.Name, "is_primary": s.IsPrimary == 1, "plan_id": s.PlanID,
		"group_id": s.GroupID, "transfer_enable": s.TransferEnable, "u": s.U, "d": s.D,
		"device_limit": s.DeviceLimit, "speed_limit": s.SpeedLimit, "expired_at": s.ExpiredAt,
		"auto_renewal": s.AutoRenewal == 1, "banned": s.Banned == 1, "created_at": s.CreatedAt,
	}
}

var subscriptionCols = []string{"id", "user_id", "name", "is_primary", "plan_id", "group_id", "transfer_enable", "u", "d",
	"device_limit", "speed_limit", "expired_at", "auto_renewal", "banned", "created_at"}

func (d *jsData) Subscription(ctx context.Context, id int64) (map[string]any, error) {
	if err := d.need(PermSubscriptionRead); err != nil {
		return nil, err
	}
	var s model.Subscription
	err := d.db(ctx).Select(subscriptionCols).Where("id = ?", id).Take(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return subscriptionView(&s), nil
}

func (d *jsData) Subscriptions(ctx context.Context, userID int64) ([]map[string]any, error) {
	if err := d.need(PermSubscriptionRead); err != nil {
		return nil, err
	}
	var list []model.Subscription
	if err := d.db(ctx).Select(subscriptionCols).Where("user_id = ?", userID).Order("id").Find(&list).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, subscriptionView(&list[i]))
	}
	return out, nil
}

func (d *jsData) changed(ctx context.Context, id int64) {
	var uid int64
	if err := d.db(ctx).Model(&model.Subscription{}).Where("id = ?", id).Pluck("user_id", &uid).Error; err == nil {
		d.m.Emit(ctx, hook.EventSubscriptionChanged, hook.Subscription{SubscriptionID: id, UserID: uid, Action: hook.SubscriptionPlugin})
	}
}

// AddTraffic 增加订阅的总流量（字节）。
func (d *jsData) AddTraffic(ctx context.Context, id, bytes int64) error {
	if err := d.need(PermSubscriptionWrite); err != nil {
		return err
	}
	if bytes <= 0 || bytes > maxTrafficDelta {
		return fmt.Errorf("增加的流量必须为正整数，且不超过 %d 字节", maxTrafficDelta)
	}
	res := d.db(ctx).Model(&model.Subscription{}).Where("id = ?", id).
		Update("transfer_enable", gorm.Expr("transfer_enable + ?", bytes))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("订阅不存在")
	}
	d.m.log().Info("插件增加订阅流量", zap.String("plugin", d.code), zap.Int64("subscription_id", id), zap.Int64("bytes", bytes))
	d.changed(ctx, id)
	return nil
}

// Extend 延长订阅到期时间（秒）：已到期的从现在开始计算；长期有效的订阅不变，返回 false。
func (d *jsData) Extend(ctx context.Context, id, seconds int64) (bool, error) {
	if err := d.need(PermSubscriptionWrite); err != nil {
		return false, err
	}
	if seconds <= 0 || seconds > maxExtendDelta {
		return false, fmt.Errorf("延长时间必须为正整数，且不超过 %d 秒", maxExtendDelta)
	}
	var s model.Subscription
	err := d.db(ctx).Select("id", "expired_at").Where("id = ?", id).Take(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, errors.New("订阅不存在")
	}
	if err != nil {
		return false, err
	}
	if s.ExpiredAt == nil {
		return false, nil
	}
	now := d.m.now().Unix()
	res := d.db(ctx).Model(&model.Subscription{}).Where("id = ? AND expired_at IS NOT NULL", id).
		Update("expired_at", gorm.Expr("CASE WHEN expired_at < ? THEN ? ELSE expired_at + ? END", now, now+seconds, seconds))
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	d.m.log().Info("插件延长订阅", zap.String("plugin", d.code), zap.Int64("subscription_id", id), zap.Int64("seconds", seconds))
	d.changed(ctx, id)
	return true, nil
}

func orderView(o *model.Order) map[string]any {
	return map[string]any{
		"trade_no": o.TradeNo, "user_id": o.UserID, "subscription_id": o.SubscriptionID, "plan_id": o.PlanID,
		"period": o.Period, "type": o.Type, "status": o.Status, "total_amount": o.TotalAmount,
		"balance_amount": o.BalanceAmount, "discount_amount": o.DiscountAmount, "paid_at": o.PaidAt,
		"created_at": o.CreatedAt, "updated_at": o.UpdatedAt,
	}
}

var orderCols = []string{"trade_no", "user_id", "subscription_id", "plan_id", "period", "type", "status", "total_amount",
	"balance_amount", "discount_amount", "paid_at", "created_at", "updated_at"}

func (d *jsData) Order(ctx context.Context, tradeNo string) (map[string]any, error) {
	if err := d.need(PermOrderRead); err != nil {
		return nil, err
	}
	var o model.Order
	err := d.db(ctx).Select(orderCols).Where("trade_no = ?", tradeNo).Take(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return orderView(&o), nil
}

// Orders 用户最近的订单（按创建时间倒序）。
func (d *jsData) Orders(ctx context.Context, userID int64, limit int) ([]map[string]any, error) {
	if err := d.need(PermOrderRead); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxOrderList {
		limit = maxOrderList
	}
	var list []model.Order
	if err := d.db(ctx).Select(orderCols).Where("user_id = ?", userID).Order("created_at DESC").Order("id DESC").
		Limit(limit).Find(&list).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, orderView(&list[i]))
	}
	return out, nil
}
