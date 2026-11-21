// Package balance 用户余额变动：修改余额并在同一事务中写入余额变动日志（决策 D86）。
// 核心代码中所有修改 v2_user.balance 的位置都必须经过本包。
package balance

import (
	"errors"
	"unicode/utf8"

	"gorm.io/gorm"

	"gvboard/internal/model"
)

// 变动类型。
const (
	TypeOrderPay     = "order_pay"     // 下单余额抵扣
	TypeOrderCancel  = "order_cancel"  // 取消订单退回抵扣
	TypeOrderRestore = "order_restore" // 补单重新扣回抵扣
	TypeRenewal      = "renewal"       // 自动续费
	TypeDeposit      = "deposit"       // 充值到账
	TypePlanRefund   = "plan_refund"   // 更换套餐差额退回
	TypeGiftcard     = "giftcard"      // 礼品卡
	TypeCommission   = "commission"    // 邀请佣金直接入余额
	TypeTransfer     = "transfer"      // 佣金转余额
	TypeAdmin        = "admin"         // 管理员修改
	TypePlugin       = "plugin"        // 插件
)

// Types 全部变动类型。
var Types = []string{TypeOrderPay, TypeOrderCancel, TypeOrderRestore, TypeRenewal, TypeDeposit, TypePlanRefund,
	TypeGiftcard, TypeCommission, TypeTransfer, TypeAdmin, TypePlugin}

// RemarkMax 备注最大字符数（v2_balance_log.remark 为 varchar(255)）。
const RemarkMax = 255

// ErrInsufficient 减少余额时余额不足（或用户不存在）。
var ErrInsufficient = errors.New("余额不足")

// Change 一次余额变动。Amount 单位为分，正数增加、负数减少。
type Change struct {
	UserID     int64
	Amount     int64
	Type       string
	TradeNo    string
	OperatorID int64 // 操作管理员，0 表示无
	Plugin     string
	Remark     string
}

// Apply 在事务 tx 中修改余额并写入日志，返回变动后的余额。
// 减少余额时要求余额足够，否则返回 ErrInsufficient 且不做任何修改；Amount 为 0 时直接返回当前余额。
func Apply(tx *gorm.DB, c Change) (int64, error) {
	q := tx.Model(&model.User{}).Where("id = ?", c.UserID)
	if c.Amount == 0 {
		var cur int64
		err := q.Pluck("balance", &cur).Error
		return cur, err
	}
	if c.Amount < 0 {
		q = q.Where("balance >= ?", -c.Amount)
	}
	res := q.Update("balance", gorm.Expr("balance + ?", c.Amount))
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected != 1 {
		if c.Amount < 0 {
			return 0, ErrInsufficient
		}
		return 0, gorm.ErrRecordNotFound
	}
	var after int64
	if err := tx.Model(&model.User{}).Where("id = ?", c.UserID).Pluck("balance", &after).Error; err != nil {
		return 0, err
	}
	return after, Record(tx, c, after)
}

// Record 只写入日志（余额已由调用方修改），after 为变动后的余额。Amount 为 0 时不记录。
func Record(tx *gorm.DB, c Change, after int64) error {
	if c.Amount == 0 {
		return nil
	}
	row := model.BalanceLog{
		UserID: c.UserID, Amount: c.Amount, Balance: after, Type: c.Type,
		TradeNo: c.TradeNo, Plugin: c.Plugin, Remark: truncate(c.Remark, RemarkMax),
	}
	if c.OperatorID > 0 {
		id := c.OperatorID
		row.OperatorID = &id
	}
	return tx.Create(&row).Error
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
