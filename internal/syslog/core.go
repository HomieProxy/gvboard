package syslog

import (
	"go.uber.org/zap/zapcore"

	"gvboard/internal/model"
)

// 请求信息字段名（middleware.Logger 注入请求级日志），写入 v2_log 对应列而不是 context。
const (
	FieldMethod = "req_method"
	FieldURI    = "req_uri"
	FieldIP     = "req_ip"
	FieldHost   = "req_host"
)

// levelName 与原项目（Monolog）的级别名一致。
func levelName(l zapcore.Level) string {
	switch {
	case l >= zapcore.DPanicLevel:
		return "CRITICAL"
	case l == zapcore.ErrorLevel:
		return "ERROR"
	case l == zapcore.WarnLevel:
		return "WARNING"
	case l == zapcore.InfoLevel:
		return "INFO"
	}
	return "DEBUG"
}

// core 把达到级别的日志写入 v2_log。字段中的敏感项脱敏。
type core struct {
	w      *Writer
	min    zapcore.Level
	fields []zapcore.Field
}

// Core 返回写入系统日志的 zap Core，记录 min 及以上级别。
func (w *Writer) Core(min zapcore.Level) zapcore.Core { return &core{w: w, min: min} }

func (c *core) Enabled(l zapcore.Level) bool { return l >= c.min }

func (c *core) With(fs []zapcore.Field) zapcore.Core {
	cp := *c
	cp.fields = append(append([]zapcore.Field{}, c.fields...), fs...)
	return &cp
}

func (c *core) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(e.Level) {
		return ce.AddCore(e, c)
	}
	return ce
}

func (c *core) Write(e zapcore.Entry, fs []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.fields {
		f.AddTo(enc)
	}
	for _, f := range fs {
		f.AddTo(enc)
	}
	l := model.Log{Title: e.Message, Level: ptr(levelName(e.Level)), CreatedAt: e.Time.Unix()}
	take := func(key string) string {
		v, _ := enc.Fields[key].(string)
		delete(enc.Fields, key)
		return v
	}
	l.Method, l.URI = take(FieldMethod), take(FieldURI)
	if ip := take(FieldIP); ip != "" {
		l.IP = ptr(ip)
	}
	if host := take(FieldHost); host != "" {
		l.Host = ptr(host)
	}
	ctx := Mask(map[string]any(enc.Fields)).(map[string]any)
	if e.LoggerName != "" {
		ctx["logger"] = e.LoggerName
	}
	if e.Caller.Defined {
		ctx["caller"] = e.Caller.TrimmedPath()
	}
	if e.Stack != "" {
		ctx["stack"] = e.Stack
	}
	l.Context = jsonText(ctx)
	c.w.Add(l)
	return nil
}

func (c *core) Sync() error { return nil }
