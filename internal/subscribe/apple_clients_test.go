package subscribe

import (
	"strings"
	"testing"

	"gvboard/internal/phpx"
)

func TestQxHysteria2(t *testing.T) {
	uuid := "3b2f6a1e-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
	got := qxHysteria2(uuid, phpx.Map(
		"name", "hy2: v2",
		"host", "hy2.example.com",
		"port", int64(8443),
		"obfs", "salamander",
		"obfs_password", "pw",
		"up_mbps", int64(50),
		"insecure", int64(0),
	))
	want := "hysteria2=hy2.example.com:8443,password=" + uuid + ",salamander-password=pw,download-bandwidth=50,udp-relay=true,tag=hy2: v2\r\n"
	if got != want {
		t.Fatalf("旧表 hy2\n期望 %q\n实际 %q", want, got)
	}

	got = qxHysteria2(uuid, phpx.Map(
		"name", "v2n hy2",
		"host", "v2n6.example.com",
		"port", "20000-30000",
		"obfs", "salamander",
		"obfs_password", "op",
		"tls_settings", phpx.Map("server_name", "hy2.v2n.com", "allow_insecure", int64(1)),
	))
	want = "hysteria2=v2n6.example.com:20000,password=" + uuid + ",port-hopping=20000-30000,salamander-password=op,tls-host=hy2.v2n.com,tls-verification=false,udp-relay=true,tag=v2n hy2\r\n"
	if got != want {
		t.Fatalf("v2node hy2\n期望 %q\n实际 %q", want, got)
	}
}

func TestSurgeVless(t *testing.T) {
	uuid := "3b2f6a1e-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
	got := surgeVless(uuid, phpx.Map(
		"name", "vless tls",
		"host", "vl.example.com",
		"port", int64(443),
		"tls", int64(1),
		"flow", "xtls-rprx-vision",
		"network", "tcp",
		"tls_settings", phpx.Map("server_name", "vl.example.com", "allow_insecure", "0", "fingerprint", "safari"),
	))
	want := "vless tls=vless,vl.example.com,443,username=" + uuid + ",tls=true,sni=vl.example.com,client-fingerprint=safari,flow=xtls-rprx-vision\r\n"
	if got != want {
		t.Fatalf("tls+vision\n期望 %q\n实际 %q", want, got)
	}

	got = surgeVless(uuid, phpx.Map(
		"name", "vless reality",
		"host", "vl2.example.com",
		"port", int64(443),
		"tls", int64(2),
		"flow", "xtls-rprx-vision",
		"network", "tcp",
		"tls_settings", phpx.Map("server_name", "www.apple.com", "public_key", "pubkey123", "short_id", "abcd"),
	))
	want = "vless reality=vless,vl2.example.com,443,username=" + uuid + ",reality-public-key=pubkey123,reality-short-id=abcd,sni=www.apple.com,flow=xtls-rprx-vision\r\n"
	if got != want {
		t.Fatalf("reality\n期望 %q\n实际 %q", want, got)
	}

	got = surgeVless(uuid, phpx.Map(
		"name", "vless ws",
		"host", "vl3.example.com",
		"port", int64(80),
		"tls", int64(0),
		"network", "ws",
		"network_settings", phpx.Map("path", "/vlws", "headers", phpx.Map("Host", "vlcdn.com")),
		"encryption", "mlkem768x25519plus",
		"encryption_settings", phpx.Map("mode", "native", "rtt", "0rtt", "client_padding", "100-111-1111", "password", "encpw"),
	))
	if !strings.Contains(got, "encryption=mlkem768x25519plus.native.0rtt.100-111-1111.encpw") || !strings.Contains(got, "ws=true") || !strings.Contains(got, "ws-path=/vlws") {
		t.Fatalf("ws+encryption 异常: %q", got)
	}

	got = surgeVless(uuid, phpx.Map(
		"name", "vless grpc",
		"host", "vl4.example.com",
		"port", int64(443),
		"tls", int64(1),
		"flow", "xtls-rprx-vision",
		"network", "grpc",
		"network_settings", phpx.Map("serviceName", "vlgrpc"),
		"tls_settings", phpx.Map("server_name", "grpc.vl.com"),
	))
	if strings.Contains(got, "flow=") || !strings.Contains(got, "grpc=true") || !strings.Contains(got, "grpc-service-name=vlgrpc") {
		t.Fatalf("grpc 不应带 flow: %q", got)
	}

	if surgeVless(uuid, phpx.Map("name", "k", "host", "h", "port", int64(1), "network", "kcp")) != "" {
		t.Fatal("kcp 应跳过")
	}
}

func TestSurgeHysteria2V2node(t *testing.T) {
	uuid := "3b2f6a1e-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
	got := surgeHysteria2(uuid, phpx.Map(
		"name", "v2n hy2",
		"host", "v2n6.example.com",
		"port", "20000-30000",
		"tls_settings", phpx.Map("server_name", "hy2.v2n.com", "allow_insecure", int64(0)),
	))
	want := "v2n hy2=hysteria2,v2n6.example.com,20000,password=" + uuid + ",sni=hy2.v2n.com,udp-relay=true\r\n"
	if got != want {
		t.Fatalf("v2node hy2\n期望 %q\n实际 %q", want, got)
	}
}
