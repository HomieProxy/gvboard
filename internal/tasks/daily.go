package tasks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	"gvboard/internal/model"
	"gvboard/internal/telegram"
	"gvboard/internal/util"
)

// DailyReport 把 day 所在自然日的站点统计发到管理员话题群组的每日统计话题（决策 D72，每天 0:20 发前一天）。
// 统计取 0:10 生成的统计记录，记录不存在时先生成。
func (s *Service) DailyReport(ctx context.Context, day time.Time) error {
	if s.Notify == nil {
		return nil
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	from, to := start.Unix(), start.AddDate(0, 0, 1).Unix()
	db := s.DB.WithContext(ctx)
	var st model.Stat
	err := db.Where("record_at = ? AND record_type = ?", from, "d").Take(&st).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err = s.Statistics(ctx, day); err == nil {
			err = db.Where("record_at = ? AND record_type = ?", from, "d").Take(&st).Error
		}
	}
	if err != nil {
		return err
	}
	var tickets int64
	if err := db.Model(&model.Ticket{}).Where("created_at >= ? AND created_at < ?", from, to).Count(&tickets).Error; err != nil {
		return err
	}
	transfer, _ := strconv.ParseInt(st.TransferUsedTotal, 10, 64)
	params := map[string]any{
		"date":       start.Format("2006-01-02"),
		"paid_count": st.PaidCount, "paid_total": yuan(st.PaidTotal),
		"order_count": st.OrderCount, "order_total": yuan(st.OrderTotal),
		"register": st.RegisterCount, "invite": st.InviteCount,
		"commission_count": st.CommissionCount, "commission_total": yuan(st.CommissionTotal),
		"traffic": util.TrafficConvert(transfer), "tickets": tickets,
	}
	s.Notify.NotifyAdmins(ctx, telegram.TopicDaily, false, func(locale string) string {
		return s.tr(locale, "telegram.daily_report", params)
	})
	return nil
}

func yuan(cents int64) string { return fmt.Sprintf("%.2f", float64(cents)/100) }
