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
		case "trojan":
			line = surgeTrojan(c.Sub.UUID, s, false)
		case "hysteria":
			if s.Get("version") != int64(2) {
				continue
			}
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

// surgeVmess Surge / Surfboard 共用。与原项目一致只读旧表字段 tlsSettings / networkSettings。
func surgeVmess(uuid string, s *phpx.Arr) string {
	cfg := []string{s.Str("name") + "=vmess", s.Str("host"), s.Str("port"), "username=" + uuid, "vmess-aead=true", "tfo=true", "udp-relay=true"}
	if phpx.Truthy(s.Get("tls")) {
		cfg = append(cfg, "tls=true")
		if ts := s.Get("tlsSettings"); phpx.Truthy(ts) {
			if !phpx.Empty(phpx.Get(ts, "allowInsecure")) {
				cfg = append(cfg, "skip-cert-verify=true")
			}
			if !phpx.Empty(phpx.Get(ts, "serverName")) {
				cfg = append(cfg, "sni="+phpx.Str(phpx.Get(ts, "serverName")))
			}
		}
	}
	if s.Get("network") == "ws" {
		cfg = append(cfg, "ws=true")
		if ns := s.Get("networkSettings"); phpx.Truthy(ns) {
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
	sni := ""
	if phpx.Truthy(s.Get("server_name")) {
		sni = "sni=" + s.Str("server_name")
	}
	cfg := []string{s.Str("name") + "=trojan", s.Str("host"), s.Str("port"), "password=" + password, sni, "tfo=true", "udp-relay=true"}
	if !phpx.Empty(s.Get("allow_insecure")) {
		cfg = append(cfg, "skip-cert-verify=true")
	}
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

func surgeHysteria2(password string, s *phpx.Arr) string {
	sni := ""
	if phpx.Truthy(s.Get("server_name")) {
		sni = "sni=" + s.Str("server_name")
	}
	cfg := []string{s.Str("name") + "=hysteria2", s.Str("host"), phpx.FirstPort(s.Get("port")), "password=" + password,
		"download-bandwidth=" + s.Str("up_mbps"), sni, "udp-relay=true"}
	if !phpx.Empty(s.Get("insecure")) {
		cfg = append(cfg, "skip-cert-verify=true")
	}
	return joinFilter(",", cfg) + "\r\n"
}

// surgeAnyTLS surfboard 为 true 时不带 tfo、追加 reuse=false。
func surgeAnyTLS(password string, s *phpx.Arr, surfboard bool) string {
	ts := arr(s.Get("tls_settings"))
	insecure := "false"
	if phpx.LooseEq(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0)), int64(1)) {
		insecure = "true"
	}
	sni := phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), "")
	cfg := []string{s.Str("name") + "=anytls", s.Str("host"), s.Str("port"), "password=" + password, "skip-cert-verify=" + insecure}
	if !surfboard {
		cfg = append(cfg, "tfo=true")
	}
	if phpx.Truthy(sni) {
		cfg = append(cfg, "sni="+phpx.Str(sni))
	}
	if surfboard {
		cfg = append(cfg, "reuse=false")
	}
	return strings.Join(cfg, ", ") + "\r\n"
}
