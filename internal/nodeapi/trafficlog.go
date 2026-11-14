package nodeapi

import (
	"context"
	"time"

	"gvboard/internal/model"
)

// TrafficLog 用户流量明细的一行（原始流量，未乘倍率）。
type TrafficLog struct {
	U              int64   `json:"u"`
	D              int64   `json:"d"`
	RecordAt       int64   `json:"record_at"`
	UserID         int64   `json:"user_id"`
	SubscriptionID *int64  `json:"subscription_id"`
	ServerRate     float64 `json:"server_rate"`
}

// TrafficLogs 用户本月的流量明细，按日期倒序；subID 不为空时只返回该订阅的记录（决策 D37）。
func (s *Service) TrafficLogs(ctx context.Context, uid int64, subID *int64) ([]TrafficLog, error) {
	n := s.now()
	monthStart := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, n.Location()).Unix()
	q := s.DB.WithContext(ctx).Model(&model.StatUser{}).
		Select("u, d, record_at, user_id, subscription_id, server_rate").
		Where("user_id = ? AND record_at >= ?", uid, monthStart)
	if subID != nil {
		q = q.Where("subscription_id = ?", *subID)
	}
	out := []TrafficLog{}
	err := q.Order("record_at DESC").Order("id DESC").Find(&out).Error
	return out, err
}
