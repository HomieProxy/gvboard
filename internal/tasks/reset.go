package tasks

import (
	"context"
	"fmt"
	"html"
	"slices"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/database"
	"gvboard/internal/model"
	"gvboard/internal/telegram"
)

// 流量重置方式（v2_plan.reset_traffic_method，为空时使用设置项 reset_traffic_method）。
const (
	ResetMonthFirstDay = 0 // 每月 1 日
	ResetExpireDay     = 1 // 每月到期日
	ResetNever         = 2 // 不重置
	ResetYearFirstDay  = 3 // 每年 1 月 1 日
	ResetExpireYear    = 4 // 每年到期日
)

// resetLockTTL 流量重置锁的有效期（与原项目一致为 300 秒），期间暂停写入流量。
const resetLockTTL = 300 * time.Second

// expireDayMargin 按到期日重置时，距到期不足该时长（25 天）的订阅不重置（与原项目一致）。
const expireDayMargin = 2160000

var (
	resetRetries    = 3
	resetRetryDelay = 5 * time.Second
)

// ResetTraffic 按套餐的重置方式清零已用流量（原项目 reset:traffic，每天 0:00），返回重置的订阅数。
// 只处理未到期的周期订阅（一次性套餐与已到期的订阅不重置）。修复原项目全局设置为"每年 1 月 1 日"时
// 同时执行"每年到期日"重置的问题（决策 D54）。
func (s *Service) ResetTraffic(ctx context.Context) (int64, error) {
	lockKey := s.Cache.KeyOf(cache.KeyTrafficResetLock, nil)
	if err := s.Cache.Set(ctx, lockKey, 1, resetLockTTL).Err(); err != nil {
		return 0, err
	}
	defer s.Cache.Del(context.WithoutCancel(ctx), lockKey)

	var plans []model.Plan
	if err := s.DB.WithContext(ctx).Select("id", "reset_traffic_method").Find(&plans).Error; err != nil {
		return 0, err
	}
	byMethod := map[int][]int64{}
	global := int(s.Settings.Int("reset_traffic_method"))
	for _, p := range plans {
		m := global
		if p.ResetTrafficMethod != nil {
			m = *p.ResetTrafficMethod
		}
		byMethod[m] = append(byMethod[m], p.ID)
	}
	now := s.now()
	var total int64
	for _, m := range []int{ResetMonthFirstDay, ResetExpireDay, ResetYearFirstDay, ResetExpireYear} {
		if len(byMethod[m]) == 0 {
			continue
		}
		n, err := s.resetPlans(ctx, m, byMethod[m], now)
		total += n
		if err != nil {
			s.notifyResetFailed(ctx, now, err)
			return total, err
		}
	}
	return total, nil
}

// resetPlans 重置使用指定套餐、未到期的周期订阅。
func (s *Service) resetPlans(ctx context.Context, method int, planIDs []int64, now time.Time) (int64, error) {
	active := func(tx *gorm.DB) *gorm.DB {
		return tx.Model(&model.Subscription{}).
			Where("plan_id IN ? AND expired_at IS NOT NULL AND expired_at > ?", planIDs, now.Unix())
	}
	switch method {
	case ResetMonthFirstDay, ResetYearFirstDay:
		if now.Day() != 1 || (method == ResetYearFirstDay && now.Month() != time.January) {
			return 0, nil
		}
		return s.resetWhere(ctx, func(tx *gorm.DB) *gorm.DB { return active(tx) })
	}
	var subs []model.Subscription
	if err := active(s.DB.WithContext(ctx)).Select("id", "expired_at").Find(&subs).Error; err != nil {
		return 0, err
	}
	lastDay := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()
	var ids []int64
	for _, sub := range subs {
		exp := time.Unix(*sub.ExpiredAt, 0).In(now.Location())
		var hit bool
		if method == ResetExpireDay {
			hit = (exp.Day() == now.Day() || (now.Day() == lastDay && exp.Day() >= lastDay)) &&
				now.Unix() < *sub.ExpiredAt-expireDayMargin
		} else {
			hit = exp.Month() == now.Month() && exp.Day() == now.Day()
		}
		if hit {
			ids = append(ids, sub.ID)
		}
	}
	var total int64
	for chunk := range slices.Chunk(ids, 1000) {
		n, err := s.resetWhere(ctx, func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&model.Subscription{}).Where("id IN ?", chunk)
		})
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// resetWhere 清零已用流量，遇到死锁或序列化失败时最多重试 3 次（与原项目一致）。
func (s *Service) resetWhere(ctx context.Context, scope func(tx *gorm.DB) *gorm.DB) (int64, error) {
	var err error
	for attempt := 1; ; attempt++ {
		var n int64
		err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			res := scope(tx).UpdateColumns(map[string]any{"u": 0, "d": 0})
			n = res.RowsAffected
			return res.Error
		})
		if err == nil {
			return n, nil
		}
		if attempt >= resetRetries || !database.IsRetryable(err) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(resetRetryDelay):
		}
	}
}

// notifyResetFailed 流量重置失败时通知管理员（原项目 sendMessageWithAdmin）。
func (s *Service) notifyResetFailed(ctx context.Context, now time.Time, err error) {
	s.log().Error("用户流量重置失败", zap.Error(err))
	if s.Notify == nil {
		return
	}
	s.Notify.NotifyAdmins(ctx, telegram.TopicAlert, false, func(locale string) string {
		return s.tr(locale, "telegram.reset_failed", map[string]any{
			"time": now.Format("2006/01/02 15:04:05"), "error": html.EscapeString(fmt.Sprint(err)),
		})
	})
}
