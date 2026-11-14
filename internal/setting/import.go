package setting

import (
	"context"
	"fmt"
	"sort"
)

// ImportReport 配置导入结果。
type ImportReport struct {
	Imported []string          // 已导入的已知设置项
	Unknown  []string          // 未在 Gvboard 中定义、但原样保存的项
	Skipped  map[string]string // 因格式错误跳过的项及原因
	Notices  []string
}

// legacyAliases 原项目的旧键名 → 新键名。
var legacyAliases = map[string]string{
	"frontend_admin_path": "secure_path",
}

// PrepareImport 把解析后的原项目配置转换为待写入的设置，不访问数据库。
func PrepareImport(raw map[string]any) (map[string]any, *ImportReport) {
	rep := &ImportReport{Skipped: map[string]string{}}
	values := map[string]any{}

	for old, cur := range legacyAliases {
		if v, ok := raw[old]; ok {
			if _, exists := raw[cur]; !exists {
				raw[cur] = v
			}
			delete(raw, old)
		}
	}

	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := raw[k]
		d, known := Lookup(k)
		if !known {
			values[k] = v
			rep.Unknown = append(rep.Unknown, k)
			continue
		}
		cv, err := Coerce(d, v)
		if err != nil {
			if k == "secure_path" {
				// 原项目允许 8 位路径，不满足 Gvboard 要求时由 EnsureDefaults 重新生成。
				rep.Notices = append(rep.Notices, "原管理端路径不满足至少 16 位的要求，将重新生成")
				continue
			}
			rep.Skipped[k] = err.Error()
			continue
		}
		if cv == nil {
			continue
		}
		values[k] = cv
		rep.Imported = append(rep.Imported, k)
	}

	if _, ok := values["server_token"]; !ok {
		rep.Notices = append(rep.Notices, "原配置中没有节点通信密钥 server_token，节点将无法对接，请在管理端设置")
	}
	return values, rep
}

// ImportPHP 解析原项目 config/v2board.php 并写入设置表，随后补齐必须的随机设置。
func (s *Service) ImportPHP(ctx context.Context, src []byte) (*ImportReport, error) {
	raw, err := ParsePHPConfig(src)
	if err != nil {
		return nil, err
	}
	values, rep := PrepareImport(raw)
	if err := s.save(ctx, values); err != nil {
		return rep, fmt.Errorf("写入设置失败: %w", err)
	}
	notices, err := s.EnsureDefaults(ctx)
	rep.Notices = append(rep.Notices, notices...)
	return rep, err
}
