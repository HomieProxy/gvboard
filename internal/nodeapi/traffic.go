package nodeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/cache"
	"gvboard/internal/database"
	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/node"
	"gvboard/internal/queue"
	"gvboard/internal/scheduler"
)

// Traffic 一次上报的流量：订阅 ID → [上行, 下行]（字节，未乘倍率）。
type Traffic map[int64][2]int64

// errInvalidTraffic 上报数据格式错误。
var errInvalidTraffic = errors.New("Invalid traffic data")

// ParseUniTraffic 解析 UniProxy 上报的流量 {"订阅ID": [上行, 下行]}，返回流量与上报条数。
// 空请求体视为没有流量；订阅 ID 或数值无效的条目跳过，负数按 0 计算。
func ParseUniTraffic(b []byte) (Traffic, int, error) {
	out := Traffic{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return out, 0, nil
	}
	var raw any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if err := d.Decode(&raw); err != nil {
		return nil, 0, errInvalidTraffic
	}
	switch m := raw.(type) {
	case []any:
		if len(m) == 0 {
			return out, 0, nil
		}
		return nil, 0, errInvalidTraffic
	case map[string]any:
		for k, v := range m {
			id, err := strconv.ParseInt(k, 10, 64)
			pair, ok := v.([]any)
			if err != nil || id <= 0 || !ok || len(pair) < 2 {
				continue
			}
			out[id] = [2]int64{toBytes(pair[0]), toBytes(pair[1])}
		}
		return out, len(m), nil
	}
	return nil, 0, errInvalidTraffic
}

// ParseLegacyTraffic 解析旧版后端上报的流量 [{"user_id":1,"u":0,"d":0}]，返回流量与上报条数。
func ParseLegacyTraffic(b []byte) (Traffic, int, error) {
	var items []map[string]any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if err := d.Decode(&items); err != nil {
		return nil, 0, errInvalidTraffic
	}
	out := Traffic{}
	for _, it := range items {
		id := toBytes(it["user_id"])
		if id <= 0 {
			continue
		}
		out[id] = [2]int64{toBytes(it["u"]), toBytes(it["d"])}
	}
	return out, len(items), nil
}

func toBytes(v any) int64 {
	var n int64
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			n = i
		} else if f, err := x.Float64(); err == nil {
			n = int64(f)
		}
	case string:
		n, _ = strconv.ParseInt(x, 10, 64)
	}
	if n < 0 {
		return 0
	}
	return n
}

// Record 记录节点上报的流量：乘以节点倍率后累加到 Redis，每分钟由 FlushTraffic 写入订阅；
// 原始流量投递到统计队列写入用户与节点统计表。
func (s *Service) Record(ctx context.Context, typ string, srv node.Server, data Traffic) error {
	if len(data) == 0 {
		return nil
	}
	r := rate(srv)
	up, down := s.Cache.KeyOf(cache.KeyTrafficUpload, nil), s.Cache.KeyOf(cache.KeyTrafficDownload, nil)
	_, err := s.Cache.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for id, v := range data {
			f := strconv.FormatInt(id, 10)
			p.HIncrBy(ctx, up, f, int64(float64(v[0])*r))
			p.HIncrBy(ctx, down, f, int64(float64(v[1])*r))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.Hooks != nil {
		ev := hook.Traffic{ServerType: typ, ServerID: srv.Common().ID, Users: len(data)}
		for _, v := range data {
			ev.Upload += v[0]
			ev.Download += v[1]
		}
		s.Hooks.Emit(ctx, hook.EventTrafficReported, ev)
	}
	task := statTask{ServerID: srv.Common().ID, ServerType: typ, Rate: r, RecordAt: s.today(), Data: data}
	if s.Queue == nil {
		return s.Stat(ctx, task)
	}
	if err := s.Queue.Enqueue(ctx, TaskStat, task, ""); err != nil {
		// 流量已累计，此处返回错误会让节点重复上报，只记录日志。
		s.log().Error("投递流量统计任务失败", zap.String("type", typ), zap.Int64("id", task.ServerID), zap.Error(err))
	}
	return nil
}

// today 当天零点（服务器时区），与原项目 strtotime(date('Y-m-d')) 一致。
func (s *Service) today() int64 {
	n := s.now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location()).Unix()
}

// TaskStat 流量统计任务（原项目 StatUserJob 与 StatServerJob 合并）。
const TaskStat = "stat_traffic"

type statTask struct {
	ServerID   int64   `json:"server_id"`
	ServerType string  `json:"server_type"`
	Rate       float64 `json:"rate"`
	RecordAt   int64   `json:"record_at"`
	Data       Traffic `json:"data"`
}

// RegisterTask 注册流量统计任务。
func RegisterTask(q *queue.Queue, s *Service) {
	q.Register(queue.Spec{Type: TaskStat, Queue: queue.QueueStat, Tries: 3, Timeout: 60 * time.Second},
		func(ctx context.Context, payload []byte) error {
			var t statTask
			if err := json.Unmarshal(payload, &t); err != nil {
				s.log().Error("流量统计任务数据无效", zap.Error(err))
				return nil
			}
			return s.Stat(ctx, t)
		})
}

// statChunk 每批写入的统计行数。
const statChunk = 500

// 多个任务并发插入同一天的新统计行时，MySQL 唯一索引的间隙锁会互相死锁（按 ID 排序也无法避免），
// 因此统计事务持有命名锁依次执行；statAttempts 为仍遇到并发冲突时的最多执行次数。
const (
	statLock        = "stat_traffic"
	statLockTimeout = 30 * time.Second
	statAttempts    = 5
)

// Stat 在一个事务中原子累加节点与用户的当日流量统计（决策 D38），失败时整体回滚，重试不会重复计数。
// 统计的是未乘倍率的原始流量，用户统计按订阅与倍率分行。
func (s *Service) Stat(ctx context.Context, t statTask) error {
	if len(t.Data) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(t.Data))
	var su, sd int64
	for id, v := range t.Data {
		ids = append(ids, id)
		su += v[0]
		sd += v[1]
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	now := s.now().Unix()
	return database.Transaction(ctx, s.DB, statAttempts, func(tx *gorm.DB) error {
		unlock, err := database.XactLock(tx, statLock, statLockTimeout)
		if err != nil {
			return err
		}
		defer unlock()
		owners := make(map[int64]int64, len(ids))
		for i := 0; i < len(ids); i += 1000 {
			var subs []model.Subscription
			if err := tx.Select("id, user_id").Where("id IN ?", ids[i:min(i+1000, len(ids))]).Find(&subs).Error; err != nil {
				return err
			}
			for _, sub := range subs {
				owners[sub.ID] = sub.UserID
			}
		}
		rows := make([]model.StatUser, 0, len(owners))
		for _, id := range ids {
			uid, ok := owners[id]
			if !ok {
				continue
			}
			sub := id
			rows = append(rows, model.StatUser{
				UserID: uid, SubscriptionID: &sub, ServerRate: t.Rate, U: t.Data[id][0], D: t.Data[id][1],
				RecordType: "d", RecordAt: t.RecordAt, CreatedAt: now, UpdatedAt: now,
			})
		}
		for i := 0; i < len(rows); i += statChunk {
			batch := rows[i:min(i+statChunk, len(rows))]
			if err := tx.Clauses(addOnConflict(tx, "v2_stat_user", "server_rate", "subscription_id", "record_at")).Create(&batch).Error; err != nil {
				return fmt.Errorf("写入用户流量统计失败: %w", err)
			}
		}
		row := model.StatServer{
			ServerID: t.ServerID, ServerType: t.ServerType, U: su, D: sd,
			RecordType: "d", RecordAt: t.RecordAt, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Clauses(addOnConflict(tx, "v2_stat_server", "server_id", "server_type", "record_at")).Create(&row).Error; err != nil {
			return fmt.Errorf("写入节点流量统计失败: %w", err)
		}
		return nil
	})
}

// addOnConflict 唯一键冲突时把 u、d 累加到已有行。
func addOnConflict(db *gorm.DB, table string, keys ...string) clause.OnConflict {
	add := func(col string) clause.Expr {
		if database.IsPostgres(db) {
			return gorm.Expr(fmt.Sprintf("%s.%s + EXCLUDED.%s", table, col, col))
		}
		return gorm.Expr(fmt.Sprintf("%s + VALUES(%s)", col, col))
	}
	cols := make([]clause.Column, len(keys))
	for i, k := range keys {
		cols[i] = clause.Column{Name: k}
	}
	updated := clause.Expr{SQL: "EXCLUDED.updated_at"}
	if !database.IsPostgres(db) {
		updated = clause.Expr{SQL: "VALUES(updated_at)"}
	}
	return clause.OnConflict{Columns: cols, DoUpdates: clause.Set{
		{Column: clause.Column{Name: "u"}, Value: add("u")},
		{Column: clause.Column{Name: "d"}, Value: add("d")},
		{Column: clause.Column{Name: "updated_at"}, Value: updated},
	}}
}

// FlushJob 流量写入任务（原项目 traffic:update，每分钟，不重叠）。
func (s *Service) FlushJob() scheduler.Job {
	return scheduler.Job{Name: "traffic_update", Spec: "* * * * *", NoOverlap: true, Run: s.FlushTraffic}
}

// moveScript 把累计中的流量原子转入待写入哈希；上次写入失败留下的数据会与本次合并。
var moveScript = redis.NewScript(`
for i = 1, 2 do
  local src, dst = KEYS[i], KEYS[i + 2]
  if redis.call('exists', src) == 1 then
    if redis.call('exists', dst) == 1 then
      local kv = redis.call('hgetall', src)
      for j = 1, #kv, 2 do redis.call('hincrby', dst, kv[j], kv[j + 1]) end
      redis.call('del', src)
    else
      redis.call('rename', src, dst)
    end
  end
end
return 1`)

// flushChunk 每条 UPDATE 语句更新的订阅数。
const flushChunk = 500

// FlushTraffic 把 Redis 中累计的流量写入订阅（决策 D38）：先原子转移，数据库用自增方式更新，
// 每批写入成功后才删除对应数据，失败的部分留到下次重试。流量重置进行中时跳过。
func (s *Service) FlushTraffic(ctx context.Context) error {
	c := s.Cache
	locked, err := c.Exists(ctx, c.KeyOf(cache.KeyTrafficResetLock, nil)).Result()
	if err != nil || locked > 0 {
		return err
	}
	keys := []string{
		c.KeyOf(cache.KeyTrafficUpload, nil), c.KeyOf(cache.KeyTrafficDownload, nil),
		c.KeyOf(cache.KeyTrafficUploadPending, nil), c.KeyOf(cache.KeyTrafficDownloadPending, nil),
	}
	if err := moveScript.Run(ctx, c.Client, keys).Err(); err != nil {
		return err
	}
	upRaw, err := c.HGetAll(ctx, keys[2]).Result()
	if err != nil {
		return err
	}
	downRaw, err := c.HGetAll(ctx, keys[3]).Result()
	if err != nil {
		return err
	}
	data := map[int64][2]int64{}
	var bad []string
	collect := func(raw map[string]string, idx int) {
		for f, v := range raw {
			id, err1 := strconv.ParseInt(f, 10, 64)
			n, err2 := strconv.ParseInt(v, 10, 64)
			if err1 != nil || err2 != nil || id <= 0 {
				bad = append(bad, f)
				continue
			}
			p := data[id]
			p[idx] = n
			data[id] = p
		}
	}
	collect(upRaw, 0)
	collect(downRaw, 1)
	if len(bad) > 0 {
		c.HDel(ctx, keys[2], bad...)
		c.HDel(ctx, keys[3], bad...)
	}
	ids := make([]int64, 0, len(data))
	for id := range data {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	now := s.now().Unix()
	for i := 0; i < len(ids); i += flushChunk {
		batch := ids[i:min(i+flushChunk, len(ids))]
		if err := s.addTraffic(ctx, batch, data, now); err != nil {
			return fmt.Errorf("写入订阅流量失败: %w", err)
		}
		fields := make([]string, len(batch))
		for j, id := range batch {
			fields[j] = strconv.FormatInt(id, 10)
		}
		if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
			p.HDel(ctx, keys[2], fields...)
			p.HDel(ctx, keys[3], fields...)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) addTraffic(ctx context.Context, ids []int64, data map[int64][2]int64, now int64) error {
	zero := "0"
	if database.IsPostgres(s.DB) {
		zero = "CAST(0 AS BIGINT)"
	}
	var sb strings.Builder
	args := make([]any, 0, len(ids)*4+3)
	for idx, col := range []string{"u", "d"} {
		if idx > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s = %s + CASE id", col, col)
		for _, id := range ids {
			sb.WriteString(" WHEN ? THEN ?")
			args = append(args, id, data[id][idx])
		}
		fmt.Fprintf(&sb, " ELSE %s END", zero)
	}
	args = append(args, now, now, ids)
	return s.DB.WithContext(ctx).
		Exec("UPDATE v2_subscription SET "+sb.String()+", t = ?, updated_at = ? WHERE id IN ?", args...).Error
}
