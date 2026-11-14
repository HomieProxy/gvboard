package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"go.uber.org/zap"

	"gvboard/internal/hook"
	"gvboard/internal/queue"
	sdk "gvboard/pkg/plugin"
)

// TaskEvent 插件事件任务。
const TaskEvent = "plugin_event"

type eventTask struct {
	Plugin string          `json:"plugin"`
	Event  string          `json:"event"`
	Data   json.RawMessage `json:"data"`
	Time   int64           `json:"time"`
}

var _ hook.Hooks = (*Manager)(nil)

// RegisterTask 注册插件事件任务：每个订阅了该事件的插件一个任务，失败最多重试 3 次。
// 未注册时事件在调用方同步执行（仅用于测试）。
func (m *Manager) RegisterTask(q *queue.Queue) {
	m.queue = q
	q.Register(queue.Spec{Type: TaskEvent, Queue: queue.QueuePlugin, Tries: 3, Timeout: time.Minute},
		func(ctx context.Context, raw []byte) error {
			var t eventTask
			if err := json.Unmarshal(raw, &t); err != nil {
				m.log().Error("插件事件任务参数无效", zap.Error(err))
				return nil
			}
			return m.handleEvent(ctx, t)
		})
}

// Emit 发出事件：为每个已启用且订阅了该事件的插件投递一个任务。
func (m *Manager) Emit(ctx context.Context, event string, data any) {
	var targets []string
	for _, e := range m.list() {
		if len(e.events[event]) > 0 && m.Enabled(e.code) {
			targets = append(targets, e.code)
		}
	}
	if len(targets) == 0 {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		m.log().Error("插件事件数据序列化失败", zap.String("event", event), zap.Error(err))
		return
	}
	now := m.now().Unix()
	for _, code := range targets {
		t := eventTask{Plugin: code, Event: event, Data: raw, Time: now}
		if m.queue == nil {
			if err := m.handleEvent(ctx, t); err != nil {
				m.log().Warn("插件事件处理失败", zap.String("plugin", code), zap.String("event", event), zap.Error(err))
			}
			continue
		}
		if err := m.queue.Enqueue(context.WithoutCancel(ctx), TaskEvent, t, ""); err != nil {
			m.log().Warn("投递插件事件失败", zap.String("plugin", code), zap.String("event", event), zap.Error(err))
		}
	}
}

// handleEvent 执行一个插件对某事件的全部处理函数，插件已停用时忽略。
func (m *Manager) handleEvent(ctx context.Context, t eventTask) error {
	e := m.entry(t.Plugin)
	if e == nil || !m.Enabled(t.Plugin) {
		return nil
	}
	ev := sdk.Event{Name: t.Event, Data: t.Data, Time: t.Time}
	for _, h := range e.events[t.Event] {
		if err := safeEvent(ctx, h, ev); err != nil {
			return fmt.Errorf("插件 %s 处理事件 %s 失败: %w", t.Plugin, t.Event, err)
		}
	}
	return nil
}

func safeEvent(ctx context.Context, h sdk.EventHandler, ev sdk.Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h(ctx, ev)
}

// Filter 依次执行已启用插件的过滤器（priority 升序，相同时按插件标识）。
// 某个过滤器出错、超时、panic、返回类型不同或未通过 check 时跳过该插件的修改。
func (m *Manager) Filter(ctx context.Context, name string, value any, check func(old, new any) error) any {
	var regs []filterReg
	for _, e := range m.list() {
		if len(e.filters[name]) > 0 && m.Enabled(e.code) {
			regs = append(regs, e.filters[name]...)
		}
	}
	sort.SliceStable(regs, func(i, j int) bool {
		if regs[i].priority != regs[j].priority {
			return regs[i].priority < regs[j].priority
		}
		return regs[i].code < regs[j].code
	})
	for _, r := range regs {
		out, err := m.runFilter(ctx, r, value)
		if err == nil && reflect.TypeOf(out) != reflect.TypeOf(value) {
			err = fmt.Errorf("返回值类型 %T 与 %T 不同", out, value)
		}
		if err == nil && check != nil {
			err = check(value, out)
		}
		if err != nil {
			m.log().Warn("插件过滤器执行失败，已忽略", zap.String("plugin", r.code), zap.String("filter", name), zap.Error(err))
			continue
		}
		value = out
	}
	return value
}

func (m *Manager) runFilter(ctx context.Context, r filterReg, value any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, m.filterTimeout())
	defer cancel()
	type result struct {
		v   any
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- result{err: fmt.Errorf("panic: %v", p)}
			}
		}()
		v, err := r.fn(ctx, value)
		ch <- result{v, err}
	}()
	select {
	case res := <-ch:
		return res.v, res.err
	case <-ctx.Done():
		return nil, fmt.Errorf("执行超时（%s）", m.filterTimeout())
	}
}
