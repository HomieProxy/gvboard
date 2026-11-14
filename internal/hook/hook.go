// Package hook 业务事件与过滤器的接入点（插件系统，决策 D56）。
// 业务包只依赖本包，由插件管理器实现 Hooks；未启用插件系统时 Hooks 为 nil，所有调用都是空操作。
package hook

import (
	"context"
	"errors"
)

// Hooks 插件管理器实现的接口。
type Hooks interface {
	// Emit 业务事件（必须在事务提交后调用），由插件在队列中异步处理，不影响业务。
	Emit(ctx context.Context, event string, data any)
	// Filter 依次交给已启用插件修改 value 并返回结果；某个插件出错、超时或结果未通过 check 时跳过该插件。
	Filter(ctx context.Context, name string, value any, check func(old, new any) error) any
}

// Emit 发出事件，h 为 nil 时忽略。
func Emit(h Hooks, ctx context.Context, event string, data any) {
	if h != nil {
		h.Emit(ctx, event, data)
	}
}

// Filter 带类型的过滤器。Check 校验插件返回的结果，不通过时忽略该插件的修改。
type Filter[T any] struct {
	Name  string
	Check func(old, new T) error
}

// Apply 执行过滤器，h 为 nil 时原样返回。
func Apply[T any](h Hooks, ctx context.Context, f Filter[T], v T) T {
	if h == nil {
		return v
	}
	check := func(old, new any) error {
		o, ok1 := old.(T)
		n, ok2 := new.(T)
		if !ok1 || !ok2 {
			return errors.New("过滤器返回值类型错误")
		}
		if f.Check != nil {
			return f.Check(o, n)
		}
		return nil
	}
	if out, ok := h.Filter(ctx, f.Name, v, check).(T); ok {
		return out
	}
	return v
}
