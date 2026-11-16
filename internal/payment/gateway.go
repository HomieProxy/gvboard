// Package payment 支付网关与支付方式管理，对应原项目 PaymentService、app/Payments/*、
// 用户端 / 访客 / 管理端的支付相关控制器。
package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gvboard/internal/httpx"
)

// Field 后台配置表单字段（原项目 form() 的一项）。Type 为 alert 时只展示 Content。
type Field struct {
	Key         string
	Label       string
	Description string
	Type        string
	Content     string
	Value       any
}

// Form 有序的配置表单，序列化为与原项目相同的对象结构（保持字段顺序）。
type Form []Field

func input(key, label, desc string) Field {
	return Field{Key: key, Label: label, Description: desc, Type: "input"}
}

func (f Form) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, x := range f {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(x.Key)
		b.Write(k)
		b.WriteByte(':')
		m := map[string]any{"type": x.Type}
		if x.Type == "alert" {
			m["content"] = x.Content
		} else {
			m["label"], m["description"] = x.Label, x.Description
		}
		if x.Value != nil {
			m["value"] = x.Value
		}
		v, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Config 支付方式配置（v2_payment.config）。
type Config map[string]any

// Str 取配置的字符串形式，未配置时为空字符串。
func (c Config) Str(key string) string { return phpStr(c[key]) }

// Has 配置项存在且不为 nil（PHP isset）。
func (c Config) Has(key string) bool { return c[key] != nil }

// Order 发起支付所需的订单信息，TotalAmount 已包含手续费（分）。
type Order struct {
	TradeNo     string
	TotalAmount int64
	UserID      int64
	Email       string
	StripeToken string
	NotifyURL   string
	ReturnURL   string
	UserAgent   string
	ClientIP    string
}

// Result 支付结果：Type 0 展示二维码、1 跳转链接、2 已直接扣款（与原项目一致）。
type Result struct {
	Type int
	Data any
}

// Notification 支付平台回调内容。
type Notification struct {
	Params Params
	Body   []byte
	Header http.Header
}

// Paid 验签通过的已支付回调。Reply 为返回给支付平台的内容，为空时返回 success；
// Amount 不为 nil 时必须等于订单应付金额（订单金额 + 手续费，分）。
type Paid struct {
	TradeNo    string
	CallbackNo string
	Reply      string
	Amount     *int64
}

// SecretKeys 可选接口：回调验签依赖的配置项，任一为空时直接拒绝回调
// （否则签名退化为不带密钥的摘要，任何人都能伪造）。
type SecretKeys interface {
	SecretKeys() []string
}

// Env 网关运行环境。
type Env struct {
	Ctx     context.Context
	Config  Config
	HTTP    *http.Client
	Now     time.Time
	AppName string
}

// Gateway 支付网关。Notify 返回错误表示验签失败或非成功状态，回调统一按失败处理。
type Gateway interface {
	Form() Form
	Pay(e *Env, o *Order) (*Result, error)
	Notify(e *Env, n *Notification) (*Paid, error)
}

type regEntry struct {
	g       Gateway
	enabled func() bool // 插件提供的网关：插件停用时为 false
	alias   bool        // 兼容旧名称：可按名称查找，不出现在可选列表中
}

func (e regEntry) available() bool { return e.enabled == nil || e.enabled() }

var (
	regMu    sync.RWMutex
	registry = map[string]regEntry{}
)

// Register 注册内置支付网关。名称写入 v2_payment.payment，最长 16 个字符。
func Register(name string, g Gateway) {
	if err := register(name, g, nil); err != nil {
		panic(err)
	}
}

// registerAlias 为已注册的内置网关登记旧名称，已使用旧名称的支付方式仍可下单、回调与编辑。
func registerAlias(alias, name string) {
	regMu.Lock()
	defer regMu.Unlock()
	e, ok := registry[name]
	if _, dup := registry[alias]; !ok || dup || len(alias) > 16 {
		panic("payment: 别名注册失败: " + alias)
	}
	e.alias = true
	registry[alias] = e
}

// RegisterExt 注册插件提供的支付网关，enabled 返回 false 时该网关视为不存在。
func RegisterExt(name string, g Gateway, enabled func() bool) error {
	if enabled == nil {
		return errors.New("payment: enabled 不能为空")
	}
	return register(name, g, enabled)
}

func register(name string, g Gateway, enabled func() bool) error {
	if name == "" || len(name) > 16 {
		return errors.New("payment: 网关名称必须为 1-16 个字符: " + name)
	}
	if g == nil {
		return errors.New("payment: 网关不能为空: " + name)
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := registry[name]; ok {
		return errors.New("payment: 网关重复注册: " + name)
	}
	registry[name] = regEntry{g: g, enabled: enabled}
	return nil
}

// UnregisterExt 注销插件提供的支付网关（内置网关不受影响）。
func UnregisterExt(name string) {
	regMu.Lock()
	defer regMu.Unlock()
	if e, ok := registry[name]; ok && e.enabled != nil {
		delete(registry, name)
	}
}

// Lookup 按名称查找可用的网关。
func Lookup(name string) (Gateway, bool) {
	regMu.RLock()
	e, ok := registry[name]
	regMu.RUnlock()
	if !ok || !e.available() {
		return nil, false
	}
	return e.g, true
}

// Names 全部可用网关名称，顺序与原项目一致：名称含 Paytaro 的按逆序排在最前，其余按字节序。
func Names() []string {
	regMu.RLock()
	all := make([]string, 0, len(registry))
	for k, e := range registry {
		if e.available() && !e.alias {
			all = append(all, k)
		}
	}
	regMu.RUnlock()
	sort.Strings(all)
	var head, tail []string
	for _, n := range all {
		if strings.Contains(n, "Paytaro") {
			head = append([]string{n}, head...)
		} else {
			tail = append(tail, n)
		}
	}
	return append(head, tail...)
}

// errVerify 回调验签失败或状态不是已支付。
var errVerify = errors.New("payment: 回调校验未通过")

// gatewayError 发起支付失败，消息原样（可翻译时翻译）返回给用户，与原项目 abort(500, $message) 一致。
func gatewayError(msg string) error {
	return &httpx.Error{Status: http.StatusInternalServerError, Code: httpx.CodeInternal, Msg: msg}
}

var (
	reEPayMobile    = regexp.MustCompile(`(?i)mobile|android|iphone|ipad`)
	rePaytaroMobile = regexp.MustCompile(`(?i)Android|iPhone|iPad|iPod|Mobile|HarmonyOS`)
)
