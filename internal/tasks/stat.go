package tasks

import (
	"context"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"

	"gvboard/internal/model"
)

// Statistics 生成 day 所在自然日的站点统计（原项目 v2board:statistics，每天 0:10 统计前一天），已存在时覆盖。
func (s *Service) Statistics(ctx context.Context, day time.Time) error {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	from, to := start.Unix(), start.AddDate(0, 0, 1).Unix()
	db := s.DB.WithContext(ctx)
	st := model.Stat{RecordAt: from, RecordType: "d"}
	var transfer int64
	steps := []func() error{
		func() error {
			return db.Model(&model.Order{}).Where("created_at >= ? AND created_at < ?", from, to).Count(&st.OrderCount).Error
		},
		func() error {
			return db.Model(&model.Order{}).Where("created_at >= ? AND created_at < ?", from, to).
				Select("COALESCE(SUM(total_amount), 0)").Scan(&st.OrderTotal).Error
		},
		func() error {
			return db.Model(&model.Order{}).Where("paid_at >= ? AND paid_at < ? AND status NOT IN (0, 2)", from, to).Count(&st.PaidCount).Error
		},
		func() error {
			return db.Model(&model.Order{}).Where("paid_at >= ? AND paid_at < ? AND status NOT IN (0, 2)", from, to).
				Select("COALESCE(SUM(total_amount), 0)").Scan(&st.PaidTotal).Error
		},
		func() error {
			return db.Model(&model.CommissionLog{}).Where("created_at >= ? AND created_at < ?", from, to).Count(&st.CommissionCount).Error
		},
		func() error {
			return db.Model(&model.CommissionLog{}).Where("created_at >= ? AND created_at < ?", from, to).
				Select("COALESCE(SUM(get_amount), 0)").Scan(&st.CommissionTotal).Error
		},
		func() error {
			return db.Model(&model.User{}).Where("created_at >= ? AND created_at < ?", from, to).Count(&st.RegisterCount).Error
		},
		func() error {
			return db.Model(&model.User{}).Where("created_at >= ? AND created_at < ? AND invite_user_id IS NOT NULL", from, to).
				Count(&st.InviteCount).Error
		},
		// 节点流量统计实时累加到当天的日记录（决策 D38），按记录日期汇总。
		func() error {
			return db.Model(&model.StatServer{}).Where("record_at = ? AND record_type = ?", from, "d").
				Select("COALESCE(SUM(u + d), 0)").Scan(&transfer).Error
		},
	}
	for _, f := range steps {
		if err := f(); err != nil {
			return err
		}
	}
	st.TransferUsedTotal = strconv.FormatInt(transfer, 10)
	return db.Transaction(func(tx *gorm.DB) error {
		var old model.Stat
		err := tx.Where("record_at = ? AND record_type = ?", from, "d").Take(&old).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&st).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&old).Select("order_count", "order_total", "paid_count", "paid_total", "commission_count",
			"commission_total", "register_count", "invite_count", "transfer_used_total").Updates(&st).Error
	})
}

// cleanBatch 每批删除的行数，避免长时间锁表。
const cleanBatch = 5000

// CleanResult 各表清理的行数。
type CleanResult struct {
	StatUser   int64 `json:"stat_user"`
	StatServer int64 `json:"stat_server"`
	Log        int64 `json:"log"`
	Subscribe  int64 `json:"subscribe_log"`
	Audit      int64 `json:"audit_log"`
}

// CleanLogs 清理过期记录（原项目 reset:log）：节点 / 用户流量统计保留 2 个月，系统日志保留 1 个月；
// 另清理超过保留天数的订阅请求日志（决策 D46）与审计日志（决策 D81）。
func (s *Service) CleanLogs(ctx context.Context) (*CleanResult, error) {
	now := s.now()
	statBefore := now.AddDate(0, -2, 0).Unix()
	r := &CleanResult{}
	var err error
	if r.StatUser, err = s.deleteBefore(ctx, &model.StatUser{}, "record_at", statBefore); err != nil {
		return r, err
	}
	if r.StatServer, err = s.deleteBefore(ctx, &model.StatServer{}, "record_at", statBefore); err != nil {
		return r, err
	}
	if r.Log, err = s.deleteBefore(ctx, &model.Log{}, "created_at", now.AddDate(0, -1, 0).Unix()); err != nil {
		return r, err
	}
	if s.CleanSubscribeLogs != nil {
		if r.Subscribe, err = s.CleanSubscribeLogs(ctx); err != nil {
			return r, err
		}
	}
	if s.CleanAuditLogs != nil {
		if r.Audit, err = s.CleanAuditLogs(ctx); err != nil {
			return r, err
		}
	}
	return r, nil
}

// deleteBefore 分批删除 col < before 的记录。
func (s *Service) deleteBefore(ctx context.Context, m any, col string, before int64) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		var ids []int64
		if err := s.DB.WithContext(ctx).Model(m).Where(col+" < ?", before).Order("id").Limit(cleanBatch).Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		res := s.DB.WithContext(ctx).Where("id IN ?", ids).Delete(m)
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if len(ids) < cleanBatch {
			return total, nil
		}
	}
}
