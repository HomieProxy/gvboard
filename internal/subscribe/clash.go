package subscribe

import (
	"strings"

	"gvboard/internal/phpx"
)

// 对应原项目 Protocols/Clash.php（原版 Clash，只支持 SS / VMess / Trojan）。

var clashCiphers = []string{"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"}

func clash(c *Ctx) (*Response, error) {
	config, err := c.yamlTemplate(tplClash)
	if err != nil {
		return nil, err
	}
	var proxy []*phpx.Arr
	var names []string
	for _, s := range c.Servers {
		switch typeOf(s) {
		case "shadowsocks":
			if phpx.StrIn(s.Get("cipher"), clashCiphers...) {
				proxy = append(proxy, clashSS(c.Sub.UUID, s))
				names = append(names, s.Str("name"))
			}
		case "vmess":
			proxy = append(proxy, clashVmess(c.Sub.UUID, s))
			names = append(names, s.Str("name"))
		case "trojan":
			proxy = append(proxy, clashTrojan(c.Sub.UUID, s))
			names = append(names, s.Str("name"))
		}
	}
	setProxies(config, proxy)
	fillGroups(config, names, false)
	body := strings.ReplaceAll(phpx.YAMLDump(config, 2, 4, true), "$app_name", c.AppName)
	return text(body,
		[2]string{"subscription-userinfo", c.userinfo()},
		[2]string{"profile-update-interval", "24"},
		[2]string{"content-disposition", "attachment;filename*=UTF-8''" + phpx.RawURLEncode(c.AppName)},
		[2]string{"profile-web-page-url", c.AppURL},
	), nil
}

// setProxies $config['proxies'] = array_merge($config['proxies'] ?: [], $proxy)。
func setProxies(config *phpx.Arr, proxy []*phpx.Arr) {
	list := phpx.New()
	if base, ok := config.Get("proxies").(*phpx.Arr); ok {
		list = phpx.Merge(base)
	}
	for _, p := range proxy {
		list.Push(p)
	}
	config.Set("proxies", list)
}

// fillGroups 把节点名加入各代理组：组内的正则（如 /香港|HK/i）替换为匹配的节点名，
// 不含正则的组追加全部节点；最后移除没有节点的组。skipNonArray 为 Stash 的行为（proxies 不是数组的组不处理）。
func fillGroups(config *phpx.Arr, names []string, skipNonArray bool) {
	groups := arr(config.Get("proxy-groups"))
	for _, k := range groups.Keys() {
		g, ok := groups.Get(k).(*phpx.Arr)
		if !ok {
			continue
		}
		if _, ok := g.Get("proxies").(*phpx.Arr); !ok {
			if skipNonArray {
				continue
			}
			g.Set("proxies", phpx.New())
		}
		isFilter := false
		for _, src := range g.Arr("proxies").Values() {
			for _, dst := range names {
				re := phpx.CompileRegex(phpx.Str(src))
				if re == nil {
					continue
				}
				isFilter = true
				next := phpx.New()
				for _, v := range g.Arr("proxies").Values() {
					if phpx.Str(v) != phpx.Str(src) {
						next.Push(v)
					}
				}
				if re.Match(dst) {
					next.Push(dst)
				}
				g.Set("proxies", next)
			}
		}
		if isFilter {
			continue
		}
		g.Set("proxies", phpx.Merge(g.Arr("proxies"), phpx.Strings(names...)))
	}
	kept := phpx.New()
	for _, g := range groups.Values() {
		if phpx.Truthy(phpx.Get(g, "proxies")) {
			kept.Push(g)
		}
	}
	config.Set("proxy-groups", kept)
}

func clashSS(uuid string, s *phpx.Arr) *phpx.Arr {
	a := phpx.Map(
		"name", s.Get("name"),
		"type", "ss",
		"server", s.Get("host"),
		"port", s.Get("port"),
		"cipher", s.Get("cipher"),
		"password", uuid,
		"udp", true,
	)
	if s.Get("obfs") == "http" {
		opts := phpx.Map("mode", "http")
		if s.Isset("obfs-host") {
			opts.Set("host", s.Get("obfs-host"))
		}
		a.Set("plugin", "obfs")
		a.Set("plugin-opts", opts)
	} else if phpx.LooseEq(s.Get("network"), "http") {
		// 决策 D40：原项目此处为 $server['network'] ?? null == 'http'，任何带 network 的 v2node SS 都会误加 obfs 插件。
		opts := phpx.Map("mode", "http")
		if s.Isset("network_settings", "Host") {
			opts.Set("host", s.Get("network_settings", "Host"))
		}
		a.Set("plugin", "obfs")
		a.Set("plugin-opts", opts)
	}
	return a
}

func clashVmess(uuid string, s *phpx.Arr) *phpx.Arr {
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
		ts := phpx.Coalesce(s.Get("tls_settings"), s.Get("tlsSettings"), phpx.New())
		a.Set("skip-cert-verify", phpx.Int(phpx.Coalesce(phpx.Get(ts, "allow_insecure"), phpx.Get(ts, "allowInsecure"), int64(0))) == 1)
		a.Set("servername", phpx.Coalesce(phpx.Get(ts, "server_name"), phpx.Get(ts, "serverName"), ""))
	}
	ns := phpx.Coalesce(s.Get("network_settings"), s.Get("networkSettings"), phpx.New())
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
	case "grpc":
		a.Set("network", "grpc")
		opts := phpx.New()
		a.Set("grpc-opts", opts)
		if g("serviceName") != nil {
			opts.Set("grpc-service-name", g("serviceName"))
		}
	}
	return withPin(a, s, "fingerprint")
}

// trojanTransport Clash 系 Trojan 的 grpc / ws 传输设置。
func trojanTransport(a, s *phpx.Arr) {
	if !s.Isset("network") || !phpx.StrIn(s.Get("network"), "grpc", "ws") {
		return
	}
	a.Set("network", s.Get("network"))
	if s.Get("network") == "grpc" && s.Isset("network_settings", "serviceName") {
		a.SetPath(s.Get("network_settings", "serviceName"), "grpc-opts", "grpc-service-name")
	}
	if s.Get("network") == "ws" {
		if s.Isset("network_settings", "path") {
			a.SetPath(s.Get("network_settings", "path"), "ws-opts", "path")
		}
		if s.Isset("network_settings", "headers", "Host") {
			a.SetPath(s.Get("network_settings", "headers", "Host"), "ws-opts", "headers", "Host")
		}
	}
}

func clashTrojan(password string, s *phpx.Arr) *phpx.Arr {
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
	a.Set("skip-cert-verify", phpx.Int(phpx.Coalesce(ts.Get("allow_insecure"), s.Get("allow_insecure"), int64(0))) == 1)
	return withPin(a, s, "fingerprint")
}

// appConfig 原项目 AppController::getConfig：App 内置 Clash 配置，只含旧表的 SS（4 种 AEAD 加密）/ VMess / Trojan 节点。
func appConfig(c *Ctx) (*Response, error) {
	config, err := c.yamlTemplate(tplAppClash)
	if err != nil {
		return nil, err
	}
	var proxy []*phpx.Arr
	names := phpx.New()
	for _, s := range c.Servers {
		switch s.Get("type") {
		case "shadowsocks":
			if phpx.StrIn(s.Get("cipher"), clashCiphers...) {
				proxy = append(proxy, clashSS(c.Sub.UUID, s))
				names.Push(s.Get("name"))
			}
		case "vmess":
			proxy = append(proxy, clashVmess(c.Sub.UUID, s))
			names.Push(s.Get("name"))
		case "trojan":
			proxy = append(proxy, clashTrojan(c.Sub.UUID, s))
			names.Push(s.Get("name"))
		}
	}
	setProxies(config, proxy)
	groups := arr(config.Get("proxy-groups"))
	for _, g := range groups.Values() {
		if ga, ok := g.(*phpx.Arr); ok {
			ga.Set("proxies", phpx.Merge(arr(ga.Get("proxies")), names))
		}
	}
	return &Response{ContentType: "text/yaml", Body: []byte(phpx.YAMLDump(config, 2, 4, false))}, nil
}
