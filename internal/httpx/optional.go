package httpx

import (
	"bytes"
	"encoding/json"
)

// Optional 可为空字段的修改参数：请求中没有该字段时 Set 为 false（不修改），
// 值为 null 时 Set 为 true、Value 为 nil（清空）。
type Optional[T any] struct {
	Set   bool
	Value *T
}

func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// Some 有值的 Optional（测试与内部调用使用）。
func Some[T any](v T) Optional[T] { return Optional[T]{Set: true, Value: &v} }
