// Package model 定义与数据库表对应的结构体。表名与字段与原项目保持一致（v2_ 前缀）。
package model

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// 原项目用 Laravel 的 array 转换把数组/对象以 JSON 文本存在 varchar/text 列中。
// 以下类型负责兼容这种存储方式。

func scanBytes(src any) ([]byte, error) {
	switch v := src.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	}
	return nil, fmt.Errorf("不支持的 JSON 列类型 %T", src)
}

func isEmptyJSON(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) == 0 || bytes.Equal(b, []byte("null"))
}

// unwrapJSONString 原项目部分字段先 json_encode 再经 array 转换写入，库里存的是 "[1,2]" 这种
// 被再编码一次的 JSON 字符串（如 v2_giftcard.used_user_ids），读取时剥掉这一层。
func unwrapJSONString(b []byte) ([]byte, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '"' {
		return b, nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// IntList 整数数组，兼容元素为字符串（"1"）或数字（1）的历史数据。
type IntList []int64

func (l *IntList) Scan(src any) error {
	b, err := scanBytes(src)
	if err != nil {
		return err
	}
	if b, err = unwrapJSONString(b); err != nil {
		return fmt.Errorf("解析整数数组失败: %w", err)
	}
	if isEmptyJSON(b) {
		*l = nil
		return nil
	}
	var raw []any
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("解析整数数组失败: %w", err)
	}
	out := make(IntList, 0, len(raw))
	for _, item := range raw {
		switch v := item.(type) {
		case float64:
			out = append(out, int64(v))
		case string:
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("整数数组包含非数字元素 %q", v)
			}
			out = append(out, n)
		case nil:
		default:
			return fmt.Errorf("整数数组包含不支持的元素 %v", v)
		}
	}
	*l = out
	return nil
}

func (l IntList) Value() (driver.Value, error) {
	if l == nil {
		return nil, nil
	}
	b, err := json.Marshal([]int64(l))
	return string(b), err
}

// GormDataType 声明为普通文本列，否则 GORM 会把切片字段当作关联关系解析。
func (IntList) GormDataType() string { return "text" }

// Contains 判断是否包含指定值。
func (l IntList) Contains(v int64) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// StrList 字符串数组，数字元素会被转为字符串。
type StrList []string

func (l *StrList) Scan(src any) error {
	b, err := scanBytes(src)
	if err != nil {
		return err
	}
	if b, err = unwrapJSONString(b); err != nil {
		return fmt.Errorf("解析字符串数组失败: %w", err)
	}
	if isEmptyJSON(b) {
		*l = nil
		return nil
	}
	var raw []any
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("解析字符串数组失败: %w", err)
	}
	out := make(StrList, 0, len(raw))
	for _, item := range raw {
		switch v := item.(type) {
		case string:
			out = append(out, v)
		case float64:
			out = append(out, strconv.FormatFloat(v, 'f', -1, 64))
		case nil:
		default:
			return fmt.Errorf("字符串数组包含不支持的元素 %v", v)
		}
	}
	*l = out
	return nil
}

func (l StrList) Value() (driver.Value, error) {
	if l == nil {
		return nil, nil
	}
	b, err := json.Marshal([]string(l))
	return string(b), err
}

func (StrList) GormDataType() string { return "text" }

// JSON 任意 JSON 文本（对象或数组），原样保存。
type JSON json.RawMessage

func (j *JSON) Scan(src any) error {
	b, err := scanBytes(src)
	if err != nil {
		return err
	}
	if isEmptyJSON(b) {
		*j = nil
		return nil
	}
	if !json.Valid(b) {
		return fmt.Errorf("列内容不是合法 JSON")
	}
	*j = append((*j)[:0], b...)
	return nil
}

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return string(j), nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *JSON) UnmarshalJSON(b []byte) error {
	if isEmptyJSON(b) {
		*j = nil
		return nil
	}
	*j = append((*j)[:0], b...)
	return nil
}

// Decode 解析到目标结构。
func (j JSON) Decode(v any) error {
	if len(j) == 0 {
		return nil
	}
	return json.Unmarshal(j, v)
}

// Now 当前 Unix 秒，所有 created_at / updated_at 等时间戳字段均使用此格式。
func Now() int64 { return time.Now().Unix() }
