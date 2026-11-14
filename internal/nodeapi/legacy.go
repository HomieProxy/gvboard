package nodeapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"gvboard/internal/model"
)

// 旧版节点后端（Deepbwork / TrojanTidalab / ShadowsocksTidalab）的输出，与原项目一致。

// legacyUsers Deepbwork、TrojanTidalab 的用户列表：限速、设备数为空时输出 null，uuid 放入 inner 对象。
func legacyUsers(users []User, name string, inner func(u User) obj) []obj {
	out := make([]obj, len(users))
	for i, u := range users {
		out[i] = obj{{"id", u.ID}, {"speed_limit", u.SpeedLimit}, {"device_limit", u.DeviceLimit}, {name, inner(u)}}
	}
	return out
}

// v2rayUser Deepbwork 下发的用户，email 保持原格式（决策 D39）。
func v2rayUser(u User) obj {
	return obj{{"uuid", u.UUID}, {"email", fmt.Sprintf("%s@v2board.user", u.UUID)}, {"alter_id", 0}, {"level", 0}}
}

func trojanUser(u User) obj { return obj{{"password", u.UUID}} }

// ssTidalabUsers ShadowsocksTidalab 的用户列表。
func ssTidalabUsers(users []User, srv *model.ServerShadowsocks) []obj {
	out := make([]obj, len(users))
	for i, u := range users {
		out[i] = obj{{"id", u.ID}, {"port", srv.ServerPort}, {"cipher", srv.Cipher}, {"secret", u.UUID}}
	}
	return out
}

const v2rayTemplate = `{"log":{"loglevel":"debug","access":"access.log","error":"error.log"},"api":{"services":["HandlerService","StatsService"],"tag":"api"},"dns":{},"stats":{},"inbounds":[{"port":443,"protocol":"vmess","settings":{"clients":[]},"sniffing":{"enabled":true,"destOverride":["http","tls"]},"streamSettings":{"network":"tcp"},"tag":"proxy"},{"listen":"127.0.0.1","port":23333,"protocol":"dokodemo-door","settings":{"address":"0.0.0.0"},"tag":"api"}],"outbounds":[{"protocol":"freedom","settings":{}},{"protocol":"blackhole","settings":{},"tag":"block"}],"routing":{"rules":[{"type":"field","inboundTag":"api","outboundTag":"api"}]},"policy":{"levels":{"0":{"handshake":4,"connIdle":300,"uplinkOnly":5,"downlinkOnly":30,"statsUserUplink":true,"statsUserDownlink":true}}}}`

const trojanTemplate = `{"run_type":"server","local_addr":"0.0.0.0","local_port":443,"remote_addr":"www.taobao.com","remote_port":80,"password":[],"ssl":{"cert":"server.crt","key":"server.key","sni":"domain.com"},"api":{"enabled":true,"api_addr":"127.0.0.1","api_port":10000}}`

const (
	certFile = "/root/.cert/server.crt"
	keyFile  = "/root/.cert/server.key"
)

func template(s string) map[string]any {
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	_ = d.Decode(&m)
	return m
}

func sub(m map[string]any, keys ...any) map[string]any {
	var cur any = m
	for _, k := range keys {
		switch x := cur.(type) {
		case map[string]any:
			cur = x[k.(string)]
		case []any:
			cur = x[k.(int)]
		}
	}
	return cur.(map[string]any)
}

// v2rayConfig Deepbwork 的 V2Ray 配置（原项目 DeepbworkController::getV2RayConfig）。
// 原项目中 dnsSettings、ruleSettings、tlsSettings 经 array 转换后按对象属性读取，
// 实际效果是：DNS 原样输出且不追加服务器，节点规则与 TLS 的 serverName / allowInsecure 不生效；
// 全局规则配置项在原项目后台无法设置，恒为空，因此始终关闭流量探测。此处按实际效果输出。
func v2rayConfig(srv *model.ServerVmess, localPort int64, logEnable bool) map[string]any {
	j := template(v2rayTemplate)
	loglevel := "none"
	if logEnable {
		loglevel = "debug"
	}
	sub(j, "log")["loglevel"] = loglevel
	inbound := sub(j, "inbounds", 0)
	sub(j, "inbounds", 1)["port"] = localPort
	inbound["port"] = srv.ServerPort
	stream := inbound["streamSettings"].(map[string]any)
	stream["network"] = srv.Network
	if dns := phpArray(srv.DNSSettings); truthy(dns) {
		j["dns"] = dns
		sub(j, "outbounds", 0, "settings")["domainStrategy"] = "UseIP"
	}
	if ns := phpArray(srv.NetworkSettings); truthy(ns) {
		names := map[string]string{
			"tcp": "tcpSettings", "kcp": "kcpSettings", "ws": "wsSettings", "http": "httpSettings",
			"domainsocket": "dsSettings", "quic": "quicSettings", "grpc": "grpcSettings",
		}
		if name, ok := names[srv.Network]; ok {
			stream[name] = ns
		}
	}
	sub(inbound, "sniffing")["enabled"] = false
	if srv.TLS != 0 {
		stream["security"] = "tls"
		stream["tlsSettings"] = map[string]any{
			"certificates": []any{obj{{"certificateFile", certFile}, {"keyFile", keyFile}}},
		}
	}
	return j
}

// trojanConfig TrojanTidalab 的配置（原项目 TrojanTidalabController::getTrojanConfig）。
func trojanConfig(srv *model.ServerTrojan, localPort int64) map[string]any {
	j := template(trojanTemplate)
	j["local_port"] = srv.ServerPort
	ssl := sub(j, "ssl")
	ssl["sni"] = srv.Host
	if srv.ServerName != nil && *srv.ServerName != "" && *srv.ServerName != "0" {
		ssl["sni"] = *srv.ServerName
	}
	ssl["cert"] = certFile
	ssl["key"] = keyFile
	sub(j, "api")["api_port"] = localPort
	return j
}

// truthy PHP 中数组是否非空。
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case string:
		return x != "" && x != "0"
	case bool:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	}
	return true
}
