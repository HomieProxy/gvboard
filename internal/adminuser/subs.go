package adminuser

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/subscription"
	"gvboard/internal/util"
)

// SubInput 新增或修改订阅的参数（对应原项目 admin/user/update 中的订阅字段，决策 D53）。没有传的字段不修改。
// 流量单位为字节；可为空的字段传 null 表示清空：plan_id 为 null 时同时清空权限组（与原项目一致），
// expired_at 为 null 表示长期有效，device_limit、speed_limit 为 null 表示不限制。
type SubInput struct {
	Name           *string               `json:"name"`
	PlanID         httpx.Optional[int64] `json:"plan_id"`
	TransferEnable *int64                `json:"transfer_enable"`
	U              *int64                `json:"u"`
	D              *int64                `json:"d"`
	DeviceLimit    httpx.Optional[int64] `json:"device_limit"`
	SpeedLimit     httpx.Optional[int64] `json:"speed_limit"`
	ExpiredAt      httpx.Optional[int64] `json:"expired_at"`
	Banned         *int                  `json:"banned"`
	AutoRenewal    *int                  `json:"auto_renewal"`
	RemindExpire   *int                  `json:"remind_expire"`
	RemindTraffic  *int                  `json:"remind_traffic"`
}

func nonNegative(o httpx.Optional[int64]) bool { return o.Value == nil || *o.Value >= 0 }

// values 校验并转换为要更新的列。
func (in *SubInput) values(tx *gorm.DB) (map[string]any, error) {
	for _, v := range []*int{in.Banned, in.AutoRenewal, in.RemindExpire, in.RemindTraffic} {
		if !flag(v) {
			return nil, errInvalid
		}
	}
	for _, v := range []*int64{in.TransferEnable, in.U, in.D} {
		if v != nil && *v < 0 {
			return nil, errInvalid
		}
	}
	if !nonNegative(in.DeviceLimit) || !nonNegative(in.SpeedLimit) || !nonNegative(in.ExpiredAt) {
		return nil, errInvalid
	}
	values := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if utf8.RuneCountInString(name) > 64 {
			return nil, errInvalid
		}
		values["name"] = name
	}
	if in.PlanID.Set {
		values["plan_id"], values["group_id"] = nil, nil
		if in.PlanID.Value != nil {
			var p model.Plan
			err := tx.Select("id", "group_id").Take(&p, *in.PlanID.Value).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errPlanNotExist
			}
			if err != nil {
				return nil, err
			}
			values["plan_id"], values["group_id"] = p.ID, p.GroupID
		}
	}
	for col, v := range map[string]*int64{"transfer_enable": in.TransferEnable, "u": in.U, "d": in.D} {
		if v != nil {
			values[col] = *v
		}
	}
	for col, o := range map[string]httpx.Optional[int64]{"device_limit": in.DeviceLimit, "speed_limit": in.SpeedLimit, "expired_at": in.ExpiredAt} {
		if o.Set {
			values[col] = o.Value
		}
	}
	for col, v := range map[string]*int{"banned": in.Banned, "auto_renewal": in.AutoRenewal, "remind_expire": in.RemindExpire, "remind_traffic": in.RemindTraffic} {
		if v != nil {
			values[col] = *v
		}
	}
	return values, nil
}

func (s *Service) findSub(tx *gorm.DB, id int64) (*model.Subscription, error) {
	var sub model.Subscription
	err := tx.Take(&sub, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errSubNotExist
	}
	return &sub, err
}

// CreateSub 为用户新开订阅。不受订阅数量上限限制；选择套餐时按套餐设置权限组、流量、设备数与限速，
// 其余参数在此基础上覆盖。expired_at 没有传时为长期有效。
func (s *Service) CreateSub(ctx context.Context, userID int64, in SubInput) (*model.Subscription, error) {
	var out *model.Subscription
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := s.user(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), userID); err != nil {
			return err
		}
		values, err := in.values(tx)
		if err != nil {
			return err
		}
		var plan *model.Plan
		if in.PlanID.Value != nil {
			var p model.Plan
			if err := tx.Take(&p, *in.PlanID.Value).Error; err != nil {
				return err
			}
			plan = &p
		}
		name, _ := values["name"].(string)
		sub, err := s.Subscriptions.Create(tx, subscription.NewSubscription{
			UserID: userID, Name: name, Plan: plan, ExpiredAt: in.ExpiredAt.Value, IgnoreLimit: true,
		})
		if err != nil {
			return err
		}
		delete(values, "name")
		delete(values, "plan_id")
		delete(values, "group_id")
		delete(values, "expired_at")
		if len(values) > 0 {
			if err := tx.Model(sub).Updates(values).Error; err != nil {
				return err
			}
		}
		out, err = s.findSub(tx, sub.ID)
		return err
	})
	return out, err
}

// UpdateSub 修改订阅。
func (s *Service) UpdateSub(ctx context.Context, id int64, in SubInput) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sub, err := s.findSub(tx.Clauses(clause.Locking{Strength: "UPDATE"}), id)
		if err != nil {
			return err
		}
		values, err := in.values(tx)
		if err != nil || len(values) == 0 {
			return err
		}
		return tx.Model(sub).Updates(values).Error
	})
}

// DeleteSub 删除订阅。主订阅不能删除；有待支付或开通中的订单时不能删除（订单开通需要订阅）。
func (s *Service) DeleteSub(ctx context.Context, id int64) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sub, err := s.findSub(tx.Clauses(clause.Locking{Strength: "UPDATE"}), id)
		if err != nil {
			return err
		}
		if sub.IsPrimary == 1 {
			return errSubPrimary
		}
		var n int64
		err = tx.Model(&model.Order{}).Where("subscription_id = ? AND status IN ?", id,
			[]int{model.OrderStatusPending, model.OrderStatusProcessing}).Count(&n).Error
		if err != nil {
			return err
		}
		if n > 0 {
			return errSubHasOrder
		}
		return tx.Delete(sub).Error
	})
}

// ResetSub 重置订阅的 uuid 与 token（对应原项目 admin/user/resetSecret，多订阅后按订阅重置）。
func (s *Service) ResetSub(ctx context.Context, id int64) error {
	sub, err := s.findSub(s.DB.WithContext(ctx), id)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Model(sub).Updates(map[string]any{"uuid": util.UUIDv4(), "token": util.GUID()}).Error
}

// SetPrimary 把订阅设为所属用户的主订阅。
func (s *Service) SetPrimary(ctx context.Context, id int64) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sub, err := s.findSub(tx, id)
		if err != nil {
			return err
		}
		if _, err := s.user(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), sub.UserID); err != nil {
			return err
		}
		if err := tx.Model(&model.Subscription{}).Where("user_id = ? AND id <> ?", sub.UserID, id).Update("is_primary", 0).Error; err != nil {
			return err
		}
		return tx.Model(sub).Update("is_primary", 1).Error
	})
}
