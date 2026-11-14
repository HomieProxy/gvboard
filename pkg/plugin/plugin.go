// Package plugin Gvboard 编译期插件 SDK（决策 D5、D56）。
//
// 编写插件：实现 Plugin 接口，在 init 中调用 Register，然后在 cmd/gvboard 或 plugins/all.go 中
// 以 import _ "你的插件包" 引入并重新编译。插件在管理端「插件」中启用、停用与配置。
//
//	type Hello struct{}
//
//	func (Hello) Info() plugin.Info { return plugin.Info{Code: "hello", Name: "示例", Version: "1.0.0"} }
//
//	func (Hello) Setup(h plugin.Host) error {
//		h.On(plugin.EventOrderPaid, func(ctx context.Context, e plugin.Event) error { ... })
//		h.GuestRoutes().GET("/ping", func(c *gin.Context) { plugin.OK(c, "pong") })
//		return nil
//	}
//
//	func init() { plugin.Register(Hello{}) }
package plugin

import (
	"context"
	"sync"
)

// Info 插件信息。Code 为插件标识：小写字母开头，只能包含小写字母、数字、下划线、短横线，2～32 个字符，
// 用于路由路径（{API 前缀}/plugin/{Code}/）与数据存储，发布后不要修改。
type Info struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

// Plugin 编译期插件。
type Plugin interface {
	Info() Info
	// Setup 进程启动时调用一次（无论是否启用），在其中注册事件、过滤器、路由、定时任务、支付方式与订阅格式。
	// 注册的功能只在插件启用后生效。返回错误时插件标记为加载失败，不能启用。
	Setup(h Host) error
}

// Configurable 可选接口：插件配置项，管理端据此生成配置表单。
type Configurable interface {
	Fields() []Field
}

// Enabler 可选接口：管理员启用插件时调用（只在处理请求的实例上调用一次），返回错误时不启用。
type Enabler interface {
	Enable(ctx context.Context, h Host) error
}

// Disabler 可选接口：管理员停用插件后调用，错误只记录日志。
type Disabler interface {
	Disable(ctx context.Context, h Host) error
}

var (
	regMu      sync.Mutex
	registered []Plugin
)

// Register 注册插件，在插件包的 init 中调用。
func Register(p Plugin) {
	regMu.Lock()
	defer regMu.Unlock()
	registered = append(registered, p)
}

// Registered 已注册的全部插件。
func Registered() []Plugin {
	regMu.Lock()
	defer regMu.Unlock()
	return append([]Plugin(nil), registered...)
}
