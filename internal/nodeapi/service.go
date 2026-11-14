// Package nodeapi 节点通信接口：UniProxy、旧版 Deepbwork / Tidalab、v2 config，
// 以及流量累计、在线 IP、设备数限制与流量统计，对应原项目 Server/* 控制器与流量相关任务。
// 节点看到的"用户"即订阅：id 为订阅 ID，uuid 为订阅 UUID。
package nodeapi

import (
	"context"
	"crypto/subtle"
	"strconv"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/node"
	"gvboard/internal/queue"
	"gvboard/internal/setting"
)

// statusTTL 节点检查 / 推送时间与在线人数的缓存时长（与原项目一致）。
const statusTTL = time.Hour

// Service 节点通信业务。
type Service struct {
	DB       *gorm.DB
	Cache    *cache.Cache
	Settings *setting.Service
	// Queue 为空时流量统计同步写入（仅测试使用）。
	Queue *queue.Queue
	// Hooks 插件事件（流量上报），可为 nil。
	Hooks hook.Hooks
	Log   *zap.Logger
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *zap.Logger {
	if s.Log == nil {
		return zap.NewNop()
	}
	return s.Log
}

// TokenOK 校验节点通信密钥。未配置密钥时任何请求都不通过。
func (s *Service) TokenOK(token string) bool {
	want := s.Settings.String("server_token")
	return token != "" && want != "" && subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1
}

// NodeType 把节点后端上报的类型转换为内部类型（v2ray → vmess，hysteria2 → hysteria）。
func NodeType(t string) string {
	switch t {
	case "v2ray":
		return model.ServerTypeVmess
	case "hysteria2":
		return model.ServerTypeHysteria
	}
	return t
}

// FindNode 按类型与 ID 读取节点，类型无效、ID 非法或不存在时返回 nil。
func (s *Service) FindNode(ctx context.Context, typ, id string) (node.Server, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return nil, nil
	}
	return node.Find(ctx, s.DB, typ, n)
}

func (s *Service) setStatus(ctx context.Context, name cache.KeyName, typ string, id, v int64) error {
	return s.Cache.SetJSON(ctx, s.Cache.KeyOf(name, cache.ServerKey(typ, id)), v, statusTTL)
}

// MarkCheck 记录节点最后一次拉取用户的时间。
func (s *Service) MarkCheck(ctx context.Context, typ string, id int64) error {
	return s.setStatus(ctx, cache.KeyServerLastCheckAt, typ, id, s.now().Unix())
}

// MarkPush 记录节点在线人数与最后一次上报流量的时间。
func (s *Service) MarkPush(ctx context.Context, typ string, id int64, online int) error {
	if err := s.setStatus(ctx, cache.KeyServerOnlineUser, typ, id, int64(online)); err != nil {
		return err
	}
	return s.setStatus(ctx, cache.KeyServerLastPushAt, typ, id, s.now().Unix())
}

// User 下发给节点的用户（订阅）。限速、设备数为空时不输出该字段（与原项目一致）。
type User struct {
	ID          int64  `json:"id" msgpack:"id"`
	UUID        string `json:"uuid" msgpack:"uuid"`
	SpeedLimit  *int64 `json:"speed_limit,omitempty" msgpack:"speed_limit,omitempty"`
	DeviceLimit *int64 `json:"device_limit,omitempty" msgpack:"device_limit,omitempty"`
}

// available 可用订阅：流量未用完、未到期（或长期有效）、订阅与所属用户均未被封禁（决策 D36）。
func (s *Service) available(ctx context.Context) *gorm.DB {
	return s.DB.WithContext(ctx).Table("v2_subscription AS s").
		Joins("JOIN v2_user AS owner ON owner.id = s.user_id").
		Where("s.u + s.d < s.transfer_enable").
		Where("(s.expired_at >= ? OR s.expired_at IS NULL)", s.now().Unix()).
		Where("s.banned = 0 AND owner.banned = 0")
}

// Users 权限组内的可用订阅，按 ID 排序。
func (s *Service) Users(ctx context.Context, groupIDs model.IntList) ([]User, error) {
	out := []User{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	err := s.available(ctx).Where("s.group_id IN ?", []int64(groupIDs)).
		Select("s.id, s.uuid, s.speed_limit, s.device_limit").Order("s.id").Find(&out).Error
	return out, err
}

// rate 节点倍率，无法解析时按 1 计算。
func rate(srv node.Server) float64 {
	r, err := strconv.ParseFloat(srv.Common().Rate, 64)
	if err != nil || r < 0 {
		return 1
	}
	return r
}
