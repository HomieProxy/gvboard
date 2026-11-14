package subscribe

import (
	"strings"

	"gvboard/internal/phpx"
)

// 对应原项目 Protocols/ClashMeta.php、ClashVerge.php（与 Meta 完全相同）、ClashNyanpasu.php。

type metaVariant int

const (
	metaMeta metaVariant = iota
	metaVerge
	metaNyanpasu
)

func clashMeta(v metaVariant) func(*Ctx) (*Response, error) {
	return func(c *Ctx) (*Response, error) {
		config, err := c.yamlTemplate(tplClash)
		if err != nil {
			return nil, err
		}
		var proxy []*phpx.Arr
		var names []string
		for _, s := range c.Servers {
			var p *phpx.Arr
			switch typeOf(s) {
			case "shadowsocks":
				p = metaSS(c.Sub.UUID, s)
			case "vmess":
				p = metaVmess(c.Sub.UUID, s, v)
			case "vless":
				p = metaVless(c.Sub.UUID, s, v)
			case "trojan":
				p = metaTrojan(c.Sub.UUID, s)
			case "tuic":
				p = metaTuic(c.Sub.UUID, s)
			case "anytls":
				p = metaAnyTLS(c.Sub.UUID, s)
			case "hysteria":
				p = metaHysteria(c.Sub.UUID, s)
			case "hysteria2":
				p = metaHysteria2(c.Sub.UUID, s)
			default:
				continue
			}
			proxy = append(proxy, p)
			names = append(names, s.Str("name"))
		}
		setProxies(config, proxy)
		fillGroups(config, names, false)
		body := strings.ReplaceAll(phpx.YAMLDump(config, 2, 4, true), "$app_name", c.AppName)
		return text(body,
			[2]string{"subscription-userinfo", c.userinfo()},
			[2]string{"profile-update-interval", "24"},
			[2]string{"content-disposition", "attachment;filename*=UTF-8''" + phpx.RawURLEncode(c.AppName)},
		), nil
	}
}

// echOpts 原项目 Meta 系的 ECH 设置。
func echOpts(a *phpx.Arr, ts any) {
	ech := phpx.Get(ts, "ech")
	if phpx.Empty(ech) {
		return
	}
	if ech == "cloudflare" {
		a.Set("ech-opts", phpx.Map("enable", true, "query-server-name", "cloudflare-ech.com"))
	} else if ech == "custom" && !phpx.Empty(phpx.Get(ts, "ech_config")) {
		cfg := phpx.Get(ts, "ech_config")
		if _, ok := cfg.(*phpx.Arr); !ok {
			cfg = phpx.List(cfg)
		}
		a.Set("ech-opts", phpx.Map("enable", true, "config", cfg))
	}
}

// encryption VLESS 加密串。决策 D40：原项目 '.' . $x ?? 'default' 的优先级错误导致默认值不生效，这里按本意补默认值。
func encryption(a, s *phpx.Arr) {
	if phpx.Empty(s.Get("encryption")) || phpx.Empty(s.Get("encryption_settings")) {
		return
	}
	es := s.Get("encryption_settings")
	e := s.Str("encryption")
	e += "." + phpx.Str(phpx.Coalesce(phpx.Get(es, "mode"), "native"))
	e += "." + phpx.Str(phpx.Coalesce(phpx.Get(es, "rtt"), "1rtt"))
	if !phpx.Empty(phpx.Get(es, "client_padding")) {
		e += "." + phpx.Str(phpx.Get(es, "client_padding"))
	}
	e += "." + phpx.Str(phpx.Coalesce(phpx.Get(es, "password"), ""))
	a.Set("encryption", e)
}

func metaSS(uuid string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "ss",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"cipher", s.Get("cipher"),
		"password", ssPassword(uuid, s, false),
		"udp", true,
	)
	if s.Get("obfs") == "http" {
		opts := phpx.Map("mode", "http", "host", phpx.Coalesce(s.Get("obfs-host"), ""))
		if s.Isset("obfs-path") {
			opts.Set("path", s.Get("obfs-path"))
		}
		a.Set("plugin", "obfs")
		a.Set("plugin-opts", opts)
	} else if s.Get("network") == "http" && s.Isset("network_settings", "Host") {
		ns := s.Get("network_settings")
		opts := phpx.Map("mode", "http", "host", phpx.Coalesce(phpx.Get(ns, "Host"), ""))
		if phpx.Get(ns, "path") != nil {
			opts.Set("path", phpx.Get(ns, "path"))
		}
		a.Set("plugin", "obfs")
		a.Set("plugin-opts", opts)
	}
	return a
}

func metaVmess(uuid string, s *phpx.Arr, v metaVariant) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "vmess",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"uuid", uuid,
		"alterId", int64(0),
		"cipher", "auto",
		"udp", true,
	)
	if phpx.Truthy(s.Get("tls")) {
		a.Set("tls", true)
		ts := phpx.Coalesce(s.Get("tlsSettings"), s.Get("tls_settings"))
		if phpx.Truthy(ts) {
			if ai := phpx.Get(ts, "allowInsecure"); !phpx.Empty(ai) {
				a.Set("skip-cert-verify", true)
			}
			if sn := phpx.Get(ts, "serverName"); !phpx.Empty(sn) {
				a.Set("servername", sn)
			}
			echOpts(a, ts)
		}
	}
	ns := phpx.Coalesce(s.Get("networkSettings"), s.Get("network_settings"))
	g := func(path ...any) any { return phpx.Get(ns, path...) }
	switch s.Get("network") {
	case "tcp":
		if g("header", "type") != nil && phpx.LooseEq(g("header", "type"), "http") {
			a.Set("network", g("header", "type"))
			if h := g("header", "request", "headers", "Host"); h != nil {
				a.SetPath(h, "http-opts", "headers", "Host")
			}
			if p := g("header", "request", "path"); p != nil {
				a.SetPath(p, "http-opts", "path")
			}
		}
	case "ws":
		a.Set("network", "ws")
		if phpx.Truthy(ns) {
			opts := phpx.New()
			a.Set("ws-opts", opts)
			if !phpx.Empty(g("path")) {
				opts.Set("path", g("path"))
			}
			if !phpx.Empty(g("headers", "Host")) {
				opts.Set("headers", phpx.Map("Host", g("headers", "Host")))
			}
			if g("security") != nil {
				a.Set("cipher", g("security"))
			}
		}
	case "grpc":
		a.Set("network", "grpc")
		if phpx.Truthy(ns) {
			opts := phpx.New()
			a.Set("grpc-opts", opts)
			if g("serviceName") != nil {
				opts.Set("grpc-service-name", g("serviceName"))
			}
		}
	}
	if v == metaNyanpasu {
		encryption(a, s)
	}
	return a
}

func metaVless(uuid string, s *phpx.Arr, v metaVariant) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "vless",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"uuid", uuid,
		"udp", true,
	)
	if v != metaNyanpasu && !phpx.Empty(s.Get("flow")) {
		a.Set("flow", s.Get("flow"))
	}
	if phpx.Truthy(s.Get("tls")) {
		a.Set("tls", true)
		ts := arr(s.Get("tls_settings"))
		a.Set("skip-cert-verify", phpx.LooseEq(phpx.Coalesce(ts.Get("allow_insecure"), int64(0)), int64(1)))
		if v == metaNyanpasu {
			flow := any("")
			if !phpx.Empty(s.Get("flow")) {
				flow = s.Get("flow")
			}
			a.Set("flow", flow)
		}
		fp := any("chrome")
		if !phpx.Empty(ts.Get("fingerprint")) {
			fp = ts.Get("fingerprint")
		}
		a.Set("client-fingerprint", fp)
		if ts.Len() > 0 {
			if !phpx.Empty(ts.Get("server_name")) {
				a.Set("servername", ts.Get("server_name"))
			}
			if phpx.LooseEq(s.Get("tls"), int64(2)) {
				a.Set("reality-opts", phpx.Map(
					"public-key", ts.Get("public_key"),
					"short-id", ts.Get("short_id"),
					"support-x25519mlkem768", true,
				))
			}
			echOpts(a, ts)
		}
	}
	ns := s.Get("network_settings")
	g := func(path ...any) any { return phpx.Get(ns, path...) }
	switch s.Get("network") {
	case "tcp":
		if g("header", "type") != nil && phpx.LooseEq(g("header", "type"), "http") {
			a.Set("network", g("header", "type"))
			if h := g("header", "request", "headers", "Host"); h != nil {
				a.SetPath(h, "http-opts", "headers", "Host")
			}
			if p := g("header", "request", "path"); p != nil {
				a.SetPath(p, "http-opts", "path")
			}
		}
	case "ws":
		a.Set("network", "ws")
		if phpx.Truthy(ns) {
			opts := phpx.New()
			a.Set("ws-opts", opts)
			if !phpx.Empty(g("path")) {
				opts.Set("path", g("path"))
			}
			if !phpx.Empty(g("headers", "Host")) {
				opts.Set("headers", phpx.Map("Host", g("headers", "Host")))
			}
		}
	case "grpc":
		a.Set("network", "grpc")
		if phpx.Truthy(ns) {
			opts := phpx.New()
			a.Set("grpc-opts", opts)
			if g("serviceName") != nil {
				opts.Set("grpc-service-name", g("serviceName"))
			}
		}
	case "xhttp":
		a.Set("network", "xhttp")
		if phpx.Truthy(ns) {
			opts := phpx.New()
			a.Set("xhttp-opts", opts)
			for _, k := range []string{"path", "host", "mode"} {
				if g(k) != nil {
					opts.Set(k, g(k))
				}
			}
		}
	}
	encryption(a, s)
	return a
}

func metaTrojan(password string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "trojan",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"password", password,
		"udp", true,
	)
	trojanTransport(a, s)
	ts := arr(s.Get("tls_settings"))
	a.Set("sni", phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""))
	a.Set("skip-cert-verify", phpx.LooseEq(phpx.Coalesce(s.Get("allow_insecure"), ts.Get("allow_insecure"), int64(0)), int64(1)))
	echOpts(a, ts)
	return a
}

func metaTuic(password string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "tuic",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"uuid", password,
		"password", password,
		"alpn", phpx.Strings("h3"),
		"disable-sni", phpx.Truthy(s.Get("disable_sni")),
		"reduce-rtt", phpx.Truthy(s.Get("zero_rtt_handshake")),
		"udp-relay-mode", phpx.Coalesce(s.Get("udp_relay_mode"), "native"),
		"congestion-controller", phpx.Coalesce(s.Get("congestion_control"), "cubic"),
	)
	ts := arr(s.Get("tls_settings"))
	a.Set("skip-cert-verify", phpx.LooseEq(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0)), int64(1)))
	a.Set("sni", phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""))
	return a
}

func metaAnyTLS(password string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "anytls",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"password", password,
		"client-fingerprint", "chrome",
		"udp", true,
		"alpn", phpx.Strings("h2", "http/1.1"),
	)
	ts := arr(s.Get("tls_settings"))
	a.Set("sni", phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""))
	a.Set("skip-cert-verify", phpx.LooseEq(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0)), int64(1)))
	return a
}

// setPorts 端口取首个端口，多端口/端口段时另加 ports 与 mport。
func setPorts(a, s *phpx.Arr) {
	a.Set("port", phpx.Int(phpx.FirstPort(s.Get("port"))))
	if phpx.MultiPort(s.Get("port")) {
		a.Set("ports", s.Get("port"))
		a.Set("mport", s.Get("port"))
	}
}

func metaHysteria(password string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map("name", s.Get("name"), "server", s.Get("host"))
	setPorts(a, s)
	a.Set("udp", true)
	a.Set("skip-cert-verify", phpx.LooseEq(s.Get("insecure"), int64(1)))
	if s.Isset("server_name") {
		a.Set("sni", s.Get("server_name"))
	}
	if s.Get("version") == int64(2) {
		a.Set("type", "hysteria2")
		a.Set("password", password)
		if s.Isset("obfs") {
			a.Set("obfs", s.Get("obfs"))
			a.Set("obfs-password", s.Get("obfs_password"))
		}
	} else {
		a.Set("type", "hysteria")
		a.Set("auth_str", password)
		if s.Isset("obfs") && s.Isset("obfs_password") {
			a.Set("obfs", s.Get("obfs_password"))
		}
		a.Set("up", s.Get("down_mbps"))
		a.Set("down", s.Get("up_mbps"))
		a.Set("protocol", "udp")
	}
	return a
}

func metaHysteria2(password string, s *phpx.Arr) *phpx.Arr {
	ts := arr(s.Get("tls_settings"))
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "hysteria2",
		"server", s.Get("host"),
		"password", password,
		"skip-cert-verify", phpx.LooseEq(phpx.Coalesce(ts.Get("allow_insecure"), int64(0)), int64(1)),
		"sni", phpx.Coalesce(ts.Get("server_name"), ""),
		"udp", true,
	)
	setPorts(a, s)
	if s.Isset("obfs") {
		a.Set("obfs", s.Get("obfs"))
		a.Set("obfs-password", s.Get("obfs_password"))
	}
	return a
}
