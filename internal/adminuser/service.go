// Package adminuser 管理端用户与订阅管理（对应原项目 Admin/UserController，决策 D30、D53）。
package adminuser

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
)

var (
	errInvalid         = httpx.Validation("Invalid parameters")
	errUserNotExist    = httpx.NotFoundErr("The user does not exist")
	errEmailFormat     = httpx.Validation("Email format is incorrect")
	errEmailUsed       = httpx.Conflict("Email is already in use")
	errPasswordMin     = httpx.Validation("Password must be greater than 8 digits")
	errPlanNotExist    = httpx.BadRequest("Subscription plan does not exist")
	errInviterNotExist = httpx.BadRequest("The inviter does not exist")
	errInviterSelf     = httpx.BadRequest("The inviter cannot be the user or one of the users invited by the user")
	errSelf            = httpx.BadRequest("You cannot ban, delete or remove the admin permission of your own account")
	errFilterRequired  = httpx.Validation("Please set at least one filter condition")
	errSubNotExist     = httpx.NotFoundErr("The subscription does not exist")
	errSubPrimary      = httpx.BadRequest("The primary subscription cannot be deleted")
	errSubHasOrder     = httpx.BadRequest("The subscription has unfinished orders")
	errMailRequired    = httpx.Validation("Subject and content cannot be empty")
	errGenerateCount   = httpx.Validation("The number of users generated at a time is 1 to 500")
)

// Mailer 投递群发邮件（mail.Dispatcher）。
type Mailer interface {
	DispatchMass(ctx context.Context, p mail.Payload) error
}

// ShareCounter 订阅防分享统计（subscribe.Service）。
type ShareCounter interface {
	ShareIPs(ctx context.Context, subID int64) (int64, error)
}

// Service 管理端用户服务。
type Service struct {
	DB            *gorm.DB
	Settings      *setting.Service
	Auth          *auth.Service
	Subscriptions *subscription.Service
	Mail          Mailer
	Guard         ShareCounter // 可为 nil
	Now           func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

const gb int64 = 1073741824

// Filter 筛选条件，Condition 为 >、<、=、>=、<=、!=、模糊（与原项目一致）。
type Filter struct {
	Key       string `json:"key"`
	Condition string `json:"condition"`
	Value     string `json:"value"`
}

var conditions = map[string]string{">": ">", "<": "<", "=": "=", ">=": ">=", "<=": "<=", "!=": "<>", "模糊": "LIKE"}

type filterKey struct {
	col     string
	sub     bool  // 订阅字段：用户任一订阅符合即命中（决策 D53）
	numeric bool  // 整数列：值必须是整数，不支持模糊
	scale   int64 // 输入值的单位换算（流量按 GB 输入）
}

// filterKeys 原项目的筛选字段，另加 is_staff、telegram_id、balance、commission_balance、
// u、subscription_id（按订阅 ID 找用户）与 subscription_banned。
var filterKeys = map[string]filterKey{
	"id":                  {col: "id", numeric: true},
	"email":               {col: "email"},
	"invite_user_id":      {col: "invite_user_id", numeric: true},
	"banned":              {col: "banned", numeric: true},
	"remarks":             {col: "remarks"},
	"is_admin":            {col: "is_admin", numeric: true},
	"is_staff":            {col: "is_staff", numeric: true},
	"telegram_id":         {col: "telegram_id", numeric: true},
	"balance":             {col: "balance", numeric: true},
	"commission_balance":  {col: "commission_balance", numeric: true},
	"transfer_enable":     {col: "transfer_enable", sub: true, numeric: true, scale: gb},
	"d":                   {col: "d", sub: true, numeric: true, scale: gb},
	"u":                   {col: "u", sub: true, numeric: true, scale: gb},
	"device_limit":        {col: "device_limit", sub: true, numeric: true},
	"expired_at":          {col: "expired_at", sub: true, numeric: true},
	"uuid":                {col: "uuid", sub: true},
	"token":               {col: "token", sub: true},
	"plan_id":             {col: "plan_id", sub: true, numeric: true},
	"subscription_id":     {col: "id", sub: true, numeric: true},
	"subscription_banned": {col: "banned", sub: true, numeric: true},
}

// applyFilters 把筛选条件加到用户查询上。字段与条件只允许白名单内的值。
func applyFilters(db, q *gorm.DB, filters []Filter) (*gorm.DB, error) {
	for _, f := range filters {
		op, ok := conditions[f.Condition]
		if !ok {
			return nil, errInvalid
		}
		if f.Key == "invite_by_email" {
			inviters := db.Model(&model.User{}).Select("id")
			inviters = emailCond(inviters, op, f.Value)
			q = q.Where("invite_user_id IN (?)", inviters)
			continue
		}
		k, ok := filterKeys[f.Key]
		if !ok {
			return nil, errInvalid
		}
		target := q
		if k.sub {
			target = db.Model(&model.Subscription{}).Select("user_id")
		}
		switch {
		case f.Key == "plan_id" && f.Value == "null":
			if op != "=" {
				return nil, errInvalid
			}
			target = target.Where("plan_id IS NULL")
		case f.Key == "email":
			target = emailCond(target, op, f.Value)
		case k.numeric:
			if op == "LIKE" {
				return nil, errInvalid
			}
			var v any
			if k.scale > 0 {
				n, err := strconv.ParseFloat(f.Value, 64)
				if err != nil {
					return nil, errInvalid
				}
				v = int64(n * float64(k.scale))
			} else {
				n, err := strconv.ParseInt(f.Value, 10, 64)
				if err != nil {
					return nil, errInvalid
				}
				v = n
			}
			target = target.Where(k.col+" "+op+" ?", v)
		case op == "LIKE":
			target = target.Where(k.col+" LIKE ?", "%"+escapeLike(f.Value)+"%")
		default:
			target = target.Where(k.col+" "+op+" ?", f.Value)
		}
		if k.sub {
			q = q.Where("id IN (?)", target)
		} else {
			q = target
		}
	}
	return q, nil
}

// emailCond 邮箱条件不区分大小写（决策 D14）。
func emailCond(q *gorm.DB, op, v string) *gorm.DB {
	if op == "LIKE" {
		return q.Where("LOWER(email) LIKE ?", "%"+escapeLike(strings.ToLower(v))+"%")
	}
	return q.Where("LOWER(email) "+op+" LOWER(?)", v)
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

var userSortable = []string{"id", "email", "balance", "commission_balance", "banned", "is_admin", "is_staff", "last_login_at", "created_at", "updated_at"}

func sortColumn(sort string) string {
	if slices.Contains(userSortable, sort) {
		return sort
	}
	return "created_at"
}

// Page 分页参数。每页最少 10 条（与原项目一致），最多 100 条。
type Page struct {
	Current  int `json:"current"`
	PageSize int `json:"page_size"`
}

func (p Page) normalize() (offset, limit int) {
	cur, size := max(p.Current, 1), p.PageSize
	size = min(max(size, 10), 100)
	return (cur - 1) * size, size
}

// PageResult 分页结果。
type PageResult[T any] struct {
	Data  []T   `json:"data"`
	Total int64 `json:"total"`
}
