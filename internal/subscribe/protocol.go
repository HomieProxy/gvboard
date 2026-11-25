package subscribe

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"gvboard/internal/model"
	"gvboard/internal/phpx"
)

// Response 订阅下发的响应。
type Response struct {
	ContentType string
	Headers     [][2]string
	Body        []byte
}

// htmlType 原项目返回字符串时 Laravel 使用的默认类型。
const htmlType = "text/html; charset=UTF-8"

func text(body string, headers ...[2]string) *Response {
	return &Response{ContentType: htmlType, Headers: headers, Body: []byte(body)}
}

// Ctx 生成订阅内容所需的数据。Sub 对应原项目协议类中的 $user。
type Ctx struct {
	context.Context
	svc     *Service
	Sub     *model.Subscription
	Servers []*phpx.Arr
	AppName string
	AppURL  string
	// Host 当前请求的 Host（原项目 $_SERVER['HTTP_HOST']），BaseURL 为协议 + Host。
	Host    string
	BaseURL string
}

// expire 到期时间，null 表示长期有效。
func (c *Ctx) expire() any {
	if c.Sub.ExpiredAt == nil {
		return nil
	}
	return *c.Sub.ExpiredAt
}

// userinfo 客户端通用的 subscription-userinfo 头。
func (c *Ctx) userinfo() string {
	return "upload=" + phpx.Str(c.Sub.U) + "; download=" + phpx.Str(c.Sub.D) + "; total=" + phpx.Str(c.Sub.TransferEnable) + "; expire=" + phpx.Str(c.expire())
}

// subscribeURL 当前订阅的订阅链接（Surge / Surfboard 托管配置使用）。
func (c *Ctx) subscribeURL() (string, error) {
	return c.svc.Linker.URL(c, c.Sub, c.BaseURL)
}

// UserInfo 客户端通用的 subscription-userinfo 响应头的值（供插件格式使用）。
func (c *Ctx) UserInfo() string { return c.userinfo() }

// SubscribeURL 当前订阅的订阅链接（供插件格式使用）。
func (c *Ctx) SubscribeURL() (string, error) { return c.subscribeURL() }

// Protocol 一种客户端订阅格式。Flag 为 User-Agent（或 flag 参数）中用于识别客户端的子串（小写）。
type Protocol struct {
	Name   string
	Flag   string
	Handle func(c *Ctx) (*Response, error)
	// RemoteCert 为 true 时，证书模式为 remote 的 v2node TLS 节点即使没有证书指纹也允许不安全证书。
	RemoteCert bool
}

type extProtocol struct {
	p       *Protocol
	enabled func() bool
}

var (
	extMu sync.RWMutex
	exts  []extProtocol
)

// RegisterExt 注册插件提供的订阅格式，enabled 为 true 时优先于内置格式匹配。名称不能与已有格式重复。
func RegisterExt(p *Protocol, enabled func() bool) error {
	if p == nil || p.Handle == nil || enabled == nil {
		return errors.New("subscribe: 订阅格式与处理函数不能为空")
	}
	if p.Name == "" || len(p.Name) > 32 || p.Flag == "" || p.Flag != strings.ToLower(p.Flag) {
		return fmt.Errorf("subscribe: 订阅格式 %q 的名称须为 1-32 个字符，标识不能为空且必须为小写", p.Name)
	}
	extMu.Lock()
	defer extMu.Unlock()
	if strings.EqualFold(p.Name, clientApp) {
		return fmt.Errorf("subscribe: 订阅格式 %q 与内置格式重复", p.Name)
	}
	for _, x := range slices.Concat(builtin(), []*Protocol{protoSingbox, protoSingboxOld, protoGeneral}) {
		if strings.EqualFold(x.Name, p.Name) {
			return fmt.Errorf("subscribe: 订阅格式 %q 与内置格式重复", p.Name)
		}
	}
	for _, x := range exts {
		if strings.EqualFold(x.p.Name, p.Name) {
			return fmt.Errorf("subscribe: 订阅格式 %q 重复注册", p.Name)
		}
	}
	exts = append(exts, extProtocol{p: p, enabled: enabled})
	return nil
}

// UnregisterExt 注销插件提供的订阅格式。
func UnregisterExt(p *Protocol) {
	extMu.Lock()
	defer extMu.Unlock()
	exts = slices.DeleteFunc(exts, func(x extProtocol) bool { return x.p == p })
}

// extProtocols 当前可用的插件订阅格式。
func extProtocols() []*Protocol {
	extMu.RLock()
	defer extMu.RUnlock()
	var out []*Protocol
	for _, x := range exts {
		if x.enabled() {
			out = append(out, x.p)
		}
	}
	return out
}

// builtin 内置格式，顺序与原项目一致（按 Protocols 目录文件名倒序依次匹配）。
func builtin() []*Protocol {
	return []*Protocol{
		{Name: "v2RayTun", Flag: "v2raytun", Handle: v2RayTun},
		{Name: "V2rayNG", Flag: "v2rayng", Handle: general},
		{Name: "V2rayN", Flag: "v2rayn", Handle: general},
		{Name: "Surge", Flag: "surge", Handle: surge},
		{Name: "Surfboard", Flag: "surfboard", Handle: surfboard},
		{Name: "Stash", Flag: "stash", Handle: stash},
		{Name: "Shadowsocks", Flag: "shadowsocks", Handle: sip008},
		{Name: "Shadowrocket", Flag: "shadowrocket", Handle: shadowrocket},
		{Name: "SagerNet", Flag: "sagernet", Handle: sagerNet},
		{Name: "SSRPlus", Flag: "ssrplus", Handle: general},
		{Name: "QuantumultX", Flag: "quantumult%20x", Handle: quantumultX},
		{Name: "Passwall", Flag: "passwall", Handle: general},
		{Name: "Loon", Flag: "loon", Handle: loon},
		{Name: "General", Flag: "general", Handle: general},
		{Name: "ClashVerge", Flag: "verge", Handle: clashMeta(metaVerge), RemoteCert: true},
		{Name: "ClashNyanpasu", Flag: "nyanpasu", Handle: clashMeta(metaNyanpasu), RemoteCert: true},
		{Name: "ClashMeta", Flag: "meta", Handle: clashMeta(metaMeta), RemoteCert: true},
		{Name: "Clash", Flag: "clash", Handle: clash, RemoteCert: true},
	}
}

var (
	protoGeneral    = &Protocol{Name: "General", Flag: "general", Handle: general}
	protoSingbox    = &Protocol{Name: "Singbox", Flag: "sing", Handle: singbox(false), RemoteCert: true}
	protoSingboxOld = &Protocol{Name: "SingboxOld", Flag: "sing", Handle: singbox(true), RemoteCert: true}
	singVersion     = regexp.MustCompile(`(?i)sing-box\s+([0-9.]+)`)
)

// selectProtocol 按原项目 ClientController::subscribe 的规则选择格式；
// info 返回是否需要插入订阅信息伪节点（flag 不含 "sing" 或以 "sing" 开头时）。
func selectProtocol(list []*Protocol, flag string) (p *Protocol, info bool) {
	if flag == "" || flag == "0" {
		return protoGeneral, false
	}
	if strings.Index(flag, "sing") <= 0 {
		info = true
		for _, x := range list {
			if strings.Contains(flag, x.Flag) {
				p = x
				break
			}
		}
	}
	if p == nil && strings.Contains(flag, "sing") {
		version := ""
		if m := singVersion.FindStringSubmatch(flag); m != nil {
			version = m[1]
		}
		if version != "" && version >= "1.12.0" {
			p = protoSingbox
		} else {
			p = protoSingboxOld
		}
	}
	if p == nil {
		p = protoGeneral
	}
	return p, info
}

// addInfoServers 原项目 setSubscribeInfoToServers：复制第一个节点，依次插入
// "套餐到期"、"距离下次重置剩余"（有重置日时）、"剩余流量" 三个伪节点，最终顺序为剩余流量在最前。
func addInfoServers(servers []*phpx.Arr, sub *model.Subscription, resetDay *int) []*phpx.Arr {
	if len(servers) == 0 {
		return servers
	}
	remaining := phpx.TrafficConvert(sub.TransferEnable - (sub.U + sub.D))
	expired := "长期有效"
	if sub.ExpiredAt != nil && *sub.ExpiredAt != 0 {
		expired = phpx.Date("Y-m-d", *sub.ExpiredAt)
	}
	prepend := func(name string) {
		c := servers[0].Clone()
		c.Set("name", name)
		servers = append([]*phpx.Arr{c}, servers...)
	}
	prepend("套餐到期：" + expired)
	if resetDay != nil && *resetDay != 0 {
		prepend("距离下次重置剩余：" + phpx.Str(int64(*resetDay)) + " 天")
	}
	prepend("剩余流量：" + remaining)
	return servers
}

// markCertPin 对应原项目 ClientController::subscribe：有证书指纹的 TLS 节点（remote 为 true 时还包括
// 证书模式为 remote 的 v2node 节点）规范指纹并允许不安全证书，不支持证书固定的客户端仍可连接自签名证书节点。
func markCertPin(servers []*phpx.Arr, remote bool) {
	for i, s := range servers {
		typ := typeOf(s)
		if typ == "shadowsocks" || phpx.Int(phpx.Coalesce(s.Get("tls"), int64(1))) != 1 {
			continue
		}
		ts := arr(phpx.Coalesce(s.Get("tls_settings"), s.Get("tlsSettings")))
		pin, _ := ts.Get("pinned_peer_cert_sha256").(string)
		isRemote := s.Get("type") == "v2node" && ts.Get("cert_mode") == "remote"
		if strings.TrimSpace(pin) == "" && !(remote && isRemote) {
			continue
		}
		c := s.Clone()
		ts = ts.Clone()
		if fp := normPin(pin); fp != "" {
			ts.Set("pinned_peer_cert_sha256", fp)
		}
		ts.Set("allow_insecure", int64(1))
		if typ == "vmess" {
			ts.Set("allowInsecure", int64(1))
		}
		c.Set("tls_settings", ts)
		for _, k := range []string{"allow_insecure", "insecure"} {
			if _, ok := c.Lookup(k); ok {
				c.Set(k, int64(1))
			}
		}
		servers[i] = c
	}
}

var pinPattern = regexp.MustCompile(`\A[0-9a-f]{64}\z`)

// normPin 证书 SHA-256 指纹规范为 64 位小写十六进制（可带冒号），格式不对时返回空串。
func normPin(v any) string {
	p, ok := v.(string)
	if !ok {
		return ""
	}
	p = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), ":", ""))
	if !pinPattern.MatchString(p) {
		return ""
	}
	return p
}

// certPin Helper::getTlsCertificateFingerprint：TLS（非 REALITY）节点的证书指纹。
func certPin(s *phpx.Arr) string {
	if phpx.Int(phpx.Coalesce(s.Get("tls"), int64(1))) != 1 {
		return ""
	}
	return normPin(phpx.Get(phpx.Coalesce(s.Get("tls_settings"), s.Get("tlsSettings")), "pinned_peer_cert_sha256"))
}

// withPin ClashMeta::withCertificateFingerprint：Clash 系配置写入证书指纹（键名由客户端决定）并跳过 CA 校验。
func withPin(a, s *phpx.Arr, key string) *phpx.Arr {
	if a.Get("type") == "ss" {
		return a
	}
	if fp := certPin(s); fp != "" {
		a.Set(key, fp)
		a.Set("skip-cert-verify", true)
	}
	return a
}

// appendPin Helper::appendTlsCertificateFingerprint：Surge / Loon 等文本配置追加 key=指纹。
func appendPin(cfg []string, s *phpx.Arr, key string) []string {
	if fp := certPin(s); fp != "" {
		cfg = append(cfg, key+"="+fp)
	}
	return cfg
}
