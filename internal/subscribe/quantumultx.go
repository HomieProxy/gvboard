package subscribe

import (
	"strings"

	"gvboard/internal/phpx"
)

// 对应原项目 Protocols/QuantumultX.php。

func quantumultX(c *Ctx) (*Response, error) {
	var b strings.Builder
	for _, s := range c.Servers {
		if phpx.StrIn(phpx.Coalesce(s.Get("network"), "tcp"), "grpc", "httpupgrade", "xhttp") {
			continue
		}
		switch typeOf(s) {
		case "shadowsocks":
			b.WriteString(qxSS(c.Sub.UUID, s))
		case "vmess":
			b.WriteString(qxVmess(c.Sub.UUID, s))
		case "vless":
			b.WriteString(qxVless(c.Sub.UUID, s))
		case "trojan":
			b.WriteString(qxTrojan(c.Sub.UUID, s))
		case "hysteria":
			if s.Get("version") == int64(2) {
				b.WriteString(qxHysteria2(c.Sub.UUID, s))
			}
		case "hysteria2":
			b.WriteString(qxHysteria2(c.Sub.UUID, s))
		case "anytls":
			b.WriteString(qxAnyTLS(c.Sub.UUID, s))
		}
	}
	expire := "0"
	if c.Sub.ExpiredAt != nil {
		expire = phpx.Str(*c.Sub.ExpiredAt)
	}
	info := "upload=" + phpx.Str(c.Sub.U) + "; download=" + phpx.Str(c.Sub.D) + "; total=" + phpx.Str(c.Sub.TransferEnable) + "; expire=" + expire
	return text(phpx.Base64(b.String()), [2]string{"subscription-userinfo", info}), nil
}

// qxHostPath ws 取 headers.Host / path；tcp 且伪装为 http 时取 Host / path 首项；无 host 时用 sni。
func qxHostPath(cfg []string, network, ns, sni any) []string {
	var host, path any
	if network == "tcp" {
		if phpx.Get(ns, "header", "type") == "http" {
			host = phpx.Get(ns, "header", "request", "headers", "Host", 0)
			path = phpx.Get(ns, "header", "request", "path", 0)
		}
	} else if network == "ws" {
		host = phpx.Get(ns, "headers", "Host")
		path = phpx.Get(ns, "path")
	}
	if phpx.Empty(host) && !phpx.Empty(sni) {
		host = sni
	}
	if phpx.Truthy(host) {
		cfg = append(cfg, "obfs-host="+phpx.Str(host))
	}
	if phpx.Truthy(path) {
		cfg = append(cfg, "obfs-uri="+phpx.Str(path))
	}
	return cfg
}

// qxObfs ws 为 obfs=ws/wss；tcp 带 TLS 为 over-tls，不带 TLS 且伪装为 http 时为 obfs=http。
func qxObfs(cfg []string, network, ns any, isTLS bool) []string {
	switch network {
	case "ws":
		if isTLS {
			return append(cfg, "obfs=wss")
		}
		return append(cfg, "obfs=ws")
	case "tcp":
		if isTLS {
			return append(cfg, "obfs=over-tls")
		}
		if phpx.Get(ns, "header", "type") == "http" {
			return append(cfg, "obfs=http")
		}
	}
	return cfg
}

func qxSS(uuid string, s *phpx.Arr) string {
	network := phpx.Coalesce(s.Get("network"), "tcp")
	ns := s.Get("network_settings")
	if !phpx.Empty(s.Get("obfs")) {
		// 旧表 Shadowsocks 的 obfs / obfs_settings 映射为 v2node 写法。
		network = s.Get("obfs")
		n := phpx.New()
		if a, ok := ns.(*phpx.Arr); ok {
			n = a.Clone()
		}
		if os, ok := s.Get("obfs_settings").(*phpx.Arr); ok {
			if !phpx.Empty(os.Get("host")) {
				n.SetPath(os.Get("host"), "headers", "Host")
			}
			if !phpx.Empty(os.Get("path")) {
				n.Set("path", os.Get("path"))
			}
		}
		ns = n
	}
	cfg := []string{"shadowsocks=" + s.Str("host") + ":" + s.Str("port"), "method=" + s.Str("cipher"), "password=" + ssPassword(uuid, s, false)}
	if network == "http" {
		cfg = append(cfg, "obfs=http")
		host := phpx.Coalesce(phpx.Get(ns, "headers", "Host"), phpx.Get(ns, "Host"))
		if phpx.Truthy(host) {
			cfg = append(cfg, "obfs-host="+phpx.Str(host))
		}
		if path := phpx.Get(ns, "path"); phpx.Truthy(path) {
			cfg = append(cfg, "obfs-uri="+phpx.Str(path))
		}
	}
	cfg = append(cfg, "fast-open=false", "udp-relay=true", "tag="+s.Str("name"))
	return joinFilter(",", cfg) + "\r\n"
}

func qxVmess(uuid string, s *phpx.Arr) string {
	ns := s.Get("network_settings")
	if s.Isset("networkSettings") {
		legacy, _ := s.Get("networkSettings").(*phpx.Arr)
		ns = phpx.ReplaceRecursive(arr(legacy), arr(phpx.Coalesce(ns, phpx.New())))
	}
	ts := s.Get("tls_settings")
	if s.Isset("tlsSettings") {
		legacy := phpx.New()
		if a, ok := s.Get("tlsSettings").(*phpx.Arr); ok {
			legacy = a.Clone()
		}
		if legacy.Isset("serverName") {
			legacy.Set("server_name", legacy.Get("serverName"))
		}
		if legacy.Isset("allowInsecure") {
			legacy.Set("allow_insecure", legacy.Get("allowInsecure"))
		}
		ts = phpx.ReplaceRecursive(legacy, arr(phpx.Coalesce(ts, phpx.New())))
	}
	network := phpx.Coalesce(s.Get("network"), "tcp")
	isTLS := !phpx.Empty(s.Get("tls"))
	method := "method=chacha20-poly1305"
	if network == "ws" && phpx.Get(ns, "security") != nil && phpx.Get(ns, "security") != "auto" {
		method = "method=" + phpx.Str(phpx.Get(ns, "security"))
	}
	cfg := []string{"vmess=" + s.Str("host") + ":" + s.Str("port"), method, "password=" + uuid, "fast-open=true", "udp-relay=true", "tag=" + s.Str("name")}
	if isTLS {
		cfg = append(cfg, "tls13=true")
		if phpx.Truthy(phpx.Get(ts, "allow_insecure")) {
			cfg = append(cfg, "tls-verification=false")
		}
	}
	cfg = qxObfs(cfg, network, ns, isTLS)
	cfg = qxHostPath(cfg, network, ns, phpx.Get(ts, "server_name"))
	return strings.Join(cfg, ",") + "\r\n"
}

func qxVless(uuid string, s *phpx.Arr) string {
	cfg := []string{"vless=" + s.Str("host") + ":" + s.Str("port"), "method=none", "password=" + uuid, "udp-relay=true", "tag=" + s.Str("name")}
	if phpx.LooseEq(s.Get("tls"), int64(2)) {
		cfg = append(cfg, "fast-open=false")
	} else {
		cfg = append(cfg, "fast-open=true")
	}
	if !phpx.Empty(s.Get("encryption")) && !phpx.Empty(s.Get("encryption_settings")) {
		return ""
	}
	ts := s.Get("tls_settings")
	ns := s.Get("network_settings")
	network := phpx.Coalesce(s.Get("network"), "tcp")
	isTLS := !phpx.Empty(s.Get("tls"))
	if isTLS {
		cfg = append(cfg, "tls13=true")
		if phpx.Truthy(phpx.Get(ts, "allow_insecure")) {
			cfg = append(cfg, "tls-verification=false")
		}
		if !phpx.Empty(s.Get("flow")) {
			cfg = append(cfg, "vless-flow="+s.Str("flow"))
		}
		if phpx.LooseEq(s.Get("tls"), int64(2)) {
			if v := phpx.Get(ts, "public_key"); v != nil {
				cfg = append(cfg, "reality-base64-pubkey="+phpx.Str(v))
			}
			if v := phpx.Get(ts, "short_id"); v != nil {
				cfg = append(cfg, "reality-hex-shortid="+phpx.Str(v))
			}
		}
	}
	cfg = qxObfs(cfg, network, ns, isTLS)
	cfg = qxHostPath(cfg, network, ns, phpx.Get(ts, "server_name"))
	return strings.Join(cfg, ",") + "\r\n"
}

func qxTrojan(password string, s *phpx.Arr) string {
	ts := s.Get("tls_settings")
	sni := phpx.Get(ts, "server_name")
	allowInsecure := phpx.Coalesce(phpx.Get(ts, "allow_insecure"), false)
	// 旧表 Trojan 的外层 server_name / allow_insecure 覆盖 tls_settings。
	if s.Isset("allow_insecure") {
		allowInsecure = phpx.Truthy(s.Get("allow_insecure"))
	}
	if s.Isset("server_name") {
		sni = s.Get("server_name")
	}
	network := phpx.Coalesce(s.Get("network"), "tcp")
	ns := s.Get("network_settings")
	cfg := []string{"trojan=" + s.Str("host") + ":" + s.Str("port"), "password=" + password, "fast-open=true", "udp-relay=true", "tag=" + s.Str("name")}
	switch network {
	case "tcp":
		cfg = append(cfg, "over-tls=true")
		if phpx.Truthy(sni) {
			cfg = append(cfg, "tls-host="+phpx.Str(sni))
		}
		if phpx.Truthy(allowInsecure) {
			cfg = append(cfg, "tls-verification=false")
		} else {
			cfg = append(cfg, "tls-verification=true")
		}
	case "ws":
		cfg = append(cfg, "obfs=wss")
		cfg = qxHostPath(cfg, network, ns, sni)
		if phpx.Truthy(allowInsecure) {
			cfg = append(cfg, "tls-verification=false")
		}
	}
	return joinFilter(",", cfg) + "\r\n"
}

func qxAnyTLS(password string, s *phpx.Arr) string {
	ts := s.Get("tls_settings")
	sni := phpx.Get(ts, "server_name")
	cfg := []string{"anytls=" + s.Str("host") + ":" + s.Str("port"), "password=" + password, "udp-relay=true", "tag=" + s.Str("name")}
	if phpx.Coalesce(s.Get("network"), "tcp") == "tcp" {
		cfg = append(cfg, "over-tls=true")
		if phpx.Truthy(sni) {
			cfg = append(cfg, "tls-host="+phpx.Str(sni))
		}
		if phpx.Truthy(phpx.Get(ts, "allow_insecure")) {
			cfg = append(cfg, "tls-verification=false")
		} else {
			cfg = append(cfg, "tls-verification=true")
		}
	}
	return joinFilter(",", cfg) + "\r\n"
}

// qxHysteria2 Quantumult X 1.9.0+ 的 Hysteria2（原项目尚未支持）。
// 旧表 hysteria version=2 与 v2node protocol=hysteria2 共用；字段参考官方样例与 Sub-Store 输出。
func qxHysteria2(password string, s *phpx.Arr) string {
	port := phpx.FirstPort(s.Get("port"))
	cfg := []string{"hysteria2=" + s.Str("host") + ":" + port, "password=" + password}
	if phpx.MultiPort(s.Get("port")) {
		cfg = append(cfg, "port-hopping="+s.Str("port"))
	}
	if s.Isset("obfs") && !phpx.Empty(s.Get("obfs_password")) {
		cfg = append(cfg, "salamander-password="+s.Str("obfs_password"))
	}
	ts := arr(s.Get("tls_settings"))
	if sni := phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""); phpx.Truthy(sni) {
		cfg = append(cfg, "tls-host="+phpx.Str(sni))
	}
	if phpx.Truthy(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0))) {
		cfg = append(cfg, "tls-verification=false")
	}
	if !phpx.Empty(s.Get("up_mbps")) {
		cfg = append(cfg, "download-bandwidth="+s.Str("up_mbps"))
	}
	cfg = append(cfg, "udp-relay=true", "tag="+s.Str("name"))
	return joinFilter(",", cfg) + "\r\n"
}
