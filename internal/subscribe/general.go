package subscribe

import (
	"strings"

	"gvboard/internal/phpx"
)

// general General / V2rayN / V2rayNG / Passwall / SSRPlus：全部节点分享链接拼接后 base64。
func general(c *Ctx) (*Response, error) {
	var b strings.Builder
	for _, s := range c.Servers {
		b.WriteString(buildURI(c.Sub.UUID, s))
	}
	return text(phpx.Base64(b.String())), nil
}

// sagerNet 与 General 相同，但跳过 Hysteria（v1/v2 旧表）节点。
func sagerNet(c *Ctx) (*Response, error) {
	var b strings.Builder
	for _, s := range c.Servers {
		if s.Get("type") == "hysteria" {
			continue
		}
		b.WriteString(buildURI(c.Sub.UUID, s))
	}
	return text(phpx.Base64(b.String())), nil
}

// v2RayTun 内容同 General，另带客户端识别的标题、流量与更新间隔头。
func v2RayTun(c *Ctx) (*Response, error) {
	r, _ := general(c)
	var info []string
	info = append(info, "upload="+phpx.Str(c.Sub.U), "download="+phpx.Str(c.Sub.D), "total="+phpx.Str(c.Sub.TransferEnable))
	if c.Sub.ExpiredAt != nil {
		info = append(info, "expire="+phpx.Str(*c.Sub.ExpiredAt))
	}
	r.Headers = [][2]string{
		{"profile-title", c.AppName},
		{"subscription-userinfo", strings.Join(info, "; ")},
		{"profile-update-interval", "24"},
		{"Content-Disposition", `attachment; filename="` + c.AppName + `"`},
	}
	return r, nil
}

// sip008 Shadowsocks SIP008 JSON，只含 4 种 AEAD 加密的 Shadowsocks 节点。
func sip008(c *Ctx) (*Response, error) {
	servers := phpx.New()
	for _, s := range c.Servers {
		if s.Get("type") == "shadowsocks" && phpx.StrIn(s.Get("cipher"), "aes-128-gcm", "aes-256-gcm", "aes-192-gcm", "chacha20-ietf-poly1305") {
			servers.Push(phpx.Map(
				"id", s.Get("id"),
				"remarks", s.Get("name"),
				"server", s.Get("host"),
				"server_port", s.Get("port"),
				"password", c.Sub.UUID,
				"method", s.Get("cipher"),
			))
		}
	}
	used := c.Sub.U + c.Sub.D
	subs := phpx.Map(
		"servers", servers,
		"bytes_used", used,
		"bytes_remaining", c.Sub.TransferEnable-used,
		"version", int64(1),
	)
	return text(string(phpx.JSONEncode(subs, phpx.JSONUnescapedSlashes|phpx.JSONPretty))), nil
}

// shadowrocket 首行为流量与到期信息，VMess 使用 Shadowrocket 专用格式，其余同 General。
func shadowrocket(c *Ctx) (*Response, error) {
	expired := "长期有效"
	if c.Sub.ExpiredAt != nil {
		expired = phpx.Date("Y-m-d", *c.Sub.ExpiredAt)
	}
	var b strings.Builder
	b.WriteString("STATUS=🚀↑:" + phpx.FloatStr(phpx.GB(c.Sub.U)) + "GB,↓:" + phpx.FloatStr(phpx.GB(c.Sub.D)) + "GB,TOT:" + phpx.FloatStr(phpx.GB(c.Sub.TransferEnable)) + "GB💡Expires:" + expired + "\r\n")
	for _, s := range c.Servers {
		if s.Get("type") == "vmess" || (s.Get("type") == "v2node" && s.Get("protocol") == "vmess") {
			b.WriteString(shadowrocketVmess(c.Sub.UUID, s))
		} else {
			b.WriteString(buildURI(c.Sub.UUID, s))
		}
	}
	return text(phpx.Base64(b.String())), nil
}

func shadowrocketVmess(uuid string, s *phpx.Arr) string {
	userinfo := phpx.Base64("auto:" + uuid + "@" + s.Str("host") + ":" + s.Str("port"))
	cfg := phpx.Map("tfo", int64(1), "remark", s.Get("name"), "alterId", int64(0))
	var ts any
	hasTLS := false
	if phpx.Truthy(s.Get("tls")) {
		cfg.Set("tls", int64(1))
		ts = phpx.Coalesce(s.Get("tls_settings"), s.Get("tlsSettings"), phpx.New())
		hasTLS = true
		cfg.Set("allowInsecure", phpx.Int(phpx.Coalesce(phpx.Get(ts, "allow_insecure"), phpx.Get(ts, "allowInsecure"), int64(0))))
		cfg.Set("peer", phpx.Coalesce(phpx.Get(ts, "server_name"), phpx.Get(ts, "serverName"), ""))
	}
	ns := phpx.Coalesce(s.Get("network_settings"), s.Get("networkSettings"), phpx.New())
	g := func(path ...any) any { return phpx.Get(ns, path...) }
	switch s.Get("network") {
	case "tcp":
		if !phpx.Empty(g("header", "type")) {
			cfg.Set("obfs", g("header", "type"))
		}
		if !phpx.Empty(g("header", "request", "path", 0)) {
			cfg.Set("path", g("header", "request", "path", 0))
		}
		if g("header", "request", "headers", "Host", 0) != nil {
			cfg.Set("obfsParam", g("header", "request", "headers", "Host", 0))
		}
	case "ws":
		cfg.Set("obfs", "websocket")
		if !phpx.Empty(g("path")) {
			cfg.Set("path", g("path"))
		}
		if !phpx.Empty(g("headers", "Host")) {
			cfg.Set("obfsParam", g("headers", "Host"))
		}
		if g("security") != nil {
			cfg.Set("method", g("security"))
		}
	case "grpc":
		cfg.Set("obfs", "grpc")
		if !phpx.Empty(g("serviceName")) {
			cfg.Set("path", g("serviceName"))
		}
		if hasTLS {
			cfg.Set("host", phpx.Coalesce(phpx.Get(ts, "server_name"), phpx.Get(ts, "serverName"), ""))
		} else {
			cfg.Set("host", s.Get("host"))
		}
	}
	return "vmess://" + userinfo + "?" + phpx.HTTPBuildQuery(cfg, true) + "\r\n"
}
