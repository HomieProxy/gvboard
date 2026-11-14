// Package subscription 订阅（单账号多订阅）的查询与操作。
package subscription

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/cache"
	"gvboard/internal/hook"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/util"
)

var (
	ErrNotFound          = httpx.NotFoundErr("The subscription does not exist")
	ErrLimit             = httpx.Forbidden("The number of subscriptions has reached the limit")
	errInvalidParams     = httpx.Validation("Invalid parameters")
	errSaveFailed        = httpx.BadRequest("Save failed")
	errNewPeriodDisabled = httpx.Forbidden("Renewal is not allowed")
	errTrafficRemaining  = httpx.BadRequest("You have not used up your traffic, you cannot renew your subscription")
	errNoNewPeriod       = httpx.BadRequest("You do not allow to renew the subscription")
	errInvalidPeriod     = httpx.BadRequest("Invalid reset period")
	errNotEnoughTime     = httpx.BadRequest("You do not have enough time to renew your subscription")
)

// Service 订阅服务。
type Service struct {
	DB       *gorm.DB
	Cache    *cache.Cache
	Settings *setting.Service
	Linker   *Linker
	// Hooks 插件事件（订阅变更），可为 nil。
	Hooks hook.Hooks
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// PlanBrief 订阅所属套餐的简要信息。
type PlanBrief struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// View 用户端看到的订阅。
type View struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	IsPrimary      bool       `json:"is_primary"`
	Plan           *PlanBrief `json:"plan"`
	TransferEnable int64      `json:"transfer_enable"`
	U              int64      `json:"u"`
	D              int64      `json:"d"`
	DeviceLimit    *int64     `json:"device_limit"`
	SpeedLimit     *int64     `json:"speed_limit"`
	ExpiredAt      *int64     `json:"expired_at"`
	AutoRenewal    bool       `json:"auto_renewal"`
	RemindExpire   bool       `json:"remind_expire"`
	RemindTraffic  bool       `json:"remind_traffic"`
	Banned         bool       `json:"banned"`
	ResetDay       *int       `json:"reset_day"`
	AliveIP        int64      `json:"alive_ip"`
	SubscribeURL   string     `json:"subscribe_url"`
	AllowNewPeriod bool       `json:"allow_new_period"`
	CreatedAt      int64      `json:"created_at"`
}

// List 用户的全部订阅，主订阅在前。
func (s *Service) List(ctx context.Context, userID int64, baseURL string) ([]View, error) {
	var subs []model.Subscription
	if err := s.DB.WithContext(ctx).Where("user_id = ?", userID).Order("is_primary DESC, id ASC").Find(&subs).Error; err != nil {
		return nil, err
	}
	plans, err := s.plans(ctx, subs)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(subs))
	for i := range subs {
		v, err := s.view(ctx, &subs[i], plans[deref(subs[i].PlanID)], baseURL)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get 用户的指定订阅，不属于该用户时返回 ErrNotFound。
func (s *Service) Get(ctx context.Context, userID, id int64, baseURL string) (*View, error) {
	sub, err := s.Find(ctx, s.DB, userID, id)
	if err != nil {
		return nil, err
	}
	plans, err := s.plans(ctx, []model.Subscription{*sub})
	if err != nil {
		return nil, err
	}
	v, err := s.view(ctx, sub, plans[deref(sub.PlanID)], baseURL)
	return &v, err
}

// Find 读取属于 userID 的订阅。
func (s *Service) Find(ctx context.Context, db *gorm.DB, userID, id int64) (*model.Subscription, error) {
	var sub model.Subscription
	err := db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Take(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &sub, err
}

// Primary 用户的主订阅。
func (s *Service) Primary(ctx context.Context, db *gorm.DB, userID int64) (*model.Subscription, error) {
	var sub model.Subscription
	err := db.WithContext(ctx).Where("user_id = ? AND is_primary = 1", userID).Take(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &sub, err
}

func (s *Service) plans(ctx context.Context, subs []model.Subscription) (map[int64]*model.Plan, error) {
	var ids []int64
	for _, sub := range subs {
		if sub.PlanID != nil {
			ids = append(ids, *sub.PlanID)
		}
	}
	out := map[int64]*model.Plan{}
	if len(ids) == 0 {
		return out, nil
	}
	var plans []model.Plan
	if err := s.DB.WithContext(ctx).Where("id IN ?", ids).Find(&plans).Error; err != nil {
		return nil, err
	}
	for i := range plans {
		out[plans[i].ID] = &plans[i]
	}
	return out, nil
}

func (s *Service) view(ctx context.Context, sub *model.Subscription, plan *model.Plan, baseURL string) (View, error) {
	link, err := s.Linker.URL(ctx, sub, baseURL)
	if err != nil {
		return View{}, err
	}
	alive, err := s.AliveIP(ctx, sub.ID)
	if err != nil {
		return View{}, err
	}
	v := View{
		ID: sub.ID, Name: sub.Name, IsPrimary: sub.IsPrimary == 1,
		TransferEnable: sub.TransferEnable, U: sub.U, D: sub.D,
		DeviceLimit: sub.DeviceLimit, SpeedLimit: sub.SpeedLimit, ExpiredAt: sub.ExpiredAt,
		AutoRenewal: sub.AutoRenewal == 1, RemindExpire: sub.RemindExpire == 1, RemindTraffic: sub.RemindTraffic == 1,
		Banned: sub.Banned == 1, AliveIP: alive, SubscribeURL: link,
		AllowNewPeriod: s.Settings.Bool("allow_new_period"), CreatedAt: sub.CreatedAt,
		ResetDay: ResetDay(sub, plan, s.Settings.Int("reset_traffic_method"), s.now()),
	}
	if plan != nil {
		v.Plan = &PlanBrief{ID: plan.ID, Name: plan.Name}
	}
	return v, nil
}

// AliveIP 订阅当前在线设备数（由节点上报写入缓存，单元 10）。
func (s *Service) AliveIP(ctx context.Context, subID int64) (int64, error) {
	var v struct {
		AliveIP int64 `json:"alive_ip"`
	}
	if _, err := s.Cache.GetJSON(ctx, s.Cache.KeyOf(cache.KeyAliveIP, subID), &v); err != nil {
		return 0, err
	}
	return v.AliveIP, nil
}

// AliveDevice 订阅在某个节点上的在线 IP（节点上报，单元 10）。
type AliveDevice struct {
	Node     string `json:"node"` // 节点类型 + 节点 ID，如 vmess3
	IP       string `json:"ip"`
	UpdateAt int64  `json:"update_at"`
}

// AliveDevices 订阅在各节点的在线 IP（对应原项目管理端用户列表的 ips）。
func (s *Service) AliveDevices(ctx context.Context, subID int64) ([]AliveDevice, error) {
	raw, err := s.Cache.HGetAll(ctx, s.Cache.KeyOf(cache.KeyAliveNodes, subID)).Result()
	if err != nil {
		return nil, err
	}
	out := []AliveDevice{}
	for node, v := range raw {
		var e struct {
			AliveIPs     []string `json:"aliveips"`
			LastUpdateAt int64    `json:"lastupdateAt"`
		}
		if json.Unmarshal([]byte(v), &e) != nil {
			continue
		}
		for _, ipNode := range e.AliveIPs {
			ip, _, _ := strings.Cut(ipNode, "_")
			out = append(out, AliveDevice{Node: node, IP: ip, UpdateAt: e.LastUpdateAt})
		}
	}
	slices.SortFunc(out, func(a, b AliveDevice) int { return cmp.Or(cmp.Compare(a.Node, b.Node), cmp.Compare(a.IP, b.IP)) })
	return out, nil
}

// UpdateInput 用户可修改的订阅设置，未传的项不修改。
type UpdateInput struct {
	Name          *string `json:"name"`
	AutoRenewal   *int    `json:"auto_renewal"`
	RemindExpire  *int    `json:"remind_expire"`
	RemindTraffic *int    `json:"remind_traffic"`
}

// Update 修改备注名、自动续费与提醒设置（对应原项目 user/update，多订阅后按订阅设置）。
func (s *Service) Update(ctx context.Context, userID, id int64, in UpdateInput) error {
	sub, err := s.Find(ctx, s.DB, userID, id)
	if err != nil {
		return err
	}
	values := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if utf8.RuneCountInString(name) > 64 {
			return errInvalidParams
		}
		values["name"] = name
	}
	for col, v := range map[string]*int{"auto_renewal": in.AutoRenewal, "remind_expire": in.RemindExpire, "remind_traffic": in.RemindTraffic} {
		if v == nil {
			continue
		}
		if *v != 0 && *v != 1 {
			return errInvalidParams
		}
		values[col] = *v
	}
	if len(values) == 0 {
		return nil
	}
	if err := s.DB.WithContext(ctx).Model(sub).Updates(values).Error; err != nil {
		return errSaveFailed
	}
	s.emitChanged(ctx, userID, sub.ID, hook.SubscriptionUpdated)
	return nil
}

// ResetSecurity 重置订阅的 uuid 与 token，旧订阅链接与节点认证随即失效，返回新的订阅链接。
func (s *Service) ResetSecurity(ctx context.Context, userID, id int64, baseURL string) (string, error) {
	sub, err := s.Find(ctx, s.DB, userID, id)
	if err != nil {
		return "", err
	}
	sub.UUID, sub.Token = util.UUIDv4(), util.GUID()
	if err := s.DB.WithContext(ctx).Model(sub).Updates(map[string]any{"uuid": sub.UUID, "token": sub.Token}).Error; err != nil {
		return "", err
	}
	s.emitChanged(ctx, userID, sub.ID, hook.SubscriptionResetSecurity)
	return s.Linker.URL(ctx, sub, baseURL)
}

// NewPeriod 流量用完后提前开始下一个重置周期：扣除距下次重置的天数并清零流量。
// 逻辑与原项目 user/newPeriod 完全一致。
func (s *Service) NewPeriod(ctx context.Context, userID, id int64) error {
	if !s.Settings.Bool("allow_new_period") {
		return errNewPeriodDisabled
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sub model.Subscription
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).Take(&sub).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if sub.TransferEnable > sub.U+sub.D {
			return errTrafficRemaining
		}
		var plan *model.Plan
		if sub.PlanID != nil {
			var p model.Plan
			if err := tx.Take(&p, *sub.PlanID).Error; err == nil {
				plan = &p
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		now, global := s.now(), s.Settings.Int("reset_traffic_method")
		day := ResetDay(&sub, plan, global, now)
		if day == nil {
			return errNoNewPeriod
		}
		period := ResetPeriod(&sub, plan, global, now)
		if period == nil {
			return errNoNewPeriod
		}
		resetDay, resetPeriod := *day, *period
		switch resetPeriod {
		case 1:
			resetDay, resetPeriod = 30, 30
		case 30:
		case 12:
			resetDay, resetPeriod = 365, 365
		case 365:
		default:
			return errInvalidPeriod
		}
		if resetDay <= 0 {
			resetDay = resetPeriod
		}
		if sub.ExpiredAt == nil || int64(resetPeriod+1)*86400 >= *sub.ExpiredAt-now.Unix() {
			return errNotEnoughTime
		}
		return tx.Model(&sub).Updates(map[string]any{
			"expired_at": *sub.ExpiredAt - int64(resetDay)*86400, "u": 0, "d": 0,
		}).Error
	})
	if err == nil {
		s.emitChanged(ctx, userID, id, hook.SubscriptionNewPeriod)
	}
	return err
}

// emitChanged 发出订阅变更事件。
func (s *Service) emitChanged(ctx context.Context, userID, id int64, action string) {
	hook.Emit(s.Hooks, ctx, hook.EventSubscriptionChanged, hook.Subscription{SubscriptionID: id, UserID: userID, Action: action})
}

// Limit 用户可拥有的订阅数量上限：用户单独设置优先，否则使用系统设置（决策 D7）。
func (s *Service) Limit(u *model.User) int64 {
	if u.SubscriptionLimit != nil {
		return *u.SubscriptionLimit
	}
	return s.Settings.Int("subscription_limit")
}

// NewSubscription 新开订阅参数。
type NewSubscription struct {
	UserID    int64
	Name      string
	Plan      *model.Plan
	ExpiredAt *int64 // 为空表示长期有效
	// IgnoreLimit 不检查数量上限，用于已支付订单的开通（决策 D23）。
	IgnoreLimit bool
}

// Create 在事务 tx 中新开订阅（非主订阅），超过数量上限时返回 ErrLimit。
// 会锁定用户行，保证并发新开时数量上限仍然有效。
func (s *Service) Create(tx *gorm.DB, in NewSubscription) (*model.Subscription, error) {
	if !in.IgnoreLimit {
		if err := s.CheckLimit(tx, in.UserID); err != nil {
			return nil, err
		}
	}
	sub := &model.Subscription{
		UserID: in.UserID, Name: in.Name, UUID: util.UUIDv4(), Token: util.GUID(),
		ExpiredAt: in.ExpiredAt, RemindExpire: 1, RemindTraffic: 1,
	}
	if p := in.Plan; p != nil {
		ApplyPlan(sub, p)
	}
	if err := tx.Create(sub).Error; err != nil {
		return nil, err
	}
	return sub, nil
}

// CheckLimit 在事务 tx 中锁定用户行并检查是否还能新开订阅，已达上限时返回 ErrLimit。
func (s *Service) CheckLimit(tx *gorm.DB, userID int64) error {
	var u model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "subscription_limit").Take(&u, userID).Error; err != nil {
		return err
	}
	var n int64
	if err := tx.Model(&model.Subscription{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		return err
	}
	if n >= s.Limit(&u) {
		return ErrLimit
	}
	return nil
}

// Available 订阅是否可用：未封禁、有流量、未到期或长期有效（对应原项目 UserService::isAvailable）。
func Available(sub *model.Subscription, now int64) bool {
	return sub.Banned == 0 && sub.TransferEnable != 0 && (sub.ExpiredAt == nil || *sub.ExpiredAt > now)
}

// ApplyPlan 把套餐的权限组、流量、设备数、限速应用到订阅，并清零已用流量。
func ApplyPlan(sub *model.Subscription, p *model.Plan) {
	planID, groupID := p.ID, p.GroupID
	sub.PlanID, sub.GroupID = &planID, &groupID
	sub.TransferEnable = p.TransferEnable * 1073741824
	sub.DeviceLimit, sub.SpeedLimit = p.DeviceLimit, p.SpeedLimit
	sub.U, sub.D = 0, 0
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
