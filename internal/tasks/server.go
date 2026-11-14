package tasks

import (
	"context"
	"html"

	"go.uber.org/zap"

	"gvboard/internal/cache"
	"gvboard/internal/node"
)

// offlineAfter 节点超过该时间（秒）未拉取用户即视为掉线（与原项目一致为 30 分钟）。
const offlineAfter = 1800

// Offline 掉线的节点。
type Offline struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
}

// CheckServers 节点掉线检查（原项目 check:server）：父节点超过 30 分钟未拉取用户时通知管理员，
// 并删除最后检查时间，每次掉线只通知一次（节点恢复后重新记录）。
func (s *Service) CheckServers(ctx context.Context) ([]Offline, error) {
	servers, err := node.All(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	now := s.now().Unix()
	out := []Offline{}
	for _, srv := range servers {
		c := srv.Common()
		if c.ParentID != nil && *c.ParentID != 0 {
			continue
		}
		key := s.Cache.KeyOf(cache.KeyServerLastCheckAt, cache.ServerKey(srv.Type, c.ID))
		var last float64
		ok, err := s.Cache.GetJSON(ctx, key, &last)
		if err != nil {
			s.log().Warn("读取节点最后检查时间失败", zap.String("type", srv.Type), zap.Int64("id", c.ID), zap.Error(err))
			continue
		}
		if !ok || last <= 0 || now-int64(last) <= offlineAfter {
			continue
		}
		o := Offline{Type: srv.Type, ID: c.ID, Name: c.Name, Host: c.Host}
		out = append(out, o)
		if s.Notify != nil {
			s.Notify.NotifyAdmins(ctx, false, func(locale string) string {
				return s.tr(locale, "telegram.server_offline", map[string]any{
					"name": html.EscapeString(o.Name), "host": html.EscapeString(o.Host),
				})
			})
		}
		if err := s.Cache.Del(ctx, key).Err(); err != nil {
			return out, err
		}
	}
	return out, nil
}
