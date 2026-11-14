package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	sdk "gvboard/pkg/plugin"
)

var fieldKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

// 配置值长度上限（字符）。
const (
	maxStringLen = 2000
	maxTextLen   = 65535
	maxFields    = 100
)

// validateFields 校验插件声明的配置项。
func validateFields(fields []sdk.Field) error {
	if len(fields) > maxFields {
		return fmt.Errorf("配置项不能超过 %d 个", maxFields)
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if !fieldKeyPattern.MatchString(f.Key) {
			return fmt.Errorf("配置项键名 %q 无效", f.Key)
		}
		if seen[f.Key] {
			return fmt.Errorf("配置项 %q 重复", f.Key)
		}
		seen[f.Key] = true
		switch f.Type {
		case sdk.FieldString, sdk.FieldText, sdk.FieldPassword, sdk.FieldNumber, sdk.FieldBool:
		case sdk.FieldSelect:
			if len(f.Options) == 0 {
				return fmt.Errorf("单选配置项 %q 没有选项", f.Key)
			}
		default:
			return fmt.Errorf("配置项 %q 的类型 %q 无效", f.Key, f.Type)
		}
		if f.Default != nil {
			if _, err := coerce(f, f.Default); err != nil {
				return fmt.Errorf("配置项 %q 的默认值无效: %w", f.Key, err)
			}
		}
	}
	return nil
}

// zero 配置项未设置默认值时的值。
func zero(f sdk.Field) any {
	switch f.Type {
	case sdk.FieldNumber:
		return float64(0)
	case sdk.FieldBool:
		return false
	}
	return ""
}

// withDefaults 合并已保存的配置与默认值，已保存的值类型不符时使用默认值。
// 未声明配置项的插件（如 JS 插件由其自身定义）原样保留已保存的值。
func withDefaults(fields []sdk.Field, saved map[string]any) map[string]any {
	out := map[string]any{}
	if len(fields) == 0 {
		for k, v := range saved {
			out[k] = v
		}
		return out
	}
	for _, f := range fields {
		v := f.Default
		if v == nil {
			v = zero(f)
		}
		v, _ = coerce(f, v)
		if raw, ok := saved[f.Key]; ok {
			if cv, err := coerce(f, raw); err == nil {
				v = cv
			}
		}
		out[f.Key] = v
	}
	return out
}

// normalize 校验管理员提交的配置：未声明的键报错，未提交的项保持原值，必填项不能为空。
func normalize(fields []sdk.Field, current, in map[string]any) (map[string]any, error) {
	known := map[string]sdk.Field{}
	for _, f := range fields {
		known[f.Key] = f
	}
	for k := range in {
		if _, ok := known[k]; !ok {
			return nil, fmt.Errorf("未知配置项 %s", k)
		}
	}
	out := withDefaults(fields, current)
	for k, raw := range in {
		f := known[k]
		v, err := coerce(f, raw)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", label(f), err)
		}
		out[k] = v
	}
	if err := checkRequired(fields, out); err != nil {
		return nil, err
	}
	return out, nil
}

// checkRequired 必填项不能为空。
func checkRequired(fields []sdk.Field, cfg map[string]any) error {
	for _, f := range fields {
		if !f.Required {
			continue
		}
		if s, ok := cfg[f.Key].(string); (ok && strings.TrimSpace(s) == "") || cfg[f.Key] == nil {
			return fmt.Errorf("%s 不能为空", label(f))
		}
	}
	return nil
}

func label(f sdk.Field) string {
	if f.Label != "" {
		return f.Label
	}
	return f.Key
}

// coerce 按配置项类型转换值。
func coerce(f sdk.Field, v any) (any, error) {
	switch f.Type {
	case sdk.FieldString, sdk.FieldPassword, sdk.FieldText, sdk.FieldSelect:
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case json.Number:
			s = x.String()
		case float64:
			s = strconv.FormatFloat(x, 'f', -1, 64)
		case int:
			s = strconv.Itoa(x)
		case int64:
			s = strconv.FormatInt(x, 10)
		case nil:
		default:
			return nil, errors.New("必须为文本")
		}
		limit := maxStringLen
		if f.Type == sdk.FieldText {
			limit = maxTextLen
		}
		if utf8.RuneCountInString(s) > limit {
			return nil, fmt.Errorf("不能超过 %d 个字符", limit)
		}
		if f.Type == sdk.FieldSelect && s != "" && !slices.ContainsFunc(f.Options, func(o sdk.Option) bool { return o.Value == s }) {
			return nil, errors.New("不是可选的值")
		}
		return s, nil
	case sdk.FieldNumber:
		var n float64
		switch x := v.(type) {
		case float64:
			n = x
		case int:
			n = float64(x)
		case int64:
			n = float64(x)
		case json.Number:
			var err error
			if n, err = x.Float64(); err != nil {
				return nil, errors.New("必须为数字")
			}
		case string:
			var err error
			if n, err = strconv.ParseFloat(strings.TrimSpace(x), 64); err != nil {
				return nil, errors.New("必须为数字")
			}
		default:
			return nil, errors.New("必须为数字")
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, errors.New("必须为数字")
		}
		return n, nil
	case sdk.FieldBool:
		switch x := v.(type) {
		case bool:
			return x, nil
		case float64:
			return x != 0, nil
		case int:
			return x != 0, nil
		case int64:
			return x != 0, nil
		case json.Number:
			return x.String() != "0", nil
		case string:
			switch x {
			case "1", "true":
				return true, nil
			case "0", "false", "":
				return false, nil
			}
		}
		return nil, errors.New("必须为开关值")
	}
	return v, nil
}
