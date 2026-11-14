package subscribe

import (
	"strings"

	"gvboard/internal/phpx"
)

// 对应原项目 Protocols/Singbox/Singbox.php（sing-box 1.12+）与 SingboxOld.php（旧版本）。
// 旧版差异：无 domain_resolver、无 ECH、不支持 AnyTLS、Hysteria 只取首个端口。

func singbox(old bool) func(*Ctx) (*Response, error) {
	return func(c *Ctx) (*Response, error) {
		tpl := tplSingbox
		if old {
			tpl = tplSingboxOld
		}
		config, err := c.jsonTemplate(tpl)
		if err != nil {
			return nil, err
		}
		b := sbBuilder{old: old, uuid: c.Sub.UUID, speedLimit: c.Sub.SpeedLimit}
		var proxies []*phpx.Arr
		for _, s := range c.Servers {
			typ := s.Get("type")
			if typ == "v2node" {
				typ = s.Get("protocol")
			}
			var p *phpx.Arr
			switch typ {
			case "shadowsocks":
				p = b.ss(s)
			case "trojan":
				p = b.trojan(s)
			case "vmess":
				p = b.vmess(s)
			case "vless":
				p = b.vless(s)
			case "tuic":
				p = b.tuic(s)
			case "anytls":
				if old {
					continue
				}
				p = b.anytls(s)
			case "hysteria":
				p = b.hysteria(s)
			case "hysteria2":
				p = b.hysteria2(s)
			default:
				continue
			}
			proxies = append(proxies, p)
		}
		tags := phpx.Column(proxies, "tag")
		outbounds := arr(config.Get("outbounds"))
		for _, o := range outbounds.Values() {
			ob, ok := o.(*phpx.Arr)
			if !ok {
				continue
			}
			typ, tag := ob.Get("type"), ob.Get("tag")
			if (typ == "selector" && tag == "节点选择") || (typ == "urltest" && tag == "自动选择") ||
				(typ == "selector" && strings.HasPrefix(phpx.Str(tag), "#")) {
				list, ok := ob.Get("outbounds").(*phpx.Arr)
				if !ok {
					list = phpx.New()
					ob.Set("outbounds", list)
				}
				for _, t := range tags.Values() {
					list.Push(t)
				}
			}
		}
		merged := phpx.Merge(outbounds)
		for _, p := range proxies {
			merged.Push(p)
		}
		config.Set("outbounds", merged)
		return &Response{
			ContentType: "application/json",
			Headers: [][2]string{
				{"subscription-userinfo", c.userinfo()},
				{"profile-update-interval", "24"},
				{"Profile-Title", "base64:" + phpx.Base64(c.AppName)},
				{"Content-Disposition", `attachment; filename="` + c.AppName + `"`},
			},
			Body: phpx.JSONEncode(config, phpx.JSONUnescapedSlashes),
		}, nil
	}
}

type sbBuilder struct {
	old        bool
	uuid       string
	speedLimit *int64
}

// resolver sing-box 1.12+ 的节点均带 domain_resolver。
func (b sbBuilder) resolver(a *phpx.Arr) {
	if !b.old {
		a.Set("domain_resolver", "local")
	}
}

// ech 与 Meta 系相同的 ECH 设置，字段名为 sing-box 写法；旧版不支持。
func (b sbBuilder) ech(tls *phpx.Arr, ts any) {
	if b.old {
		return
	}
	ech := phpx.Get(ts, "ech")
	if phpx.Empty(ech) {
		return
	}
	if ech == "cloudflare" {
		tls.Set("ech", phpx.Map("enabled", true, "query_server_name", "cloudflare-ech.com"))
	} else if ech == "custom" && !phpx.Empty(phpx.Get(ts, "ech_config")) {
		cfg := phpx.Get(ts, "ech_config")
		if _, ok := cfg.(*phpx.Arr); !ok {
			cfg = phpx.List(cfg)
		}
		tls.Set("ech", phpx.Map("enabled", true, "config", cfg))
	}
}

func (b sbBuilder) ss(s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"tag", s.Get("name"),
		"type", "shadowsocks",
		"server", s.Get("host"),
		"server_port", s.Get("port"),
		"method", s.Get("cipher"),
		"password", ssPassword(b.uuid, s, true),
	)
	b.resolver(a)
	if s.Get("obfs") == "http" {
		parts := []string{"obfs=" + s.Str("obfs")}
		if s.Isset("obfs-host") {
			parts = append(parts, "obfs-host="+s.Str("obfs-host"))
		}
		if s.Isset("obfs-path") {
			parts = append(parts, "path="+s.Str("obfs-path"))
		}
		a.Set("plugin", "obfs-local")
		a.Set("plugin_opts", strings.Join(parts, ";"))
	} else if phpx.LooseEq(s.Get("network"), "http") && s.Isset("network_settings", "Host") {
		ns := s.Get("network_settings")
		a.Set("plugin", "obfs-local")
		a.Set("plugin_opts", "obfs=http;obfs-host="+phpx.Str(phpx.Get(ns, "Host"))+";path="+phpx.Str(phpx.Coalesce(phpx.Get(ns, "path"), "/")))
	}
	return a
}

// transportTCP tcp 伪装为 http 时的 transport。
func transportTCP(a *phpx.Arr, ns any) {
	if t := phpx.Get(ns, "header", "type"); t != nil && phpx.LooseEq(t, "http") {
		a.SetPath(t, "transport", "type")
	}
	if h := phpx.Get(ns, "header", "request", "headers", "Host"); h != nil {
		a.SetPath(h, "transport", "host")
	}
	if p := phpx.Get(ns, "header", "request", "path", 0); p != nil {
		a.SetPath(p, "transport", "path")
	}
}

// transportV2node vless / anytls 的 tcp / ws / grpc 传输（只读 network_settings）。
func transportV2node(a, s *phpx.Arr) {
	ns := s.Get("network_settings")
	switch s.Get("network") {
	case "tcp":
		transportTCP(a, ns)
	case "ws":
		a.SetPath("ws", "transport", "type")
		if phpx.Truthy(ns) {
			if p := phpx.Get(ns, "path"); !phpx.Empty(p) {
				a.SetPath(p, "transport", "path")
			}
			if h := phpx.Get(ns, "headers", "Host"); !phpx.Empty(h) {
				a.SetPath(phpx.Map("Host", phpx.List(h)), "transport", "headers")
			}
			a.SetPath(int64(2048), "transport", "max_early_data")
			a.SetPath("Sec-WebSocket-Protocol", "transport", "early_data_header_name")
		}
	case "grpc":
		a.SetPath("grpc", "transport", "type")
		if phpx.Truthy(ns) {
			if sn := phpx.Get(ns, "serviceName"); sn != nil {
				a.SetPath(sn, "transport", "service_name")
			}
		}
	}
}

// vmess 决策 D40：原项目先写入 transport = []，无传输设置时输出 "transport":[]（sing-box 无法解析），这里为空时移除该键。
func (b sbBuilder) vmess(s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"tag", s.Get("name"),
		"type", "vmess",
		"server", s.Get("host"),
		"server_port", s.Get("port"),
		"uuid", b.uuid,
		"security", "auto",
		"alter_id", int64(0),
		"transport", phpx.New(),
	)
	b.resolver(a)
	if phpx.Truthy(s.Get("tls")) {
		tls := phpx.Map("enabled", true)
		ts := phpx.Coalesce(s.Get("tls_settings"), s.Get("tlsSettings"), phpx.New())
		ai := phpx.Coalesce(phpx.Get(ts, "allow_insecure"), phpx.Get(ts, "allowInsecure"), int64(0))
		if b.old {
			ai = phpx.Int(ai)
		}
		tls.Set("insecure", phpx.LooseEq(ai, int64(1)))
		tls.Set("server_name", phpx.Coalesce(phpx.Get(ts, "server_name"), phpx.Get(ts, "serverName"), ""))
		b.ech(tls, ts)
		a.Set("tls", tls)
	}
	ns := phpx.Coalesce(s.Get("networkSettings"), s.Get("network_settings"), phpx.New())
	switch s.Get("network") {
	case "tcp":
		transportTCP(a, ns)
	case "ws":
		a.SetPath("ws", "transport", "type")
		a.SetPath(phpx.Coalesce(phpx.Get(ns, "path"), "/"), "transport", "path")
		if h := phpx.Get(ns, "headers", "Host"); !phpx.Empty(h) {
			a.SetPath(phpx.Map("Host", phpx.List(h)), "transport", "headers")
		}
		a.SetPath(int64(2048), "transport", "max_early_data")
		a.SetPath("Sec-WebSocket-Protocol", "transport", "early_data_header_name")
	case "grpc":
		a.SetPath("grpc", "transport", "type")
		if sn := phpx.Get(ns, "serviceName"); sn != nil {
			a.SetPath(sn, "transport", "service_name")
		}
	}
	if t, ok := a.Get("transport").(*phpx.Arr); ok && t.Len() == 0 {
		a.Del("transport")
	}
	return a
}

func (b sbBuilder) vless(s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"type", "vless",
		"tag", s.Get("name"),
		"server", s.Get("host"),
		"server_port", s.Get("port"),
		"uuid", b.uuid,
	)
	b.resolver(a)
	a.Set("packet_encoding", "xudp")
	if phpx.Truthy(s.Get("tls")) {
		tls := phpx.Map("enabled", true)
		flow := any("")
		if !phpx.Empty(s.Get("flow")) {
			flow = s.Get("flow")
		}
		a.Set("flow", flow)
		if ts := s.Get("tls_settings"); phpx.Truthy(ts) {
			tls.Set("insecure", phpx.LooseEq(phpx.Coalesce(phpx.Get(ts, "allow_insecure"), int64(0)), int64(1)))
			tls.Set("server_name", phpx.Get(ts, "server_name"))
			if phpx.LooseEq(s.Get("tls"), int64(2)) {
				tls.Set("reality", phpx.Map("enabled", true, "public_key", phpx.Get(ts, "public_key"), "short_id", phpx.Get(ts, "short_id")))
			}
			tls.Set("utls", phpx.Map("enabled", true, "fingerprint", phpx.Coalesce(phpx.Get(ts, "fingerprint"), "chrome")))
			b.ech(tls, ts)
		}
		a.Set("tls", tls)
	}
	transportV2node(a, s)
	return a
}

func (b sbBuilder) trojan(s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"tag", s.Get("name"),
		"type", "trojan",
		"server", s.Get("host"),
		"server_port", s.Get("port"),
		"password", b.uuid,
	)
	b.resolver(a)
	ts := arr(s.Get("tls_settings"))
	tls := phpx.Map(
		"enabled", true,
		"insecure", phpx.LooseEq(phpx.Coalesce(s.Get("allow_insecure"), ts.Get("allow_insecure"), int64(0)), int64(1)),
		"server_name", phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""),
	)
	b.ech(tls, ts)
	a.Set("tls", tls)
	if s.Isset("network") && phpx.StrIn(s.Get("network"), "grpc", "ws") {
		a.SetPath(s.Get("network"), "transport", "type")
		if s.Get("network") == "grpc" && s.Isset("network_settings", "serviceName") {
			a.SetPath(s.Get("network_settings", "serviceName"), "transport", "service_name")
		}
		if s.Get("network") == "ws" {
			if b.old || s.Isset("network_settings", "path") {
				a.SetPath(phpx.Coalesce(s.Get("network_settings", "path"), "/"), "transport", "path")
			}
			if s.Isset("network_settings", "headers", "Host") {
				a.SetPath(phpx.Map("Host", phpx.List(s.Get("network_settings", "headers", "Host"))), "transport", "headers")
			}
			a.SetPath(int64(2048), "transport", "max_early_data")
			a.SetPath("Sec-WebSocket-Protocol", "transport", "early_data_header_name")
		}
	}
	return a
}

func (b sbBuilder) tuic(s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"tag", s.Get("name"),
		"type", "tuic",
		"server", s.Get("host"),
		"server_port", s.Get("port"),
		"uuid", b.uuid,
		"password", b.uuid,
		"congestion_control", phpx.Coalesce(s.Get("congestion_control"), "cubic"),
		"udp_relay_mode", phpx.Coalesce(s.Get("udp_relay_mode"), "native"),
		"zero_rtt_handshake", phpx.Truthy(s.Get("zero_rtt_handshake")),
	)
	b.resolver(a)
	ts := arr(s.Get("tls_settings"))
	a.Set("tls", phpx.Map(
		"enabled", true,
		"insecure", phpx.LooseEq(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0)), int64(1)),
		"alpn", phpx.Strings("h3"),
		"disable_sni", phpx.Truthy(s.Get("disable_sni")),
		"server_name", phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""),
	))
	return a
}

func (b sbBuilder) anytls(s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"tag", s.Get("name"),
		"type", "anytls",
		"server", s.Get("host"),
		"server_port", s.Get("port"),
		"password", b.uuid,
	)
	b.resolver(a)
	ts := arr(s.Get("tls_settings"))
	tls := phpx.Map(
		"enabled", true,
		"insecure", phpx.LooseEq(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0)), int64(1)),
		"alpn", phpx.Strings("h2", "http/1.1"),
		"server_name", phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""),
	)
	if phpx.Truthy(s.Get("tls_settings")) {
		if phpx.LooseEq(s.Get("tls"), int64(2)) {
			tls.Set("reality", phpx.Map("enabled", true, "public_key", ts.Get("public_key"), "short_id", ts.Get("short_id")))
		}
		tls.Set("utls", phpx.Map("enabled", true, "fingerprint", phpx.Coalesce(ts.Get("fingerprint"), "chrome")))
	}
	a.Set("tls", tls)
	transportV2node(a, s)
	return a
}

// phpMin PHP min(a, b)：b 小于 a 时取 b，否则取 a。
func phpMin(a, b any) any {
	if phpx.Compare(b, a) < 0 {
		return b
	}
	return a
}

func (b sbBuilder) hysteria(s *phpx.Arr) *phpx.Arr {
	tls := phpx.Map("enabled", true, "insecure", phpx.Truthy(s.Get("insecure")), "server_name", s.Get("server_name"))
	var a *phpx.Arr
	if b.old {
		a = phpx.Map("server", s.Get("host"), "server_port", phpx.Int(phpx.FirstPort(s.Get("port"))), "tls", tls)
	} else {
		a = phpx.Map("tag", s.Get("name"), "server", s.Get("host"), "domain_resolver", "local", "tls", tls)
		parts := strings.Split(s.Str("port"), ",")
		for i := range parts {
			parts[i] = strings.Trim(parts[i], " \t\n\r\x00\x0B")
		}
		if len(parts) == 1 && !strings.Contains(parts[0], "-") {
			a.Set("server_port", phpx.Int(parts[0]))
		} else {
			ports := phpx.New()
			for _, p := range parts {
				if strings.Contains(p, "-") {
					ports.Push(strings.ReplaceAll(p, "-", ":"))
				}
			}
			a.Set("server_ports", ports)
		}
	}
	version := s.Get("version")
	if version == nil || phpx.LooseEq(version, int64(1)) {
		a.Set("auth_str", b.uuid)
		if b.old {
			a.Set("tag", s.Get("name"))
		}
		a.Set("type", "hysteria")
		up, down := s.Get("down_mbps"), s.Get("up_mbps")
		if b.speedLimit != nil && *b.speedLimit != 0 {
			up, down = phpMin(up, *b.speedLimit), phpMin(down, *b.speedLimit)
		}
		a.Set("up_mbps", up)
		a.Set("down_mbps", down)
		if s.Isset("obfs") && s.Isset("obfs_password") {
			a.Set("obfs", s.Get("obfs_password"))
		}
		a.Set("disable_mtu_discovery", true)
	} else if phpx.LooseEq(version, int64(2)) {
		a.Set("password", b.uuid)
		if b.old {
			a.Set("tag", s.Get("name"))
		}
		a.Set("type", "hysteria2")
		if s.Isset("obfs") {
			a.Set("obfs", phpx.Map("type", s.Get("obfs"), "password", s.Get("obfs_password")))
		}
	}
	return a
}

func (b sbBuilder) hysteria2(s *phpx.Arr) *phpx.Arr {
	ts := arr(s.Get("tls_settings"))
	a := phpx.Map(
		"server", s.Get("host"),
		"server_port", phpx.Int(phpx.FirstPort(s.Get("port"))),
		"tls", phpx.Map(
			"enabled", true,
			"insecure", phpx.LooseEq(phpx.Coalesce(ts.Get("allow_insecure"), int64(0)), int64(1)),
			"server_name", phpx.Coalesce(ts.Get("server_name"), ""),
		),
	)
	b.resolver(a)
	a.Set("password", b.uuid)
	a.Set("tag", s.Get("name"))
	a.Set("type", "hysteria2")
	if s.Isset("obfs") {
		a.Set("obfs", phpx.Map("type", s.Get("obfs"), "password", s.Get("obfs_password")))
	}
	return a
}
