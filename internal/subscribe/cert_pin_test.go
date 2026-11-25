package subscribe

import (
	"strings"
	"testing"

	"gvboard/internal/model"
	"gvboard/internal/phpx"
)

var (
	rawPin  = strings.Repeat("AB:", 31) + "AB"
	wantPin = strings.Repeat("ab", 32)
)

func pinnedV2node(protocol string, extra ...any) *phpx.Arr {
	s := phpx.Map(
		"type", "v2node",
		"protocol", protocol,
		"name", "n1",
		"host", "n1.example.com",
		"port", "443",
		"tls", int64(1),
		"network", "tcp",
		"tls_settings", phpx.Map("server_name", "n1.example.com", "allow_insecure", int64(0), "pinned_peer_cert_sha256", rawPin),
	)
	for i := 0; i+1 < len(extra); i += 2 {
		s.Set(extra[i], extra[i+1])
	}
	return s
}

func TestNormPin(t *testing.T) {
	cases := map[any]string{
		rawPin:                   wantPin,
		" " + wantPin + " ":      wantPin,
		strings.ToUpper(wantPin): wantPin,
		wantPin[:62]:             "",
		strings.Repeat("zz", 32): "",
		"":                       "",
		int64(1):                 "",
		wantPin + "00":           "",
	}
	for in, want := range cases {
		if got := normPin(in); got != want {
			t.Errorf("normPin(%v) = %q，期望 %q", in, got, want)
		}
	}
}

func TestMarkCertPin(t *testing.T) {
	pinned := pinnedV2node("vless")
	reality := pinnedV2node("vless", "tls", int64(2))
	ss := pinnedV2node("shadowsocks")
	remote := pinnedV2node("trojan")
	remote.Arr("tls_settings").Set("pinned_peer_cert_sha256", "")
	remote.Arr("tls_settings").Set("cert_mode", "remote")
	legacy := phpx.Map("type", "hysteria", "insecure", int64(0),
		"tls_settings", phpx.Map("pinned_peer_cert_sha256", wantPin))

	servers := []*phpx.Arr{pinned, reality, ss, remote, legacy}
	markCertPin(servers, false)

	ts := servers[0].Arr("tls_settings")
	if ts.Get("allow_insecure") != int64(1) || ts.Get("pinned_peer_cert_sha256") != wantPin {
		t.Fatalf("有指纹的节点应允许不安全证书并规范指纹：%v", ts.Get("allow_insecure"))
	}
	if pinned.Get("tls_settings", "allow_insecure") != int64(0) || pinned.Get("tls_settings", "pinned_peer_cert_sha256") != rawPin {
		t.Fatal("不应修改原节点数据")
	}
	if servers[1] != reality || servers[2] != ss {
		t.Fatal("REALITY 与 Shadowsocks 节点不处理")
	}
	if servers[3] != remote {
		t.Fatal("非 Clash 系客户端：remote 证书且无指纹的节点不处理")
	}
	if servers[4].Get("insecure") != int64(1) || servers[4].Get("tls_settings", "allow_insecure") != int64(1) {
		t.Fatal("节点自身的 insecure 字段也应置为 1")
	}

	servers = []*phpx.Arr{remote}
	markCertPin(servers, true)
	if servers[0].Get("tls_settings", "allow_insecure") != int64(1) {
		t.Fatal("Clash 系与 sing-box：remote 证书节点即使没有指纹也允许不安全证书")
	}

	vm := pinnedV2node("vmess")
	servers = []*phpx.Arr{vm}
	markCertPin(servers, false)
	if servers[0].Get("tls_settings", "allowInsecure") != int64(1) {
		t.Fatal("VMess 还要设置旧字段 allowInsecure")
	}
}

func TestHysteria2URIPin(t *testing.T) {
	uuid := "3b2f6a1e-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
	servers := []*phpx.Arr{pinnedV2node("hysteria2")}
	markCertPin(servers, false)
	got := hysteria2URI(uuid, servers[0])
	if !strings.Contains(got, "/?insecure=1&sni=n1.example.com&pinSHA256="+wantPin+"&pcs="+wantPin+"#") {
		t.Fatalf("Hysteria2 链接应带 pinSHA256 与 pcs：%q", got)
	}

	plain := pinnedV2node("hysteria2")
	plain.Arr("tls_settings").Del("pinned_peer_cert_sha256")
	if got := hysteria2URI(uuid, plain); strings.Contains(got, "pcs=") || strings.Contains(got, "pinSHA256") {
		t.Fatalf("没有指纹时不应输出指纹参数：%q", got)
	}

	bad := pinnedV2node("hysteria2")
	bad.Arr("tls_settings").Set("pinned_peer_cert_sha256", "not-a-pin")
	if got := hysteria2URI(uuid, bad); strings.Contains(got, "pinSHA256") || !strings.Contains(got, "&pcs=not-a-pin") {
		t.Fatalf("格式不对的指纹只按原样放入 pcs：%q", got)
	}
}

func TestURIPin(t *testing.T) {
	uuid := "3b2f6a1e-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
	vless := vlessURI(uuid, pinnedV2node("vless"))
	if !strings.Contains(vless, "&insecure=0&pcs="+rawPinEsc()+"&allowInsecure=0&sni=") {
		t.Fatalf("VLESS 有指纹时输出 pcs 与 allowInsecure：%q", vless)
	}
	reality := vlessURI(uuid, pinnedV2node("vless", "tls", int64(2)))
	if strings.Contains(reality, "pcs=") {
		t.Fatalf("REALITY 不输出 pcs：%q", reality)
	}
	anytls := anytlsURI(uuid, pinnedV2node("anytls"))
	if !strings.Contains(anytls, "&pcs="+rawPinEsc()) {
		t.Fatalf("AnyTLS 有指纹时输出 pcs：%q", anytls)
	}
	trojan := trojanURI(uuid, pinnedV2node("trojan"))
	if !strings.Contains(trojan, "?allowInsecure=0&insecure=0&peer=") {
		t.Fatalf("Trojan 同时输出 allowInsecure 与 insecure：%q", trojan)
	}
}

func rawPinEsc() string { return strings.ReplaceAll(rawPin, ":", "%3A") }

func TestClashFamilyPin(t *testing.T) {
	uuid := "3b2f6a1e-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
	s := pinnedV2node("trojan")
	if a := clashTrojan(uuid, s); a.Get("fingerprint") != wantPin || a.Get("skip-cert-verify") != true {
		t.Fatalf("Clash Trojan 应带 fingerprint 并跳过校验：%v", a.Get("fingerprint"))
	}
	if a := withPin(metaTrojan(uuid, s), s, "server-cert-fingerprint"); a.Get("server-cert-fingerprint") != wantPin {
		t.Fatal("Stash 使用 server-cert-fingerprint")
	}
	ss := pinnedV2node("shadowsocks", "cipher", "aes-128-gcm")
	if a := withPin(metaSS(uuid, ss), ss, "fingerprint"); a.Get("fingerprint") != nil {
		t.Fatal("Shadowsocks 不加指纹")
	}
	reality := pinnedV2node("vless", "tls", int64(2))
	if a := withPin(metaVless(uuid, reality, metaMeta), reality, "fingerprint"); a.Get("fingerprint") != nil {
		t.Fatal("REALITY 不加指纹")
	}
}

func TestTextClientsPin(t *testing.T) {
	uuid := "3b2f6a1e-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
	surgeKey := "server-cert-fingerprint-sha256=" + wantPin
	for name, line := range map[string]string{
		"Surge VMess":      surgeVmess(uuid, pinnedV2node("vmess")),
		"Surge Trojan":     surgeTrojan(uuid, pinnedV2node("trojan"), false),
		"Surge Hysteria2":  surgeHysteria2(uuid, pinnedV2node("hysteria2")),
		"Surge AnyTLS":     surgeAnyTLS(uuid, pinnedV2node("anytls"), false),
		"Surge VLESS":      surgeVless(uuid, pinnedV2node("vless")),
		"Surfboard AnyTLS": surgeAnyTLS(uuid, pinnedV2node("anytls"), true),
	} {
		if !strings.Contains(line, surgeKey) {
			t.Errorf("%s 缺少证书指纹：%q", name, line)
		}
	}
	loonKey := "tls-cert-sha256=" + wantPin
	for name, line := range map[string]string{
		"Loon VMess":     loonVmess(uuid, pinnedV2node("vmess")),
		"Loon VLESS":     loonVless(uuid, pinnedV2node("vless")),
		"Loon Trojan":    loonTrojan(uuid, pinnedV2node("trojan")),
		"Loon Hysteria2": loonHysteria2(uuid, pinnedV2node("hysteria2")),
		"Loon AnyTLS":    loonAnyTLS(uuid, pinnedV2node("anytls")),
	} {
		if !strings.Contains(line, loonKey) {
			t.Errorf("%s 缺少证书指纹：%q", name, line)
		}
	}
}

func TestLoonV2nodeHysteria2(t *testing.T) {
	uuid := "3b2f6a1e-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
	s := pinnedV2node("hysteria2", "port", "20000-30000", "up_mbps", int64(100), "obfs", "salamander", "obfs_password", "op")
	s.Arr("tls_settings").Del("pinned_peer_cert_sha256")
	resp, err := loon(&Ctx{Sub: &model.Subscription{UUID: uuid}, Servers: []*phpx.Arr{s}})
	if err != nil {
		t.Fatal(err)
	}
	want := `n1=hysteria2,n1.example.com,20000,` + uuid + `,sni=n1.example.com,udp=true,download-bandwidth=100,server-ports="20000:30000",salamander-password=op` + "\r\n"
	if string(resp.Body) != want {
		t.Fatalf("Loon v2node Hysteria2\n期望 %q\n实际 %q", want, resp.Body)
	}
}
