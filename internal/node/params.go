package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// params 节点保存请求的参数。按原项目 Laravel 中间件的处理方式，
// 所有层级的字符串去除首尾空白，空字符串视为 null。
type params struct {
	m map[string]any
	// touched 由服务端补充或改写的字段，修改节点时这些列与请求中出现的列一起写入。
	touched map[string]bool
}

func parseParams(body []byte) (*params, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, false
	}
	for k, v := range m {
		m[k] = clean(v)
	}
	return &params{m: m, touched: map[string]bool{}}, true
}

func clean(v any) any {
	switch x := v.(type) {
	case string:
		if x = strings.TrimSpace(x); x == "" {
			return nil
		}
		return x
	case map[string]any:
		for k, e := range x {
			x[k] = clean(e)
		}
	case []any:
		for i, e := range x {
			x[i] = clean(e)
		}
	}
	return v
}

// has 请求中出现了该字段（含 null）或由服务端改写过。
func (p *params) has(k string) bool {
	_, ok := p.m[k]
	return ok || p.touched[k]
}

// isset 对应 PHP isset：存在且不为 null。
func (p *params) isset(k string) bool { return p.m[k] != nil }

// required 对应 Laravel required：不为 null，也不是空数组。
func (p *params) required(k string) bool { return !isEmptyArray(p.m[k]) && p.m[k] != nil }

func (p *params) set(k string, v any) {
	p.m[k] = v
	p.touched[k] = true
}

func (p *params) str(k string) string { return phpString(p.m[k]) }

// obj 取对象字段，不是对象时返回新的空对象（对应原项目 `$params[k] ?? []` 后按键赋值）。
func (p *params) obj(k string) map[string]any {
	if m, ok := p.m[k].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// in 字段为 null 或取值在列表中。
func (p *params) in(k string, allowed ...string) bool {
	v := p.m[k]
	if v == nil {
		return true
	}
	if _, ok := v.(bool); ok {
		return false
	}
	s := phpString(v)
	for _, a := range allowed {
		if s == a {
			return true
		}
	}
	return false
}

// isArray 字段为 null、对象或数组（对应 Laravel nullable|array）。
func (p *params) isArray(k string) bool {
	switch p.m[k].(type) {
	case nil, map[string]any, []any:
		return true
	}
	return false
}

func (p *params) isNumeric(k string) bool {
	v := p.m[k]
	if v == nil {
		return true
	}
	_, ok := toFloat(v)
	return ok
}

func (p *params) isInteger(k string) bool {
	v := p.m[k]
	if v == nil {
		return true
	}
	f, ok := toFloat(v)
	return ok && f == math.Trunc(f) && !strings.ContainsAny(phpString(v), ".eE")
}

func isEmptyArray(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// phpEmpty 对应 PHP empty。
func phpEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == "" || x == "0"
	case json.Number:
		f, err := x.Float64()
		return err == nil && f == 0
	case bool:
		return !x
	case map[string]any, []any:
		return isEmptyArray(x)
	}
	return false
}

func phpString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		if x {
			return "1"
		}
		return ""
	}
	return fmt.Sprint(v)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

// phpInt 对应 PHP (int) 转换。
func phpInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return int64(f)
	case string:
		s := strings.TrimSpace(x)
		end := 0
		for end < len(s) && (s[end] >= '0' && s[end] <= '9' || end == 0 && (s[end] == '-' || s[end] == '+') || s[end] == '.') {
			end++
		}
		f, _ := strconv.ParseFloat(s[:end], 64)
		return int64(f)
	}
	return 0
}

// phpBool 对应 filter_var(v, FILTER_VALIDATE_BOOLEAN)。
func phpBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	switch strings.ToLower(strings.TrimSpace(phpString(v))) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}
