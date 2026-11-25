package subscribe

import (
	"strings"

	"gvboard/internal/phpx"
)

// 对应原项目 Protocols/Loon.php。

func loon(c *Ctx) (*Response, error) {
	var b strings.Builder
	for _, s := range c.Servers {
		network := s.Get("network")
		switch typeOf(s) {
		case "shadowsocks":
			b.WriteString(loonSS(c.Sub.UUID, s))
		case "vmess":
			b.WriteString(loonVmess(c.Sub.UUID, s))
		case "vless":
			if network == "tcp" || network == "ws" {
				b.WriteString(loonVless(c.Sub.UUID, s))
			}
		case "trojan":
			if network != "grpc" {
				b.WriteString(loonTrojan(c.Sub.UUID, s))
			}
		case "hysteria":
			if phpx.Int(s.Get("version")) == 2 {
				b.WriteString(loonHysteria2(c.Sub.UUID, s))
			}
		case "hysteria2":
			b.WriteString(loonHysteria2(c.Sub.UUID, s))
		case "anytls":
			b.WriteString(loonAnyTLS(c.Sub.UUID, s))
		}
	}
	return text(b.String(), [2]string{"Subscription-Userinfo", c.userinfo()}), nil
}

func loonSS(uuid string, s *phpx.Arr) string {
	cfg := []string{s.Str("name") + "=Shadowsocks", s.Str("host"), s.Str("port"), s.Str("cipher"), ssPassword(uuid, s, false)}
	if s.Get("obfs") == "http" {
		cfg = append(cfg, "obfs-name="+s.Str("obfs"))
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

// loonTCP tcp 传输：header.type 为 http 时 transport=http（原项目对整个数组做 str_replace），另取 path / Host 首项。
func loonTCP(cfg []string, ns any) []string {
	cfg = append(cfg, "transport=tcp")
	if !phpx.Truthy(ns) {
		return cfg
	}
	if t := phpx.Get(ns, "header", "type"); !phpx.Empty(t) && phpx.LooseEq(t, "http") {
		for i := range cfg {
			cfg[i] = strings.ReplaceAll(cfg[i], "transport=tcp", "transport="+phpx.Str(t))
		}
	}
	if p := phpx.Get(ns, "header", "request", "path", 0); !phpx.Empty(p) {
		cfg = append(cfg, "path="+phpx.Str(p))
	}
	if h := phpx.Get(ns, "header", "request", "headers", "Host", 0); !phpx.Empty(h) {
		cfg = append(cfg, "host="+phpx.Str(h))
	}
	return cfg
}

func loonWS(cfg []string, ns any) []string {
	cfg = append(cfg, "transport=ws")
	if !phpx.Truthy(ns) {
		return cfg
	}
	if p := phpx.Get(ns, "path"); !phpx.Empty(p) {
		cfg = append(cfg, "path="+phpx.Str(p))
	}
	if h := phpx.Get(ns, "headers", "Host"); !phpx.Empty(h) {
		cfg = append(cfg, "host="+phpx.Str(h))
	}
	return cfg
}

// loonVmess 新旧表字段（network_settings / networkSettings 等）都读取。
func loonVmess(uuid string, s *phpx.Arr) string {
	ns := phpx.Coalesce(s.Get("networkSettings"), s.Get("network_settings"))
	cfg := []string{s.Str("name") + "=vmess", s.Str("host"), s.Str("port"),
		phpx.Str(phpx.Coalesce(phpx.Get(ns, "security"), "auto")), uuid, "fast-open=false", "udp=true", "alterId=0"}
	if s.Get("network") == "tcp" {
		cfg = loonTCP(cfg, ns)
	}
	if phpx.Truthy(s.Get("tls")) {
		cfg = append(cfg, "over-tls=true")
		ts := phpx.Coalesce(s.Get("tls_settings"), s.Get("tlsSettings"))
		if !phpx.Empty(phpx.Coalesce(phpx.Get(ts, "allow_insecure"), phpx.Get(ts, "allowInsecure"), int64(0))) {
			cfg = append(cfg, "skip-cert-verify=true")
		}
		if sni := phpx.Str(phpx.Coalesce(phpx.Get(ts, "server_name"), phpx.Get(ts, "serverName"), "")); sni != "" {
			cfg = append(cfg, "sni="+sni)
		}
		cfg = appendPin(cfg, s, "tls-cert-sha256")
	}
	if s.Get("network") == "ws" {
		cfg = loonWS(cfg, ns)
	}
	return strings.Join(cfg, ",") + "\r\n"
}

func loonVless(uuid string, s *phpx.Arr) string {
	ns := s.Get("network_settings")
	cfg := []string{s.Str("name") + "=vless", s.Str("host"), s.Str("port"), uuid, "fast-open=false", "udp=true", "alterId=0"}
	if s.Get("network") == "tcp" {
		cfg = loonTCP(cfg, ns)
	}
	ts := s.Get("tls_settings")
	switch phpx.Int(s.Get("tls")) {
	case 1:
		cfg = append(cfg, "over-tls=true", "flow="+s.Str("flow"))
		if phpx.Truthy(ts) {
			if !phpx.Empty(phpx.Get(ts, "allow_insecure")) {
				cfg = append(cfg, "skip-cert-verify=true")
			}
			if sn := phpx.Get(ts, "server_name"); !phpx.Empty(sn) {
				cfg = append(cfg, "sni="+phpx.Str(sn))
			}
		}
		cfg = appendPin(cfg, s, "tls-cert-sha256")
	case 2:
		cfg = append(cfg, "flow="+s.Str("flow"))
		if phpx.Truthy(ts) {
			if v := phpx.Get(ts, "public_key"); !phpx.Empty(v) {
				cfg = append(cfg, "public-key="+phpx.Str(v))
			}
			if v := phpx.Get(ts, "short_id"); !phpx.Empty(v) {
				cfg = append(cfg, "short-id="+phpx.Str(v))
			}
			if v := phpx.Get(ts, "server_name"); !phpx.Empty(v) {
				cfg = append(cfg, "sni="+phpx.Str(v))
			}
			if !phpx.Empty(phpx.Get(ts, "allow_insecure")) {
				cfg = append(cfg, "skip-cert-verify=true")
			}
		}
	}
	if s.Get("network") == "ws" {
		cfg = loonWS(cfg, ns)
	}
	return strings.Join(cfg, ",") + "\r\n"
}

func loonTrojan(password string, s *phpx.Arr) string {
	ts := arr(s.Get("tls_settings"))
	sni := ""
	if v := phpx.Str(phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), "")); v != "" {
		sni = "sni=" + v
	}
	cfg := []string{s.Str("name") + "=trojan", s.Str("host"), s.Str("port"), password, sni, "fast-open=false", "udp=true"}
	if !phpx.Empty(phpx.Coalesce(s.Get("allow_insecure"), ts.Get("allow_insecure"), int64(0))) {
		cfg = append(cfg, "skip-cert-verify=true")
	}
	cfg = appendPin(cfg, s, "tls-cert-sha256")
	if s.Get("network") != nil && phpx.Str(s.Get("network")) == "ws" {
		cfg = append(cfg, "ws=true")
		if ns := s.Get("network_settings"); phpx.Truthy(ns) {
			if p := phpx.Get(ns, "path"); !phpx.Empty(p) {
				cfg = append(cfg, "ws-path="+phpx.Str(p))
			}
			if h := phpx.Get(ns, "headers", "Host"); !phpx.Empty(h) {
				cfg = append(cfg, "ws-headers=Host:"+phpx.Str(h))
			}
		}
	}
	return joinFilter(",", cfg) + "\r\n"
}

func loonHysteria2(password string, s *phpx.Arr) string {
	ts := arr(s.Get("tls_settings"))
	sni := ""
	if v := phpx.Str(phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), "")); v != "" {
		sni = "sni=" + v
	}
	cfg := []string{s.Str("name") + "=hysteria2", s.Str("host"), phpx.FirstPort(s.Get("port")), password, sni, "udp=true"}
	if !phpx.Empty(s.Get("up_mbps")) {
		cfg = append(cfg, "download-bandwidth="+s.Str("up_mbps"))
	}
	if phpx.MultiPort(s.Get("port")) {
		cfg = append(cfg, `server-ports="`+strings.ReplaceAll(s.Str("port"), "-", ":")+`"`)
	}
	if !phpx.Empty(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0))) {
		cfg = append(cfg, "skip-cert-verify=true")
	}
	if s.Str("obfs") == "salamander" && !phpx.Empty(s.Get("obfs_password")) {
		cfg = append(cfg, "salamander-password="+s.Str("obfs_password"))
	}
	cfg = appendPin(cfg, s, "tls-cert-sha256")
	return joinFilter(",", cfg) + "\r\n"
}

func loonAnyTLS(password string, s *phpx.Arr) string {
	cfg := []string{s.Str("name") + "=anytls", s.Str("host"), s.Str("port"), password, "udp=true"}
	ts := arr(s.Get("tls_settings"))
	if sni := phpx.Coalesce(s.Get("server_name"), ts.Get("server_name"), ""); phpx.Truthy(sni) {
		cfg = append(cfg, "sni="+phpx.Str(sni))
	}
	insecure := "false"
	if phpx.Truthy(phpx.Coalesce(s.Get("insecure"), ts.Get("allow_insecure"), int64(0))) {
		insecure = "true"
	}
	cfg = append(cfg, "skip-cert-verify="+insecure)
	cfg = appendPin(cfg, s, "tls-cert-sha256")
	return strings.Join(cfg, ",") + "\r\n"
}
