package plugin

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/model"
	sdk "gvboard/pkg/plugin"
)

// KV 限制。
const (
	maxKeyLen   = 191
	maxValueLen = 1 << 20
)

var errKVKey = errors.New("键不能为空且不能超过 191 字节，值不能超过 1 MB")

// kv 插件键值存储（v2_plugin_kv）。
type kv struct {
	m    *Manager
	code string
}

var _ sdk.KV = (*kv)(nil)

func (s *kv) db(ctx context.Context) *gorm.DB { return s.m.DB.WithContext(ctx) }

func (s *kv) expireAt(ttl time.Duration) *int64 {
	if ttl <= 0 {
		return nil
	}
	v := s.m.now().Add(ttl).Unix()
	return &v
}

func checkKV(key, value string) error {
	if key == "" || len(key) > maxKeyLen || len(value) > maxValueLen {
		return errKVKey
	}
	return nil
}

func (s *kv) Get(ctx context.Context, key string) (string, error) {
	var row model.PluginKV
	err := s.db(ctx).Where("plugin_code = ? AND k = ?", s.code, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", sdk.ErrKVNotFound
	}
	if err != nil {
		return "", err
	}
	if row.ExpireAt != nil && *row.ExpireAt <= s.m.now().Unix() {
		return "", sdk.ErrKVNotFound
	}
	return row.V, nil
}

func (s *kv) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := checkKV(key, value); err != nil {
		return err
	}
	row := model.PluginKV{PluginCode: s.code, K: key, V: value, ExpireAt: s.expireAt(ttl)}
	return s.db(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "plugin_code"}, {Name: "k"}},
		DoUpdates: clause.AssignmentColumns([]string{"v", "expire_at", "updated_at"}),
	}).Create(&row).Error
}

func (s *kv) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	if err := checkKV(key, value); err != nil {
		return false, err
	}
	ok := false
	err := s.db(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("plugin_code = ? AND k = ? AND expire_at IS NOT NULL AND expire_at <= ?", s.code, key, s.m.now().Unix()).
			Delete(&model.PluginKV{}).Error; err != nil {
			return err
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&model.PluginKV{PluginCode: s.code, K: key, V: value, ExpireAt: s.expireAt(ttl)})
		ok = res.RowsAffected == 1
		return res.Error
	})
	return ok, err
}

func (s *kv) Delete(ctx context.Context, key string) error {
	return s.db(ctx).Where("plugin_code = ? AND k = ?", s.code, key).Delete(&model.PluginKV{}).Error
}

// CleanKV 删除已过期的插件键值，返回删除数量。
func (m *Manager) CleanKV(ctx context.Context) (int64, error) {
	var total int64
	for {
		var ids []int64
		err := m.DB.WithContext(ctx).Model(&model.PluginKV{}).Where("expire_at IS NOT NULL AND expire_at <= ?", m.now().Unix()).
			Order("id").Limit(5000).Pluck("id", &ids).Error
		if err != nil || len(ids) == 0 {
			return total, err
		}
		res := m.DB.WithContext(ctx).Where("id IN ?", ids).Delete(&model.PluginKV{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if len(ids) < 5000 {
			return total, nil
		}
	}
}
