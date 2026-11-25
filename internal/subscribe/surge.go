package subscribe

import (
	"strings"

	"gvboard/internal/phpx"
)

// 对应原项目 Protocols/Surge.php 与 Surfboard.php：文本模板替换。

func surge(c *Ctx) (*Response, error) {
	var proxies, group strings.Builder
	for _, s := range c.Servers {
		var line string
		switch typeOf(s) {
		case "shadowsocks":
			line = surgeSS(c.Sub.UUID, s)
		case "vmess":
			line = surgeVmess(c.Sub.UUID, s)
		case "vless":
			line = surgeVless(c.Sub.UUID, s)
			if line == "" {
				continue
			}
		case "trojan":
			line = surgeTrojan(c.Sub.UUID, s, false)
		case "hysteria":
			if s.Get("version") != int64(2) {
				continue
			}
			line = surgeHysteria2(c.Sub.UUID, s)
		case "hysteria2":
			line = surgeHysteria2(c.Sub.UUID, s)
		case "anytls":
			line = surgeAnyTLS(c.Sub.UUID, s, false)
		default:
			continue
		}
		proxies.WriteString(line)
		group.WriteString(s.Str("name") + ", ")
	}
	return surgeRender(c, tplSurge, proxies.String(), group.String())
}

func surfboard(c *Ctx) (*Response, error) {
	var proxies, group strings.Builder
	for _, s := range c.Servers {
		var line string
		switch typeOf(s) {
		case "shadowsocks":
			if !phpx.StrIn(s.Get("cipher"), clashCiphers...) {
				continue
			}
			line = joinFilter(",", []string{
				s.Str("name") + "=ss", s.Str("host"), s.Str("port"),
				"encrypt-method=" + s.Str("cipher"), "password=" + c.Sub.UUID, "tfo=true", "udp-relay=true",
			}) + "\r\n"
		case "vmess":
			line = surgeVmess(c.Sub.UUID, s)
		case "trojan":
			line = surgeTrojan(c.Sub.UUID, s, true)
		case "anytls":
			line = surgeAnyTLS(c.Sub.UUID, s, true)
		default:
			continue
		}
		proxies.WriteString(line)
		group.WriteString(s.Str("name") + ", ")
	}
	return surgeRender(c, tplSurfboard, proxies.String(), group.String())
}

func surgeRender(c *Ctx, tpl, proxies, group string) (*Response, error) {
	config, err := c.textTemplate(tpl)
	if err != nil {
		return nil, err
	}
	subsURL, err := c.subscribeURL()
	if err != nil {
		return nil, err
	}
	config = strings.ReplaceAll(config, "$subs_link", subsURL)
	config = strings.ReplaceAll(config, "$subs_domain", c.Host)
	config = strings.ReplaceAll(config, "$proxies", proxies)
	config = strings.ReplaceAll(config, "$proxy_group", strings.TrimRight(group, ", "))

	upload := phpx.GB(c.Sub.U)
	download := phpx.GB(c.Sub.D)
	total := phpx.GB(c.Sub.TransferEnable)
	expire := "长期有效"
	if c.Sub.ExpiredAt != nil {
		expire = phpx.Date("Y-m-d H:i:s", *c.Sub.ExpiredAt)
	}
	// 原项目"剩余流量"实为已用流量（上传 + 下载），保持一致；\n 为字面量反斜杠 n，由客户端解析换行。
	info := "title=" + c.AppName + "订阅信息, content=上传流量：" + phpx.FloatStr(upload) + `GB\n下载流量：` + phpx.FloatStr(download) +
		`GB\n剩余流量：` + phpx.FloatStr(upload+download) + `GB\n套餐流量：` + phpx.FloatStr(total) + `GB\n到期时间：` + expire
	config = strings.ReplaceAll(config, "$subscribe_info", info)
	return text(config, [2]string{"content-disposition", "attachment;filename*=UTF-8''" + phpx.RawURLEncode(c.AppName) + ".conf"}), nil
}

// joinFilter array_filter 后 implode：去掉空串与 "0"。
func joinFilter(sep string, parts []string) string {
	kept := parts[:0:0]
	for _, p := range parts {
		if p != "" && p != "0" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func surgeSS(uuid string, s *phpx.Arr) string {
	cfg := []string{s.Str("name") + "=ss", s.Str("host"), s.Str("port"), "encrypt-method=" + s.Str("cipher"), "password=" + ssPassword(uuid, s, false)}
	if s.Get("obfs") == "http" {
		cfg = append(cfg, "obfs="+s.Str("obfs"))
		if !phpx.Empty(s.Get("obfs-host")) {
			cfg = append(cfg, "obfs-host="+s.Str("obfs-host"))
		}
		if s.Isset("obfs-path") {
			cfg = append(cfg, "obfs-uri="+s.Str("obfs-path"))
		}
	}
	cfg = append(cfg, "fast-open=false", "udp=true")
	return strings.Join(cfg, ",") + "\r\n"
}

// surgeVmess Surge / Surfboard 共用，新旧表字段（tls_settings / tlsSettings 等）都读取。
func surgeVmess(uuid string, s *phpx.Arr) string {
	cfg := []string{s.Str("name") + "=vmess", s.Str("host"), s.Str("port"), "username=" + uuid, "vmess-aead=true", "tfo=true", "udp-relay=true"}
	if phpx.Truthy(s.Get("tls")) {
		cfg = append(cfg, "tls=true")
		ts := phpx.Coalesce(s.Get("tls_settings"), s.Get("tlsSettings"))
		if !phpx.Empty(phpx.Coalesce(phpx.Get(ts, "allow_insecure"), phpx.Get(ts, "allowInsecure"), int64(0))) {
			cfg = append(cfg, "skip-cert-verify=true")
		}
		if sni := phpx.Str(phpx.Coalesce(phpx.Get(ts, "server_name"), phpx.Get(ts, "serverName"), "")); sni != "" {
			cfg = append(cfg, "sni="+sni)
		}
		cfg = appendPin(cfg, s, "server-cert-fingerprint-sha256")
	}
	if s.Get("network") == "ws" {
		cfg = append(cfg, "ws=true")
		if ns := phpx.Coalesce(s.Get("networkSettings"), s.Get("network_settings")); phpx.Truthy(ns) {
			if !phpx.Empty(phpx.Get(ns, "path")) {
				cfg = append(cfg, "ws-path="+phpx.Str(phpx.Get(ns, "path")))
			}
			if !phpx.Empty(phpx.Get(ns, "headers", "Host")) {
				cfg = append(cfg, "ws-headers=Host:"+phpx.Str(phpx.Get(ns, "headers", "Host")))
			}
			if phpx.Get(ns, "security") != nil {
				cfg = append(cfg, "encrypt-method="+phpx.Str(phpx.Get(ns, "security")))
			}
		}
	}
	return strings.Join(cfg, ",") + "\r\n"
}

// surgeTrojan surfboard 为 true 时按 Surfboard 的 ws 写法（只看 isset，不要求 network_settings 非空）。
func surgeTrojan(password string, s *phpx.Arr, surfboard bool) string {
	ts := arr(s.Get("tls_settings"))
	sni := ""
	if v := phpx.Str(phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), "")); v != "" {
		sni = "sni=" + v
	}
	cfg := []string{s.Str("name") + "=trojan", s.Str("host"), s.Str("port"), "password=" + password, sni, "tfo=true", "udp-relay=true"}
	if !phpx.Empty(phpx.Coalesce(s.Get("allow_insecure"), ts.Get("allow_insecure"), int64(0))) {
		cfg = append(cfg, "skip-cert-verify=true")
	}
	cfg = appendPin(cfg, s, "server-cert-fingerprint-sha256")
	if s.Get("network") != nil && phpx.Str(s.Get("network")) == "ws" {
		cfg = append(cfg, "ws=true")
		ns := s.Get("network_settings")
		if surfboard {
			if phpx.Get(ns, "path") != nil {
				cfg = append(cfg, "ws-path="+phpx.Str(phpx.Get(ns, "path")))
			}
			if phpx.Get(ns, "headers", "Host") != nil {
				cfg = append(cfg, "ws-headers=Host:"+phpx.Str(phpx.Get(ns, "headers", "Host")))
			}
		} else if phpx.Truthy(ns) {
			if !phpx.Empty(phpx.Get(ns, "path")) {
				cfg = append(cfg, "ws-path="+phpx.Str(phpx.Get(ns, "path")))
			}
			if !phpx.Empty(phpx.Get(ns, "headers", "Host")) {
				cfg = append(cfg, "ws-headers=Host:"+phpx.Str(phpx.Get(ns, "headers", "Host")))
			}
		}
	}
	return joinFilter(",", cfg) + "\r\n"
}

// surgeHysteria2 参考 https://manual.nssurge.com/policy/proxy.html。
func surgeHysteria2(password string, s *phpx.Arr) string {
	ts := arr(s.Get("tls_settings"))
	sni := ""
	if v := phpx.Str(phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), "")); v != "" {
		sni = "sni=" + v
	}
	cfg := []string{s.Str("name") + "=hysteria2", s.Str("host"), phpx.FirstPort(s.Get("port")), "password=" + password, sni, "udp-relay=true"}
	if !phpx.Empty(s.Get("up_mbps")) {
		cfg = append(cfg, "download-bandwidth="+s.Str("up_mbps"))
	}
	if phpx.MultiPort(s.Get("port")) {
		cfg = append(cfg, `port-hopping="`+strings.ReplaceAll(s.Str("port"), ",", ";")+`"`)
	}
	if !phpx.Empty(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0))) {
		cfg = append(cfg, "skip-cert-verify=true")
	}
	if obfs := s.Str("obfs"); (obfs == "salamander" || obfs == "gecko") && !phpx.Empty(s.Get("obfs_password")) {
		cfg = append(cfg, obfs+"-password="+s.Str("obfs_password"))
	}
	cfg = appendPin(cfg, s, "server-cert-fingerprint-sha256")
	return joinFilter(",", cfg) + "\r\n"
}

// surgeVless Surge iOS 6.0.0+ / Mac 6.12.0+ 的 VLESS（原项目尚未支持）。
// 按官方手册：RAW/WS/HTTPUpgrade/gRPC/XHTTP，TLS / REALITY / Vision / Encryption；不支持 mKCP。
func surgeVless(uuid string, s *phpx.Arr) string {
	network := phpx.Str(phpx.Coalesce(s.Get("network"), "tcp"))
	if network == "kcp" {
		return ""
	}
	cfg := []string{s.Str("name") + "=vless", s.Str("host"), s.Str("port"), "username=" + uuid}
	ts := arr(s.Get("tls_settings"))
	ns := arr(s.Get("network_settings"))
	if !phpx.Empty(s.Get("encryption")) && !phpx.Empty(s.Get("encryption_settings")) {
		es := s.Get("encryption_settings")
		e := s.Str("encryption")
		e += "." + phpx.Str(phpx.Coalesce(phpx.Get(es, "mode"), "native"))
		e += "." + phpx.Str(phpx.Coalesce(phpx.Get(es, "rtt"), "1rtt"))
		if !phpx.Empty(phpx.Get(es, "client_padding")) {
			e += "." + phpx.Str(phpx.Get(es, "client_padding"))
		}
		e += "." + phpx.Str(phpx.Coalesce(phpx.Get(es, "password"), ""))
		cfg = append(cfg, "encryption="+e)
	}
	isReality := phpx.LooseEq(s.Get("tls"), int64(2))
	if isReality {
		if v := ts.Get("public_key"); !phpx.Empty(v) {
			cfg = append(cfg, "reality-public-key="+phpx.Str(v))
		}
		if v := ts.Get("short_id"); !phpx.Empty(v) {
			cfg = append(cfg, "reality-short-id="+phpx.Str(v))
		}
	} else if !phpx.Empty(s.Get("tls")) {
		cfg = append(cfg, "tls=true")
		if phpx.Truthy(ts.Get("allow_insecure")) {
			cfg = append(cfg, "skip-cert-verify=true")
		}
		cfg = appendPin(cfg, s, "server-cert-fingerprint-sha256")
	}
	if sni := ts.Get("server_name"); phpx.Truthy(sni) {
		cfg = append(cfg, "sni="+phpx.Str(sni))
	}
	if fp := ts.Get("fingerprint"); !phpx.Empty(fp) {
		cfg = append(cfg, "client-fingerprint="+phpx.Str(fp))
	}
	// Vision 不能与 gRPC 同用；有 Encryption 时可配 WS / XHTTP。
	if flow := s.Str("flow"); flow != "" && network != "grpc" {
		cfg = append(cfg, "flow="+flow)
	}
	switch network {
	case "ws":
		cfg = append(cfg, "ws=true")
		if p := ns.Get("path"); !phpx.Empty(p) {
			cfg = append(cfg, "ws-path="+phpx.Str(p))
		}
		if h := ns.Get("headers", "Host"); !phpx.Empty(h) {
			cfg = append(cfg, "ws-headers=Host:"+phpx.Str(h))
		}
	case "httpupgrade":
		cfg = append(cfg, "ws=true", "ws-http-upgrade=true")
		if p := ns.Get("path"); !phpx.Empty(p) {
			cfg = append(cfg, "ws-path="+phpx.Str(p))
		}
		if h := phpx.Coalesce(ns.Get("host"), ns.Get("headers", "Host"), ""); !phpx.Empty(h) {
			cfg = append(cfg, "ws-headers=Host:"+phpx.Str(h))
		}
	case "grpc":
		cfg = append(cfg, "grpc=true")
		if sn := ns.Get("serviceName"); !phpx.Empty(sn) {
			cfg = append(cfg, "grpc-service-name="+phpx.Str(sn))
		}
	case "xhttp":
		cfg = append(cfg, "xhttp=true")
		if p := ns.Get("path"); !phpx.Empty(p) {
			cfg = append(cfg, "xhttp-path="+phpx.Str(p))
		}
		if h := ns.Get("host"); !phpx.Empty(h) {
			cfg = append(cfg, "xhttp-host="+phpx.Str(h))
		}
		if m := ns.Get("mode"); !phpx.Empty(m) {
			cfg = append(cfg, "xhttp-mode="+phpx.Str(m))
		}
	}
	return strings.Join(cfg, ",") + "\r\n"
}

// surgeAnyTLS surfboard 为 true 时密码不带 password= 前缀、不带 tfo、追加 reuse=false。
func surgeAnyTLS(password string, s *phpx.Arr, surfboard bool) string {
	ts := arr(s.Get("tls_settings"))
	insecure := "false"
	if phpx.LooseEq(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0)), int64(1)) {
		insecure = "true"
	}
	sni := phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), "")
	auth := "password=" + password
	if surfboard {
		auth = password
	}
	cfg := []string{s.Str("name") + "=anytls", s.Str("host"), s.Str("port"), auth, "skip-cert-verify=" + insecure}
	if !surfboard {
		cfg = append(cfg, "tfo=true")
	}
	if phpx.Truthy(sni) {
		cfg = append(cfg, "sni="+phpx.Str(sni))
	}
	cfg = appendPin(cfg, s, "server-cert-fingerprint-sha256")
	if surfboard {
		cfg = append(cfg, "reuse=false")
	}
	return strings.Join(cfg, ", ") + "\r\n"
}
