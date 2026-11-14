package nodeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"gvboard/internal/cache"
)

const (
	aliveTTL   = 120 * time.Second // 在线数据缓存时长
	aliveStale = 100               // 某节点超过该秒数未上报时不再计入
	aliveList  = 60 * time.Second  // 在线设备数列表缓存时长
	mgetChunk  = 1000
)

// errInvalidAlive 在线数据格式错误。
var errInvalidAlive = errors.New("Invalid online data format")

// ParseAlive 解析在线 IP 上报 {"订阅ID": ["IP_节点ID", ...]}，订阅 ID 无效或值不是数组的条目跳过。
func ParseAlive(b []byte) (map[int64][]string, error) {
	out := map[int64][]string{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return out, nil
	}
	var raw any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if err := d.Decode(&raw); err != nil {
		return nil, errInvalidAlive
	}
	switch m := raw.(type) {
	case []any:
		if len(m) == 0 {
			return out, nil
		}
		return nil, errInvalidAlive
	case map[string]any:
		for k, v := range m {
			id, err := strconv.ParseInt(k, 10, 64)
			list, ok := v.([]any)
			if err != nil || id <= 0 || !ok {
				continue
			}
			ips := make([]string, 0, len(list))
			for _, ip := range list {
				switch x := ip.(type) {
				case string:
					ips = append(ips, x)
				case json.Number:
					ips = append(ips, x.String())
				}
			}
			out[id] = ips
		}
		return out, nil
	}
	return nil, errInvalidAlive
}

type aliveEntry struct {
	AliveIPs     []string `json:"aliveips"`
	LastUpdateAt int64    `json:"lastupdateAt"`
}

type aliveCount struct {
	AliveIP int64 `json:"alive_ip"`
}

// Alive 记录节点上报的在线 IP，并重新计算各订阅的在线设备数。
// 每个节点的数据独立保存在订阅的哈希中，多个节点同时上报不会互相覆盖；
// 超过 100 秒未上报的节点不再计入。device_limit_mode 为 1 时按不同 IP 计数，否则按连接数累加。
func (s *Service) Alive(ctx context.Context, typ string, nodeID int64, data map[int64][]string) error {
	if len(data) == 0 {
		return nil
	}
	now := s.now().Unix()
	nodeField := typ + strconv.FormatInt(nodeID, 10)
	ids := make([]int64, 0, len(data))
	for id := range data {
		ids = append(ids, id)
	}
	all := make([]*redis.MapStringStringCmd, len(ids))
	_, err := s.Cache.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, id := range ids {
			b, _ := json.Marshal(aliveEntry{AliveIPs: data[id], LastUpdateAt: now})
			k := s.Cache.KeyOf(cache.KeyAliveNodes, id)
			p.HSet(ctx, k, nodeField, b)
			p.Expire(ctx, k, aliveTTL)
			all[i] = p.HGetAll(ctx, k)
		}
		return nil
	})
	if err != nil {
		return err
	}
	unique := s.Settings.Int("device_limit_mode") == 1
	_, err = s.Cache.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, id := range ids {
			var stale []string
			var count int64
			seen := map[string]bool{}
			for f, v := range all[i].Val() {
				var e aliveEntry
				if json.Unmarshal([]byte(v), &e) != nil || now-e.LastUpdateAt > aliveStale {
					stale = append(stale, f)
					continue
				}
				if !unique {
					count += int64(len(e.AliveIPs))
					continue
				}
				for _, ipNode := range e.AliveIPs {
					seen[strings.SplitN(ipNode, "_", 2)[0]] = true
				}
			}
			if unique {
				count = int64(len(seen))
			}
			if len(stale) > 0 {
				p.HDel(ctx, s.Cache.KeyOf(cache.KeyAliveNodes, id), stale...)
			}
			b, _ := json.Marshal(aliveCount{AliveIP: count})
			p.Set(ctx, s.Cache.KeyOf(cache.KeyAliveIP, id), b, aliveTTL)
		}
		return nil
	})
	return err
}

// AliveList 有设备数限制的可用订阅当前在线设备数（订阅 ID → 设备数），缓存 60 秒。
func (s *Service) AliveList(ctx context.Context) (map[int64]int64, error) {
	key := s.Cache.KeyOf(cache.KeyAliveList, nil)
	out := map[int64]int64{}
	if ok, err := s.Cache.GetJSON(ctx, key, &out); err != nil || ok {
		return out, err
	}
	var ids []int64
	if err := s.available(ctx).Where("s.device_limit > 0").Order("s.id").Pluck("s.id", &ids).Error; err != nil {
		return nil, err
	}
	for i := 0; i < len(ids); i += mgetChunk {
		batch := ids[i:min(i+mgetChunk, len(ids))]
		keys := make([]string, len(batch))
		for j, id := range batch {
			keys[j] = s.Cache.KeyOf(cache.KeyAliveIP, id)
		}
		vals, err := s.Cache.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, fmt.Errorf("读取在线设备数失败: %w", err)
		}
		for j, v := range vals {
			str, ok := v.(string)
			var c aliveCount
			if ok && json.Unmarshal([]byte(str), &c) == nil {
				out[batch[j]] = c.AliveIP
			}
		}
	}
	return out, s.Cache.SetJSON(ctx, key, out, aliveList)
}
