package setting

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Coerce 把任意输入（JSON 解码结果、PHP 配置解析结果、表单值）转换为设置项的规范类型。
// 返回 nil 表示未设置（读取时使用默认值）。
func Coerce(d Def, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if n, ok := v.(json.Number); ok {
		v = string(n)
	}
	var out any
	var err error
	switch d.Type {
	case TypeString:
		out, err = toString(v)
	case TypeInt:
		out, err = toInt(v)
	case TypeNumber:
		out, err = toFloat(v)
	case TypeBool:
		out, err = toBoolInt(v)
	case TypeStrings:
		out, err = toStrings(v)
	default:
		err = fmt.Errorf("未知类型")
	}
	if err != nil {
		return nil, fmt.Errorf("设置项 %s: %w", d.Key, err)
	}
	if out != nil && d.Validate != nil {
		if err := d.Validate(out); err != nil {
			return nil, fmt.Errorf("设置项 %s: %w", d.Key, err)
		}
	}
	return out, nil
}

func toString(v any) (any, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		if x {
			return "1", nil
		}
		return "0", nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case int:
		return strconv.Itoa(x), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	}
	return nil, fmt.Errorf("无法转换为字符串: %v", v)
}

func toInt(v any) (any, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case float64:
		if x != math.Trunc(x) {
			return nil, fmt.Errorf("需要整数: %v", x)
		}
		return int64(x), nil
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil, nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(s, 64)
			if ferr != nil || f != math.Trunc(f) {
				return nil, fmt.Errorf("需要整数: %q", x)
			}
			n = int64(f)
		}
		return n, nil
	}
	return nil, fmt.Errorf("需要整数: %v", v)
}

func toFloat(v any) (any, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case int:
		return float64(x), nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("需要数字: %q", x)
		}
		return f, nil
	}
	return nil, fmt.Errorf("需要数字: %v", v)
}

func toBoolInt(v any) (any, error) {
	switch x := v.(type) {
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "", "0", "false", "off", "no":
			return int64(0), nil
		case "1", "true", "on", "yes":
			return int64(1), nil
		}
		return nil, fmt.Errorf("需要开关值 0/1: %q", x)
	}
	n, err := toInt(v)
	if err != nil {
		return nil, fmt.Errorf("需要开关值 0/1: %v", v)
	}
	if n.(int64) != 0 {
		return int64(1), nil
	}
	return int64(0), nil
}

// toStrings 兼容数组与逗号分隔字符串（原项目部分设置两种格式都出现过）。
func toStrings(v any) (any, error) {
	switch x := v.(type) {
	case []string:
		return x, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if item == nil {
				continue
			}
			s, err := toString(item)
			if err != nil {
				return nil, err
			}
			out = append(out, s.(string))
		}
		return out, nil
	case string:
		if strings.TrimSpace(x) == "" {
			return []string{}, nil
		}
		parts := strings.Split(x, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out, nil
	case map[string]any:
		// PHP 关联数组被解析为 map 时（键不连续），按值收集。
		out := make([]string, 0, len(x))
		for _, item := range x {
			s, err := toString(item)
			if err != nil {
				return nil, err
			}
			out = append(out, s.(string))
		}
		return out, nil
	}
	return nil, fmt.Errorf("需要字符串数组: %v", v)
}
