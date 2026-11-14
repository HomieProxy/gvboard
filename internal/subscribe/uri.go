package subscribe

import (
	"strings"

	"gvboard/internal/phpx"
)

// 以下对应原项目 App\Utils\Helper 中的 build*Uri，逐项保持一致。

// arr 取数组，不是数组时返回空数组（$x ?? [] 后按数组访问）。
func arr(v any) *phpx.Arr {
	if a, ok := v.(*phpx.Arr); ok && a != nil {
		return a
	}
	return phpx.New()
}

// typeOf 节点实际协议：v2node 取其 protocol。
func typeOf(s *phpx.Arr) string {
	if s.Get("type") == "v2node" && s.Isset("protocol") {
		return s.Str("protocol")
	}
	return s.Str("type")
}

// buildURI Helper::buildUri：按协议生成分享链接，不支持的协议返回空串。
func buildURI(uuid string, s *phpx.Arr) string {
	typ := s.Str("type")
	if phpx.LooseEq(s.Get("type"), "v2node") {
		typ = s.Str("protocol")
	}
	switch strings.ToLower(typ) {
	case "shadowsocks":
		return ssURI(uuid, s)
	case "vmess":
		return vmessURI(uuid, s)
	case "vless":
		return vlessURI(uuid, s)
	case "trojan":
		return trojanURI(uuid, s)
	case "hysteria":
		return hysteriaURI(uuid, s)
	case "hysteria2":
		return hysteria2URI(uuid, s)
	case "tuic":
		return tuicURI(uuid, s)
	case "anytls":
		return anytlsURI(uuid, s)
	}
	return ""
}

// ssPassword Shadowsocks 2022 的密码为"服务端密钥:用户密钥"，其余为 uuid。
func ssPassword(uuid string, s *phpx.Arr, contains bool) string {
	cipher := s.Str("cipher")
	n := 0
	switch {
	case contains && strings.Contains(cipher, "2022-blake3"):
		n = 32
		if cipher == "2022-blake3-aes-128-gcm" {
			n = 16
		}
	case cipher == "2022-blake3-aes-128-gcm":
		n = 16
	case cipher == "2022-blake3-aes-256-gcm":
		n = 32
	}
	if n == 0 {
		return uuid
	}
	return phpx.ServerKey(s.Get("created_at"), n) + ":" + phpx.UUIDToBase64(uuid, n)
}

func ssURI(uuid string, s *phpx.Arr) string {
	cipher := s.Str("cipher")
	password := ssPassword(uuid, s, true)
	name := phpx.RawURLEncode(s.Str("name"))
	str := strings.NewReplacer("+", "-", "/", "_", "=", "").Replace(phpx.Base64(cipher + ":" + password))
	uri := "ss://" + str + "@" + phpx.FormatHost(s.Str("host")) + ":" + s.Str("port")
	if phpx.LooseEq(s.Get("obfs"), "http") {
		uri += "?plugin=obfs-local;obfs=http;obfs-host=" + s.Str("obfs-host") + ";path=" + s.Str("obfs-path")
	} else if phpx.LooseEq(s.Get("network"), "http") && s.Isset("network_settings", "Host") {
		path := phpx.Coalesce(s.Get("network_settings", "path"), "/")
		uri += "?plugin=obfs-local;obfs=tls;obfs-host=" + s.Str("network_settings", "Host") + ";path=" + phpx.Str(path)
	}
	return uri + "#" + name + "\r\n"
}

func vmessURI(uuid string, s *phpx.Arr) string {
	tls := ""
	if phpx.Truthy(s.Get("tls")) {
		tls = "tls"
	}
	cfg := phpx.Map(
		"v", "2",
		"ps", s.Get("name"),
		"add", phpx.FormatHost(s.Str("host")),
		"port", s.Str("port"),
		"id", uuid,
		"aid", "0",
		"scy", "auto",
		"net", s.Get("network"),
		"type", "none",
		"host", "",
		"path", "",
		"tls", tls,
		"fp", "chrome",
	)
	if phpx.Truthy(s.Get("tls")) {
		ts := phpx.Coalesce(s.Get("tls_settings"), s.Get("tlsSettings"), phpx.New())
		cfg.Set("allowInsecure", phpx.Int(phpx.Coalesce(phpx.Get(ts, "allow_insecure"), phpx.Get(ts, "allowInsecure"), int64(0))))
		cfg.Set("sni", phpx.Coalesce(phpx.Get(ts, "server_name"), phpx.Get(ts, "serverName"), ""))
		cfg.Set("pcs", phpx.Coalesce(phpx.Get(ts, "pinned_peer_cert_sha256"), ""))
	}
	ns := phpx.Coalesce(s.Get("networkSettings"), s.Get("network_settings"), phpx.New())
	g := func(path ...any) any { return phpx.Get(ns, path...) }
	switch s.Str("network") {
	case "tcp":
		if !phpx.Empty(g("header", "type")) && g("header", "type") == "http" {
			cfg.Set("type", g("header", "type"))
			cfg.Set("host", g("header", "request", "headers", "Host", 0))
			cfg.Set("path", g("header", "request", "path", 0))
		}
	case "ws":
		cfg.Set("path", g("path"))
		cfg.Set("host", g("headers", "Host"))
		if g("security") != nil {
			cfg.Set("scy", g("security"))
		}
	case "grpc":
		cfg.Set("path", g("serviceName"))
	case "kcp":
		if g("seed") != nil {
			cfg.Set("path", g("seed"))
		}
		cfg.Set("type", phpx.Coalesce(g("header", "type"), "none"))
	case "httpupgrade":
		cfg.Set("path", g("path"))
		cfg.Set("host", g("host"))
	case "xhttp":
		cfg.Set("path", g("path"))
		cfg.Set("host", g("host"))
		cfg.Set("mode", phpx.Coalesce(g("mode"), "auto"))
		cfg.Set("extra", jsonExtra(g("extra")))
	}
	return "vmess://" + phpx.Base64(string(phpx.JSONEncode(cfg, 0))) + "\r\n"
}

func jsonExtra(v any) any {
	if v == nil {
		return nil
	}
	return string(phpx.JSONEncode(v, phpx.JSONUnescapedSlashes))
}

// echParam 分享链接中的 ech 参数。
func echParam(cfg, ts *phpx.Arr) {
	if phpx.Empty(ts.Get("ech")) {
		return
	}
	switch {
	case ts.Get("ech") == "cloudflare":
		cfg.Set("ech", "cloudflare-ech.com+https://doh.pub/dns-query")
	case ts.Get("ech") == "custom" && !phpx.Empty(ts.Get("ech_config")):
		if c, ok := ts.Get("ech_config").(*phpx.Arr); ok {
			cfg.Set("ech", c.Get(0))
		} else {
			cfg.Set("ech", ts.Get("ech_config"))
		}
	}
}

func vlessURI(uuid string, s *phpx.Arr) string {
	name := phpx.EncodeURIComponent(s.Str("name"))
	ts := arr(s.Get("tls_settings"))
	security := ""
	if !phpx.LooseEq(s.Get("tls"), int64(0)) {
		security = "tls"
		if phpx.LooseEq(s.Get("tls"), int64(2)) {
			security = "reality"
		}
	}
	cfg := phpx.Map(
		"type", s.Get("network"),
		"encryption", "none",
		"host", "",
		"path", "",
		"headerType", "none",
		"quicSecurity", "none",
		"serviceName", "",
		"security", security,
		"flow", s.Get("flow"),
		"fp", phpx.Coalesce(ts.Get("fingerprint"), "chrome"),
		"insecure", phpx.Coalesce(ts.Get("allow_insecure"), int64(0)),
		"pcs", phpx.Coalesce(ts.Get("pinned_peer_cert_sha256"), ""),
	)
	if phpx.Truthy(s.Get("tls")) {
		cfg.Set("sni", phpx.Coalesce(ts.Get("server_name"), ""))
		if phpx.LooseEq(s.Get("tls"), int64(2)) {
			cfg.Set("pbk", phpx.Coalesce(ts.Get("public_key"), ""))
			cfg.Set("sid", phpx.Coalesce(ts.Get("short_id"), ""))
		}
	}
	echParam(cfg, ts)
	if s.Isset("encryption") && phpx.LooseEq(s.Get("encryption"), "mlkem768x25519plus") {
		enc := s.Get("encryption_settings")
		e := "mlkem768x25519plus." + phpx.Str(phpx.Coalesce(phpx.Get(enc, "mode"), "native")) + "." + phpx.Str(phpx.Coalesce(phpx.Get(enc, "rtt"), "1rtt"))
		if !phpx.Empty(phpx.Get(enc, "client_padding")) {
			e += "." + phpx.Str(phpx.Get(enc, "client_padding"))
		}
		e += "." + phpx.Str(phpx.Coalesce(phpx.Get(enc, "password"), ""))
		cfg.Set("encryption", e)
	}
	configureNetwork(s, cfg)
	return uriString("vless", uuid, s, name, cfg)
}

// uriString Helper::buildUriString。
func uriString(scheme, auth string, s *phpx.Arr, name string, params *phpx.Arr) string {
	return scheme + "://" + auth + "@" + phpx.FormatHost(s.Str("host")) + ":" + s.Str("port") + "?" + phpx.HTTPBuildQuery(params, false) + "#" + name + "\r\n"
}

func trojanURI(password string, s *phpx.Arr) string {
	ts := arr(s.Get("tls_settings"))
	sni := phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), "")
	cfg := phpx.Map(
		"allowInsecure", phpx.Coalesce(s.Get("allow_insecure"), ts.Get("allow_insecure"), int64(0)),
		"peer", sni,
		"sni", sni,
		"pcs", phpx.Coalesce(ts.Get("pinned_peer_cert_sha256"), ""),
		"type", s.Get("network"),
	)
	if s.Isset("network") && phpx.StrIn(s.Get("network"), "grpc", "ws") {
		if s.Get("network") == "grpc" && s.Isset("network_settings", "serviceName") {
			cfg.Set("serviceName", s.Get("network_settings", "serviceName"))
		}
		if s.Get("network") == "ws" {
			if s.Isset("network_settings", "path") {
				cfg.Set("path", s.Get("network_settings", "path"))
			}
			if s.Isset("network_settings", "headers", "Host") {
				cfg.Set("host", s.Get("network_settings", "headers", "Host"))
			}
		}
	}
	echParam(cfg, ts)
	return "trojan://" + password + "@" + phpx.FormatHost(s.Str("host")) + ":" + s.Str("port") + "?" + phpx.HTTPBuildQuery(cfg, false) + "#" + phpx.RawURLEncode(s.Str("name")) + "\r\n"
}

func hysteriaURI(password string, s *phpx.Arr) string {
	remote := phpx.FormatHost(s.Str("host"))
	name := phpx.EncodeURIComponent(s.Str("name"))
	first := phpx.FirstPort(s.Get("port"))
	v2 := phpx.LooseEq(s.Get("version"), int64(2))
	var uri string
	if v2 {
		uri = "hysteria2://" + password + "@" + remote + ":" + first + "/?insecure=" + s.Str("insecure") + "&sni=" + s.Str("server_name")
	} else {
		uri = "hysteria://" + remote + ":" + first + "/?protocol=udp&auth=" + password + "&insecure=" + s.Str("insecure") + "&peer=" + s.Str("server_name") + "&upmbps=" + s.Str("down_mbps") + "&downmbps=" + s.Str("up_mbps")
	}
	if s.Isset("obfs") && s.Isset("obfs_password") {
		op := phpx.RawURLEncode(s.Str("obfs_password"))
		if v2 {
			uri += "&obfs=" + s.Str("obfs") + "&obfs-password=" + op
		} else {
			uri += "&obfs=" + s.Str("obfs") + "&obfsParam" + op
		}
	}
	if phpx.MultiPort(s.Get("port")) {
		uri += "&mport=" + s.Str("mport")
	}
	return uri + "#" + name + "\r\n"
}

func hysteria2URI(password string, s *phpx.Arr) string {
	remote := phpx.FormatHost(s.Str("host"))
	name := phpx.EncodeURIComponent(s.Str("name"))
	ts := arr(s.Get("tls_settings"))
	uri := "hysteria2://" + password + "@" + remote + ":" + phpx.FirstPort(s.Get("port")) +
		"/?insecure=" + phpx.Str(phpx.Coalesce(ts.Get("allow_insecure"), int64(0))) +
		"&sni=" + phpx.Str(phpx.Coalesce(ts.Get("server_name"), "")) +
		"&pcs=" + phpx.Str(phpx.Coalesce(ts.Get("pinned_peer_cert_sha256"), ""))
	if s.Isset("obfs") && s.Isset("obfs_password") {
		uri += "&obfs=" + s.Str("obfs") + "&obfs-password=" + phpx.RawURLEncode(s.Str("obfs_password"))
	}
	if phpx.MultiPort(s.Get("port")) {
		uri += "&mport=" + s.Str("mport")
	}
	return uri + "#" + name + "\r\n"
}

func tuicURI(password string, s *phpx.Arr) string {
	ts := arr(s.Get("tls_settings"))
	cfg := phpx.Map(
		"sni", phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""),
		"alpn", "h3",
		"congestion_control", s.Get("congestion_control"),
		"allow_insecure", phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0)),
		"disable_sni", s.Get("disable_sni"),
		"udp_relay_mode", s.Get("udp_relay_mode"),
		"pcs", phpx.Coalesce(ts.Get("pinned_peer_cert_sha256"), ""),
	)
	return "tuic://" + password + ":" + password + "@" + phpx.FormatHost(s.Str("host")) + ":" + s.Str("port") + "?" + phpx.HTTPBuildQuery(cfg, false) + "#" + phpx.EncodeURIComponent(s.Str("name")) + "\r\n"
}

func anytlsURI(password string, s *phpx.Arr) string {
	ts := arr(s.Get("tls_settings"))
	cfg := phpx.Map(
		"type", phpx.Coalesce(s.Get("network"), "tcp"),
		"insecure", phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0)),
		"fp", phpx.Coalesce(ts.Get("fingerprint"), "chrome"),
		"pcs", phpx.Coalesce(ts.Get("pinned_peer_cert_sha256"), ""),
	)
	if s.Isset("server_name") || ts.Isset("server_name") {
		cfg.Set("sni", phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""))
	}
	if s.Isset("tls") && phpx.LooseEq(s.Get("tls"), int64(2)) {
		cfg.Set("security", "reality")
		cfg.Set("pbk", phpx.Coalesce(ts.Get("public_key"), ""))
		cfg.Set("sid", phpx.Coalesce(ts.Get("short_id"), ""))
	}
	if s.Isset("network") && s.Isset("network_settings") {
		configureNetwork(s, cfg)
	}
	return "anytls://" + password + "@" + phpx.FormatHost(s.Str("host")) + ":" + s.Str("port") + "/?" + phpx.HTTPBuildQuery(cfg, false) + "#" + phpx.EncodeURIComponent(s.Str("name")) + "\r\n"
}

// configureNetwork Helper::configureNetworkSettings。
func configureNetwork(s *phpx.Arr, cfg *phpx.Arr) {
	st := phpx.Coalesce(s.Get("network_settings"), s.Get("networkSettings"), phpx.New())
	g := func(path ...any) any { return phpx.Get(st, path...) }
	switch {
	case phpx.LooseEq(s.Get("network"), "tcp"):
		header := phpx.Coalesce(g("header"), phpx.New())
		if phpx.Get(header, "type") == "http" {
			cfg.Set("headerType", "http")
			cfg.Set("host", phpx.Coalesce(phpx.Get(header, "request", "headers", "Host", 0), ""))
			cfg.Set("path", phpx.Coalesce(phpx.Get(header, "request", "path", 0), ""))
		}
	case phpx.LooseEq(s.Get("network"), "ws"):
		cfg.Set("path", phpx.Coalesce(g("path"), ""))
		cfg.Set("host", phpx.Coalesce(g("headers", "Host"), ""))
	case phpx.LooseEq(s.Get("network"), "grpc"):
		cfg.Set("serviceName", phpx.Coalesce(g("serviceName"), ""))
	case phpx.LooseEq(s.Get("network"), "kcp"):
		cfg.Set("headerType", phpx.Coalesce(g("header", "type"), "none"))
		if g("seed") != nil {
			cfg.Set("seed", g("seed"))
		}
	case phpx.LooseEq(s.Get("network"), "httpupgrade"):
		cfg.Set("path", phpx.Coalesce(g("path"), ""))
		cfg.Set("host", phpx.Coalesce(g("host"), ""))
	case phpx.LooseEq(s.Get("network"), "xhttp"):
		cfg.Set("path", phpx.Coalesce(g("path"), ""))
		cfg.Set("host", phpx.Coalesce(g("host"), ""))
		cfg.Set("mode", phpx.Coalesce(g("mode"), "auto"))
		cfg.Set("extra", jsonExtra(g("extra")))
	}
}
