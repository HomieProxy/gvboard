// Package account 用户账号的公共操作。
package account

import (
	"fmt"

	"gorm.io/gorm"

	"gvboard/internal/model"
	"gvboard/internal/util"
)

// GB 套餐流量单位换算（套餐中以 GB 存储，用户 / 订阅中以字节存储）。
const GB int64 = 1073741824

// NewUser 新建用户参数。
type NewUser struct {
	Email        string
	Password     string // 明文，保存为 bcrypt
	PasswordHash string // 已计算的 bcrypt 哈希，不为空时忽略 Password（批量生成时并行计算）
	InviteUserID *int64
	// Plan 不为空时按套餐开通主订阅（如注册试用），ExpiredAt 为到期时间（为空表示长期有效）；
	// Plan 为空时主订阅没有套餐，ExpiredAt 为空表示已到期。
	Plan        *model.Plan
	ExpiredAt   *int64
	LastLoginAt *int64
}

// Create 在事务 tx 中创建用户及其主订阅。
// v2_user 中的订阅字段与主订阅保持一致，以满足原表的非空与唯一约束（uuid、token）。
// 未设置的列与原表默认值一致：到期时间 0、到期与流量提醒开启、自动续费关闭。
func Create(tx *gorm.DB, in NewUser) (*model.User, *model.Subscription, error) {
	hash := in.PasswordHash
	if hash == "" {
		var err error
		if hash, err = util.HashPassword(in.Password); err != nil {
			return nil, nil, err
		}
	}
	zero, on, off := int64(0), 1, 0
	u := &model.User{
		Email:         in.Email,
		Password:      hash,
		InviteUserID:  in.InviteUserID,
		UUID:          util.UUIDv4(),
		Token:         util.GUID(),
		ExpiredAt:     &zero,
		AutoRenewal:   &off,
		RemindExpire:  &on,
		RemindTraffic: &on,
		LastLoginAt:   in.LastLoginAt,
	}
	if p := in.Plan; p != nil {
		planID, groupID := p.ID, p.GroupID
		u.PlanID = &planID
		u.GroupID = &groupID
		u.TransferEnable = p.TransferEnable * GB
		u.DeviceLimit = p.DeviceLimit
		u.SpeedLimit = p.SpeedLimit
		u.ExpiredAt = in.ExpiredAt
	} else if in.ExpiredAt != nil {
		u.ExpiredAt = in.ExpiredAt
	}
	if err := tx.Create(u).Error; err != nil {
		return nil, nil, fmt.Errorf("创建用户失败: %w", err)
	}
	sub := &model.Subscription{
		UserID:         u.ID,
		IsPrimary:      1,
		PlanID:         u.PlanID,
		GroupID:        u.GroupID,
		UUID:           u.UUID,
		Token:          u.Token,
		TransferEnable: u.TransferEnable,
		DeviceLimit:    u.DeviceLimit,
		SpeedLimit:     u.SpeedLimit,
		ExpiredAt:      u.ExpiredAt,
		AutoRenewal:    off,
		RemindExpire:   on,
		RemindTraffic:  on,
	}
	if err := tx.Create(sub).Error; err != nil {
		return nil, nil, fmt.Errorf("创建主订阅失败: %w", err)
	}
	return u, sub, nil
}
