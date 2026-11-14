package subscribe

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"

	"gorm.io/gorm"

	"gvboard/internal/model"
	"gvboard/internal/phpx"
	"gvboard/internal/subscription"
)

// ServerView 用户端节点列表的展示字段（决策 D42），不含地址、端口、TLS 等连接信息。
type ServerView struct {
	ID          any    `json:"id"`
	Type        string `json:"type"`
	Protocol    any    `json:"protocol,omitempty"`
	Name        any    `json:"name"`
	Rate        any    `json:"rate"`
	Tags        any    `json:"tags"`
	IsOnline    any    `json:"is_online"`
	LastCheckAt any    `json:"last_check_at"`
	CacheKey    string `json:"cache_key"`
}

// UserServers 订阅可用的节点列表与 ETag（原项目 User\ServerController::fetch，ETag 算法一致）。订阅不可用时为空列表。
func (s *Service) UserServers(ctx context.Context, userID, subID int64) ([]ServerView, string, error) {
	var sub model.Subscription
	err := s.DB.WithContext(ctx).Where("id = ? AND user_id = ?", subID, userID).Take(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", subscription.ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	var servers []*phpx.Arr
	ok, err := s.Available(ctx, &sub)
	if err != nil {
		return nil, "", err
	}
	if ok {
		if servers, err = s.Servers(ctx, sub.GroupID); err != nil {
			return nil, "", err
		}
	}
	views := make([]ServerView, 0, len(servers))
	keys := phpx.New()
	for _, r := range servers {
		views = append(views, ServerView{
			ID:          r.Get("id"),
			Type:        r.Str("type"),
			Protocol:    r.Get("protocol"),
			Name:        r.Get("name"),
			Rate:        r.Get("rate"),
			Tags:        r.Get("tags"),
			IsOnline:    r.Get("is_online"),
			LastCheckAt: r.Get("last_check_at"),
			CacheKey:    r.Str("cache_key"),
		})
		keys.Push(r.Get("cache_key"))
	}
	sum := sha1.Sum(phpx.JSONEncode(keys, 0))
	return views, hex.EncodeToString(sum[:]), nil
}
