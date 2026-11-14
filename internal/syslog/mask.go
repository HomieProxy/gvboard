// Package syslog 系统日志：警告 / 错误日志与管理员修改操作异步批量写入 v2_log（决策 D53）。
package syslog

import (
	"strings"
	"unicode"
)

// Masked 敏感值的替代内容。
const Masked = "***"

// sensitiveWords 键名（转小写后）包含这些词即脱敏。
var sensitiveWords = []string{
	"password", "passwd", "secret", "token", "private", "credential", "cookie", "apikey", "appkey", "authorization",
}

// sensitiveParts 键名按 _ - . 与大小写边界拆分后，任一部分等于这些词即脱敏（如 key、pkey、secure_path）。
var sensitiveParts = map[string]bool{
	"key": true, "pkey": true, "skey": true, "pwd": true, "auth": true, "sign": true, "signature": true,
	"salt": true, "session": true, "card": true, "cvc": true, "cvv": true, "secure": true, "sql": true,
	"uuid": true, "sk": true, "pass": true,
}

// Sensitive 判断键名是否敏感。
func Sensitive(key string) bool {
	lower := strings.ToLower(key)
	for _, w := range sensitiveWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	for _, p := range splitKey(key) {
		if sensitiveParts[p] {
			return true
		}
	}
	return false
}

// splitKey 按 _ - . 空格与驼峰边界拆分并转小写。
func splitKey(key string) []string {
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	prevLower := false
	for _, r := range key {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			flush()
			prevLower = false
			continue
		case unicode.IsUpper(r) && prevLower:
			flush()
		}
		cur = append(cur, r)
		prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
	}
	flush()
	return parts
}

// Mask 返回脱敏后的副本：对象中敏感键的值（不论类型）替换为 ***，递归处理对象与数组。
func Mask(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if Sensitive(k) && val != nil {
				out[k] = Masked
			} else {
				out[k] = Mask(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = Mask(x[i])
		}
		return out
	case map[string][]string:
		out := make(map[string]any, len(x))
		for k, vals := range x {
			if Sensitive(k) {
				out[k] = Masked
			} else if len(vals) == 1 {
				out[k] = vals[0]
			} else {
				out[k] = vals
			}
		}
		return out
	}
	return v
}
