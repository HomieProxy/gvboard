package setting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/model"
)

// Service 业务设置服务。全部设置常驻内存，修改后通过 Redis 通知其它实例重新加载。
type Service struct {
	db    *gorm.DB
	cache *cache.Cache // 可为 nil（命令行工具场景）
	log   *zap.Logger

	mu   sync.RWMutex
	vals map[string]any
}

func New(db *gorm.DB, c *cache.Cache, log *zap.Logger) *Service {
	return &Service{db: db, cache: c, log: log, vals: map[string]any{}}
}

// NewStatic 只读的设置服务（不连接数据库，不能调用 Set / Load），用于测试。
func NewStatic(vals map[string]any) *Service {
	s := New(nil, nil, zap.NewNop())
	for k, v := range vals {
		s.vals[k] = v
	}
	return s
}

const changedChannel = "settings:changed"

// Load 从数据库加载全部设置。
func (s *Service) Load(ctx context.Context) error {
	var rows []model.Setting
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return fmt.Errorf("加载设置失败: %w", err)
	}
	vals := make(map[string]any, len(rows))
	for _, r := range rows {
		v, err := decodeValue(r.Value)
		if err != nil {
			s.log.Warn("设置值解析失败，使用默认值", zap.String("name", r.Name), zap.Error(err))
			continue
		}
		if d, ok := Lookup(r.Name); ok {
			if v, err = Coerce(d, v); err != nil {
				s.log.Warn("设置值类型不符，使用默认值", zap.String("name", r.Name), zap.Error(err))
				continue
			}
		}
		if v != nil {
			vals[r.Name] = v
		}
	}
	s.mu.Lock()
	s.vals = vals
	s.mu.Unlock()
	return nil
}

func decodeValue(raw string) (any, error) {
	if raw == "" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return normalizeNumbers(v), nil
}

// normalizeNumbers 把 json.Number 转为 int64 或 float64。
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	case []any:
		for i := range x {
			x[i] = normalizeNumbers(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = normalizeNumbers(x[k])
		}
	}
	return v
}

// Get 读取设置值，未设置时返回默认值。
func (s *Service) Get(key string) any {
	s.mu.RLock()
	v, ok := s.vals[key]
	s.mu.RUnlock()
	if ok {
		return v
	}
	if d, ok := Lookup(key); ok {
		return d.Default
	}
	return nil
}

func (s *Service) String(key string) string {
	v, _ := toString(s.Get(key))
	str, _ := v.(string)
	return str
}

func (s *Service) Int(key string) int64 {
	v, _ := toInt(s.Get(key))
	n, _ := v.(int64)
	return n
}

func (s *Service) Float(key string) float64 {
	v, _ := toFloat(s.Get(key))
	f, _ := v.(float64)
	return f
}

// FloatPtr 读取可为空的小数设置（如三级分销比例），未设置返回 nil。
func (s *Service) FloatPtr(key string) *float64 {
	v := s.Get(key)
	if v == nil {
		return nil
	}
	f, err := toFloat(v)
	if err != nil || f == nil {
		return nil
	}
	x := f.(float64)
	return &x
}

func (s *Service) Bool(key string) bool { return s.Int(key) != 0 }

func (s *Service) Strings(key string) []string {
	v, _ := toStrings(s.Get(key))
	out, _ := v.([]string)
	return out
}

// themeConfigPrefix 主题配置值在设置表中的键名前缀，值为 JSON 对象（字段名 → 值）。
const themeConfigPrefix = "theme_config."

// ThemeConfig 返回已保存的主题配置副本，未保存时返回空 map。
func (s *Service) ThemeConfig(name string) map[string]any {
	m, _ := s.Get(themeConfigPrefix + name).(map[string]any)
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// SetThemeConfig 保存主题配置（由主题模块按主题定义校验后调用）。
func (s *Service) SetThemeConfig(ctx context.Context, name string, values map[string]any) error {
	return s.save(ctx, map[string]any{themeConfigPrefix + name: values})
}

// All 返回全部已定义设置的当前值（含默认值）。
func (s *Service) All() map[string]any {
	out := make(map[string]any, len(defs))
	for _, d := range defs {
		out[d.Key] = s.Get(d.Key)
	}
	return out
}

// Set 批量修改已定义的设置项，校验失败时整体不生效。
func (s *Service) Set(ctx context.Context, values map[string]any) error {
	clean := make(map[string]any, len(values))
	for k, v := range values {
		d, ok := Lookup(k)
		if !ok {
			return fmt.Errorf("未知设置项: %s", k)
		}
		cv, err := Coerce(d, v)
		if err != nil {
			return err
		}
		clean[k] = cv
	}
	return s.save(ctx, clean)
}

// save 写入数据库（不校验键名，供导入使用），然后刷新本地并通知其它实例。
func (s *Service) save(ctx context.Context, values map[string]any) error {
	if len(values) == 0 {
		return nil
	}
	now := time.Now().Unix()
	rows := make([]model.Setting, 0, len(values))
	for k, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("设置项 %s 编码失败: %w", k, err)
		}
		rows = append(rows, model.Setting{Name: k, Value: string(b), CreatedAt: now, UpdatedAt: now})
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&rows).Error
	if err != nil {
		return fmt.Errorf("保存设置失败: %w", err)
	}
	if err := s.Load(ctx); err != nil {
		return err
	}
	s.publish(ctx)
	return nil
}

func (s *Service) publish(ctx context.Context) {
	if s.cache == nil {
		return
	}
	if err := s.cache.Publish(ctx, s.cache.Key(changedChannel), "1").Err(); err != nil {
		s.log.Warn("广播设置变更失败", zap.Error(err))
	}
}

// Watch 订阅设置变更通知并重新加载，阻塞直到 ctx 取消。
func (s *Service) Watch(ctx context.Context) {
	if s.cache == nil {
		return
	}
	sub := s.cache.Subscribe(ctx, s.cache.Key(changedChannel))
	defer sub.Close()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if err := s.Load(ctx); err != nil {
				s.log.Error("重新加载设置失败", zap.Error(err))
			}
		}
	}
}

// EnsureDefaults 初始化必须存在且需随机生成的设置（管理端路径、订阅路径），返回提示信息。
func (s *Service) EnsureDefaults(ctx context.Context) ([]string, error) {
	var notices []string
	values := map[string]any{}

	if err := validateSecurePath(s.String("secure_path")); err != nil {
		p, err := config.RandomString(20)
		if err != nil {
			return nil, err
		}
		values["secure_path"] = p
		notices = append(notices, "已生成管理端路径: /"+p+" （请妥善保存）")
	}
	if s.String("subscribe_path") == "" {
		p, err := config.RandomString(12)
		if err != nil {
			return nil, err
		}
		values["subscribe_path"] = "/" + p
		notices = append(notices, "已生成订阅路径: /"+p)
	}
	return notices, s.save(ctx, values)
}
