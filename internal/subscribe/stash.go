package subscribe

import (
	"strings"

	"gvboard/internal/phpx"
)

// 对应原项目 Protocols/Stash.php。

func stash(c *Ctx) (*Response, error) {
	config, err := c.yamlTemplate(tplStash)
	if err != nil {
		return nil, err
	}
	var proxy []*phpx.Arr
	var names []string
	for _, s := range c.Servers {
		var p *phpx.Arr
		switch typeOf(s) {
		case "shadowsocks":
			p = stashSS(c.Sub.UUID, s)
		case "vmess":
			p = stashVmess(c.Sub.UUID, s)
		case "vless":
			p = stashVless(c.Sub.UUID, s)
		case "trojan":
			p = stashTrojan(c.Sub.UUID, s)
		case "tuic":
			p = stashTuic(c.Sub.UUID, s)
		case "hysteria":
			p = stashHysteria(c.Sub.UUID, s)
		case "hysteria2":
			p = metaHysteria2(c.Sub.UUID, s)
		case "anytls":
			p = stashAnyTLS(c.Sub.UUID, s)
		default:
			continue
		}
		proxy = append(proxy, p)
		names = append(names, s.Str("name"))
	}
	setProxies(config, proxy)
	fillGroups(config, names, true)
	if c.Host != "" {
		rules, ok := config.Get("rules").(*phpx.Arr)
		if !ok {
			rules = phpx.New()
			config.Set("rules", rules)
		}
		rules.Unshift("DOMAIN," + c.Host + ",DIRECT")
	}
	body := strings.ReplaceAll(phpx.YAMLDump(config, 2, 4, true), "$app_name", c.AppName)
	return text(body,
		[2]string{"subscription-userinfo", c.userinfo()},
		[2]string{"profile-update-interval", "24"},
		[2]string{"content-disposition", "filename*=UTF-8''" + phpx.RawURLEncode(c.AppName)},
	), nil
}

func stashSS(uuid string, s *phpx.Arr) *phpx.Arr {
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
		opts := phpx.Map("mode", "http")
		if !phpx.Empty(s.Get("obfs-host")) {
			opts.Set("host", s.Get("obfs-host"))
		}
		a.Set("plugin", "obfs")
		a.Set("plugin-opts", opts)
	}
	return a
}

// stashVmess 决策 D40：原项目只读旧表字段 tlsSettings / networkSettings，v2node 的 VMess 会丢失 TLS 与传输设置，
// 这里依次回退到 tls_settings / network_settings（以及 server_name / allow_insecure）。
func stashVmess(uuid string, s *phpx.Arr) *phpx.Arr {
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
			if ai := phpx.Coalesce(phpx.Get(ts, "allowInsecure"), phpx.Get(ts, "allow_insecure")); !phpx.Empty(ai) {
				a.Set("skip-cert-verify", true)
			}
			if sn := phpx.Coalesce(phpx.Get(ts, "serverName"), phpx.Get(ts, "server_name")); !phpx.Empty(sn) {
				a.Set("servername", sn)
			}
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
		}
	case "ws":
		a.Set("network", "ws")
		if phpx.Truthy(ns) {
			stashWS(a, ns)
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
	return a
}

// stashWS Stash 的 ws-opts 与旧版 ws-path / ws-headers 同时下发。
func stashWS(a *phpx.Arr, ns any) {
	opts := phpx.New()
	a.Set("ws-opts", opts)
	path, host := phpx.Get(ns, "path"), phpx.Get(ns, "headers", "Host")
	if !phpx.Empty(path) {
		opts.Set("path", path)
	}
	if !phpx.Empty(host) {
		opts.Set("headers", phpx.Map("Host", host))
	}
	if !phpx.Empty(path) {
		a.Set("ws-path", path)
	}
	if !phpx.Empty(host) {
		a.Set("ws-headers", phpx.Map("Host", host))
	}
}

func stashVless(uuid string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "vless",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"uuid", uuid,
		"flow", s.Get("flow"),
		"udp", true,
	)
	if phpx.Truthy(s.Get("tls")) {
		a.Set("tls", true)
		if ts := s.Get("tls_settings"); phpx.Truthy(ts) {
			if !phpx.Empty(phpx.Get(ts, "server_name")) {
				a.Set("servername", phpx.Get(ts, "server_name"))
			}
			if phpx.LooseEq(s.Get("tls"), int64(2)) {
				a.Set("reality-opts", phpx.Map("public-key", phpx.Get(ts, "public_key"), "short-id", phpx.Get(ts, "short_id")))
			}
			a.Set("skip-cert-verify", phpx.LooseEq(phpx.Coalesce(phpx.Get(ts, "allow_insecure"), int64(0)), int64(1)))
			a.Set("client-fingerprint", phpx.Get(ts, "fingerprint"))
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
			if p := g("header", "request", "path", 0); p != nil {
				a.SetPath(p, "http-opts", "path")
			}
		}
	case "ws":
		a.Set("network", "ws")
		if phpx.Truthy(ns) {
			stashWS(a, ns)
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
	return a
}

func stashTrojan(password string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "trojan",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"password", password,
		"udp", true,
	)
	trojanTransport(a, s)
	if !phpx.Empty(s.Get("server_name")) {
		a.Set("sni", s.Get("server_name"))
	}
	if !phpx.Empty(s.Get("allow_insecure")) {
		a.Set("skip-cert-verify", true)
	}
	return a
}

func stashTuic(password string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "tuic",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"version", int64(5),
		"uuid", password,
		"password", password,
		"alpn", phpx.Strings("h3"),
		"skip-cert-verify", phpx.Truthy(s.Get("insecure")),
	)
	if s.Isset("server_name") {
		a.Set("sni", s.Get("server_name"))
	}
	return a
}

func stashHysteria(password string, s *phpx.Arr) *phpx.Arr {
	a := metaHysteria(password, s)
	if a.Get("type") == "hysteria2" {
		// Stash 的 Hysteria2 鉴权字段名为 auth（Meta 为 password），键位置保持不变。
		b := phpx.New()
		for _, k := range a.Keys() {
			if k == "password" {
				b.Set("auth", a.Get(k))
			} else {
				b.Set(k, a.Get(k))
			}
		}
		return b
	}
	return a
}

func stashAnyTLS(password string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "anytls",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"password", password,
	)
	if phpx.Truthy(s.Get("tls")) {
		a.Set("tls", true)
		ts := arr(s.Get("tls_settings"))
		fp := any("chrome")
		if !phpx.Empty(ts.Get("fingerprint")) {
			fp = ts.Get("fingerprint")
		}
		a.Set("client-fingerprint", fp)
		a.Set("sni", phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""))
		a.Set("skip-cert-verify", phpx.LooseEq(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0)), int64(1)))
	}
	return a
}
