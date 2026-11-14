package migrate

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"

	"gvboard/internal/model"
)

// TestModelsMatchSchema 数据模型能被 GORM 解析，且模型与表结构定义的列完全一致。
func TestModelsMatchSchema(t *testing.T) {
	tables := map[string]map[string]bool{}
	for _, tb := range Schema() {
		cols := map[string]bool{}
		for _, c := range tb.Columns {
			cols[c.Name] = true
		}
		tables[tb.Name] = cols
	}

	cache := &sync.Map{}
	seen := map[string]bool{}
	for _, m := range model.All() {
		s, err := schema.Parse(m, cache, schema.NamingStrategy{})
		if err != nil {
			t.Errorf("%T 无法被 GORM 解析: %v", m, err)
			continue
		}
		cols, ok := tables[s.Table]
		if !ok {
			t.Errorf("%T 对应的表 %s 不在表结构定义中", m, s.Table)
			continue
		}
		seen[s.Table] = true
		fields := map[string]bool{}
		for _, f := range s.Fields {
			if f.DBName == "" {
				continue
			}
			fields[f.DBName] = true
			if !cols[f.DBName] {
				t.Errorf("%s: 模型字段 %s 在表结构中不存在", s.Table, f.DBName)
			}
		}
		for c := range cols {
			if !fields[c] {
				t.Errorf("%s: 表结构中的列 %s 在模型中缺失", s.Table, c)
			}
		}
	}
	for name := range tables {
		if !seen[name] && name != migrationTable && name != "failed_jobs" {
			t.Errorf("表 %s 没有对应的数据模型", name)
		}
	}
}
